package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func queryHTTPFixture(t *testing.T) (*application, string) {
	t.Helper()
	app := newApplication()
	addOnlineDevice(app, "QUERY", connectionTypeDesc(connectTypeUSB))
	root := t.TempDir()
	app.configs["QUERY"].BackupDirectory = root
	if err := os.Mkdir(filepath.Join(root, "QUERY"), 0700); err != nil {
		t.Fatal(err)
	}
	return app, root
}

func TestBackupQueryUnknownReasonUsesLocalizedFallback(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Language", locale)
		err := &backupQueryError{diagnostic: backupQueryDiagnostic{Operation: "list", Reason: "future_reason"}}
		payload := operationErrorPayload(w, err, "api.backup_list_failed")
		if payload["message_code"] != "api.backup_query_command_failed" || payload["code"] != "api.backup_list_failed" {
			t.Fatalf("unknown reason lost stable localized fallback: %+v", payload)
		}
		if payload["diagnostic"].(backupQueryDiagnostic).Reason != "future_reason" {
			t.Fatal("raw diagnostic reason must be preserved")
		}
	}
}

func TestBackupQueryHTTP(t *testing.T) {
	for _, op := range []string{"info", "list"} {
		for _, locale := range []string{"en", "zh-CN"} {
			t.Run(op+"/"+locale, func(t *testing.T) {
				app, _ := queryHTTPFixture(t)
				app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
					io.WriteString(out, "a,domain,1\nERROR: Could not perform list (ErrorCode 207)\n")
					return errors.New("exit status 49")
				})
				r := httptest.NewRequest("GET", "/api/backup-"+op+"/QUERY", nil)
				w := httptest.NewRecorder()
				w.Header().Set("Content-Language", locale)
				if op == "info" {
					app.handleBackupInfo(w, r)
				} else {
					app.handleBackupList(w, r)
				}
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("diagnostics response can be cached")
				}
				if op == "info" {
					if w.Code != 200 || body["info_status"] != "unavailable" || body["size_human"] == nil {
						t.Fatalf("lost partial result: %s", w.Body)
					}
					body, _ = body["info_error"].(map[string]any)
				} else if w.Code != 409 || body["entries"] != nil {
					t.Fatalf("failed list became success: %s", w.Body)
				}
				if body["code"] != "api.backup_"+op+"_failed" {
					t.Fatalf("unstable code: %+v", body)
				}
				d, _ := body["diagnostic"].(map[string]any)
				if d["device_error_code"] != float64(207) || d["exit_code"] != nil {
					t.Fatalf("missing/invented codes: %+v", d)
				}
			})
		}
	}
	t.Run("confirmed_absent", func(t *testing.T) {
		app, root := queryHTTPFixture(t)
		os.Remove(filepath.Join(root, "QUERY"))
		called := false
		app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
			called = true
			return nil, errors.New("absent backup ran device command")
		}
		w := httptest.NewRecorder()
		app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY", nil))
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != 200 || body["empty"] != true || called {
			t.Fatalf("not a confirmed empty result: called=%v %s", called, w.Body)
		}
	})
	t.Run("unavailable_root", func(t *testing.T) {
		app, root := queryHTTPFixture(t)
		app.configs["QUERY"].BackupDirectory = filepath.Join(root, "missing-root")
		app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, errors.New("failed") }
		w := httptest.NewRecorder()
		app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY", nil))
		if w.Code == 200 {
			t.Fatalf("unavailable root became empty: %s", w.Body)
		}
	})
	t.Run("unsafe_id", func(t *testing.T) {
		app, _ := queryHTTPFixture(t)
		for _, id := range []string{"../QUERY", "QUERY/child", "UNKNOWN"} {
			w := httptest.NewRecorder()
			app.handleBackupInfo(w, httptest.NewRequest("GET", "/api/backup-info/"+id, nil))
			if w.Code == 200 {
				t.Fatalf("unsafe or unknown id accepted: %s", id)
			}
		}
	})
}
