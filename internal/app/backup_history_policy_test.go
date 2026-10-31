package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"testing"
)

func TestBackupHistoryPolicyPreviewAndApply(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "good", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	policy := backupstore.Policy{Enabled: true, Days: 1}
	preview, err := app.previewHistoryPolicy(context.Background(), "CONTROL", policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); !errors.Is(err, backupstore.ErrConflict) {
		t.Fatal("shortening without preview accepted", err)
	}
	st, _ := app.backupStorage.deviceState("CONTROL")
	s := app.backupStorage.stores[st.StoreID]
	c, _ := s.Snapshot()
	if err = s.SetPinned(context.Background(), st.LatestPoint, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", policy, &preview); !errors.Is(err, backupstore.ErrConflict) {
		t.Fatal("stale preview accepted", err)
	}
	preview, err = app.previewHistoryPolicy(context.Background(), "CONTROL", policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", policy, &preview); err != nil {
		t.Fatal(err)
	}
	if app.configs["CONTROL"].History.Days != 1 {
		t.Fatal("policy not saved")
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 1}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range historyPoints(t, app) {
		if !p.Manual {
			t.Fatal("disable did not preserve point")
		}
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	next, _ := app.backupStorage.deviceState("CONTROL")
	if next.EpochID == st.EpochID {
		t.Fatal("reenable reused epoch")
	}
}
