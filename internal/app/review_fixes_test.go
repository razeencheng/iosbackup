package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// codex review [P2]: unback 端点缺 CSRF 校验。无 X-IOSBK-CSRF 头应 403。
func TestHandleBackupUnbackRequiresCSRF(t *testing.T) {
	app := newApplication()
	req := httptest.NewRequest("POST", "/api/backup-unback/U-X", nil) // 不带 CSRF 头
	rr := httptest.NewRecorder()
	app.handleBackupUnback(rr, req)
	if rr.Code != 403 {
		t.Errorf("无 CSRF 头应 403，得到 %d", rr.Code)
	}
}

// codex review [P2]: Unback 未占用 backupInProgress 槽，可与备份/重复 unback 并发。
// 设备正忙时 Unback 应直接报错且不执行外部命令。
func TestUnbackReentrancyGuard(t *testing.T) {
	app := newApplication()
	app.devices["U-RB"] = &device{UDID: "U-RB", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.backupInProgress["U-RB"] = true // 已有操作占用

	called := false
	app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
		called = true
		return []byte(""), nil
	}

	err := app.Unback(context.Background(), "U-RB")
	if err == nil {
		t.Fatal("设备正忙时 Unback 应返回错误")
	}
	if !strings.Contains(err.Error(), "正忙") {
		t.Errorf("应是「正忙」类错误，得到: %v", err)
	}
	if called {
		t.Error("正忙时不应执行 idevicebackup2 unback")
	}
}

// codex review [P2]: 改址/清空 IP 时 netmuxd 旧条目不更新。决策应区分 重置/重放/无操作。
func TestNetmuxdSyncDecision(t *testing.T) {
	cases := []struct {
		oldIP, newIP, want string
	}{
		{"", "", netmuxdNoop},                   // 一直无 IP
		{"", "10.0.0.5", netmuxdReplay},         // 首次设置 → 仅新增
		{"10.0.0.5", "10.0.0.5", netmuxdReplay}, // 不变 → 幂等重放
		{"10.0.0.5", "10.0.0.9", netmuxdReset},  // 改址 → 重置
		{"10.0.0.5", "", netmuxdReset},          // 清空 → 重置（清掉旧条目）
	}
	for _, c := range cases {
		if got := netmuxdSyncDecision(c.oldIP, c.newIP); got != c.want {
			t.Errorf("netmuxdSyncDecision(%q,%q)=%q，期望 %q", c.oldIP, c.newIP, got, c.want)
		}
	}
}
