package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func completedHeldBackup(t *testing.T) (*application, string) {
	t.Helper()
	app := newBackupControlTestApp(t)
	app.configs["CONTROL"].AutoBackupEnabled = true
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	id := app.buildStatusSnapshot().Devices[0].BackupJobID
	if err := app.stopBackup("CONTROL", id); err != nil {
		t.Fatal(err)
	}
	return app, id
}

func TestBackupHoldPostReplaceFailureReportsUncertaintyAndResumeRepairs(t *testing.T) {
	app, id := completedHeldBackup(t)
	// 模拟原子替换已完成，但父目录同步报错；主文件与 last-good 均可能已改变。
	app.configStore.writeAtomic = func(path string, data []byte, mode os.FileMode) error {
		if err := writeFileAtomic(path, data, mode); err != nil {
			return err
		}
		if path == app.configStore.path {
			return errors.New("injected directory sync failure after replacement")
		}
		return nil
	}
	if err := app.persistBackupHold("CONTROL", id, true); err == nil {
		t.Fatal("uncertain resume reported success")
	}
	d := app.buildStatusSnapshot().Devices[0]
	if !d.AutoBackupHold || d.AutoBackupHoldPersistence != "error" || len(app.autoBackupCandidates()) != 0 {
		t.Fatalf("lost memory barrier: %+v", d)
	}
	for _, path := range []string{app.configStore.path, app.configStore.lastGoodPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var disk backupConfigEnvelope
		if err := json.Unmarshal(data, &disk); err != nil {
			t.Fatal(err)
		}
		if !disk.Configs["CONTROL"].AutoBackupHold {
			t.Fatalf("repair left hold cleared in %s", path)
		}
	}
	restart := newApplicationWithRuntime(context.Background(), app.runtimeConfig)
	if err := restart.loadConfigs(); err != nil {
		t.Fatal(err)
	}
	if !restart.configs["CONTROL"].AutoBackupHold {
		t.Fatal("reloaded repaired configuration lost hold")
	}
	app.configStore.writeAtomic = nil
	if err := app.persistBackupHold("CONTROL", id, false); err != nil {
		t.Fatal(err)
	}
	if err := app.persistBackupHold("CONTROL", id, true); err != nil {
		t.Fatal(err)
	}
	if len(app.autoBackupCandidates()) != 1 {
		t.Fatal("resumed device did not return to scheduler candidates")
	}
}

func TestBackupHoldBlocksSchedulerCandidatesAndChecks(t *testing.T) {
	app, _ := completedHeldBackup(t)
	if len(app.autoBackupCandidates()) != 0 {
		t.Error("held device remains a scheduler candidate")
	}
	if app.tryBeginAutoBackupCheck("CONTROL") {
		t.Error("held device acquired an automatic check")
	}
}

func TestBackupHoldBlocksAlreadyCheckingScheduler(t *testing.T) {
	app, _ := completedHeldBackup(t)
	app.checkInProgress["CONTROL"] = true
	if job, err := app.beginBackupJob("CONTROL", "", "", true); err == nil {
		app.finishBackupJob(job, context.Canceled)
		t.Fatal("already checking automatic task bypassed hold")
	}
}

func TestBackupHoldSurvivesReloadAndConfigSave(t *testing.T) {
	app, id := completedHeldBackup(t)
	rr := backupControlRequest(t, app, "/api/save-config/CONTROL", `{"min_battery_level":42}`)
	if rr.Code != 200 {
		t.Fatalf("save: %d %s", rr.Code, rr.Body.String())
	}
	restart := newApplicationWithRuntime(context.Background(), app.runtimeConfig)
	if err := restart.loadConfigs(); err != nil {
		t.Fatal(err)
	}
	c := restart.configs["CONTROL"]
	if !c.AutoBackupHold || c.AutoBackupHoldVersion != id || !c.AutoBackupEnabled || c.MinBatteryLevel != 42 {
		t.Fatalf("hold or schedule lost: %+v", c)
	}
	for _, body := range []string{`{"auto_backup_hold":false}`, `{"auto_backup_hold_version":"forged"}`} {
		if r := backupControlRequest(t, app, "/api/save-config/CONTROL", body); r.Code != 400 {
			t.Fatalf("ordinary config could change control field: %s", r.Body.String())
		}
	}
}

func TestBackupHoldManualBackupKeepsHoldAndStaleRequestsAreRejected(t *testing.T) {
	app, oldID := completedHeldBackup(t)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !app.buildStatusSnapshot().Devices[0].AutoBackupHold {
		t.Fatal("manual backup resumed automatic schedule")
	}
	newID := app.buildStatusSnapshot().Devices[0].BackupJobID
	if err := app.stopBackup("CONTROL", oldID); err == nil {
		t.Fatal("stale stop accepted")
	}
	if err := app.stopBackup("CONTROL", newID); err != nil {
		t.Fatal(err)
	}
	if err := app.persistBackupHold("CONTROL", oldID, true); err == nil {
		t.Fatal("stale resume cleared newer hold")
	}
	if !app.buildStatusSnapshot().Devices[0].AutoBackupHold {
		t.Fatal("new hold cleared")
	}
}

func TestBackupHoldLateDuplicateStopAfterResumeIsNoOp(t *testing.T) {
	app, id := completedHeldBackup(t)
	app.configs["CONTROL"].AutoBackupEnabled = false
	if err := app.persistBackupHold("CONTROL", id, true); err != nil {
		t.Fatal(err)
	}
	if err := app.stopBackup("CONTROL", id); err != nil {
		t.Fatal(err)
	}
	if d := app.buildStatusSnapshot().Devices[0]; d.AutoBackupHold {
		t.Fatal("late duplicate reinstated hold")
	}
	if app.configs["CONTROL"].AutoBackupEnabled {
		t.Fatal("resume enabled disabled schedule")
	}
}

func TestBackupHoldSaveFailureStillStopsAndCanRetry(t *testing.T) {
	app := newBackupControlTestApp(t)
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishBackupJob(job, context.Canceled)
	// 使 .last-good 写入成功，而主文件的替换失败。
	path := app.configStore.path
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "obstacle"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.stopBackup("CONTROL", job.id); err == nil {
		t.Fatal("partial persistence reported success")
	}
	if !errors.Is(context.Cause(job.ctx), errBackupStoppedByUser) {
		t.Fatal("save failure prevented cancellation")
	}
	if d := app.buildStatusSnapshot().Devices[0]; !d.AutoBackupHold || d.AutoBackupHoldPersistence != "error" {
		t.Fatalf("partial result hidden: %+v", d)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := app.persistBackupHold("CONTROL", job.id, false); err != nil {
		t.Fatal(err)
	}
	restart := newApplicationWithRuntime(context.Background(), app.runtimeConfig)
	if err := restart.loadConfigs(); err != nil {
		t.Fatal(err)
	}
	if !restart.configs["CONTROL"].AutoBackupHold {
		t.Fatal("retry did not persist hold")
	}
}
