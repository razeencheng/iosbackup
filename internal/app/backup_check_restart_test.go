package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iosbackup/internal/backupstore"
)

func TestCurrentHistoryCheckAfterRestart(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted-jobs", true: "expired-jobs"}[expired], func(t *testing.T) {
			a := historyTestApp(t)
			root := filepath.Join(t.TempDir(), "jobs")
			a.backupJobRecords = openJobTestStore(t, root, defaultBackupJobLimits())
			old := jobFixture(1)
			old.UDID = "CONTROL"
			old.Operation = "backup"
			a.backupJobRecords.start(old)
			a.backupJobRecords.setCheck(old.ID, backupCheckResult{ID: old.ID, Kind: "quick", State: "passed", Freshness: "current", StartedAt: old.StartedAt, CompletedAt: old.StartedAt, MetadataComplete: true})
			a.backupJobRecords.finish(old.ID, "succeeded", "", old.StartedAt)
			historyCommand(t, a, "finished", "first", 0, nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			before := a.buildStatusSnapshot().Devices[0].BackupCheck
			if before == nil {
				t.Fatal("missing fresh check")
			}
			syncJobs(t, a.backupJobRecords)
			if err := a.backupJobRecords.close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if expired {
				root = filepath.Join(t.TempDir(), "expired-jobs")
			}
			a.backupJobRecords = openJobTestStore(t, root, defaultBackupJobLimits())
			a.mu.Lock()
			a.backupCheckResults = nil
			a.mu.Unlock()
			after := a.buildStatusSnapshot().Devices[0].BackupCheck
			if after == nil || after.ID != before.ID || !after.CompletedAt.Equal(before.CompletedAt) || after.State != before.State {
				t.Fatalf("restart selected unrelated/absent check: before=%+v after=%+v", before, after)
			}
			if after.Freshness != "unknown" {
				t.Fatal("restart fabricated a fresh check", after.Freshness)
			}
		})
	}
}

func TestRestartDoesNotSelectOtherHistoricalPointCheck(t *testing.T) {
	a := historyTestApp(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	historyCommand(t, a, "finished", "first", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	st, _ := a.backupStorage.deviceState("CONTROL")
	first := st.LatestPoint
	historyCommand(t, a, "finished", "second", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	want := a.buildStatusSnapshot().Devices[0].BackupCheck
	review := jobFixture(999)
	review.UDID = "CONTROL"
	review.Operation = "check"
	review.PointID = first
	review.StartedAt = time.Now().Add(time.Minute)
	a.backupJobRecords.start(review)
	a.backupJobRecords.setCheck(review.ID, backupCheckResult{ID: review.ID, Kind: "manifest", State: "issues", Reason: "missing_objects", StartedAt: review.StartedAt, CompletedAt: review.StartedAt, Freshness: "current"})
	a.backupJobRecords.finish(review.ID, "succeeded", "", review.StartedAt)
	a.mu.Lock()
	a.backupCheckResults = nil
	a.mu.Unlock()
	got := a.buildStatusSnapshot().Devices[0].BackupCheck
	if got == nil || got.ID != want.ID {
		t.Fatalf("unrelated history review replaced current point evidence: %+v", got)
	}
}

func TestRestartRetainsCancelledCurrentPointAttempt(t *testing.T) {
	a := historyTestApp(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	historyCommand(t, a, "finished", "first", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	completed := a.buildStatusSnapshot().Devices[0].BackupCheck
	job := jobFixture(2000)
	job.UDID = "CONTROL"
	job.Operation = "check"
	job.StartedAt = time.Now().Add(time.Minute)
	a.backupJobRecords.start(job)
	attempt := *completed
	attempt.ID = job.ID
	attempt.Kind = "manifest"
	attempt.State = "unavailable"
	attempt.Reason = "cancelled"
	attempt.StartedAt = job.StartedAt
	attempt.CompletedAt = job.StartedAt
	a.backupJobRecords.setCheck(job.ID, attempt)
	a.backupJobRecords.finish(job.ID, "stopped", "", job.StartedAt)
	a.mu.Lock()
	a.backupCheckResults = nil
	a.mu.Unlock()
	got := a.buildStatusSnapshot().Devices[0].BackupCheck
	if got == nil || got.ID != job.ID || got.LastCompleted == nil || got.LastCompleted.ID != completed.ID || got.LastCompleted.SourceRef != got.SourceRef {
		t.Fatalf("attempt or source-bound completed evidence lost: %+v", got)
	}
	if got.Freshness != "unknown" || got.LastCompleted.Freshness == "current" {
		t.Fatal("restart promoted cached freshness")
	}
}

func TestRestartDropsDeletedOrChangedSourceEvidence(t *testing.T) {
	for _, action := range []string{"deleted", "different-root", "disabled-history"} {
		t.Run(action, func(t *testing.T) {
			a := historyTestApp(t)
			a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
			historyCommand(t, a, "finished", "first", 0, nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "deleted":
				st, _ := a.backupStorage.deviceState("CONTROL")
				st.CurrentDeleted = true
				if err := a.backupStorage.saveDevice("CONTROL", st); err != nil {
					t.Fatal(err)
				}
			case "different-root":
				a.mu.Lock()
				a.configs["CONTROL"].BackupDirectory = t.TempDir()
				a.mu.Unlock()
			case "disabled-history":
				if err := a.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: false, Count: 3}, nil); err != nil {
					t.Fatal(err)
				}
			}
			// 不仅重启，来源改变后的运行期缓存也不能继续冒充当前检查。
			if got := a.buildStatusSnapshot().Devices[0].BackupCheck; got != nil {
				t.Fatalf("previous source still current: %+v", got)
			}
			a.mu.Lock()
			a.backupCheckResults = nil
			a.mu.Unlock()
			if got := a.buildStatusSnapshot().Devices[0].BackupCheck; got != nil {
				t.Fatalf("restart selected previous source: %+v", got)
			}
		})
	}
}

func TestRestartLegacyCurrentPointManualCheckMatchesEvidence(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-source", false: "other-source"}[match], func(t *testing.T) {
			a := historyTestApp(t)
			a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
			historyCommand(t, a, "finished", "first", 0, nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			current := *a.buildStatusSnapshot().Devices[0].BackupCheck
			originalID := current.ID
			job := jobFixture(2200)
			job.UDID = "CONTROL"
			job.Operation = "check"
			job.StartedAt = time.Now().Add(time.Minute)
			a.backupJobRecords.start(job)
			current.SourceRef = ""
			if !match {
				current.SourceIdentity = indexDigest("other-source")
			}
			current.ID = job.ID
			current.Kind = "manifest"
			current.State = "issues"
			current.Reason = "missing_objects"
			current.MissingObjects = 1
			current.StartedAt = job.StartedAt
			current.CompletedAt = job.StartedAt
			a.backupJobRecords.setCheck(job.ID, current)
			a.backupJobRecords.finish(job.ID, "succeeded", "", job.StartedAt)
			a.mu.Lock()
			a.backupCheckResults = nil
			a.mu.Unlock()
			got := a.buildStatusSnapshot().Devices[0].BackupCheck
			if match && (got == nil || got.ID != job.ID || got.State != "issues") {
				t.Fatalf("legacy issue replaced by older quick check: %+v", got)
			}
			if !match && (got == nil || got.ID != originalID) {
				t.Fatalf("other source evidence selected: %+v", got)
			}
		})
	}
}

func TestRestartLegacyCurrentPointChangedMetadataIssueNotHidden(t *testing.T) {
	a := historyTestApp(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	historyCommand(t, a, "finished", "first", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	prior := *a.buildStatusSnapshot().Devices[0].BackupCheck
	src, err := a.resolveBackupIndexSource(context.Background(), "CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src.Directory, "Status.plist")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`<plist><dict><key>SnapshotState</key><string>in_progress</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	current := a.checkBackupSourceAt(context.Background(), "CONTROL", filepath.Dir(src.Directory), "manifest")
	current.SourceKey = prior.SourceKey // 旧版本以配置根目录标记当前检查来源。
	if current.State != "issues" || current.SourceIdentity != prior.SourceIdentity || current.SourceFingerprint == prior.SourceFingerprint {
		t.Fatalf("bad reproduction: prior=%+v current=%+v", prior, current)
	}
	job := jobFixture(2300)
	job.UDID = "CONTROL"
	job.Operation = "check"
	job.StartedAt = time.Now().Add(time.Minute)
	a.backupJobRecords.start(job)
	current.SourceRef = ""
	current.ID = job.ID
	current.StartedAt = job.StartedAt
	current.CompletedAt = job.StartedAt
	a.backupJobRecords.setCheck(job.ID, current)
	a.backupJobRecords.finish(job.ID, "succeeded", "", job.StartedAt)
	a.mu.Lock()
	a.backupCheckResults = nil
	a.mu.Unlock()
	got := a.buildStatusSnapshot().Devices[0].BackupCheck
	if got == nil || got.ID != job.ID || got.State != "issues" || got.Freshness != "unknown" {
		t.Fatalf("known same-source metadata issue replaced by old pass: %+v", got)
	}
}
