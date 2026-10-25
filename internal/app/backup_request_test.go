package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackupRejectsUnsupportedModesBeforeAdmission(t *testing.T) {
	app := newApplication()
	for _, body := range []string{`{"mode":"incremental"}`, `{"mode":"--full; dangerous"}`, `{"mode":4}`, `{"mode":"auto","extra":true}`, `null`, `{"mode":"full"} {}`} {
		rr := httptest.NewRecorder()
		app.handleBackup(rr, httptest.NewRequest(http.MethodPost, "/api/backup/PHONE", strings.NewReader(body)))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %q status=%d want 400", body, rr.Code)
		}
	}
}

func TestBackupHTTPModeAndTaskIdentity(t *testing.T) {
	for _, mode := range []string{"auto", "full"} {
		t.Run(mode, func(t *testing.T) {
			app := timeZoneApp(t, "UTC")
			t.Cleanup(app.closeBackupIndex)
			app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: true, IsCharging: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeUSB)}
			cfg := app.defaultBackupConfig("PHONE", "Phone")
			app.configs["PHONE"] = cfg
			gotArgs := make(chan []string, 1)
			release := make(chan struct{})
			app.backupCommand = func(ctx context.Context, name string, args ...string) *execCmd {
				gotArgs <- args
				<-release
				return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
			}
			t.Cleanup(func() {
				close(release)
				deadline := time.Now().Add(3 * time.Second)
				for app.backupSem.inUse() != 0 {
					if time.Now().After(deadline) {
						t.Error("task did not release")
						return
					}
					time.Sleep(time.Millisecond)
				}
			})
			rr := httptest.NewRecorder()
			app.handleBackup(rr, httptest.NewRequest(http.MethodPost, "/api/backup/PHONE", strings.NewReader(`{"mode":"`+mode+`"}`)))
			if rr.Code != 202 {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			jobID, ok := body["backup_job_id"].(string)
			if !ok || jobID == "" {
				t.Error("accepted request needs a job ID before execution")
			}
			select {
			case args := <-gotArgs:
				if strings.Contains(strings.Join(args, " "), "--full") != (mode == "full") {
					t.Errorf("requested %s got args=%v", mode, args)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("command did not start")
			}
			data, _ := json.Marshal(app.buildStatusSnapshot().Devices[0])
			var state map[string]any
			_ = json.Unmarshal(data, &state)
			if state["backup_job_id"] != jobID || state["requested_mode"] != mode || state["observed_mode"] != "unknown" {
				t.Errorf("task identity/mode state=%s", data)
			}
		})
	}
}
