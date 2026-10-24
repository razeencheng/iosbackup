package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestBackupIndexUIUsesLocalReadController(t *testing.T) {
	source := readEmbeddedPage(t, "index.html")
	for _, fragment := range []string{`/static/backup-browser.js`, `/static/backup-browser.css`, `id="bkStatus-`, `id="bkReadAt-`, `id="bkCSV-`, `id="bkSearch-`, `backupBrowser.open(udid)`, `backupBrowser.notify(u,d.backup_index)`, `backupBrowser.reconnect()`} {
		if !strings.Contains(source, fragment) {
			t.Errorf("本地备份页面缺少 %s", fragment)
		}
	}
	start := strings.Index(source, `data-sec-panel="backups"`)
	end := strings.Index(source[start:], `<!-- ===== ENCRYPTION`)
	section := source[start : start+end]
	if strings.Contains(section, `pointer-events:none`) || strings.Contains(section, `/api/backup-list/`) {
		t.Fatal("备份浏览不能因设备离线而整体禁用或下载在线清单")
	}
	if strings.Contains(source, `data.info.match`) {
		t.Fatal("加密状态必须使用结构化的本地概况")
	}
}

func TestBackupIndexUIAssetsAreEmbedded(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	for _, asset := range []string{"backup-browser.js", "backup-browser.css"} {
		rr := performRequest(handler, http.MethodGet, "/static/"+asset, nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", asset, rr.Code)
		}
	}
}
