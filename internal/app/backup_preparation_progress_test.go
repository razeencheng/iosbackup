package app

import (
	"context"
	"iosbackup/internal/backupstore"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryPreparationHasLocalPhaseBeforePhoneCommand(t *testing.T) {
	a := historyTestApp(t)
	historyCommand(t, a, "finished", "base", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	job, err := a.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	a.backupStorage.slot <- struct{}{}
	done := make(chan error, 1)
	started := make(chan struct{}, 1)
	command := a.backupCommand
	a.backupCommand = func(ctx context.Context, name string, args ...string) *execCmd {
		started <- struct{}{}
		return command(ctx, name, args...)
	}
	go func() { done <- a.runBackupJob(job) }()
	deadline := time.Now().Add(2 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		snapshot := a.buildStatusSnapshot()
		for _, d := range snapshot.Devices {
			if d.UDID == "CONTROL" && d.BackupProgress != nil && d.BackupProgress.Phase == "local_waiting" {
				seen = true
			}
		}
		if seen {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-started:
		t.Error("phone command started before local preparation")
	default:
	}
	if !seen {
		t.Error("waiting local I/O has no distinct phase")
	}
	if err = a.stopBackup("CONTROL", job.id); err != nil {
		t.Error(err)
	}
	<-a.backupStorage.slot
	<-done
}

func TestHistoryPolicyPreparationReportsLocalProgress(t *testing.T) {
	a := historyTestApp(t)
	a.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(a.paths.BackupsRoot, "CONTROL"), "finished", "legacy")
	a.backupStorage.slot <- struct{}{}
	op, err := a.startHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil)
	if err != nil {
		<-a.backupStorage.slot
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		a.mu.RLock()
		cfg := cloneBackupConfig(a.configs["CONTROL"])
		a.mu.RUnlock()
		status := a.historyPolicyStatus("CONTROL", cfg)
		if status.ID == op.ID && status.Progress != nil && status.Progress.Phase == "waiting" {
			seen = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	service := a.historyService()
	service.mu.Lock()
	run := service.policies["CONTROL"]
	run.cancel()
	service.mu.Unlock()
	<-a.backupStorage.slot
	<-run.done
	if !seen {
		t.Fatal("policy migration did not expose local waiting phase")
	}
}
