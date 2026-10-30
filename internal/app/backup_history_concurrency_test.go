package app

import (
	"context"
	"encoding/json"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestReviewHistoryPolicyConcurrentPreview(t *testing.T) {
	a, h, _ := historyHTTPFixture(t)
	requests := []map[string]any{}
	for _, days := range []int{4, 5} {
		body := map[string]any{"policy": backupstore.Policy{Enabled: true, Days: days}}
		w := historyRequest(t, a, h, "POST", "/api/backup-history/CONTROL/policy-preview", body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var result struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		body["token"] = result.Token
		requests = append(requests, body)
	}
	a.configPersistMu.Lock()
	results := make(chan int, 2)
	for _, body := range requests {
		go func(body map[string]any) {
			w := historyRequest(t, a, h, "PUT", "/api/backup-history/CONTROL/policy", body)
			results <- w.Code
		}(body)
	}
	deadline := time.After(3 * time.Second)
	for {
		s := a.historyService()
		s.mu.Lock()
		remaining := len(s.previews)
		s.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			a.configPersistMu.Unlock()
			t.Fatal("requests did not consume tokens")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	a.configPersistMu.Unlock()
	first, second := <-results, <-results
	if first == 200 && second == 200 {
		t.Fatal("both stale previews applied: concurrent settings overwrite accepted")
	}
}

type delayedDeadlineWriter struct {
	*httptest.ResponseRecorder
	cancel           context.CancelFunc
	started, release chan struct{}
	once             sync.Once
}

func (w *delayedDeadlineWriter) SetWriteDeadline(at time.Time) error {
	if !at.IsZero() && time.Until(at) < time.Second {
		w.once.Do(func() { close(w.started) })
		<-w.release
	}
	return nil
}
func (w *delayedDeadlineWriter) Flush() { w.cancel(); <-w.started }
func TestReviewDownloadJoinsDeadlineCallback(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/exports", map[string]any{})
	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/api/backup-export/"+result.ID+"/download", nil).WithContext(ctx)
	writer := &delayedDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); defer func() { recover() }(); h.ServeHTTP(writer, req) }()
	select {
	case <-writer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("no cancellation callback")
	}
	select {
	case <-done:
		close(writer.release)
		t.Fatal("download returned while cancellation callback still uses ResponseWriter")
	case <-time.After(30 * time.Millisecond):
		close(writer.release)
		<-done
	}
}
func TestReviewHistoryShutdownCancels(t *testing.T) {
	a, _, p := historyHTTPFixture(t)
	s, _ := a.backupStorage.store(p.StoreID)
	release, e := a.acquireHistoryMutation(p.UDID)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	a.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	_, e = a.startHistoryReview(s, p, release)
	if e != nil {
		release()
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("review did not start")
	}
	done := make(chan struct{})
	go func() { a.closeBackupIndex(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		service := a.historyService()
		service.mu.Lock()
		run := service.reviews[p.ID]
		run.cancel()
		service.mu.Unlock()
		<-done
		t.Fatal("coordinator shutdown failed to cancel historical scan; would wait 15 minutes")
	}
}
func TestReviewArchivedHistoryReadable(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	now := time.Now()
	a.mu.Lock()
	a.configs[p.UDID].RemovedAt = &now
	delete(a.devices, p.UDID)
	a.mu.Unlock()
	for _, url := range []string{"/api/backup-history/" + p.UDID, "/api/backup-point/" + p.ID} {
		w := historyRequest(t, a, h, "GET", url, nil)
		if w.Code != 200 {
			t.Fatal(url, w.Code, w.Body)
		}
	}
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/exports", map[string]any{})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var exported struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &exported)
	w = historyRequest(t, a, h, "DELETE", "/api/backup-export/"+exported.ID, map[string]any{})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestHistoryDeleteExcludesNewReaders(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	type result struct {
		lease *backupstore.Lease
		err   error
	}
	attempts := make(chan result, 1)
	early := false
	a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		go func() { lease, e := s.Acquire(p.ID); attempts <- result{lease, e} }()
		select {
		case r := <-attempts:
			early = true
			attempts <- r
		case <-time.After(30 * time.Millisecond):
		}
		return persistence.WriteFileAtomic(path, data, mode)
	}
	w := historyRequest(t, a, h, "DELETE", "/api/backup-point/"+p.ID, map[string]any{"revision": c.Revision, "confirm": p.ID})
	a.backupStorage.write = nil
	r := <-attempts
	if r.lease != nil {
		r.lease.Close()
	}
	if early || r.lease != nil || w.Code != 200 {
		t.Fatalf("reader entered deletion intent: early=%v lease=%v status=%d", early, r.lease != nil, w.Code)
	}
}

func TestHistoryShutdownCancels(t *testing.T) {
	a, _, p := historyHTTPFixture(t)
	s, _ := a.backupStorage.store(p.StoreID)
	release, e := a.acquireHistoryMutation(p.UDID)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	a.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	_, e = a.startHistoryReview(s, p, release)
	if e != nil {
		release()
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("review did not start")
	}
	done := make(chan struct{})
	go func() { a.closeBackupIndex(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		service := a.historyService()
		service.mu.Lock()
		run := service.reviews[p.ID]
		run.cancel()
		service.mu.Unlock()
		<-done
		t.Fatal("coordinator shutdown failed to cancel historical scan; would wait 15 minutes")
	}
}
func TestArchivedHistoryReadable(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	now := time.Now()
	a.mu.Lock()
	a.configs[p.UDID].RemovedAt = &now
	delete(a.devices, p.UDID)
	a.mu.Unlock()
	for _, url := range []string{"/api/backup-history/" + p.UDID, "/api/backup-point/" + p.ID} {
		w := historyRequest(t, a, h, "GET", url, nil)
		if w.Code != 200 {
			t.Fatal(url, w.Code, w.Body)
		}
	}
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/exports", map[string]any{})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var exported struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &exported)
	w = historyRequest(t, a, h, "DELETE", "/api/backup-export/"+exported.ID, map[string]any{})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
}
