package app

import (
	"context"
	"encoding/json"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"testing"
)

func TestHistoryRecoveryCleansOnlyUnreferencedOwnedWork(t *testing.T) {
	a := historyTestApp(t)
	historyCommand(t, a, "finished", "base", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := a.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Count: 3}, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := a.backupStorage.deviceState("CONTROL")
	s, _ := a.backupStorage.store(st.StoreID)
	current, err := s.WorkLocation(st.WritableID)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.NewWork("interrupted-backup")
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, orphan.Directory, "unfinished", "partial")
	a.closeBackupIndex()
	if err = a.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphan.Parent); !os.IsNotExist(err) {
		t.Fatal("unreferenced work retained", err)
	}
	if _, err = os.Stat(current.Directory); err != nil {
		t.Fatal("current writable lost", err)
	}
	if len(historyPoints(t, a)) != 1 {
		t.Fatal("committed history changed")
	}
}

func TestHistoryRetiredWorkCleanupResumesAfterRegistryFailure(t *testing.T) {
	for _, stage := range []string{"before_intent", "after_intent", "after_cleanup"} {
		t.Run(stage, func(t *testing.T) {
			a := historyTestApp(t)
			historyCommand(t, a, "finished", "base", 0, nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := a.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Count: 3}, nil); err != nil {
				t.Fatal(err)
			}
			st, _ := a.backupStorage.deviceState("CONTROL")
			s, _ := a.backupStorage.store(st.WritableStore)
			old, err := s.WorkLocation(st.WritableID)
			if err != nil {
				t.Fatal(err)
			}
			seen, introduced := false, false
			a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
				var loc historyLocations
				if err := json.Unmarshal(data, &loc); err != nil {
					return err
				}
				st := loc.Devices["CONTROL"]
				if st.RetiredWork != "" {
					seen = true
				}
				fail := st.RetiredWork != "" && stage != "after_cleanup" || seen && stage == "after_cleanup" && st.RetiredWork == "" && st.WritableID == ""
				if fail {
					introduced = true
					if stage == "after_intent" {
						if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
							return err
						}
					}
					return errors.New("injected retirement registry failure")
				}
				return persistence.WriteFileAtomic(path, data, mode)
			}
			policy := backupstore.Policy{Enabled: true, Count: 3}
			if err = a.applyHistoryPolicy(ctx, "CONTROL", policy, nil); err == nil || !introduced {
				t.Fatal("fault not reached", err)
			}
			if stage != "after_cleanup" {
				if _, err = os.Stat(old.Directory); err != nil {
					t.Fatal("source deleted before retirement persisted", err)
				}
			}
			a.closeBackupIndex()
			if err = a.backupStorage.close(); err != nil {
				t.Fatal(err)
			}
			if err = a.recoverBackupStorage(ctx); err != nil {
				t.Fatal(err)
			}
			if err = a.applyHistoryPolicy(ctx, "CONTROL", policy, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(old.Parent); !os.IsNotExist(err) {
				t.Fatal("cleanup not resumed", err)
			}
			if len(historyPoints(t, a)) != 2 {
				t.Fatal("migration duplicated or frozen point lost")
			}
		})
	}
}

func TestHistoryReenableReleasesSupersededWritable(t *testing.T) {
	a := historyTestApp(t)
	historyCommand(t, a, "finished", "base", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Count: 3}, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := a.backupStorage.deviceState("CONTROL")
	s, _ := a.backupStorage.store(st.WritableStore)
	old, err := s.WorkLocation(st.WritableID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Enabled: true, Count: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(old.Parent); !os.IsNotExist(err) {
		t.Fatal("superseded writable retained", err)
	}
	st, _ = a.backupStorage.deviceState("CONTROL")
	if st.WritableID != "" || st.WritableStore != "" {
		t.Fatal("stale writable reference retained")
	}
	if err = a.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Count: 3}, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = a.backupStorage.deviceState("CONTROL")
	s, _ = a.backupStorage.store(st.WritableStore)
	if _, err = s.WorkLocation(st.WritableID); err != nil {
		t.Fatal("new writable missing", err)
	}
}
