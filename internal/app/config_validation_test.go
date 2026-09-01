package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func validBackupConfig(root string) backupConfig {
	return backupConfig{
		UDID:            "TEST-DEVICE-ID",
		StartTime:       "01:30",
		EndTime:         "05:45",
		BackupInterval:  24,
		MinBatteryLevel: 20,
		BackupDirectory: filepath.Join(root, "device"),
		NetworkAddress:  "192.168.1.20",
	}
}

func TestValidateBackupConfig(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		mutate func(*backupConfig)
	}{
		{"无效开始时间", func(c *backupConfig) { c.StartTime = "25:00" }},
		{"无效结束时间", func(c *backupConfig) { c.EndTime = "5:30" }},
		{"备份间隔过小", func(c *backupConfig) { c.BackupInterval = 0 }},
		{"备份间隔过大", func(c *backupConfig) { c.BackupInterval = 721 }},
		{"电量过小", func(c *backupConfig) { c.MinBatteryLevel = -1 }},
		{"电量过大", func(c *backupConfig) { c.MinBatteryLevel = 101 }},
		{"UDID 含路径字符", func(c *backupConfig) { c.UDID = "../../etc" }},
		{"路径逃逸", func(c *backupConfig) { c.BackupDirectory = filepath.Join(root, "..", "escape") }},
		{"回环地址", func(c *backupConfig) { c.NetworkAddress = "127.0.0.1" }},
		{"未指定地址", func(c *backupConfig) { c.NetworkAddress = "0.0.0.0" }},
		{"组播地址", func(c *backupConfig) { c.NetworkAddress = "224.0.0.1" }},
		{"链路本地地址", func(c *backupConfig) { c.NetworkAddress = "169.254.1.2" }},
		{"非法地址", func(c *backupConfig) { c.NetworkAddress = "not-an-ip" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBackupConfig(root)
			tt.mutate(&cfg)
			if err := validateBackupConfig(cfg, []string{root}); err == nil {
				t.Fatal("应拒绝无效配置")
			}
		})
	}

	if err := validateBackupConfig(validBackupConfig(root), []string{root}); err != nil {
		t.Fatalf("有效配置不应被拒绝: %v", err)
	}
}

func TestValidateBackupConfigRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}
	cfg := validBackupConfig(root)
	cfg.BackupDirectory = filepath.Join(link, "device")
	if err := validateBackupConfig(cfg, []string{root}); err == nil {
		t.Fatal("备份目录不得通过符号链接逃逸允许根目录")
	}
}

func TestMergeBackupConfigRejectsUnknownAndInvalidFields(t *testing.T) {
	cfg := validBackupConfig(t.TempDir())
	for name, raw := range map[string]string{
		"unknown":  `{"unexpected":true}`,
		"bad_type": `{"backup_interval":"daily"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				t.Fatal(err)
			}
			if err := mergeBackupConfig(&cfg, fields); err == nil {
				t.Fatal("应拒绝未知字段或错误类型")
			}
		})
	}
}
