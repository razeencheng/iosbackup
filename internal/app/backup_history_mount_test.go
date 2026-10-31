package app

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBackupHistoryReviewUnavailableHistoryDoesNotPreventStartup(t *testing.T) {
	app := historyTestApp(t)
	started := make(chan struct{})
	app.backupIndexManager().read = func(ctx context.Context, _ backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
		_, release, err := app.acquireHistoryRead("CONTROL", app.paths.BackupsRoot)
		if err != nil {
			return backupIndexOverview{}, nil, err
		}
		defer release()
		close(started)
		<-ctx.Done()
		return backupIndexOverview{}, nil, ctx.Err()
	}
	historyCommand(t, app, "finished", "first", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background index did not start")
	}
	// Match service shutdown: release background readers before closing storage.
	app.closeBackupIndex()
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
