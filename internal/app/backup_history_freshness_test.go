package app

import "testing"

func TestBackupHistoryReviewHistoryFreshCheckIsCurrent(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "first", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	app.mu.RLock()
	result := app.backupCheckResults["CONTROL"]
	app.mu.RUnlock()
	if result.State != "passed" || result.Freshness != "current" {
		t.Fatalf("just-published normal point check %+v", result)
	}
}
