package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 全局并发闸：超过上限的 tryAcquire 应失败，释放后恢复。
func TestBackupSemCap(t *testing.T) {
	s := newBackupSem(1)
	if !s.tryAcquire() {
		t.Fatal("第一个应获取成功")
	}
	if s.tryAcquire() {
		t.Error("超过上限应失败")
	}
	if s.inUse() != 1 {
		t.Errorf("inUse 应为 1，得 %d", s.inUse())
	}
	s.release()
	if s.inUse() != 0 {
		t.Errorf("释放后 inUse 应为 0，得 %d", s.inUse())
	}
	if !s.tryAcquire() {
		t.Error("释放后应能再次获取")
	}
}

// 达到并发上限时，handleBackup 同步返回 409 + error_code=backup_busy（设备本身可备）。
func TestHandleBackupRejectedAtConcurrencyCap(t *testing.T) {
	app := newApplication()
	udid := "CAP-DEV"
	app.devices[udid] = &device{UDID: udid, IsOnline: true, IsCharging: true, BatteryLevel: 80}
	app.configs[udid] = &backupConfig{UDID: udid, OnlyWhenCharging: false, MinBatteryLevel: 20}
	// 用 cap=1 固定上限，使「占满 → 拒绝」可确定性断言（与生产默认值解耦）
	app.backupSem = newBackupSem(1)
	if !app.backupSem.tryAcquire() {
		t.Fatal("预置：应能占用唯一的槽")
	}
	defer app.backupSem.release()

	req := httptest.NewRequest("POST", "/api/backup/"+udid, nil)
	rr := httptest.NewRecorder()
	app.handleBackup(rr, req)

	if rr.Code != 409 {
		t.Fatalf("并发上限时应 409，得到 %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "backup_busy") {
		t.Errorf("应含 error_code=backup_busy，得到: %s", rr.Body.String())
	}
}
