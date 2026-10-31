package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var jobTestNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func openJobTestStore(t *testing.T, root string, limits backupJobLimits) *backupJobStore {
	t.Helper()
	s, err := openBackupJobStore(root, func() time.Time { return jobTestNow }, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func jobFixture(n int) *backupJobSeed {
	return &backupJobSeed{ID: fmt.Sprintf("bkp-%012x", n), UDID: "PHONE", DeviceName: "Phone", Source: "manual", RequestedMode: "auto", StartedAt: jobTestNow.Add(time.Duration(n) * time.Minute)}
}
func syncJobs(t *testing.T, s *backupJobStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.sync(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBackupJobsPersistRestartAndPrivatePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "records")
	limits := defaultBackupJobLimits()
	s := openJobTestStore(t, root, limits)
	job := jobFixture(1)
	s.start(job)
	s.activity(job.ID, backupProgressUpdate{LastActivityAt: jobTestNow, Phase: "receiving"}, jobTestNow)
	s.finish(job.ID, "succeeded", "", jobTestNow)
	syncJobs(t, s)
	if err := s.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, job.ID+".summary.json"), filepath.Join(root, job.ID+".events.jsonl")} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if path == root {
			want = 0700
		}
		if fi.Mode().Perm() != want {
			t.Fatalf("%s mode=%v", path, fi.Mode())
		}
	}
	restored := openJobTestStore(t, root, limits)
	summary, events, ok := restored.events(job.ID, 0, 100)
	if !ok || summary.State != "succeeded" || len(events) != 3 || !summary.LastActivityAt.Equal(jobTestNow) {
		t.Fatalf("restart summary=%+v events=%v", summary, events)
	}
	_, tail, _ := restored.events(job.ID, events[len(events)-1].Cursor, 100)
	if len(tail) != 0 {
		t.Fatal("cursor duplicated events")
	}
}

func TestBackupJobsCrashAndPartialTail(t *testing.T) {
	root := t.TempDir()
	s := openJobTestStore(t, root, defaultBackupJobLimits())
	job := jobFixture(2)
	s.start(job)
	syncJobs(t, s)
	if err := s.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, job.ID+".events.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"cursor":`)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	restored := openJobTestStore(t, root, defaultBackupJobLimits())
	summary, events, _ := restored.events(job.ID, 0, 100)
	if summary.State != "interrupted" || summary.ErrorCode != "process_restarted" || !summary.Truncated || len(events) != 1 {
		t.Fatalf("crash recovery=%+v events=%v", summary, events)
	}
}

func TestBackupJobsKeepRecentEventsAndTerminalSummary(t *testing.T) {
	limits := defaultBackupJobLimits()
	limits.Events = 3
	s := openJobTestStore(t, t.TempDir(), limits)
	job := jobFixture(3)
	s.start(job)
	for i := 0; i < 10; i++ {
		s.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
	}
	s.finish(job.ID, "failed", "storage_unavailable", jobTestNow)
	syncJobs(t, s)
	summary, events, _ := s.events(job.ID, 0, 100)
	if len(events) != 3 || !summary.Truncated || summary.Dropped != 9 || summary.State != "failed" || events[2].Code != "failed" {
		t.Fatalf("bounded records=%+v events=%v", summary, events)
	}
}

func TestBackupJobsTerminalEventsVisibleBeforeDiskWorkerRuns(t *testing.T) {
	// 不启动持久化线程，模拟磁盘长时间不可用；查询仍应读到有序内存事件。
	s := &backupJobStore{records: map[string]*backupJobRecord{}, limits: defaultBackupJobLimits(), now: func() time.Time { return jobTestNow }}
	job := jobFixture(99)
	s.start(job)
	s.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
	s.finish(job.ID, backupStateFailed, "backup_failed", jobTestNow)
	summary, events, ok := s.events(job.ID, 0, 100)
	if !ok || summary.State != backupStateFailed || len(events) != 3 {
		t.Fatalf("terminal summary precedes events: %+v %+v", summary, events)
	}
	for i, code := range []string{"started", "progress", backupStateFailed} {
		if events[i].Code != code || events[i].Cursor != uint64(i+1) {
			t.Fatal("out of order", events)
		}
	}
}

func TestBackupJobsMemoryPressureAndSlowDiskDoNotBlockProducer(t *testing.T) {
	limits := defaultBackupJobLimits()
	limits.Events = 16
	s := openJobTestStore(t, t.TempDir(), limits)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.writeFile = func(path string, data []byte, mode os.FileMode) error {
		once.Do(func() { close(entered); <-release })
		return writeFileAtomic(path, data, mode)
	}
	job := jobFixture(4)
	s.start(job)
	<-entered
	produced := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			s.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
		}
		s.finish(job.ID, "stopped", "backup_stopped_by_user", jobTestNow)
		close(produced)
	}()
	select {
	case <-produced:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("producer waited for disk")
	}
	close(release)
	syncJobs(t, s)
	summary, _, _ := s.events(job.ID, 0, 100)
	if summary.State != "stopped" || !summary.Truncated || summary.Dropped == 0 || summary.Persistence != "saved" {
		t.Fatalf("queue pressure lost final summary: %+v", summary)
	}
}

func TestBackupJobsDiskFailureAndSensitiveInput(t *testing.T) {
	s := openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	s.writeFile = func(string, []byte, os.FileMode) error { return errors.New("injected disk full") }
	job := jobFixture(5)
	s.start(job)
	s.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress", Phase: "/private/photos/password=secret"})
	s.finish(job.ID, "failed", "backup_failed", jobTestNow)
	syncJobs(t, s)
	summary, events, _ := s.events(job.ID, 0, 100)
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), "secret") || summary.Persistence != "error" || !summary.Truncated || summary.State != "failed" {
		t.Fatalf("failed recorder=%+v events=%s", summary, data)
	}
}

func TestBackupJobsRetentionBudgetsProtectActiveRecords(t *testing.T) {
	for _, constraint := range []string{"count", "age", "bytes"} {
		t.Run(constraint, func(t *testing.T) {
			limits := defaultBackupJobLimits()
			if constraint == "count" {
				limits.Jobs = 1
			}
			if constraint == "bytes" {
				limits.TotalBytes = 5000
			}
			s := openJobTestStore(t, t.TempDir(), limits)
			active := jobFixture(10)
			s.start(active)
			for n := 1; n <= 3; n++ {
				job := jobFixture(n)
				s.start(job)
				at := jobTestNow.Add(time.Duration(n) * time.Minute)
				if constraint == "age" {
					at = jobTestNow.Add(-31 * 24 * time.Hour)
				}
				s.finish(job.ID, "succeeded", "", at)
			}
			syncJobs(t, s)
			if _, _, ok := s.events(active.ID, 0, 100); !ok {
				t.Fatal("budget removed an active job")
			}
			list := s.list("PHONE")
			want := 2
			if constraint == "age" {
				want = 1
			}
			if len(list) != want {
				t.Fatalf("%s count=%d want %d", constraint, len(list), want)
			}
		})
	}
}

func TestBackupJobsCorruptSummaryIsReportedAndPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bkp-000000000001.summary.json")
	if err := os.WriteFile(path, []byte(`{"unfinished":`), 0600); err != nil {
		t.Fatal(err)
	}
	s := openJobTestStore(t, root, defaultBackupJobLimits())
	syncJobs(t, s)
	if s.recordingStatus() != "partial" {
		t.Fatal("corrupt summary silently hidden")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("corrupt diagnostic evidence was deleted")
	}
}

func TestBackupJobsOrphanAndCorruptFilesHaveRetentionAndByteLimits(t *testing.T) {
	root := t.TempDir()
	limits := defaultBackupJobLimits()
	limits.TotalBytes = 8192
	old := filepath.Join(root, "bkp-000000000001.events.jsonl")
	huge := filepath.Join(root, "bkp-000000000002.summary.json")
	recent := filepath.Join(root, "bkp-000000000003.events.jsonl")
	for path, data := range map[string][]byte{old: []byte("old evidence"), huge: []byte(strings.Repeat("x", 4096)), recent: []byte("recent evidence")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		at := jobTestNow.Add(-time.Hour)
		if path == old {
			at = jobTestNow.Add(-31 * 24 * time.Hour)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	s := openJobTestStore(t, root, limits)
	syncJobs(t, s)
	for _, path := range []string{old, huge} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expired/over-budget diagnostic file retained: %s: %v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent bounded evidence lost: %v", err)
	}
	if s.recordingStatus() != "partial" {
		t.Fatal("cleanup hid diagnostic corruption")
	}
}

func TestBackupJobsActiveEventsRespectTotalBudget(t *testing.T) {
	limits := defaultBackupJobLimits()
	limits.TotalBytes = 4096
	s := openJobTestStore(t, t.TempDir(), limits)
	job := jobFixture(1)
	s.start(job)
	for n := 0; n < 200; n++ {
		s.emit(job.ID, backupJobEvent{At: jobTestNow, Code: "progress"})
	}
	syncJobs(t, s)
	entries, err := os.ReadDir(s.root)
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		bytes += info.Size()
	}
	if bytes > int64(limits.TotalBytes) {
		t.Fatalf("active job exceeded total disk budget: %d > %d", bytes, limits.TotalBytes)
	}
	summary, _, ok := s.events(job.ID, 0, 1000)
	if !ok || !summary.Truncated {
		t.Fatal("active summary must remain with truncation visible")
	}
}

func TestBackupJobsCleanupFailureStopsNewDiskWrites(t *testing.T) {
	root := t.TempDir()
	// 模拟损坏条目无法正常删除；不使用权限位，避免管理员账号下测试失真。
	orphan := filepath.Join(root, "bkp-000000000099.events.jsonl")
	if err := os.Mkdir(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "unexpected"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	old := jobTestNow.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	s := openJobTestStore(t, root, defaultBackupJobLimits())
	job := jobFixture(1)
	s.start(job)
	syncJobs(t, s)
	summary, _, ok := s.events(job.ID, 0, 100)
	if !ok || summary.Persistence != "error" || s.recordingStatus() != "partial" {
		t.Fatalf("cleanup failure hidden: %+v", summary)
	}
	for _, suffix := range []string{".summary.json", ".events.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, job.ID+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wrote new records despite failed cleanup: %v", err)
		}
	}
	if err := os.Remove(filepath.Join(orphan, "unexpected")); err != nil {
		t.Fatal(err)
	}
	syncJobs(t, s)
	summary, _, _ = s.events(job.ID, 0, 100)
	if summary.Persistence != "saved" {
		t.Fatalf("did not recover after cleanup succeeded: %+v", summary)
	}
}

func TestBackupJobsInterruptedAtomicWritesDoNotAccumulate(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, ".bkp-000000000001.events.jsonl-123456")
	if err := os.WriteFile(temporary, []byte("incomplete atomic write"), 0600); err != nil {
		t.Fatal(err)
	}
	s := openJobTestStore(t, root, defaultBackupJobLimits())
	syncJobs(t, s)
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted temporary file retained: %v", err)
	}
	if s.recordingStatus() != "partial" {
		t.Fatal("interrupted persistence must be visible")
	}
}
