package app

import "testing"

// 设备被发现时应自动获得默认备份配置，否则「立即备份」会因「没有配置」失败。
// （首页提速后不再在请求路径上建配置，改由发现路径保证此不变量。）
func TestEnsureConfigUnsafeCreatesDefaultForNewDevice(t *testing.T) {
	app := newApplication()
	if _, ok := app.configs["NEW-DEV"]; ok {
		t.Fatal("前置条件：不应已有配置")
	}
	if !app.ensureConfigUnsafe("NEW-DEV", "GG") {
		t.Fatal("新设备应新建配置并返回 true")
	}
	cfg, ok := app.configs["NEW-DEV"]
	if !ok {
		t.Fatal("应已写入 app.configs")
	}
	if cfg.UDID != "NEW-DEV" || cfg.Name != "GG" {
		t.Errorf("UDID/Name 不对: %+v", cfg)
	}
	if cfg.BackupInterval != 24 || cfg.MinBatteryLevel != 20 || cfg.BackupDirectory != dirBackups {
		t.Errorf("默认值不对: %+v", cfg)
	}
	// 幂等：已存在则不覆盖、返回 false
	if app.ensureConfigUnsafe("NEW-DEV", "改名") {
		t.Error("已存在时应返回 false")
	}
	if app.configs["NEW-DEV"].Name != "GG" {
		t.Error("不应覆盖已有配置名")
	}
}
