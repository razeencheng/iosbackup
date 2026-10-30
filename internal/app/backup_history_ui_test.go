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
	if strings.Count(s, `class="snav`) != 5 {
		t.Fatal("changed parent navigation count")
	}
}
