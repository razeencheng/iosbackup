package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryImportsLargePlistIndependentlyOfCheckBudget(t *testing.T) {
	ctx := context.Background()
	data := []byte("<plist><dict><key>Device Name</key><string>Phone</string><key>Applications</key><data>" + strings.Repeat("YWFh", (9<<20)/4) + "</data></dict></plist>")
	wantDigest := sha256.Sum256(data)
	for _, tc := range []struct {
		name  string
		limit int
		state string
	}{
		{"default", 64, "normal"}, {"smaller-budget", 8, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := historyTestApp(t)
			app.runtimeConfig.PlistMaxMiB = tc.limit
			cfg := app.configs["CONTROL"]
			cfg.History.Enabled = false
			dir := filepath.Join(app.paths.BackupsRoot, "CONTROL")
			writeHistoryBackup(t, dir, "finished", "legacy")
			writeIndexFixture(t, dir, "Status.plist", `<plist><dict><key>SnapshotState</key><string>finished</string><key>Date</key><date>2020-01-01T12:00:00Z</date></dict></plist>`)
			if err := os.WriteFile(filepath.Join(dir, "Info.plist"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if !app.historyCurrentSource(ctx, "CONTROL", cfg).Available {
				t.Fatal("legacy backup hidden")
			}
			source, err := app.resolveBackupIndexSource(ctx, "CONTROL")
			if err != nil {
				t.Fatal(err)
			}
			overview, err := app.plistLimit().readBackupIndexOverview(ctx, source, nil)
			if tc.state == "pending" {
				if !errors.Is(err, errPlistTooLarge) || overview.PlistFile != "Info.plist" || overview.PlistLimitMiB != tc.limit {
					t.Fatalf("overview=%+v err=%v", overview, err)
				}
			} else if err != nil || overview.DeviceName != "Phone" {
				t.Fatalf("overview=%+v err=%v", overview, err)
			}
			policy := cfg.History
			policy.Enabled = true
			if err := app.applyHistoryPolicy(ctx, "CONTROL", policy, nil); err != nil {
				t.Fatal(err)
			}
			points := historyPoints(t, app)
			if len(points) != 1 {
				t.Fatalf("got %d points", len(points))
			}
			for _, p := range points {
				if p.State != tc.state {
					t.Fatalf("state=%s check=%+v", p.State, p.Check)
				}
				if tc.state == "pending" && (p.Check.State != "unavailable" || p.Check.Reason != "resource_limit" || p.Check.PlistFile != "Info.plist" || p.Check.PlistLimitMiB != tc.limit) {
					t.Fatalf("check=%+v", p.Check)
				}
				store, err := app.backupStorage.store(p.StoreID)
				if err != nil {
					t.Fatal(err)
				}
				catalog, err := store.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if (catalog.Baselines[p.Generation.ID] == p.ID) != (tc.state == "normal") {
					t.Fatal("unexpected baseline", catalog.Baselines)
				}
				lease, err := store.Acquire(p.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				var archive bytes.Buffer
				if err = lease.Export(ctx, &archive, "CONTROL"); err != nil {
					t.Fatal(err)
				}
				tr := tar.NewReader(&archive)
				for {
					header, err := tr.Next()
					if errors.Is(err, io.EOF) {
						t.Fatal("Info.plist missing from TAR")
					}
					if err != nil {
						t.Fatal(err)
					}
					if header.Name == "CONTROL/Info.plist" {
						digest := sha256.New()
						n, err := io.Copy(digest, tr)
						if err != nil || n != int64(len(data)) || !bytes.Equal(digest.Sum(nil), wantDigest[:]) {
							t.Fatalf("export size=%d digest=%x error=%v", n, digest.Sum(nil), err)
						}
						break
					}
				}
			}
		})
	}
}
