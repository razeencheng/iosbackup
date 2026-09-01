package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDeviceRemovalTestApp(t *testing.T, udid string) (*application, backupConfig) {
	t.Helper()
	root := t.TempDir()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = filepath.Join(root, "configs")
	cfg.BackupsRoot = filepath.Join(root, "backups")
	app := newApplicationWithRuntime(context.Background(), cfg)
	if err := os.MkdirAll(cfg.ConfigsRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.BackupsRoot, 0755); err != nil {
		t.Fatal(err)
	}

	backupCfg := validBackupConfig(cfg.BackupsRoot)
	backupCfg.UDID = udid
	backupCfg.Name = "家庭 iPhone"
	backupCfg.BackupDirectory = cfg.BackupsRoot
	backupCfg.AutoBackupEnabled = true
	backupCfg.NetworkAddress = "10.10.0.15"
	backupCfg.LastBackupConnection = connectTypeNetwork
	backupCfg.RestoreEnabled = true
	backupCfg.LastBackup = time.Date(2026, 8, 14, 7, 53, 0, 0, beijingLocation)
	if err := app.configStore.Put(backupCfg); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.configs[udid] = cloneBackupConfig(&backupCfg)
	app.devices[udid] = &device{
		UDID:       udid,
		Name:       backupCfg.Name,
		DeviceType: "iPhone14,4",
		Connection: connectionTypeDesc(connectTypeUSB),
		LastBackup: backupCfg.LastBackup,
		IsOnline:   true,
	}
	app.mu.Unlock()
	return app, backupCfg
}

func TestBackupConfigRemovedAtRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	store := newBackupConfigStore(path, []string{root})
	cfg := validBackupConfig(root)
	removedAt := time.Date(2026, 8, 14, 20, 30, 0, 0, beijingLocation)
	cfg.RemovedAt = &removedAt

	if err := store.Put(cfg); err != nil {
		t.Fatal(err)
	}
	reloaded := newBackupConfigStore(path, []string{root})
	configs, err := reloaded.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := configs[cfg.UDID].RemovedAt
	if got == nil || !got.Equal(removedAt) {
		t.Fatalf("removed_at 未正确持久化: got=%v want=%v", got, removedAt)
	}
	if _, offset := got.Zone(); offset != 8*60*60 {
		t.Fatalf("removed_at 应保留北京时间偏移，得到 %s", got.Format(time.RFC3339))
	}
}

func TestRemoveDevicePersistsWithoutChangingRetainedConfig(t *testing.T) {
	const udid = "REMOVE-PRESERVE"
	app, before := newDeviceRemovalTestApp(t, udid)

	removedAt, err := app.removeDevice(udid)
	if err != nil {
		t.Fatal(err)
	}
	if removedAt.IsZero() {
		t.Fatal("移除时间不能为空")
	}

	app.mu.RLock()
	_, visible := app.devices[udid]
	after := cloneBackupConfig(app.configs[udid])
	pending := app.deviceRemovalPending[udid]
	app.mu.RUnlock()
	if visible {
		t.Fatal("移除成功后设备不应继续出现在活动列表")
	}
	if pending {
		t.Fatal("移除成功后 pending 必须清理")
	}
	if after == nil || after.RemovedAt == nil || !after.RemovedAt.Equal(removedAt) {
		t.Fatalf("内存归档状态错误: %+v", after)
	}
	assertRetainedBackupConfig(t, before, *after)

	reloaded := newBackupConfigStore(app.paths.BackupConfigFile, []string{app.paths.BackupsRoot})
	configs, err := reloaded.Load()
	if err != nil {
		t.Fatal(err)
	}
	persisted := configs[udid]
	if persisted.RemovedAt == nil || !persisted.RemovedAt.Equal(removedAt) {
		t.Fatalf("磁盘归档状态错误: %+v", persisted)
	}
	assertRetainedBackupConfig(t, before, persisted)
}

func TestRemoveAndRestoreDeviceAreIdempotent(t *testing.T) {
	const udid = "REMOVE-IDEMPOTENT"
	app, _ := newDeviceRemovalTestApp(t, udid)

	first, err := app.removeDevice(udid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.removeDevice(udid)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Equal(first) {
		t.Fatalf("重复移除不得改写时间: first=%s second=%s", first, second)
	}

	if err := app.restoreRemovedDevice(udid); err != nil {
		t.Fatal(err)
	}
	if err := app.restoreRemovedDevice(udid); err != nil {
		t.Fatal(err)
	}
	app.mu.RLock()
	device := cloneDevice(app.devices[udid])
	cfg := cloneBackupConfig(app.configs[udid])
	app.mu.RUnlock()
	if cfg == nil || cfg.RemovedAt != nil {
		t.Fatalf("恢复后 removed_at 应为空: %+v", cfg)
	}
	if device == nil || device.IsOnline {
		t.Fatalf("恢复后应先发布离线设备卡片: %+v", device)
	}
}

func TestRemoveDeviceRejectsBusyWithoutMutation(t *testing.T) {
	const udid = "REMOVE-BUSY"
	app, before := newDeviceRemovalTestApp(t, udid)
	app.mu.Lock()
	app.backupInProgress[udid] = true
	app.mu.Unlock()

	if _, err := app.removeDevice(udid); !errors.Is(err, errDeviceBusy) {
		t.Fatalf("忙碌设备应返回 errDeviceBusy，得到 %v", err)
	}
	app.mu.RLock()
	_, visible := app.devices[udid]
	after := cloneBackupConfig(app.configs[udid])
	pending := app.deviceRemovalPending[udid]
	app.mu.RUnlock()
	if !visible || pending || after == nil || after.RemovedAt != nil {
		t.Fatalf("忙碌拒绝后状态被修改: visible=%v pending=%v cfg=%+v", visible, pending, after)
	}
	assertRetainedBackupConfig(t, before, *after)
}

func TestRemoveDevicePersistenceFailureRollsBack(t *testing.T) {
	const udid = "REMOVE-ROLLBACK"
	app, before := newDeviceRemovalTestApp(t, udid)
	app.configStore.lastGoodPath = t.TempDir() // 原子写不能用目录替代文件，稳定触发落盘失败。

	if _, err := app.removeDevice(udid); err == nil {
		t.Fatal("配置落盘失败时移除必须失败")
	}
	app.mu.RLock()
	_, visible := app.devices[udid]
	after := cloneBackupConfig(app.configs[udid])
	pending := app.deviceRemovalPending[udid]
	app.mu.RUnlock()
	if !visible || pending || after == nil || after.RemovedAt != nil {
		t.Fatalf("落盘失败后必须完整回滚: visible=%v pending=%v cfg=%+v", visible, pending, after)
	}
	assertRetainedBackupConfig(t, before, *after)
}

func TestRemovedDevicePresenceDoesNotReappear(t *testing.T) {
	const udid = "REMOVED-PRESENCE"
	app, _ := newDeviceRemovalTestApp(t, udid)
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}

	online := map[string]*device{
		udid: {UDID: udid, Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true},
	}
	newOnline, configCreated := app.applyPresenceSnapshotWithNetwork(online, nil, nowBeijing())
	if newOnline || configCreated {
		t.Fatalf("已移除设备不得被当作新上线或新配置: online=%v config=%v", newOnline, configCreated)
	}
	app.mu.RLock()
	_, visible := app.devices[udid]
	app.mu.RUnlock()
	if visible {
		t.Fatal("USB/Wi-Fi 重新发现不得让已移除设备重新出现")
	}
}

func TestBuildStatusSnapshotExcludesRemovedDeviceDefensively(t *testing.T) {
	const udid = "REMOVED-SNAPSHOT"
	app, _ := newDeviceRemovalTestApp(t, udid)
	removedAt := nowBeijing()
	app.mu.Lock()
	app.configs[udid].RemovedAt = &removedAt
	// 保留一条陈旧活动记录，模拟刷新与移除交错时的防御性快照。
	app.mu.Unlock()

	if snap := app.buildStatusSnapshot(); len(snap.Devices) != 0 {
		t.Fatalf("状态快照不得发布已移除设备: %+v", snap.Devices)
	}
}

func TestRemovedDeviceRefreshSkipsProbeAndPairing(t *testing.T) {
	const udid = "REMOVED-REFRESH"
	app, _ := newDeviceRemovalTestApp(t, udid)
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}
	runner := &mockRunner{}
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		if strings.HasSuffix(name, "idevice_id") {
			return []byte(udid + "\n"), nil
		}
		return nil, fmt.Errorf("已移除设备不应执行 %s %v", name, args)
	}
	app.cmdRunner = runner.run

	if err := app.RefreshDevices(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // 捕获错误实现可能启动的异步配对命令。
	app.mu.RLock()
	_, visible := app.devices[udid]
	app.mu.RUnlock()
	if visible {
		t.Fatal("完整刷新后已移除设备不得重新出现")
	}
	for _, call := range runner.allCalls() {
		if !strings.HasSuffix(call.name, "idevice_id") {
			t.Fatalf("已移除设备只允许参与列表读取，不得探测或配对: %+v", call)
		}
	}
}

func TestRemovedDeviceExcludedFromAutoBackupCandidates(t *testing.T) {
	const udid = "REMOVED-SCHEDULER"
	app, _ := newDeviceRemovalTestApp(t, udid)
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}

	if candidates := app.autoBackupCandidates(); len(candidates) != 0 {
		t.Fatalf("已移除设备不得进入自动备份候选: %+v", candidates)
	}
	if app.tryBeginAutoBackupCheck(udid) {
		t.Fatal("已移除设备不得占用自动检查状态")
	}
}

func TestRemovedDeviceCheckAndBackupReturnsBeforeCommand(t *testing.T) {
	const udid = "REMOVED-CHECK"
	app, cfg := newDeviceRemovalTestApp(t, udid)
	removedAt := nowBeijing()
	cfg.RemovedAt = &removedAt
	cfg.StartTime = "00:00"
	cfg.EndTime = "23:59"
	cfg.LastBackup = time.Time{}
	app.mu.Lock()
	app.configs[udid] = cloneBackupConfig(&cfg)
	app.devices[udid] = &device{
		UDID:         udid,
		Name:         cfg.Name,
		Connection:   connectionTypeDesc(connectTypeUSB),
		IsOnline:     true,
		BatteryLevel: 100,
	}
	app.checkInProgress[udid] = true
	app.mu.Unlock()
	runner := &mockRunner{}
	app.cmdRunner = runner.run

	app.checkAndBackup(udid, cfg, deviceStatus{UDID: udid, Connection: connectTypeUSB, IsOnline: true})
	if calls := runner.allCalls(); len(calls) != 0 {
		t.Fatalf("已移除设备不得执行状态探测或备份命令: %+v", calls)
	}
	app.mu.RLock()
	checking := app.checkInProgress[udid]
	app.mu.RUnlock()
	if checking {
		t.Fatal("自动检查退出后必须清理 checkInProgress")
	}
}

func TestRemovedDeviceBlocksBackupEntryPoints(t *testing.T) {
	const udid = "REMOVED-BACKUP"
	app, _ := newDeviceRemovalTestApp(t, udid)
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := app.backupGateReason(udid); code != "removed" {
		t.Fatalf("手动备份预检应返回 removed，得到 %q", code)
	}
	if err := app.PerformBackup(udid); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("PerformBackup 应拒绝已移除设备，得到 %v", err)
	}
	if err := app.PerformBackupWithConnection(udid, connectionTypeDesc(connectTypeUSB)); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("PerformBackupWithConnection 应拒绝已移除设备，得到 %v", err)
	}
	app.mu.RLock()
	busy := app.backupInProgress[udid]
	app.mu.RUnlock()
	if busy {
		t.Fatal("拒绝已移除设备后不得遗留 backupInProgress")
	}
	if calls := runner.allCalls(); len(calls) != 0 {
		t.Fatalf("拒绝已移除设备后不得执行命令: %+v", calls)
	}
}

func TestRemovedDeviceBlocksBackupToolsAndEncryption(t *testing.T) {
	const udid = "REMOVED-TOOLS"
	app, _ := newDeviceRemovalTestApp(t, udid)
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}

	if _, err := app.BackupInfo(context.Background(), udid); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("BackupInfo 应拒绝已移除设备，得到 %v", err)
	}
	if _, err := app.BackupList(context.Background(), udid); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("BackupList 应拒绝已移除设备，得到 %v", err)
	}
	if err := app.SetBackupEncryption(context.Background(), udid, false, "pw"); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("加密操作应拒绝已移除设备，得到 %v", err)
	}
	if _, _, err := app.acquireLocalBackupOp(udid); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("本地解包应拒绝已移除设备，得到 %v", err)
	}
	if calls := runner.allCalls(); len(calls) != 0 {
		t.Fatalf("被拒绝的工具操作不得执行命令: %+v", calls)
	}
}

func TestRemovedDeviceDeleteBackupKeepsFiles(t *testing.T) {
	const udid = "REMOVED-DELETE"
	app, _ := newDeviceRemovalTestApp(t, udid)
	backupPath := filepath.Join(app.paths.BackupsRoot, udid)
	if err := os.MkdirAll(backupPath, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(backupPath, "Manifest.plist")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}

	if err := app.DeleteDeviceBackup(udid); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("删除备份应拒绝已移除设备，得到 %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("已移除设备的备份文件必须保留: data=%q err=%v", data, err)
	}
}

func TestRemovalPendingBlocksNewOperations(t *testing.T) {
	const udid = "REMOVAL-PENDING"
	app, _ := newDeviceRemovalTestApp(t, udid)
	app.mu.Lock()
	app.deviceRemovalPending[udid] = true
	app.mu.Unlock()

	if code, _, _ := app.backupGateReason(udid); code != "removal_pending" {
		t.Fatalf("移除事务期间预检应返回 removal_pending，得到 %q", code)
	}
	if err := app.PerformBackup(udid); !errors.Is(err, errDeviceRemovalPending) {
		t.Fatalf("移除事务期间不得启动备份，得到 %v", err)
	}
	if _, _, err := app.acquireLocalBackupOp(udid); !errors.Is(err, errDeviceRemovalPending) {
		t.Fatalf("移除事务期间不得启动本地操作，得到 %v", err)
	}
}

func TestRemovedDeviceRoutesRejectBeforeMutationOrProbe(t *testing.T) {
	const udid = "REMOVED-ROUTES"
	app, before := newDeviceRemovalTestApp(t, udid)
	app.enableExperimentalOperations = true
	if _, err := app.removeDevice(udid); err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	app.reachProbe = func(string) (bool, string) {
		probeCalls++
		return true, "不应调用"
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    string
	}{
		{"保存设置", app.handleSaveConfig, "/api/save-config/" + udid, `{"auto_backup_enabled":false}`},
		{"测试连接", app.handleTestDeviceIP, "/api/test-ip/" + udid, `{"ip":"192.0.2.1"}`},
		{"加密", app.handleEncryption, "/api/encryption/" + udid, `{"enable":false,"password":"pw"}`},
		{"改密", app.handleChangePassword, "/api/backup-changepw/" + udid, `{"old":"old","new":"new"}`},
		{"重新配对", app.handlePairDevice, "/api/pair/" + udid, `{}`},
		{"解包", app.handleBackupUnback, "/api/backup-unback/" + udid, `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set(csrfHeader, "test")
			rr := httptest.NewRecorder()
			tc.handler(rr, req)
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "已移除") {
				t.Fatalf("已移除设备应在入口返回 409: code=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
	if probeCalls != 0 {
		t.Fatalf("已移除设备不得触发网络探测，调用 %d 次", probeCalls)
	}
	app.mu.RLock()
	after := cloneBackupConfig(app.configs[udid])
	app.mu.RUnlock()
	if after == nil {
		t.Fatal("配置不应丢失")
	}
	assertRetainedBackupConfig(t, before, *after)
}

func assertRetainedBackupConfig(t *testing.T, before, after backupConfig) {
	t.Helper()
	if !before.LastBackup.Equal(after.LastBackup) {
		t.Fatalf("移除/恢复不得修改最后备份时间: before=%s after=%s", before.LastBackup, after.LastBackup)
	}
	before.LastBackup = time.Time{}
	after.LastBackup = time.Time{}
	before.RemovedAt = nil
	after.RemovedAt = nil
	if before != after {
		t.Fatalf("移除/恢复不得修改原配置:\n before=%+v\n after=%+v", before, after)
	}
}
