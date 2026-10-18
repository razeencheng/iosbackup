package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBackupIndexHTTP(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	m.read = func(context.Context, backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		return indexTestOverview(), indexTestRows(2), nil
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.handleBackupIndex(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get("/api/backup-index/" + src.UDID)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitIndexJob(t, m, src.UDID)
	w = get("/api/backup-index/" + src.UDID)
	var snapshot backupIndexSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.FileList.PreviewCount != 2 {
		t.Fatal(w.Body.String())
	}
	csv := get("/api/backup-index/" + src.UDID + "?format=csv&revision=" + snapshot.Revision)
	if csv.Code != 200 || !strings.Contains(csv.Header().Get("Content-Type"), "text/csv") || csv.Header().Get("Content-Length") == "" {
		t.Fatal(csv.Code, csv.Body.String())
	}
	bad := get("/api/backup-index/" + src.UDID + "?format=csv&revision=bogus")
	if bad.Code != 409 || strings.Contains(bad.Header().Get("Content-Type"), "csv") {
		t.Fatal("invalid download accepted", bad.Code)
	}
	if w = get("/api/backup-index/UNKNOWN"); w.Code != 404 {
		t.Fatal("unknown device", w.Code)
	}
	w = httptest.NewRecorder()
	app.handleBackupIndex(w, httptest.NewRequest(http.MethodPost, "/api/backup-index/"+src.UDID, nil))
	if w.Code != 403 {
		t.Fatal("missing csrf accepted", w.Code)
	}
}
func TestBackupIndexHTTPAuthenticationAndCSRF(t *testing.T) {
	app, _, src := indexCoordinatorTestApp(t)
	app.authManager.enabled = true
	handler := app.setupRoutes()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backup-index/"+src.UDID, nil))
	if w.Code != 401 {
		t.Fatalf("authentication not enforced: %d", w.Code)
	}
}
func TestBackupIndexCSVRequiresPublishedList(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	w := httptest.NewRecorder()
	app.handleBackupIndex(w, httptest.NewRequest(http.MethodGet, "/api/backup-index/"+src.UDID+"?format=csv&revision="+strings.Repeat("a", 32), nil))
	if w.Code != 409 {
		t.Fatal("empty CSV success", w.Code)
	}
	m.mu.Lock()
	n := len(m.jobs)
	m.mu.Unlock()
	if n != 0 {
		t.Fatal("download triggered generation")
	}
}

// 删除原始目录后，即使缓存删除标记未能持久化，也不能把旧索引当成现存备份。
func TestBackupIndexConfirmedMissingHidesRetainedCache(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	candidate, err := m.store.prepare(context.Background(), src, "manifest-v1", indexTestRows(2))
	if err != nil {
		t.Fatal(err)
	}
	published, err := m.store.publish(src, indexTestOverview(), candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src.Directory); err != nil {
		t.Fatal(err)
	}
	missing, sourceErr := app.resolveBackupIndexSource(context.Background(), src.UDID)
	if !errors.Is(sourceErr, errBackupIndexMissing) {
		t.Fatal(sourceErr)
	}
	snapshot, err := m.snapshot(missing, sourceErr)
	if err != nil || snapshot.Overview.Status != "missing" || snapshot.FileList.Status != "missing" || snapshot.FileList.CSVAvailable || len(snapshot.FileList.Entries) != 0 {
		t.Fatal("deleted backup still exposed", err, snapshot.Overview.Status, snapshot.FileList.Status)
	}
	w := httptest.NewRecorder()
	app.handleBackupIndex(w, httptest.NewRequest(http.MethodGet, "/api/backup-index/"+src.UDID+"?format=csv&revision="+published.Revision, nil))
	if w.Code != http.StatusConflict {
		t.Fatal("deleted backup CSV available", w.Code)
	}
	// 无法访问挂载根目录是另一种情况，仍可查看带历史标记的缓存。
	if err := os.Rename(app.paths.BackupsRoot, app.paths.BackupsRoot+"-away"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(app.paths.BackupsRoot+"-away", app.paths.BackupsRoot)
	unavailable, sourceErr := app.resolveBackupIndexSource(context.Background(), src.UDID)
	if !errors.Is(sourceErr, errBackupIndexUnavailable) {
		t.Fatal(sourceErr)
	}
	snapshot, err = m.snapshot(unavailable, sourceErr)
	if err != nil || !snapshot.FileList.Complete || snapshot.FileList.Freshness != "stale" {
		t.Fatal("mount absence discarded history")
	}
}
