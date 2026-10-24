package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// 共用任务入口必须兼容索引写入屏障，停止或失败不能发布新的完整索引。
func TestBackupControlCoordinatesIndexLifecycle(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		mode := map[bool]string{false: "manual", true: "automatic"}[automatic]
		for _, outcome := range []string{"success", "failure", "stopped"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				app := newBackupControlTestApp(t)
				app.configs["CONTROL"].AutoBackupEnabled = true
				if err := os.Mkdir(filepath.Join(app.paths.BackupsRoot, "CONTROL"), 0700); err != nil {
					t.Fatal(err)
				}
				source, err := app.resolveBackupIndexSource(context.Background(), "CONTROL")
				if err != nil {
					t.Fatal(err)
				}
				manager := app.backupIndexManager()
				var scans atomic.Int32
				manager.read = func(context.Context, backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
					scans.Add(1)
					return indexTestOverview(), indexTestRows(2), nil
				}
				manager.request(source, false)
				waitIndexJob(t, manager, "CONTROL")
				scans.Store(0)
				job, err := app.beginBackupJob("CONTROL", "", "", automatic)
				if err != nil {
					t.Fatal(err)
				}
				app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
					manager.mu.Lock()
					leased := manager.writes["CONTROL"] != nil
					manager.mu.Unlock()
					if !leased {
						t.Error("backup command started without an index write lease")
					}
					if outcome == "stopped" {
						if err := app.stopBackup("CONTROL", job.id); err != nil {
							t.Error(err)
						}
					}
					script := "printf 'Backup Successful.\\n'"
					if outcome == "failure" {
						script = "exit 2"
					}
					return newExecCmd(ctx, "/bin/sh", "-c", script)
				}
				err = app.runBackupJob(job)
				if (err == nil) != (outcome == "success") || outcome == "stopped" && !errors.Is(err, errBackupStoppedByUser) {
					t.Fatalf("unexpected %s result: %v", outcome, err)
				}
				waitIndexJob(t, manager, "CONTROL")
				wantScans := int32(0)
				wantFreshness := "stale"
				if outcome == "success" {
					wantScans, wantFreshness = 1, "current"
				}
				if scans.Load() != wantScans {
					t.Errorf("index scans=%d want=%d", scans.Load(), wantScans)
				}
				manager.mu.Lock()
				leased := manager.writes["CONTROL"] != nil
				manager.mu.Unlock()
				if leased {
					t.Error("completed backup retained its index write lease")
				}
				snapshot, _ := manager.snapshot(source, nil)
				if snapshot.FileList.Freshness != wantFreshness {
					t.Errorf("freshness=%s want=%s", snapshot.FileList.Freshness, wantFreshness)
				}
			})
		}
	}
}
