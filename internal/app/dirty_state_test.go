package app

import (
	"strings"
	"testing"
)

// 备份设置：改了未保存时应有「未保存」提示 + 可标脏的保存按钮 + 标脏/清除逻辑。
func TestIndexTemplateUnsavedIndicator(t *testing.T) {
	dev := &device{UDID: "U-DIRTY", Name: "iPad", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	cfg := &backupConfig{UDID: "U-DIRTY", StartTime: "22:00", EndTime: "06:00", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-DIRTY": cfg},
		BackupStatuses: map[string]bool{"U-DIRTY": false},
	})
	for _, want := range []string{
		`id="dirty-U-DIRTY"`,       // 未保存提示元素（每设备）
		`id="saveBtn-U-DIRTY"`,     // 保存按钮可被标脏高亮
		`data-i18n="bset.unsaved"`, // 提示文案走 i18n
		"function markDirty",       // 改动 -> 标脏
		"function clearDirty",      // 保存/重载 -> 清除
		"unsaved:{zh:",             // 字典含未保存文案
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
		"unsaved:{zh:",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("notifications.html 应包含 %q", want)
		}
	}
}
