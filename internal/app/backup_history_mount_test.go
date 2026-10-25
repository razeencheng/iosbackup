package app

import (
	"context"
	"os"
	"testing"
)

func TestBackupHistoryReviewUnavailableHistoryDoesNotPreventStartup(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "first", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	path := app.backupStorage.locations.Stores[0].Path
	if err := app.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatalf("unavailable history store prevents service startup: %v", err)
	}
}
