package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackupJobsEndpointEmptyAndProtected(t *testing.T) {
	app, h := backupControlFixture(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/backup-jobs/PHONE", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"jobs":[]`) {
		t.Fatalf("empty tasks: %d %s", rr.Code, rr.Body.String())
	}
	app.authManager = testAuthManager("test-password")
	h = app.setupRoutes()
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/backup-jobs/PHONE", nil))
	if rr.Code != 401 {
		t.Fatalf("task history must require authentication: %d", rr.Code)
	}
}

func TestBackupJobsHTTPPagingAndExport(t *testing.T) {
	app, h := backupControlFixture(t)
	limits := defaultBackupJobLimits()
	limits.Events = 3
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), limits)
	job := jobFixture(7)
	app.backupJobRecords.start(job)
	for i := 0; i < 5; i++ {
		app.backupJobRecords.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
	}
	app.backupJobRecords.finish(job.ID, "succeeded", "", jobTestNow)
	syncJobs(t, app.backupJobRecords)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/backup-job/"+job.ID+"/events?after=0&limit=2", nil))
	var page struct {
		Job       backupJobSummary `json:"job"`
		Events    []backupJobEvent `json:"events"`
		Next      uint64           `json:"next_cursor"`
		More      bool             `json:"has_more"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || len(page.Events) != 2 || !page.More || !page.Truncated || page.Job.FirstCursor != 5 || page.Next != 6 {
		t.Fatalf("bad cursor page: %d %+v", rr.Code, page)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/backup-job/"+job.ID+"/export", nil))
	if rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" || len(strings.Split(strings.TrimSpace(rr.Body.String()), "\n")) != 4 || !strings.Contains(rr.Body.String(), `"truncated":true`) {
		t.Fatalf("bad export: %d %s", rr.Code, rr.Body.String())
	}
	for path, status := range map[string]int{
		"/api/backup-job/" + job.ID + "/events?after=-1":  400,
		"/api/backup-job/" + job.ID + "/events?limit=501": 400,
		"/api/backup-job/bkp-000000000099/events":         410,
		"/api/backup-job/invalid/export":                  404,
	} {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != status {
			t.Errorf("%s status=%d want=%d", path, rr.Code, status)
		}
	}
}

type blockedJobDownload struct {
	header           http.Header
	entered, release chan struct{}
	once             sync.Once
}

func (w *blockedJobDownload) Header() http.Header { return w.header }
func (w *blockedJobDownload) WriteHeader(int)     {}
func (w *blockedJobDownload) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return len(data), nil
}

func TestBackupJobsSlowDownloadDoesNotBlockTaskProgress(t *testing.T) {
	app, h := backupControlFixture(t)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	job := jobFixture(8)
	app.backupJobRecords.start(job)
	syncJobs(t, app.backupJobRecords)
	writer := &blockedJobDownload{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/api/backup-job/"+job.ID+"/export", nil))
		close(done)
	}()
	<-writer.entered
	progressed := make(chan struct{})
	go func() {
		app.backupJobRecords.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
		app.backupJobRecords.finish(job.ID, "succeeded", "", jobTestNow)
		close(progressed)
	}()
	select {
	case <-progressed:
	case <-time.After(time.Second):
		close(writer.release)
		<-done
		t.Fatal("slow download blocked task producer")
	}
	close(writer.release)
	<-done
	summary, _, _ := app.backupJobRecords.events(job.ID, 0, 100)
	if summary.State != "succeeded" {
		t.Fatalf("missing task completion: %+v", summary)
	}
}

func backupControlFixture(t *testing.T) (*application, http.Handler) {
	t.Helper()
	app := timeZoneApp(t, "UTC")
	app.devices["PHONE"] = &device{UDID: "PHONE", Name: "Phone", IsOnline: true, IsCharging: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeUSB)}
	cfg := app.defaultBackupConfig("PHONE", "Phone")
	cfg.AutoBackupEnabled = true
	app.configs["PHONE"] = cfg
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	t.Cleanup(func() { app.closeBackupIndex() })
	return app, app.setupRoutes()
}
