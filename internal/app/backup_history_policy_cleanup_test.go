package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"testing"
)

func olderHistoryPoint(t *testing.T, a *application, days int, oldGeneration bool) backupstore.Point {
	t.Helper()
	st, err := a.backupStorage.deviceState("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.backupStorage.store(st.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	g, err := a.generationRegistry().historyGeneration("CONTROL", "")
	if err != nil {
		t.Fatal(err)
	}
	if oldGeneration {
		g.ID, err = backupstore.NewID()
		if err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.NewWork("retention-fixture")
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, w.Directory, "finished", "older")
	p := a.historyPoint(st, g, a.nowTime().AddDate(0, 0, -days), backupCheckResult{Kind: "quick", State: "passed", CompletedAt: a.nowTime()})
	p, err = s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHistoryPolicyShrinkCollectsEligiblePoints(t *testing.T) {
	a := historyTestApp(t)
	a.configs["CONTROL"].History.Days = 30
	historyCommand(t, a, "finished", "base", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	eligible := olderHistoryPoint(t, a, 5, false)
	pinned := olderHistoryPoint(t, a, 6, false)
	leased := olderHistoryPoint(t, a, 7, false)
	oldGen := olderHistoryPoint(t, a, 8, true)
	s, _ := a.backupStorage.store(pinned.StoreID)
	c, _ := s.Snapshot()
	if err := s.SetPinned(context.Background(), pinned.ID, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Acquire(leased.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	policy := backupstore.Policy{Enabled: true, Days: 3}
	preview, err := a.previewHistoryPolicy(context.Background(), "CONTROL", policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Stores[pinned.StoreID].Delete) != 1 {
		t.Fatal("unexpected preview", preview)
	}
	if err = a.applyHistoryPolicy(context.Background(), "CONTROL", policy, &preview); err != nil {
		t.Fatal(err)
	}
	points := historyPoints(t, a)
	if _, ok := points[eligible.ID]; ok {
		t.Fatal("policy completed without collecting eligible point")
	}
	for _, p := range []backupstore.Point{pinned, leased, oldGen} {
		if _, ok := points[p.ID]; !ok {
			t.Fatal("protected point removed", p.ID)
		}
	}
	if a.configs["CONTROL"].HistoryPolicyPending {
		t.Fatal("successful policy left pending")
	}
}

func TestHistoryPolicyCleanupInterruptedKeepsSavedTarget(t *testing.T) {
	a := historyTestApp(t)
	a.configs["CONTROL"].History.Days = 30
	historyCommand(t, a, "finished", "base", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	old := olderHistoryPoint(t, a, 5, false)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	policy := backupstore.Policy{Enabled: true, Days: 3}
	preview, err := a.previewHistoryPolicy(context.Background(), "CONTROL", policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		err := persistence.WriteFileAtomic(path, data, mode)
		if err == nil && a.backupStorage.locations.Devices["CONTROL"].Days == 3 {
			cancel()
		}
		return err
	}
	err = a.applyHistoryPolicy(ctx, "CONTROL", policy, &preview)
	a.backupStorage.write = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatal("interrupted cleanup reported completion", err)
	}
	if a.configs["CONTROL"].History != policy || !a.configs["CONTROL"].HistoryPolicyPending {
		t.Fatal("saved target not available for retry")
	}
	if _, ok := historyPoints(t, a)[old.ID]; !ok {
		t.Fatal("canceled cleanup removed point before intent")
	}
	if err = a.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := historyPoints(t, a)[old.ID]; ok {
		t.Fatal("retry did not finish cleanup")
	}
}
