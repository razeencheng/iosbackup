package app

import (
	"strings"
	"testing"
)

// 离线设备应渲染：测试连接按钮 + 结果占位 + 离线排障提示 + testDeviceIP 逻辑。
func TestIndexTemplateTestConnUI(t *testing.T) {
	dev := &device{UDID: "U-OFF2", Name: "GG", Connection: "", IsOnline: false}
	cfg := &backupConfig{UDID: "U-OFF2", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-OFF2": cfg},
		BackupStatuses: map[string]bool{"U-OFF2": false},
	})
	for _, want := range []string{
		"testDeviceIP('U-OFF2')",        // 测试连接按钮 handler
		`data-i18n="bset.testConn"`,     // 按钮文案
		`id="ipResult-U-OFF2"`,          // 结果占位
		`data-i18n="bset.offlineHelp"`,  // 离线排障提示（仅离线时）
		"function testDeviceIP",         // 前端逻辑
		"offlineHelp:{zh:",              // 字典含排障文案
		`id="autoNote-U-OFF2"`,          // 自动备份锁屏密码提示元素
		`data-i18n="bset.passcodeNote"`, // 锁屏密码提示文案
		"passcodeNote:{zh:",             // 字典含锁屏密码文案
	} {
		if !strings.Contains(html, want) {
			t.Errorf("离线卡片应包含 %q", want)
		}
	}
}

// 在线设备不应出现离线排障提示。
func TestIndexTemplateOnlineNoOfflineHelp(t *testing.T) {
	dev := &device{UDID: "U-ON2", Name: "iPhone", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true}
	cfg := &backupConfig{UDID: "U-ON2", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups"}
	html := renderIndex(t, homePayload{
		Devices:                       []*device{dev},
		Configs:                       map[string]*backupConfig{"U-ON2": cfg},
		BackupStatuses:                map[string]bool{"U-ON2": false},
		ExperimentalOperationsEnabled: true,
	})
	// 测试连接按钮在线也有；但离线提示不应出现
	if !strings.Contains(html, "testDeviceIP('U-ON2')") {
		t.Error("在线设备也应有测试连接按钮")
	}
	// 加密状态徽章 + 加载逻辑 + 忘记密码链接（双语 URL）+ 备份管理修订（文件数/解包图标）
	for _, want := range []string{`id="encState-U-ON2"`, "function loadEncStatus", "stateOn:{zh:", "/api/encryption-status/",
		`data-i18n-href="enc.forgotUrl"`, "support.apple.com/zh-cn/108313", "support.apple.com/en-us/108313",
		`count:{zh:"备份文件数"`, "ui-icons.svg#icon-unpack",
		"checkBackupDir('U-ON2')", "function checkBackupDir", "/api/check-dir",
		"confirmDeleteBackup('U-ON2'", "function confirmDeleteBackup", "delDataTitle:{zh:"} {
		if !strings.Contains(html, want) {
			t.Errorf("加密页应包含 %q", want)
		}
	}
	if strings.Contains(html, `data-i18n="bset.offlineHelp"`) {
		t.Error("在线设备不应渲染离线提示块")
	}
}
