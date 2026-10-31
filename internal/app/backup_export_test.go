package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupExportBusyAttemptVisibleInStatus(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	id := exportSessionFixture(t, a, h, p.ID)
	service := a.historyService()
	service.exportSlot <- struct{}{}
	w := historyRequest(t, a, h, "GET", "/api/backup-export/"+id+"/download", nil)
	if w.Code != http.StatusConflict {
		t.Fatal(w.Code)
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+id, nil)
	var view map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	<-service.exportSlot
	if view["download_error"] != "busy" {
		t.Fatal("hidden download response has no visible status", view)
	}
}

func TestBackupExportEvictsOldTerminalSessions(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	service := a.historyService()
	service.mu.Lock()
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("%032x", i+1)
		done := make(chan struct{})
		close(done)
		service.exports[id] = &backupExport{ID: id, State: "transferred", Finished: time.Now().Add(-time.Duration(64-i) * time.Second), done: done, cancel: func() {}}
	}
	service.mu.Unlock()
	id := exportSessionFixture(t, a, h, p.ID)
	if id == "" {
		t.Fatal("no new session")
	}
	w := historyRequest(t, a, h, "GET", "/api/backup-export/"+fmt.Sprintf("%032x", 1), nil)
	if w.Code != http.StatusGone {
		t.Fatal("old terminal session not expired", w.Code)
	}
}

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
