package app

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestBackupDirectoryChangeRejectsBusyDevice(t *testing.T) {
	for _, operation := range []string{"backup", "check"} {
		t.Run(operation, func(t *testing.T) {
			app := newBackupControlTestApp(t)
			before := app.configs["CONTROL"].BackupDirectory
			if operation == "backup" {
				app.backupInProgress["CONTROL"] = true
			} else {
				app.checkInProgress["CONTROL"] = true
			}
			body, _ := json.Marshal(map[string]any{"backup_directory": filepath.Join(app.paths.BackupsRoot, "other")})
			response := backupControlRequest(t, app, "/api/save-config/CONTROL", string(body))
			if response.Code != http.StatusConflict {
				t.Fatalf("busy directory change accepted: %d %s", response.Code, response.Body.String())
			}
			if app.configs["CONTROL"].BackupDirectory != before {
				t.Fatal("directory changed")
			}
			response = backupControlRequest(t, app, "/api/save-config/CONTROL", `{"min_battery_level":42}`)
			if response.Code != http.StatusOK {
				t.Fatalf("unrelated settings blocked: %d", response.Code)
			}
		})
	}
}
