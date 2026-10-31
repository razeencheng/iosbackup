package app

import (
	"strings"
	"testing"
)

func TestBackupHistoryUIIntegration(t *testing.T) {
	s := readEmbeddedPage(t, "index.html")
	for _, fragment := range []string{"/static/backup-history.js", "/static/term-hints.js", "/static/backup-history.css", `id="historyPanel-`, `id="historySettings-`, `id="historySummary-`} {
		if !strings.Contains(s, fragment) {
			t.Errorf("missing %s", fragment)
		}
	}
	if strings.Count(s, `class="snav`) != 3 {
		t.Fatal("device navigation must expose backups, tasks and settings")
	}
	for _, fragment := range []string{`data-sec="backups"`, `data-sec="jobs"`, `data-sec="settings"`, `/static/backup-console.js`, `id="currentBackup-`, `id="jobList-`, `data-settings-group="schedule"`, `data-settings-group="connection"`} {
		if !strings.Contains(s, fragment) {
			t.Errorf("missing console workflow %s", fragment)
		}
	}
}
