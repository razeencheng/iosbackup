package app

import (
	"context"
	"encoding/json"
	"errors"
	"iosbackup/internal/persistence"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryDeleteLatestSelectsCompatibleRemainingPoint(t *testing.T) {
	for _, kind := range []string{"compatible", "old_generation", "unreadable_metadata"} {
		t.Run(kind, func(t *testing.T) {
			a, h, _ := historyHTTPFixture(t)
			old := olderHistoryPoint(t, a, 1, kind == "old_generation")
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			if kind == "unreadable_metadata" {
				s, _ := a.backupStorage.store(old.StoreID)
				lease, err := s.Acquire(old.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = lease.Root.Remove("Manifest.plist"); err != nil {
					t.Fatal(err)
				}
				lease.Close()
			}
			st, _ := a.backupStorage.deviceState("CONTROL")
			s, _ := a.backupStorage.store(st.LatestStore)
			c, _ := s.Snapshot()
			w := historyRequest(t, a, h, http.MethodDelete, "/api/backup-point/"+st.LatestPoint, map[string]any{"revision": c.Revision, "confirm": st.LatestPoint})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			st, err := a.backupStorage.reconcileHistoryLatest("CONTROL")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "compatible" {
				if st.CurrentDeleted || st.LatestPoint != old.ID || !a.hasLocalBackup("CONTROL") {
					t.Fatal("remaining compatible point not current")
				}
			} else if !st.CurrentDeleted || a.hasLocalBackup("CONTROL") {
				t.Fatal("incompatible source selected")
			}
			var full bool
			command := a.backupCommand
			a.backupCommand = func(ctx context.Context, name string, args ...string) *execCmd {
				full = argsHas(args, "--full")
				return command(ctx, name, args...)
			}
			if err = a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			if full != (kind != "compatible") {
				t.Fatal("command disagrees with current source", kind, full)
			}
		})
	}
}

func TestHistoryDeleteLatestIntentRecovery(t *testing.T) {
	for _, stage := range []string{"before_intent", "after_intent", "after_delete"} {
		t.Run(stage, func(t *testing.T) {
			a, h, _ := historyHTTPFixture(t)
			old := olderHistoryPoint(t, a, 1, false)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			st, _ := a.backupStorage.deviceState("CONTROL")
			target := st.LatestPoint
			s, _ := a.backupStorage.store(st.LatestStore)
			c, _ := s.Snapshot()
			injected := false
			a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
				var raw struct {
					Devices map[string]map[string]json.RawMessage `json:"devices"`
				}
				if err := json.Unmarshal(data, &raw); err != nil {
					return err
				}
				pending := len(raw.Devices["CONTROL"]["point_deletion"]) > 0
				if !injected && ((stage != "after_delete" && pending) || (stage == "after_delete" && !pending)) {
					injected = true
					if stage == "after_intent" {
						if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
							return err
						}
					}
					return errors.New("injected registry failure")
				}
				return persistence.WriteFileAtomic(path, data, mode)
			}
			w := historyRequest(t, a, h, http.MethodDelete, "/api/backup-point/"+target, map[string]any{"revision": c.Revision, "confirm": target})
			if !injected || w.Code == 200 {
				t.Fatal("failure was not reported", stage, w.Code)
			}
			a.closeBackupIndex()
			if err := a.backupStorage.close(); err != nil {
				t.Fatal(err)
			}
			if err := a.recoverBackupStorage(context.Background()); err != nil {
				t.Fatal(err)
			}
			st, err := a.backupStorage.deviceState("CONTROL")
			if err != nil {
				t.Fatal(err)
			}
			want := old.ID
			if stage == "before_intent" {
				want = target
			}
			if st.CurrentDeleted || st.LatestPoint != want {
				t.Fatal("wrong source after restart", st.LatestPoint, want)
			}
			if _, err := os.Stat(filepath.Join(a.paths.BackupsRoot, ".iosbackup-history", "v1", "CONTROL", "objects", want, "CONTROL", "payload")); err != nil {
				t.Fatal("selected source unavailable", err)
			}
		})
	}
}
