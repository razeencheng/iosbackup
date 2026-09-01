package app

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1：被限制时 handleBackup 同步返回 409 + error_code（不再静默"已开始"）。
func TestHandleBackupGatedReturnsCode(t *testing.T) {
	app := newApplication()
	app.devices["U"] = &device{UDID: "U", IsOnline: true, IsCharging: false, BatteryLevel: 50}
	app.configs["U"] = &backupConfig{UDID: "U", OnlyWhenCharging: true, MinBatteryLevel: 20}
	req := httptest.NewRequest("POST", "/api/backup/U", nil)
	rr := httptest.NewRecorder()
	app.handleBackup(rr, req)
	if rr.Code != 409 {
		t.Fatalf("被限制应 409，得到 %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not_charging") {
		t.Errorf("响应应含 error_code=not_charging，得到: %s", rr.Body.String())
	}
}

// #1：手动备份的即时拦截原因码（供 handleBackup 同步预检反馈）。
func TestBackupGateReason(t *testing.T) {
	mk := func(online, charging bool, batt, minBatt int, onlyCharging bool, busy bool) *application {
		app := newApplication()
		app.devices["U"] = &device{UDID: "U", IsOnline: online, IsCharging: charging, BatteryLevel: batt}
		app.configs["U"] = &backupConfig{UDID: "U", MinBatteryLevel: minBatt, OnlyWhenCharging: onlyCharging}
		if busy {
			app.backupInProgress["U"] = true
		}
		return app
	}
	cases := []struct {
		name string
		app  *application
		want string
	}{
		{"离线", mk(false, false, 50, 20, true, false), "offline"},
		{"正忙", mk(true, true, 50, 20, true, true), "busy"},
		{"未充电(仅充电时备份)", mk(true, false, 50, 20, true, false), "not_charging"},
		{"电量不足", mk(true, true, 10, 20, true, false), "low_battery"},
		{"可备份(充电中)", mk(true, true, 50, 20, true, false), ""},
		{"可备份(不限充电)", mk(true, false, 50, 20, false, false), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, _ := c.app.backupGateReason("U")
			if code != c.want {
				t.Errorf("got %q, want %q", code, c.want)
			}
		})
	}
	// 缺设备 → missing
	if code, _, _ := newApplication().backupGateReason("NOPE"); code != "missing" {
		t.Errorf("缺设备应 missing，得到 %q", code)
	}
}

// #3：备份目录占用空间计算 + 人类可读格式。
func TestDirSizeAndHuman(t *testing.T) {
	dir := t.TempDir()
	// 写两个文件共 3000 字节，含子目录
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0o755)
	if err := os.WriteFile(filepath.Join(sub, "b.bin"), make([]byte, 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dirSizeBytes(dir); got != 3000 {
		t.Errorf("dirSizeBytes=%d，期望 3000", got)
	}
	if got := dirSizeBytes(filepath.Join(dir, "nonexistent")); got != 0 {
		t.Errorf("不存在目录应 0，得到 %d", got)
	}

	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{3000, "2.9 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	} {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d)=%q，期望 %q", c.n, got, c.want)
		}
	}
}
