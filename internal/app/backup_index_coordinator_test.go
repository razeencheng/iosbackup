package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func indexCoordinatorTestApp(t *testing.T) (*application, *backupIndexCoordinator, backupIndexSource) {
	t.Helper()
	app, _ := indexSourceTestApp(t)
	app.paths.ConfigsRoot = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	app.rootCtx = ctx
	m := app.backupIndexManager()
	t.Cleanup(func() { cancel(); m.close() })
	src, err := app.resolveBackupIndexSource(ctx, "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	return app, m, src
}
func waitIndexJob(t *testing.T, m *backupIndexCoordinator, id string) {
	t.Helper()
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	if j == nil {
		return
	}
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		t.Fatal("index worker did not finish")
	}
}
func TestBackupIndexCoordinatorDeduplicatesAndRetainsResults(t *testing.T) {
	_, m, src := indexCoordinatorTestApp(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	m.read = func(ctx context.Context, s backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return backupIndexOverview{}, nil, ctx.Err()
			}
		}
		return indexTestOverview(), indexTestRows(3), nil
	}
	m.request(src, false)
	<-started
	for i := 0; i < 20; i++ {
		m.request(src, false)
	}
	close(release)
	waitIndexJob(t, m, src.UDID)
	got, err := m.snapshot(src, nil)
	if err != nil || !got.FileList.Complete || calls.Load() != 1 {
		t.Fatalf("first scan: %+v %v calls=%d", got, err, calls.Load())
	}
	oldTime := got.FileList.ReadAt
	m.read = func(context.Context, backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		return indexTestOverview(), func(func([]string) error) error { return io.ErrUnexpectedEOF }, nil
	}
	m.request(src, true)
	waitIndexJob(t, m, src.UDID)
	got, err = m.snapshot(src, nil)
	if err != nil || got.Refresh.State != "failed" || !got.FileList.ReadAt.Equal(oldTime) || len(got.FileList.Entries) != 3 {
		t.Fatalf("lost old result on refresh failure: %+v %v", got, err)
	}
}
func TestBackupIndexWriterCoordination(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	m.read = func(ctx context.Context, s backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return backupIndexOverview{}, nil, ctx.Err()
	}
	m.request(src, false)
	<-started
	app.mu.Lock()
	app.backupInProgress[src.UDID] = true
	app.mu.Unlock()
	lease := app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
	select {
	case <-cancelled:
	default:
		t.Fatal("writer admitted before scan stopped")
	}
	if lease == nil {
		t.Fatal("missing writer lease")
	}
	got, _ := m.snapshot(src, nil)
	if got.Refresh.State == "running" || got.Refresh.State == "queued" {
		t.Fatal("preempted scan left refresh running")
	}
	if got.FileList.Complete {
		t.Fatal("cancelled scan published")
	}
	app.mu.Lock()
	delete(app.backupInProgress, src.UDID)
	app.mu.Unlock()
	app.finishBackupIndexWrite(src.UDID, false, false)
}
func TestBackupIndexCoordinatorClosesAndNoConnectionDependency(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	app.mu.Lock()
	app.connectionClosed = true
	app.mu.Unlock()
	m.read = func(ctx context.Context, s backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		return indexTestOverview(), indexTestRows(1), nil
	}
	m.request(src, false)
	waitIndexJob(t, m, src.UDID)
	got, _ := m.snapshot(src, nil)
	if !got.FileList.Complete {
		t.Fatal("connection service blocked local index")
	}
	m.close()
	if m.request(src, true) {
		t.Fatal("closed coordinator accepted task")
	}
}
func TestBackupIndexAfterBackupAndInvalidation(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	var calls atomic.Int32
	m.read = func(context.Context, backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		calls.Add(1)
		return indexTestOverview(), indexTestRows(2), nil
	}
	app.mu.Lock()
	app.backupInProgress[src.UDID] = true
	app.mu.Unlock()
	app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
	app.finishBackupOperation(src.UDID, nil, nowBeijing())
	waitIndexJob(t, m, src.UDID)
	if calls.Load() != 1 {
		t.Fatalf("successful backup queued %d scans", calls.Load())
	}
	app.mu.Lock()
	app.backupInProgress[src.UDID] = true
	app.mu.Unlock()
	app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
	app.finishBackupOperation(src.UDID, errors.New("failed backup"), nowBeijing())
	if calls.Load() != 1 {
		t.Fatal("failed backup rescanned")
	}
	got, _ := m.snapshot(src, nil)
	if got.FileList.Freshness != "stale" {
		t.Fatal("failed writer left cache current")
	}
}

func TestBackupIndexCoordinatorGlobalLimitAndCoalescedRefresh(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	app.mu.Lock()
	app.configs["SECOND"] = &backupConfig{UDID: "SECOND"}
	app.mu.Unlock()
	if err := os.Mkdir(filepath.Join(app.paths.BackupsRoot, "SECOND"), 0700); err != nil {
		t.Fatal(err)
	}
	other, err := app.resolveBackupIndexSource(context.Background(), "SECOND")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 4)
	release := make(chan struct{})
	var running, peak atomic.Int32
	m.read = func(ctx context.Context, s backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- s.UDID
		select {
		case <-release:
		case <-ctx.Done():
			running.Add(-1)
			return backupIndexOverview{}, nil, ctx.Err()
		}
		running.Add(-1)
		return indexTestOverview(), indexTestRows(1), nil
	}
	m.request(src, false)
	<-started
	m.request(other, false)
	for i := 0; i < 20; i++ {
		m.request(src, true)
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("queued work lost")
		}
	}
	waitIndexJob(t, m, src.UDID)
	waitIndexJob(t, m, other.UDID)
	if peak.Load() != 1 {
		t.Fatalf("concurrent scans=%d", peak.Load())
	}
}

// 完成路径必须先取得协调器锁，再释放 backupInProgress，避免下一写入被旧任务清理。
func TestBackupIndexWriterFinishBarrier(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	app.mu.Lock()
	app.backupInProgress[src.UDID] = true
	app.mu.Unlock()
	old := app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
	m.mu.Lock()
	done := make(chan struct{})
	go func() { app.finishBackupOperation(src.UDID, errors.New("old failure"), nowBeijing()); close(done) }()
	select {
	case <-done:
		m.mu.Unlock()
		t.Fatal("finish crossed coordinator barrier")
	case <-time.After(30 * time.Millisecond):
	}
	app.mu.RLock()
	busy := app.backupInProgress[src.UDID]
	app.mu.RUnlock()
	m.mu.Unlock()
	if !busy {
		t.Fatal("busy released before old lease")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finish deadlocked")
	}
	app.mu.Lock()
	app.backupInProgress[src.UDID] = true
	app.mu.Unlock()
	next := app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
	app.afterBackupIndexWrite(m, old, true)
	m.mu.Lock()
	kept := m.writes[src.UDID] == next
	queued := len(m.jobs)
	m.mu.Unlock()
	if !kept || queued != 0 {
		t.Fatalf("old completion touched new writer: kept=%v queued=%d", kept, queued)
	}
	app.finishBackupOperation(src.UDID, errors.New("cleanup"), nowBeijing())
}
