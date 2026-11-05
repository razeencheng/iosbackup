package app

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"iosbackup/internal/backupstore"
)

// Exercise the pipeline, history API and archive together. Device output is
// synthetic; this does not establish encrypted-device or restore compatibility.
func TestBackupHistoryEndToEndMatrix(t *testing.T) {
	for _, history := range []bool{false, true} {
		for _, encrypted := range []bool{false, true} {
			t.Run(fmt.Sprintf("history=%t/encrypted=%t", history, encrypted), func(t *testing.T) {
				a := historyTestApp(t)
				a.configs["CONTROL"].History.Enabled = history
				if err := a.initializeSecurity(); err != nil {
					t.Fatal(err)
				}
				if encrypted {
					a.backupGenerations.secrets = a.secretStore
					a.cmdRunner = (&mockRunner{}).run
					if err := a.SetBackupEncryption(context.Background(), "CONTROL", true, "synthetic-backup-password"); err != nil {
						t.Fatal(err)
					}
				}
				h := a.setupRoutes()
				t.Cleanup(a.closeHistoryHTTP)
				iteration := 0
				a.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
					iteration++
					dir := filepath.Join(args[len(args)-1], "CONTROL")
					writeHistoryBackup(t, dir, "finished", fmt.Sprintf("version-%d", iteration))
					if encrypted {
						if err := os.WriteFile(filepath.Join(dir, "Manifest.plist"), []byte(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
				}
				if err := a.PerformBackup("CONTROL"); err != nil {
					t.Fatal(err)
				}
				if check := a.buildStatusSnapshot().Devices[0].BackupCheck; check == nil || check.State != "passed" {
					t.Fatalf("automatic quick check did not qualify the result: %+v", check)
				}
				var first backupstore.Point
				if history {
					for _, p := range historyPoints(t, a) {
						first = p
					}
					c, err := a.backupStorage.stores[first.StoreID].Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					w := historyRequest(t, a, h, "POST", "/api/backup-point/"+first.ID+"/pin", map[string]any{"revision": c.Revision, "pinned": true})
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				if err := a.PerformBackup("CONTROL"); err != nil {
					t.Fatal(err)
				}
				if check := a.buildStatusSnapshot().Devices[0].BackupCheck; check == nil || check.State != "passed" {
					t.Fatalf("automatic quick check did not qualify the result: %+v", check)
				}
				current, err := a.resolveBackupIndexSource(context.Background(), "CONTROL")
				if err != nil {
					t.Fatal(err)
				}
				payload, err := os.ReadFile(filepath.Join(current.Directory, "payload"))
				if err != nil || string(payload) != "version-2" {
					t.Fatalf("current source: %q %v", payload, err)
				}
				if !history {
					if len(historyPoints(t, a)) != 0 {
						t.Fatal("disabled history created points")
					}
					p, err := os.ReadFile(filepath.Join(a.paths.BackupsRoot, "CONTROL", "payload"))
					if err != nil || string(p) != "version-2" {
						t.Fatalf("ordinary current: %q %v", p, err)
					}
					return
				}
				points := historyPoints(t, a)
				if len(points) != 2 {
					t.Fatal("pinned point lost during same-day replacement")
				}
				if !points[first.ID].Pinned {
					t.Fatal("old point no longer pinned")
				}
				expectedKind := "unencrypted"
				if encrypted {
					expectedKind = "encrypted"
				}
				for _, point := range points {
					if point.State != "normal" || point.Check.State != "passed" || point.Generation.Kind != expectedKind {
						t.Fatalf("unqualified history point: state=%s check=%s encryption=%s", point.State, point.Check.State, point.Generation.Kind)
					}
				}
				w := historyRequest(t, a, h, "POST", "/api/backup-point/"+first.ID+"/exports", map[string]any{})
				if w.Code != 201 {
					t.Fatal(w.Code, w.Body.String())
				}
				var session struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				w = historyRequest(t, a, h, "GET", "/api/backup-export/"+session.ID+"/download", nil)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				reader := tar.NewReader(bytes.NewReader(w.Body.Bytes()))
				files := map[string]string{}
				for {
					header, err := reader.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if header.Typeflag == tar.TypeReg {
						b, err := io.ReadAll(reader)
						if err != nil {
							t.Fatal(err)
						}
						files[header.Name] = string(b)
					}
				}
				if files["CONTROL/payload"] != "version-1" {
					t.Fatal("export selected newer data")
				}
				marker := "<false/>"
				if encrypted {
					marker = "<true/>"
				}
				if !bytes.Contains([]byte(files["CONTROL/Manifest.plist"]), []byte(marker)) {
					t.Fatal("encryption state changed during export")
				}
				for _, name := range []string{"Manifest.db", "Info.plist", "Status.plist"} {
					if files["CONTROL/"+name] == "" {
						t.Fatal("missing required export file", name)
					}
				}
			})
		}
	}
}
