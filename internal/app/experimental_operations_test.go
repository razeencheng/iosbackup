package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const experimentalTestUDID = "EXPERIMENTAL-DEVICE"

func newExperimentalOperationsTestApp(t *testing.T, enabled bool) (*application, string) {
	t.Helper()
	root := t.TempDir()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = filepath.Join(root, "configs")
	cfg.BackupsRoot = filepath.Join(root, "backups")
	cfg.EnableExperimentalOperations = enabled
	app := newApplicationWithRuntime(context.Background(), cfg)
	if err := os.MkdirAll(cfg.ConfigsRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(cfg.BackupsRoot, experimentalTestUDID)
	if err := os.MkdirAll(backupPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "Manifest.db"), append([]byte("SQLite format 3\x00"), make([]byte, 32)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "Info.plist"), []byte("test backup marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "sentinel"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	lastBackup := time.Date(2026, 8, 31, 10, 30, 0, 0, beijingLocation)
	app.devices[experimentalTestUDID] = &device{
		UDID:       experimentalTestUDID,
		Name:       "实验设备",
		IsOnline:   true,
		LastBackup: lastBackup,
	}
	app.configs[experimentalTestUDID] = &backupConfig{
		UDID:            experimentalTestUDID,
		Name:            "实验设备",
		BackupDirectory: cfg.BackupsRoot,
		LastBackup:      lastBackup,
		RestoreEnabled:  true,
	}
	return app, backupPath
}

func experimentalOperationRequest(app *application, handler http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(csrfHeader, app.csrfManager.Token())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func assertExperimentalDisabledResponse(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusForbidden {
		t.Fatalf("disabled experimental operation status=%d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q, want application/json", got)
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("response is not stable JSON: %v; body=%q", err, rr.Body.String())
	}
	if response.Error != experimentalOperationsDisabledMessage {
		t.Fatalf("error=%q, want %q", response.Error, experimentalOperationsDisabledMessage)
	}
}

func directoryEntryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestExperimentalOperationsDisabledBeforeBackupStateOrFilesystemAccess(t *testing.T) {
	t.Run("unpack", func(t *testing.T) {
		app, backupPath := newExperimentalOperationsTestApp(t, false)
		before := directoryEntryNames(t, backupPath)
		streamStarted := make(chan struct{}, 1)
		app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error {
			streamStarted <- struct{}{}
			return nil
		}

		rr := experimentalOperationRequest(app, app.handleBackupUnback, "/api/backup-unback/"+experimentalTestUDID, "")
		assertExperimentalDisabledResponse(t, rr)

		select {
		case <-streamStarted:
			t.Fatal("disabled unpack must not start its background goroutine")
		case <-time.After(100 * time.Millisecond):
		}
		if after := directoryEntryNames(t, backupPath); !reflect.DeepEqual(after, before) {
			t.Fatalf("disabled unpack changed backup directory: before=%v after=%v", before, after)
		}
		app.mu.RLock()
		busy := app.backupInProgress[experimentalTestUDID]
		app.mu.RUnlock()
		if busy {
			t.Fatal("disabled unpack must not acquire the backup operation slot")
		}
	})

	t.Run("delete local backup", func(t *testing.T) {
		app, backupPath := newExperimentalOperationsTestApp(t, false)
		before := directoryEntryNames(t, backupPath)
		lastBackup := app.configs[experimentalTestUDID].LastBackup

		rr := experimentalOperationRequest(
			app,
			app.handleDeleteBackup,
			"/api/delete-backup/"+experimentalTestUDID,
			`{"confirm_name":"实验设备"}`,
		)
		assertExperimentalDisabledResponse(t, rr)

		if after := directoryEntryNames(t, backupPath); !reflect.DeepEqual(after, before) {
			t.Fatalf("disabled delete changed backup directory: before=%v after=%v", before, after)
		}
		if got := app.configs[experimentalTestUDID].LastBackup; !got.Equal(lastBackup) {
			t.Fatalf("disabled delete changed LastBackup: got=%s want=%s", got, lastBackup)
		}
		if data, err := os.ReadFile(filepath.Join(backupPath, "sentinel")); err != nil || !bytes.Equal(data, []byte("keep")) {
			t.Fatalf("disabled delete touched backup data: data=%q err=%v", data, err)
		}
		app.mu.RLock()
		busy := app.backupInProgress[experimentalTestUDID]
		app.mu.RUnlock()
		if busy {
			t.Fatal("disabled delete must not acquire the backup operation slot")
		}
	})
}

func TestExperimentalOperationsPreserveMethodAndCSRFPrecedence(t *testing.T) {
	app := newApplication()
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		path       string
		method     string
		csrf       bool
		wantStatus int
		wantBody   string
	}{
		{name: "unpack method", handler: app.handleBackupUnback, path: "/api/backup-unback/x", method: http.MethodGet, csrf: true, wantStatus: http.StatusMethodNotAllowed, wantBody: "Method not allowed"},
		{name: "delete method", handler: app.handleDeleteBackup, path: "/api/delete-backup/x", method: http.MethodGet, csrf: true, wantStatus: http.StatusMethodNotAllowed, wantBody: "Method not allowed"},
		{name: "unpack csrf", handler: app.handleBackupUnback, path: "/api/backup-unback/x", method: http.MethodPost, wantStatus: http.StatusForbidden, wantBody: "缺少 CSRF 头"},
		{name: "delete csrf", handler: app.handleDeleteBackup, path: "/api/delete-backup/x", method: http.MethodPost, wantStatus: http.StatusForbidden, wantBody: "缺少 CSRF 头"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.csrf {
				req.Header.Set(csrfHeader, app.csrfManager.Token())
				req.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			rr := httptest.NewRecorder()
			tt.handler(rr, req)
			if rr.Code != tt.wantStatus || !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("status=%d body=%q, want status=%d containing %q", rr.Code, rr.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestExperimentalOperationsEnabledPreservesExistingEndpointBehavior(t *testing.T) {
	t.Run("unpack starts and publishes", func(t *testing.T) {
		app, backupPath := newExperimentalOperationsTestApp(t, true)
		app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error { return nil }
		rr := experimentalOperationRequest(app, app.handleBackupUnback, "/api/backup-unback/"+experimentalTestUDID, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "解包已开始") {
			t.Fatalf("enabled unpack changed response: status=%d body=%s", rr.Code, rr.Body.String())
		}
		marker := filepath.Join(backupPath, "_unback_", ".iosbackup-unback.json")
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("enabled unpack did not publish its completion marker: %s", marker)
			}
			time.Sleep(5 * time.Millisecond)
		}
	})

	t.Run("delete removes local backup and resets state", func(t *testing.T) {
		app, backupPath := newExperimentalOperationsTestApp(t, true)
		rr := experimentalOperationRequest(
			app,
			app.handleDeleteBackup,
			"/api/delete-backup/"+experimentalTestUDID,
			`{"confirm_name":"实验设备"}`,
		)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "备份已删除") {
			t.Fatalf("enabled delete changed response: status=%d body=%s", rr.Code, rr.Body.String())
		}
		if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
			t.Fatalf("enabled delete did not remove backup path: %v", err)
		}
		if got := app.configs[experimentalTestUDID].LastBackup; !got.IsZero() {
			t.Fatalf("enabled delete did not reset LastBackup: %s", got)
		}
	})
}

func TestHomePageOnlyRendersExperimentalControlsWhenEnabled(t *testing.T) {
	dev := &device{UDID: experimentalTestUDID, Name: "实验设备", DeviceType: "iPhone", IsOnline: true}
	cfg := &backupConfig{UDID: experimentalTestUDID, Name: dev.Name, RestoreEnabled: true}
	payload := homePayload{
		Devices:        []*device{dev},
		Configs:        map[string]*backupConfig{experimentalTestUDID: cfg},
		BackupStatuses: map[string]bool{experimentalTestUDID: false},
	}

	disabled := renderIndex(t, payload)
	for _, hidden := range []string{`id="unbackBtn-` + experimentalTestUDID + `"`, `id="delBtn-` + experimentalTestUDID + `"`} {
		if strings.Contains(disabled, hidden) {
			t.Fatalf("default UI rendered disabled experimental control %q", hidden)
		}
	}
	for _, retained := range []string{
		`href="/api/backup-list/` + experimentalTestUDID + `?format=csv"`,
		`onclick="loadBackups('` + experimentalTestUDID + `')"`,
		`openRestoreModal('` + experimentalTestUDID + `'`,
		`data-sec="remove"`,
	} {
		if !strings.Contains(disabled, retained) {
			t.Fatalf("default UI hid retained non-experimental control %q", retained)
		}
	}

	payload.ExperimentalOperationsEnabled = true
	enabled := renderIndex(t, payload)
	for _, visible := range []string{`id="unbackBtn-` + experimentalTestUDID + `"`, `id="delBtn-` + experimentalTestUDID + `"`} {
		if !strings.Contains(enabled, visible) {
			t.Fatalf("enabled UI did not render experimental control %q", visible)
		}
	}
}
