package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func exportSessionFixture(t *testing.T, a *application, h http.Handler, point string) string {
	t.Helper()
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+point+"/exports", map[string]any{})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var v struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.ID
}
func TestBackupExportHTTPExpiryCancellationAndLimit(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	id := exportSessionFixture(t, a, h, p.ID)
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/exports", map[string]any{})
	if w.Code != 409 {
		t.Fatal("duplicate export admitted", w.Code)
	}
	w = historyRequest(t, a, h, "DELETE", "/api/backup-export/"+id, map[string]any{})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	s, _ := a.backupStorage.store(p.StoreID)
	used, e := s.InUse(p.ID)
	if e != nil || used {
		t.Fatal("cancel leaked lease", e)
	}
	id = exportSessionFixture(t, a, h, p.ID)
	svc := a.historyService()
	svc.mu.Lock()
	ex := svc.exports[id]
	ex.timer.Reset(time.Millisecond)
	svc.mu.Unlock()
	select {
	case <-ex.done:
	case <-time.After(time.Second):
		t.Fatal("expiration did not release")
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+id+"/download", nil)
	if w.Code != 410 {
		t.Fatal("expired export downloaded", w.Code)
	}
}
func TestBackupExportHTTPDisconnect(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	s, _ := a.backupStorage.store(p.StoreID)
	l, e := s.Acquire(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(filepath.Join(l.Directory, "large"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(64 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	l.Close()
	id := exportSessionFixture(t, a, h, p.ID)
	server := httptest.NewServer(h)
	defer server.Close()
	res, e := server.Client().Get(server.URL + "/api/backup-export/" + id + "/download")
	if e != nil {
		t.Fatal(e)
	}
	buffer := make([]byte, 2048)
	if _, e = io.ReadFull(res.Body, buffer); e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	svc := a.historyService()
	svc.mu.Lock()
	ex := svc.exports[id]
	svc.mu.Unlock()
	select {
	case <-ex.done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not release point")
	}
	svc.mu.Lock()
	state := ex.State
	svc.mu.Unlock()
	if state != "cancelled" && state != "failed" {
		t.Fatal("partial download reported success", state)
	}
	used, e := s.InUse(p.ID)
	if e != nil || used {
		t.Fatal("disconnect left point occupied", e)
	}
}
func TestBackupExportRejectsMissingRequiredFile(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	s, _ := a.backupStorage.store(p.StoreID)
	l, e := s.Acquire(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(l.Directory, "Manifest.db")); e != nil {
		t.Fatal(e)
	}
	l.Close()
	id := exportSessionFixture(t, a, h, p.ID)
	server := httptest.NewServer(h)
	defer server.Close()
	res, e := server.Client().Get(server.URL + "/api/backup-export/" + id + "/download")
	if e == nil {
		_, e = io.ReadAll(res.Body)
		res.Body.Close()
	}
	if e == nil {
		t.Fatal("missing-file stream ended normally")
	}
	svc := a.historyService()
	svc.mu.Lock()
	ex := svc.exports[id]
	svc.mu.Unlock()
	select {
	case <-ex.done:
	case <-time.After(time.Second):
		t.Fatal("failed export not finished")
	}
	svc.mu.Lock()
	state := ex.State
	svc.mu.Unlock()
	if state != "failed" {
		t.Fatal(state)
	}
}
