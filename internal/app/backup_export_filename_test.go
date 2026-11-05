package app

import (
	"archive/tar"
	"encoding/json"
	"io"
	"iosbackup/internal/backupstore"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackupExportFilenameIdentifiesDeviceAndDate(t *testing.T) {
	for _, name := range []string{"Test Phone", "测试手机 / ..\\ \"\r\nHeader: bad", strings.Repeat("设备", 300)} {
		t.Run(name[:min(len(name), 10)], func(t *testing.T) {
			a, h, p := historyHTTPFixture(t)
			a.mu.Lock()
			a.devices[p.UDID].Name = name
			a.configs[p.UDID].Name = name
			a.mu.Unlock()
			id := exportSessionFixture(t, a, h, p.ID)
			w := historyRequest(t, a, h, "GET", "/api/backup-export/"+id+"/download", nil)
			_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if err != nil {
				t.Fatal(err)
			}
			filename := params["filename"]
			loc, _ := time.LoadLocation(p.Timezone)
			if !strings.Contains(filename, p.CompletedAt.In(loc).Format("20060102-150405")) || !strings.Contains(filename, p.ID[:8]) {
				t.Fatal("filename does not identify backup date", filename)
			}
			if strings.ContainsAny(filename, "/\\\r\n\"") || len(filename) > 240 {
				t.Fatal("unsafe filename", filename)
			}
			if name == "Test Phone" && !strings.Contains(filename, "Test Phone") {
				t.Fatal("device name omitted")
			}
			reader := tar.NewReader(w.Body)
			for {
				hdr, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(hdr.Name, p.UDID+"/") {
					t.Fatal("archive root changed", hdr.Name)
				}
			}
		})
	}
}

func TestBackupExportFilenameUnknownCompletionUsesSavedDate(t *testing.T) {
	p := backupstore.Point{ID: "0123456789abcdef", CompletionUnknown: true, CommittedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Timezone: "Asia/Shanghai"}
	got := backupExportFilename("../\r\n", p)
	if got != "iosbackup-device-saved-20261102-180000+0800-01234567.tar" {
		t.Fatal(got)
	}
}

func TestBackupExportRangeIsExplicitlyUnsupported(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	id := exportSessionFixture(t, a, h, p.ID)
	r := httptest.NewRequest("GET", "/api/backup-export/"+id+"/download", nil)
	r.Header.Set("Range", "bytes=1024-")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestedRangeNotSatisfiable || w.Header().Get("Accept-Ranges") != "none" {
		t.Fatal("resume request silently became full archive", w.Code, w.Header())
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+id, nil)
	var status map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["state"] != "ready" || status["supports_resume"] != false {
		t.Fatal("unsupported Range consumed session", status)
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+id+"/download", nil)
	if w.Code != 200 {
		t.Fatal("fresh download failed", w.Code)
	}
}
