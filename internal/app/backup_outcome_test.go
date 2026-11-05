package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	notificationpkg "iosbackup/internal/notification"
)

func TestBackupOutcomeExecutionSuccessSurvivesCheckIssue(t *testing.T) {
	app := newBackupControlTestApp(t)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	manager := newNotificationManager()
	t.Cleanup(manager.Close)
	sink := &MockNotifier{name: "outcome", enabled: true}
	if err := manager.AddNotifier(sink); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{"backup_success": {"outcome"}, "backup_check_attention": {"outcome"}})
	app.replaceNotificationManager(manager)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	message := sink.WaitForSends(t, 1)
	if message.Type != notificationpkg.BackupCheckAttention {
		t.Fatalf("premature success notification: %+v", message)
	}
	state := app.buildStatusSnapshot().Devices[0]
	if state.BackupState != "succeeded" || state.LastBackupAt == "" || state.BackupCheck == nil || state.BackupCheck.State != "issues" {
		t.Fatalf("outcome=%+v", state)
	}
	record := app.backupJobRecords.list("CONTROL")[0]
	if record.ExecutionState != "succeeded" || record.ExecutionCompletedAt.IsZero() || record.CompletedAt.Before(record.ExecutionCompletedAt) || record.Check == nil {
		t.Fatalf("record=%+v", record)
	}
}

func TestBackupOutcomeRestartRetainsKnownExecutionAndInterruptsCheck(t *testing.T) {
	root := t.TempDir()
	store := openJobTestStore(t, root, defaultBackupJobLimits())
	seed := jobFixture(4)
	store.start(seed)
	store.executionFinished(seed.ID, "succeeded", jobTestNow)
	store.setCheck(seed.ID, backupCheckResult{ID: seed.ID, Kind: "quick", State: "running", Freshness: "unknown", StartedAt: jobTestNow})
	syncJobs(t, store)
	if err := store.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := openJobTestStore(t, root, defaultBackupJobLimits())
	record := restored.list("PHONE")[0]
	if record.ExecutionState != "succeeded" || record.Check.State != "unavailable" || record.Check.Reason != "process_restarted" {
		t.Fatalf("record=%+v", record)
	}
}

func TestBackupOutcomeNotificationRulesPersistMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	cfg := defaultNotificationConfig()
	cfg.NotificationRules = map[string][]string{"backup_failed": {"configured"}, "backup_check_attention": {}}
	data, err := json.Marshal(notificationConfigEnvelope{SchemaVersion: notificationConfigSchemaVersion, Config: *cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := newNotificationConfigStore(path, nil)
	loaded, err := store.Public()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.NotificationRules["backup_check_attention"]) != 0 || len(loaded.NotificationRules["backup_history_attention"]) != 1 {
		t.Fatalf("rules=%v", loaded.NotificationRules)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved notificationConfigEnvelope
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Config.NotificationRules["backup_history_attention"]; !ok {
		t.Fatal("rule defaults must be persisted once")
	}
}
