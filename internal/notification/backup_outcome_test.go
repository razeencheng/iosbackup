package notification

import (
	"strings"
	"testing"
)

func TestBackupOutcomeMergesAttentionRulesPerChannel(t *testing.T) {
	manager, _ := localizedTestManager(t, "en")
	other := &recordingNotifier{name: "other", enabled: true}
	if err := manager.AddNotifier(other); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"capture"}, string(BackupCheckAttention): {"capture", "other"}, string(BackupHistoryAttention): {"capture"}})
	tasks := manager.backupOutcomeJobs(BackupOutcome{Execution: "succeeded", Check: "issues", History: "failed"}, "Phone", "PHONE")
	if len(tasks) != 2 {
		t.Fatalf("deliveries=%d", len(tasks))
	}
	for _, task := range tasks {
		if task.message.Type == BackupSuccess || !strings.Contains(task.message.Content, "issues") || !strings.Contains(task.message.Content, "failed") {
			t.Fatalf("summary=%+v", task.message)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"capture"}, string(BackupCheckAttention): {}, string(BackupHistoryAttention): {}})
	if tasks := manager.backupOutcomeJobs(BackupOutcome{Execution: "succeeded", Check: "issues"}, "Phone", "PHONE"); len(tasks) != 0 {
		t.Fatal("disabled attention fell back to success")
	}
	if tasks := manager.backupOutcomeJobs(BackupOutcome{Execution: "succeeded", Check: "passed"}, "Phone", "PHONE"); len(tasks) != 1 || tasks[0].message.Type != BackupSuccess {
		t.Fatal("normal completion missing")
	}
}
func TestBackupOutcomeCustomTemplateAndFailure(t *testing.T) {
	manager, _ := localizedTestManager(t, "en")
	manager.SetRules(map[string][]string{string(BackupFailed): {"capture"}, string(BackupCheckAttention): {"capture"}})
	if err := manager.SetTemplates(map[string]string{string(BackupCheckAttention): `${device_name}: ${execution_result}; ${check_result}; ${history_result}`}); err != nil {
		t.Fatal(err)
	}
	tasks := manager.backupOutcomeJobs(BackupOutcome{Execution: "succeeded", Check: "unavailable", History: "not_enabled"}, "Phone", "PHONE")
	if len(tasks) != 1 || !strings.HasPrefix(tasks[0].message.Content, "Phone: ") || strings.Contains(tasks[0].message.Content, "${") {
		t.Fatalf("tasks=%+v", tasks)
	}
	tasks = manager.backupOutcomeJobs(BackupOutcome{Execution: "failed", FailureCode: "backup_stalled", Diagnostics: "diagnostic"}, "Phone", "PHONE")
	if len(tasks) != 1 || tasks[0].message.Type != BackupFailed || !strings.Contains(tasks[0].message.Content, "No backup activity") {
		t.Fatalf("failure=%+v", tasks)
	}
}
func TestBackupOutcomeRuleMigrationPreservesExplicitOff(t *testing.T) {
	rules := MigrateBackupOutcomeRules(map[string][]string{string(BackupFailed): {"capture"}, string(BackupCheckAttention): {}})
	if len(rules[string(BackupCheckAttention)]) != 0 || len(rules[string(BackupHistoryAttention)]) != 1 {
		t.Fatalf("rules=%+v", rules)
	}
	rules[string(BackupFailed)][0] = "changed"
	if rules[string(BackupHistoryAttention)][0] != "capture" {
		t.Fatal("migration shares mutable channels")
	}
	if len(MigrateBackupOutcomeRules(nil)[string(BackupCheckAttention)]) != 0 {
		t.Fatal("migration invented channel")
	}
}
