package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func backupControlRequest(t *testing.T, app *application, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	app.authManager = testAuthManager("control-test")
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.SetBasicAuth("iosbackup", "control-test")
	req.Header.Set(csrfHeader, app.csrfManager.Token())
	rr := httptest.NewRecorder()
	app.setupRoutes().ServeHTTP(rr, req)
	return rr
}

func TestBackupControlHTTPStopDuringStarting(t *testing.T) {
	app := newBackupControlTestApp(t)
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishBackupJob(job, context.Canceled)
	rr := backupControlRequest(t, app, "/api/backup-stop/CONTROL", `{"job_id":"`+job.id+`"}`)
	if rr.Code != 202 {
		t.Fatalf("stop must accept the registered preparation task: %d %s", rr.Code, rr.Body.String())
	}
	if context.Cause(job.ctx) == nil {
		t.Fatal("stop did not cancel preparation")
	}
	if d := app.buildStatusSnapshot().Devices[0]; !d.BackingUp || d.BackupState != "stopping" {
		t.Fatalf("released before actual exit: %+v", d)
	}
}

func TestBackupControlHTTPCompletedSuccessHoldsSchedule(t *testing.T) {
	app := newBackupControlTestApp(t)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	before := app.buildStatusSnapshot().Devices[0]
	rr := backupControlRequest(t, app, "/api/backup-stop/CONTROL", `{"job_id":"`+before.BackupJobID+`"}`)
	if rr.Code != 200 {
		t.Fatalf("late stop: %d %s", rr.Code, rr.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["auto_backup_hold"] != true {
		t.Fatalf("missing hold: %+v", response)
	}
	after := app.buildStatusSnapshot().Devices[0]
	if after.BackupState != "succeeded" || after.LastBackupAt != before.LastBackupAt {
		t.Fatalf("late stop changed success: %+v", after)
	}
}

func TestBackupControlHTTPRejectsUnauthenticatedAndCrossSiteRequests(t *testing.T) {
	app := newBackupControlTestApp(t)
	app.authManager = testAuthManager("control-test")
	handler := app.setupRoutes()
	for _, path := range []string{"/api/backup-stop/CONTROL", "/api/backup-resume/CONTROL", "/api/backup-hold-retry/CONTROL"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, rr.Code)
		}
		req = httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.SetBasicAuth("iosbackup", "control-test")
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatalf("missing CSRF %s: %d", path, rr.Code)
		}
	}
}

func TestBackupControlHTTPValidationAndResume(t *testing.T) {
	app, id := completedHeldBackup(t)
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/backup-stop/CONTROL", `{}`, 400},
		{"/api/backup-stop/CONTROL", `{"job_id":"stale"}`, 409},
		{"/api/backup-stop/UNKNOWN", `{"job_id":"stale"}`, 404},
		{"/api/backup-resume/CONTROL", `{"hold_version":"stale"}`, 409},
		{"/api/backup-resume/CONTROL", `{"hold_version":"` + id + `"}`, 200},
	} {
		if rr := backupControlRequest(t, app, tc.path, tc.body); rr.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, rr.Code, rr.Body.String())
		}
	}
	if app.buildStatusSnapshot().Devices[0].AutoBackupHold {
		t.Fatal("resume did not clear hold")
	}
}
