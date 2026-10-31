package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestBackupJobsFollowExecutionAndRestart(t *testing.T) {
	app, _ := backupControlFixture(t)
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'IOSBK_ACTIVITY 1 receiving\\n' >&2; printf 'Backup Successful.\\n'")
	}
	dir := t.TempDir()
	app.backupJobRecords = openJobTestStore(t, dir, defaultBackupJobLimits())
	job, err := app.beginBackupJob("PHONE", "", "", false, backupRequest{Mode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.runBackupJob(job); err != nil {
		t.Fatal(err)
	}
	syncJobs(t, app.backupJobRecords)
	records := app.backupJobRecords.list("PHONE")
	if len(records) != 1 || records[0].ID != job.id || records[0].State != "succeeded" || records[0].RequestedMode != "full" || records[0].LastActivityAt.IsZero() {
		t.Fatalf("records=%+v", records)
	}
	if err := app.stopBackup("PHONE", job.id); err != nil {
		t.Fatal(err)
	}
	if app.backupJobRecords.list("PHONE")[0].State != "succeeded" {
		t.Fatal("late stop changed terminal record")
	}
	if err := app.backupJobRecords.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := openJobTestStore(t, dir, defaultBackupJobLimits())
	if records = restored.list("PHONE"); len(records) != 1 || records[0].State != "succeeded" {
		t.Fatalf("restored=%+v", records)
	}
}

func TestBackupJobsStoppedPreparationIsNotFailure(t *testing.T) {
	app, _ := backupControlFixture(t)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	job, err := app.beginBackupJob("PHONE", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.stopBackup("PHONE", job.id); err != nil {
		t.Fatal(err)
	}
	if record := app.backupJobRecords.list("PHONE")[0]; record.State != "stopping" {
		t.Fatalf("record=%+v", record)
	}
	if err = app.runBackupJob(job); err != errBackupStoppedByUser {
		t.Fatalf("error=%v", err)
	}
	syncJobs(t, app.backupJobRecords)
	if record := app.backupJobRecords.list("PHONE")[0]; record.State != "stopped" || record.ErrorCode != "backup_stopped_by_user" {
		t.Fatalf("record=%+v", record)
	}
}

func TestBackupStopResponseKeepsOriginalTaskWhileHoldIsSaving(t *testing.T) {
	app := newBackupControlTestApp(t)
	old, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	app.configStore.writeAtomic = func(_ string, _ []byte, _ os.FileMode) error {
		once.Do(func() { close(entered); <-release })
		return nil
	}
	request := httptest.NewRequest("POST", "/api/backup-stop/CONTROL", strings.NewReader(`{"job_id":"`+old.id+`"}`))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { app.handleBackupStop(response, request); close(done) }()
	<-entered
	app.finishBackupJob(old, errBackupStoppedByUser)
	next, err := app.beginBackupJob("CONTROL", "", "", false)
	close(release)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishBackupJob(next, context.Canceled)
	var payload map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || payload["backup_job_id"] != old.id || payload["backing_up"] != false || payload["backup_state"] != "stopped" {
		t.Fatalf("old stop response=%d %s", response.Code, response.Body.String())
	}
}
