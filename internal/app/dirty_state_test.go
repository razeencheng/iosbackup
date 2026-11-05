package app

import (
	"strings"
	"testing"
)

// 设备设置按组保存；每组保留取消与未保存提示。
func TestIndexTemplateUnsavedIndicator(t *testing.T) {
	dev := &device{UDID: "U-DIRTY", Name: "iPad", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	cfg := &backupConfig{UDID: "U-DIRTY", StartTime: "22:00", EndTime: "06:00", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-DIRTY": cfg},
		BackupStatuses: map[string]bool{"U-DIRTY": false},
	})
	for _, want := range []string{
		`id="setting-schedule-U-DIRTY"`,
		`id="setting-connection-U-DIRTY"`,
		`data-group-save`,
		`data-group-cancel`,
		`data-group-dirty`,
		`data-i18n="bset.unsaved"`,
		`/static/backup-console.js`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html 应包含 %q", want)
		}
	}
}

// 通知设置：同样的未保存提示机制。
func TestNotificationsTemplateUnsavedIndicator(t *testing.T) {
	data, err := templateFS.ReadFile("templates/notifications.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{
		`id="notifDirty"`,
		`id="notifSaveBtn"`,
		`data-i18n="notif.unsaved"`,
		"function markNotifDirty",
		"function clearNotifDirty",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("notifications.html 应包含 %q", want)
		}
	}
}

func TestUnsavedIndicatorsHaveSharedTranslations(t *testing.T) {
	catalog := uiMessageCatalog(t)
	for _, key := range []string{"bset.unsaved", "notif.unsaved"} {
		for _, locale := range []string{"en", "zh-CN"} {
			if catalog[locale][key] == "" {
				t.Errorf("missing unsaved text %s: %s", locale, key)
			}
		}
	}
}
