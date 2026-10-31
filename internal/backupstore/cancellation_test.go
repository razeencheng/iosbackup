package backupstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteFinishesAfterIntentDespiteCancellation(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	point, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.step = func(stage string) error {
		if stage == "recycle-intent.after" {
			cancel()
		}
		return nil
	}
	if err = s.Delete(ctx, point.ID, c.Revision); err != nil {
		t.Fatal("durable deletion interrupted by caller", err)
	}
	if _, err = s.ReadSnapshot(); err != nil {
		t.Fatal("store unavailable after cancellation", err)
	}
	if _, err = s.NewWork("next-job"); err != nil {
		t.Fatal(err)
	}
}

func TestCommitFinishesAfterIntentDespiteCancellation(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.step = func(stage string) error {
		if stage == "intent.after" {
			cancel()
		}
		return nil
	}
	point, err := s.Commit(ctx, w.ID, p)
	if err != nil {
		t.Fatal("durable commit interrupted by caller", err)
	}
	lease, err := s.Acquire(point.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}

func TestAbortCancelledBeforeMutationLeavesStoreUsable(t *testing.T) {
	s := storeFixture(t)
	w, _ := preparePoint(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.AbortWork(ctx, w.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.ReadSnapshot(); err != nil {
		t.Fatal("preflight cancellation poisoned store", err)
	}
	if err := s.AbortWork(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRejectsChangesDuringSealing(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	s.step = func(stage string) error {
		if stage == "intent.after" {
			return os.WriteFile(filepath.Join(w.Directory, "Info.plist"), []byte("mutated-longer-content"), 0600)
		}
		return nil
	}
	if _, err := s.Commit(context.Background(), w.ID, p); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed source published: %v", err)
	}
	s.step = nil
	report, err := s.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Uncommitted) != 1 {
		t.Fatalf("uncommitted work lost: %+v", report)
	}
	c, err := s.ReadSnapshot()
	if err != nil || len(c.Points) != 0 {
		t.Fatal("changed point became visible", err)
	}
}

func TestCommitUsesSameDepthLimitAsCopy(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	dir := w.Directory
	for i := 0; i < 127; i++ {
		dir = filepath.Join(dir, "d")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("deep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(context.Background(), w.ID, p); err != nil {
		t.Fatal("valid source depth rejected during sealing", err)
	}
}
