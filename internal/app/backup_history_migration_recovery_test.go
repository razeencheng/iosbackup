package app

import (
	"context"
	"encoding/json"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryMigrationCommitGapIsIdempotent(t *testing.T) {
	for _, result := range []string{"normal", "unknown_time", "check_issue"} {
		for _, afterRename := range []bool{false, true} {
			t.Run(result+map[bool]string{false: "/before_registry", true: "/after_registry"}[afterRename], func(t *testing.T) {
				a := historyTestApp(t)
				a.configs["CONTROL"].History = backupstore.Policy{Count: 3}
				dir := filepath.Join(a.paths.BackupsRoot, "CONTROL")
				writeHistoryBackup(t, dir, "finished", "original source")
				if result != "unknown_time" {
					state := "finished"
					if result == "check_issue" {
						state = "unfinished"
					}
					stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
					if err := os.WriteFile(filepath.Join(dir, "Status.plist"), []byte(`<plist><dict><key>SnapshotState</key><string>`+state+`</string><key>Date</key><date>`+stamp+`</date></dict></plist>`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				injected := false
				a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
					var locations historyLocations
					if err := json.Unmarshal(data, &locations); err != nil {
						return err
					}
					st := locations.Devices["CONTROL"]
					if st.Phase == "enabling" && st.MigrationDone {
						for _, store := range a.backupStorage.stores {
							catalog, err := store.Snapshot()
							if err == nil && len(catalog.Points) > 0 {
								injected = true
								if afterRename {
									if err = persistence.WriteFileAtomic(path, data, mode); err != nil {
										return err
									}
								}
								return errors.New("injected registry failure after import commit")
							}
						}
					}
					return persistence.WriteFileAtomic(path, data, mode)
				}
				policy := backupstore.Policy{Enabled: true, Count: 3}
				if err := a.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err == nil || !injected {
					t.Fatalf("failure was not injected: %v", err)
				}
				a.closeBackupIndex()
				if err := a.backupStorage.close(); err != nil {
					t.Fatal(err)
				}
				if err := a.recoverBackupStorage(context.Background()); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err := a.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
						t.Fatal(err)
					}
				}
				points := historyPoints(t, a)
				if len(points) != 1 {
					t.Fatalf("same source imported %d times", len(points))
				}
				st, err := a.backupStorage.deviceState("CONTROL")
				if err != nil || !st.MigrationDone {
					t.Fatal("migration not reconciled", err)
				}
				if result != "normal" && st.LatestPoint != "" {
					t.Fatal("pending import became current normal source")
				}
				data, err := os.ReadFile(filepath.Join(dir, "payload"))
				if err != nil || string(data) != "original source" {
					t.Fatal("original source lost", err)
				}
			})
		}
	}
}

func TestHistoryMigrationRecoveryDoesNotUsePreviousEpoch(t *testing.T) {
	a := historyTestApp(t)
	historyCommand(t, a, "finished", "previous", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	st, _ := a.backupStorage.deviceState("CONTROL")
	st.EpochID, _ = backupstore.NewID()
	st.Phase, st.MigrationDone = "enabling", false
	st.LatestPoint, st.LatestStore = "", ""
	if err := a.backupStorage.saveDevice("CONTROL", st); err != nil {
		t.Fatal(err)
	}
	st, err := a.backupStorage.reconcileHistoryLatest("CONTROL")
	if err != nil || st.MigrationDone || st.LatestPoint != "" {
		t.Fatal("previous epoch incorrectly completed new migration", err)
	}
}
