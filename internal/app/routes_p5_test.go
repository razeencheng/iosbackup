package app

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"iosbackup/internal/buildinfo"
)

// homePayload 与 handleHome 渲染用的数据结构一致。
type homePayload struct {
	Devices                       []*device
	Configs                       map[string]*backupConfig
	BackupStatuses                map[string]bool
	HasBackupInProgress           bool
	EncryptionAvailable           bool
	ExperimentalOperationsEnabled bool
	DiscoveredIPs                 map[string]string
	Version                       string
	Commit                        string
	SourceURL                     string
	CSRFToken                     string
	AuthEnabled                   bool
}

func renderIndex(t *testing.T, p homePayload) string {
	t.Helper()
	if p.Commit == "" {
		p.Commit = buildinfo.Commit
	}
	if p.SourceURL == "" {
		p.SourceURL = buildinfo.SourceURL
	}
	data, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"contains": strings.Contains,
	}).Parse(string(data))
	if err != nil {
		t.Fatalf("index.html 模板解析失败: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		t.Fatalf("index.html 渲染失败: %v", err)
	}
	return buf.String()
}

func TestIndexTemplateOnlineDevice(t *testing.T) {
	dev := &device{UDID: "U-ON", Name: "Razeen的iPad", DeviceType: "iPad", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true, BatteryLevel: 80}
	cfg := &backupConfig{UDID: "U-ON", StartTime: "22:00", EndTime: "06:00", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups", RestoreEnabled: true}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-ON": cfg},
		BackupStatuses: map[string]bool{"U-ON": false}, EncryptionAvailable: true,
	})
	// 新模板：分段导航 + 每设备的 id/handler + i18n 字典 + CSRF
	for _, want := range []string{
		"networkAddress-U-ON",     // 设备 IP 输入框 id
		"switchSection('U-ON'",    // 分段导航 handler
		"openRestoreModal('U-ON'", // 恢复入口（RestoreEnabled=true）
		"X-IOSBK-CSRF",            // CSRF 头
		`data-sec-panel="enc"`,    // 加密分区面板
		"IOSBK_DICT",              // 内嵌双语字典
		"toggleCard('U-ON')",      // 卡片展开
	} {
		if !strings.Contains(html, want) {
			t.Errorf("渲染结果应包含 %q", want)
		}
	}
}

func TestIndexTemplateOfflineMigrationBanner(t *testing.T) {
	// 离线 + 曾 Wi-Fi 备份 + 无 IP + 有过备份 → 迁移黄条出现（gotoFillIP 入口）
	dev := &device{UDID: "U-OFF", Name: "旧iPad", DeviceType: "iPad", Connection: "离线", IsOnline: false, LastBackup: time.Date(2026, 6, 1, 2, 0, 0, 0, beijingLocation)}
	cfg := &backupConfig{UDID: "U-OFF", NetworkAddress: "", LastBackupConnection: "network"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-OFF": cfg},
		BackupStatuses: map[string]bool{"U-OFF": false}, EncryptionAvailable: false,
	})
	if !strings.Contains(html, "gotoFillIP('U-OFF')") {
		t.Error("离线+曾Wi-Fi备份+无IP 应出现迁移黄条（gotoFillIP 入口）")
	}
	if !strings.Contains(html, "IOSBK_SECRET_KEY") {
		t.Error("EncryptionAvailable=false 时应提示配置 IOSBK_SECRET_KEY")
	}
}

func TestIndexTemplateShowsDiscoveredIP(t *testing.T) {
	dev := &device{UDID: "U-DISC", Name: "iPad", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true}
	cfg := &backupConfig{UDID: "U-DISC"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-DISC": cfg},
		BackupStatuses: map[string]bool{"U-DISC": false}, EncryptionAvailable: true,
		DiscoveredIPs: map[string]string{"U-DISC": "10.10.0.249"},
	})
	// 发现块的唯一标记是语义搜索图标 + IP 值
	if !strings.Contains(html, "ui-icons.svg#icon-search") || !strings.Contains(html, "10.10.0.249") {
		t.Error("有发现 IP 时应在设备 IP 栏下显示自动发现的 IP")
	}
}

func TestIndexTemplateNoDiscoveredIPLine(t *testing.T) {
	dev := &device{UDID: "U-NODISC", Name: "iPad", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true}
	cfg := &backupConfig{UDID: "U-NODISC"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-NODISC": cfg},
		BackupStatuses: map[string]bool{"U-NODISC": false}, EncryptionAvailable: true,
		DiscoveredIPs: map[string]string{}, // 无发现 IP
	})
	if strings.Contains(html, "ui-icons.svg#icon-search") {
		t.Error("无发现 IP 时不应显示发现块")
	}
}

func TestIndexTemplateNoBannerWhenIPSet(t *testing.T) {
	// 离线但已填 IP → 不出现黄条
	dev := &device{UDID: "U-IP", Name: "iPad", IsOnline: false, LastBackup: time.Date(2026, 6, 1, 2, 0, 0, 0, beijingLocation)}
	cfg := &backupConfig{UDID: "U-IP", NetworkAddress: "10.0.0.5", LastBackupConnection: "network"}
	html := renderIndex(t, homePayload{
		Devices: []*device{dev}, Configs: map[string]*backupConfig{"U-IP": cfg},
		BackupStatuses: map[string]bool{"U-IP": false}, EncryptionAvailable: true,
	})
	if strings.Contains(html, "gotoFillIP('U-IP')") {
		t.Error("已填 IP 不应出现迁移黄条")
	}
}
