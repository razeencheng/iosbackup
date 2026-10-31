package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryImportDateFallsBackAcrossUnreadableStatus(t *testing.T) {
	for _, status := range []string{"missing", "corrupt", "wrong-type", "zero"} {
		t.Run(status, func(t *testing.T) {
			app := historyTestApp(t)
			dir := filepath.Join(t.TempDir(), "CONTROL")
			writeHistoryBackup(t, dir, "finished", "payload")
			if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte(`<plist><dict><key>Last Backup Date</key><date>2026-10-30T12:00:00Z</date></dict></plist>`), 0600); err != nil {
				t.Fatal(err)
			}
			data := []byte("corrupt")
			if status == "wrong-type" {
				data = []byte(`<plist><dict><key>Date</key><string>bad</string></dict></plist>`)
			}
			if status == "zero" {
				data = []byte(`<plist><dict><key>Date</key><date>0001-01-01T00:00:00Z</date></dict></plist>`)
			}
			if status == "missing" {
				if err := os.Remove(filepath.Join(dir, "Status.plist")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(dir, "Status.plist"), data, 0600); err != nil {
				t.Fatal(err)
			}
			got := app.historyImportCompletion(context.Background(), dir, historyDeviceState{})
			want := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
			if !got.Equal(want) {
				t.Fatal("valid Info date ignored", got)
			}
		})
	}
}

func TestUnknownSourceTimeCheckCanPersist(t *testing.T) {
	result := backupCheckResult{Kind: "quick", State: "unavailable", Reason: "unknown_source_time", Freshness: "unknown", StartedAt: jobTestNow, CompletedAt: jobTestNow}
	if !validBackupCheckResult(result) {
		t.Fatal("generated unknown source time rejected by persistence validation")
	}
}
