package backupstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func storeFixture(t *testing.T) *Store {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Create(filepath.Join(t.TempDir(), "store"), "PHONE", id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func preparePoint(t *testing.T, s *Store) (Work, Point) {
	t.Helper()
	w, err := s.NewWork("test-job")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(w.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Directory, "Info.plist"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	gen, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return w, Point{Generation: Generation{ID: gen, Kind: "unencrypted", CreatedAt: now}, EpochID: epoch, CompletedAt: now, Day: now.Format("2006-01-02"), Timezone: "UTC", State: "normal", Check: Check{State: "passed", Scope: "quick", CheckedAt: now}}
}
func TestStoreCommitAndIdentity(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	committed, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if c.Revision != 2 || c.Baselines[p.Generation.ID] != committed.ID || len(c.Points) != 1 {
		t.Fatalf("catalog %+v", c)
	}
	lease, err := s.Acquire(committed.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := readLeaseFile(lease, "Info.plist")
	if err != nil || string(content) != "original" {
		t.Fatal("lost data", err)
	}
	_ = lease.Close()
	if _, err := Open(s.path, "PHONE", s.identity.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("second writer admitted", err)
	}
	path, id := s.path, s.identity.ID
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(path, "PHONE", "00000000000000000000000000000000"); err == nil {
		t.Fatal("wrong identity accepted")
	}
	restart, err := Open(path, "PHONE", id)
	if err != nil {
		t.Fatal(err)
	}
	defer restart.Close()
	if _, err = restart.NewWork("before-recovery"); !errors.Is(err, ErrUncertain) {
		t.Fatal("write before recovery", err)
	}
	if _, err = restart.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := restart.Snapshot()
	if err != nil || got.Points[committed.ID].ID == "" {
		t.Fatal("lost committed point", err)
	}
}
func TestStoreCommitFailuresRecoverWithoutLosingOldPoint(t *testing.T) {
	for _, stage := range []string{"intent.before", "intent.after", "data.before", "data.after", "descriptor.before", "descriptor.after", "ready.before", "ready.after", "rename.before", "rename.after", "parents.before", "parents.after", "catalog.before", "catalog.after", "cleanup.before", "cleanup.after"} {
		t.Run(stage, func(t *testing.T) {
			s := storeFixture(t)
			first, point := preparePoint(t, s)
			old, err := s.Commit(context.Background(), first.ID, point)
			if err != nil {
				t.Fatal(err)
			}
			next, p := preparePoint(t, s)
			p.Generation = point.Generation
			failed := false
			s.step = func(name string) error {
				if name == stage && !failed {
					failed = true
					return errors.New("injected failure")
				}
				return nil
			}
			_, err = s.Commit(context.Background(), next.ID, p)
			if err == nil || !failed {
				t.Fatalf("fault not reached %s %v", stage, err)
			}
			s.step = nil
			path, id := s.path, s.identity.ID
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			restart, err := Open(path, "PHONE", id)
			if err != nil {
				t.Fatal(err)
			}
			defer restart.Close()
			report, err := restart.Recover(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			again, err := restart.Recover(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_ = again
			c, err := restart.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if c.Points[old.ID].ID == "" {
				t.Fatal("old point lost")
			}
			lease, err := restart.Acquire(old.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := readLeaseFile(lease, "Info.plist")
			lease.Close()
			if err != nil || string(data) != "original" {
				t.Fatal("old bytes damaged")
			}
			if len(c.Points) == 1 && len(report.Uncommitted) != 1 {
				t.Fatalf("lost unpublished result: %+v", report)
			}
		})
	}
}
func TestStoreRecoveryRefusesMissingOrUnknownStorage(t *testing.T) {
	s := storeFixture(t)
	path, id := s.path, s.identity.ID
	_ = s.Close()
	if err := os.Remove(filepath.Join(path, "catalog.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "PHONE", id); err == nil {
		t.Fatal("missing catalog recreated")
	}
	if _, err := os.Stat(filepath.Join(path, "catalog.json")); !os.IsNotExist(err) {
		t.Fatal("created empty catalog")
	}
	s = storeFixture(t)
	unknown := filepath.Join(s.path, "objects", "unowned")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(context.Background()); !errors.Is(err, ErrUncertain) {
		t.Fatal("unowned object accepted", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unowned object removed")
	}
}
func TestStoreLeaseProtectsRecycling(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, next := preparePoint(t, s)
	next.Generation = p.Generation
	if _, err = s.Commit(context.Background(), w.ID, next); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Snapshot()
	lease, err := s.Acquire(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Recycle(context.Background(), old.ID, c.Revision); !errors.Is(err, ErrBusy) {
		t.Fatal("recycled leased data", err)
	}
	if err = s.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal("closed leased store", err)
	}
	_ = lease.Close()
	_ = lease.Close()
	if err = s.Recycle(context.Background(), old.ID, c.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted stale revision", err)
	}
	if err = s.Recycle(context.Background(), old.ID, c.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Acquire(old.ID); err == nil {
		t.Fatal("deleted point still readable")
	}
	if _, err = os.Stat(filepath.Join(s.path, "objects", old.ID)); !os.IsNotExist(err) {
		t.Fatal("recycle left data")
	}
}
func TestStoreAbortOnlyOwnedUnpublishedWork(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	if err := s.AbortWork(context.Background(), "../objects"); err == nil {
		t.Fatal("unsafe cleanup accepted")
	}
	if err := s.AbortWork(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Parent); !os.IsNotExist(err) {
		t.Fatal("unpublished work not cleaned")
	}
	w, p = preparePoint(t, s)
	point, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AbortWork(context.Background(), point.ID); err == nil {
		t.Fatal("aborted committed point")
	}
}

func readLeaseFile(lease *Lease, name string) ([]byte, error) {
	f, err := lease.Root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func TestStoreRecoveryRejectsChangedPreparedObject(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	s.step = func(name string) error {
		if name == "ready.after" {
			return errors.New("crash")
		}
		return nil
	}
	if _, err := s.Commit(context.Background(), w.ID, p); err == nil {
		t.Fatal("fault not injected")
	}
	s.step = nil
	if err := os.WriteFile(filepath.Join(w.Directory, "Info.plist"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(context.Background()); !errors.Is(err, ErrChanged) {
		t.Fatal("changed prepared data published", err)
	}
}
func TestStoreCommitRejectsSharedWritableInodes(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	outside := filepath.Join(t.TempDir(), "shared")
	if err := os.Link(filepath.Join(w.Directory, "Info.plist"), outside); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(context.Background(), w.ID, p); !errors.Is(err, ErrUnsafe) {
		t.Fatal("shared writable inode published", err)
	}
}
func TestStoreRecycleFailuresRecover(t *testing.T) {
	for _, stage := range []string{"recycle-intent.before", "recycle-intent.after", "recycle-catalog.before", "recycle-catalog.after", "recycle-data.before", "recycle-data.after", "recycle-descriptor.after", "recycle-cleanup.after"} {
		t.Run(stage, func(t *testing.T) {
			s := storeFixture(t)
			w, p := preparePoint(t, s)
			old, err := s.Commit(context.Background(), w.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			w, next := preparePoint(t, s)
			next.Generation = p.Generation
			newest, err := s.Commit(context.Background(), w.ID, next)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := s.Snapshot()
			s.step = func(name string) error {
				if name == stage {
					return errors.New("crash")
				}
				return nil
			}
			if err = s.Recycle(context.Background(), old.ID, c.Revision); err == nil {
				t.Fatal("fault not injected")
			}
			s.step = nil
			path, id := s.path, s.identity.ID
			_ = s.Close()
			restart, err := Open(path, "PHONE", id)
			if err != nil {
				t.Fatal(err)
			}
			defer restart.Close()
			if _, err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			c, err = restart.Snapshot()
			if err != nil || c.Points[newest.ID].ID == "" {
				t.Fatal("latest baseline lost", err)
			}
		})
	}
}

func TestStoreRecoveryRejectsMissingCommittedPayload(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	point, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(s.path, "objects", point.ID, "PHONE")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recover(context.Background()); err == nil {
		t.Fatal("missing committed payload accepted")
	}
}
func TestStoreRecycleRejectsChangedDescriptor(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, next := preparePoint(t, s)
	next.Generation = p.Generation
	if _, err = s.Commit(context.Background(), w.ID, next); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.path, "descriptors", old.ID+".json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Snapshot()
	if err = s.Recycle(context.Background(), old.ID, c.Revision); err == nil {
		t.Fatal("unknown descriptor deleted")
	}
	if _, err = os.Stat(filepath.Join(s.path, "objects", old.ID, "PHONE")); err != nil {
		t.Fatal("unknown data removed", err)
	}
}
