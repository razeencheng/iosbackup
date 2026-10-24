package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newBackupControlTestApp(t *testing.T) *application {
	t.Helper()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	cfg.WiFiPowerAssertion = false
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices["CONTROL"] = &device{UDID: "CONTROL", Name: "Test phone", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB), BatteryLevel: 90, IsCharging: true}
	app.configs["CONTROL"] = app.defaultBackupConfig("CONTROL", "Test phone")
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	return app
}

func TestBackupControlStopWaitsForActualExit(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, conn := range []string{connectTypeUSB, connectTypeNetwork} {
			t.Run(conn+map[bool]string{false: "/manual", true: "/automatic"}[automatic], func(t *testing.T) {
				app := newBackupControlTestApp(t)
				app.configs["CONTROL"].AutoBackupEnabled = true
				app.hub = newEventHub()
				updates := app.hub.Subscribe()
				defer app.hub.Unsubscribe(updates)
				app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
					return newExecCmd(ctx, "/bin/sh", "-c", `trap 'exit 1' INT
printf 'IOSBK_ACTIVITY 1 receiving\n' >&2
while :; do sleep 0.05; done`)
				}
				job, err := app.beginBackupJob("CONTROL", connectionTypeDesc(conn), "", automatic)
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- app.runBackupJob(job) }()
				t.Cleanup(func() { job.cancel(context.Canceled) })
				deadline := time.After(5 * time.Second)
				ready := false
				for !ready {
					select {
					case data := <-updates:
						var snap statusSnapshot
						if err := json.Unmarshal(data, &snap); err != nil {
							t.Fatal(err)
						}
						ready = len(snap.Devices) > 0 && snap.Devices[0].BackupProgress != nil && snap.Devices[0].BackupProgress.Phase == "receiving"
					case <-deadline:
						t.Fatal("tool did not report readiness")
					}
				}
				if err := app.stopBackup("CONTROL", job.id); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if !errors.Is(err, errBackupStoppedByUser) {
						t.Fatalf("wrong stop result: %v", err)
					}
				case <-deadline:
					t.Fatal("stopped process did not exit")
				}
				d := app.buildStatusSnapshot().Devices[0]
				if d.BackingUp || d.BackupState != "stopped" || !d.AutoBackupHold || d.LastBackupAt != "" || app.backupSem.inUse() != 0 {
					t.Fatalf("wrong terminal state: %+v", d)
				}
				if err := app.stopBackup("CONTROL", job.id); err != nil {
					t.Fatal("duplicate stop:", err)
				}
			})
		}
	}
}

func TestBackupControlEarlierFailureSurvivesStop(t *testing.T) {
	app := newBackupControlTestApp(t)
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	job.cancel(errDeviceDisconnected)
	if err := app.stopBackup("CONTROL", job.id); err != nil {
		t.Fatal(err)
	}
	if err := app.runBackupJob(job); !errors.Is(err, errDeviceDisconnected) {
		t.Fatalf("fault overwritten: %v", err)
	}
	if d := app.buildStatusSnapshot().Devices[0]; d.LastBackupErrorCode != "device_disconnected" || !d.AutoBackupHold {
		t.Fatalf("wrong fault state: %+v", d)
	}
}

func TestBackupControlStaleStopDoesNotCancelNewJob(t *testing.T) {
	app := newBackupControlTestApp(t)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	oldID := app.buildStatusSnapshot().Devices[0].BackupJobID
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishBackupJob(job, context.Canceled)
	if err := app.stopBackup("CONTROL", oldID); err == nil {
		t.Fatal("stale stop accepted")
	}
	if context.Cause(job.ctx) != nil || app.buildStatusSnapshot().Devices[0].AutoBackupHold {
		t.Fatal("stale request changed new task")
	}
}

func TestBackupControlLateOutputCannotChangeNextJob(t *testing.T) {
	app := newBackupControlTestApp(t)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	oldID := app.buildStatusSnapshot().Devices[0].BackupJobID
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishBackupJob(job, context.Canceled)
	app.readBackupOutput("CONTROL", oldID, strings.NewReader("Backup [====] 99%\n"), "STDOUT")
	if p := app.buildStatusSnapshot().Devices[0].BackupProgress; p.OverallPercent != nil {
		t.Fatalf("old output changed next job: %+v", p)
	}
}

func TestBackupControlCompletedTaskHasStableIdentity(t *testing.T) {
	app := newBackupControlTestApp(t)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(app.buildStatusSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Devices []struct {
			JobID string `json:"backup_job_id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 || snapshot.Devices[0].JobID == "" {
		t.Fatalf("completed task must keep its identity for late stop requests: %s", data)
	}
}
