package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionSlowDetailsDoNotBlockHealthScans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := defaultRuntimeConfig()
	cfg.PresenceInterval = 10 * time.Millisecond
	app := newApplicationWithRuntime(ctx, cfg)
	app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.configs["PHONE"] = app.defaultBackupConfig("PHONE", "Phone")
	infoStarted, scannedWhileBlocked := make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	app.cmdRunner = func(ctx context.Context, bin string, args, env []string) ([]byte, error) {
		if bin == cmdIdeviceID && !envHas(env, "USBMUXD_SOCKET_ADDRESS") {
			select {
			case <-infoStarted:
				select {
				case scannedWhileBlocked <- struct{}{}:
				default:
				}
			default:
			}
			return []byte("PHONE\n"), nil
		}
		if bin == cmdIdeviceInfo {
			once.Do(func() { close(infoStarted) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}
	done := make(chan struct{})
	go func() { app.statusPoller(ctx); close(done) }()
	select {
	case <-scannedWhileBlocked:
	case <-time.After(time.Second):
		t.Error("明细查询阻塞时仍必须继续健康扫描")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("根生命周期取消后扫描和明细 worker 必须退出")
	}
}

func TestConnectionConcurrentScansShareOneBoundedObservation(t *testing.T) {
	app := newApplication()
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	app.cmdRunner = func(ctx context.Context, _ string, _, _ []string) ([]byte, error) {
		if calls.Add(1) == 1 {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 3*time.Second || time.Until(deadline) < 2*time.Second {
				t.Error("设备列表查询必须具有 3 秒截止时间")
			}
			close(started)
		}
		select {
		case <-finish:
			return nil, errors.New("unavailable")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan connectionScan, 1)
	go func() { done <- app.scanConnection(context.Background(), connectionUSB, false) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	joined := app.scanConnection(ctx, connectionUSB, false)
	close(finish)
	<-done
	if !errors.Is(joined.err, context.DeadlineExceeded) || calls.Load() != 1 || app.connectionServices[0].failures != 1 {
		t.Fatalf("等待者取消不能重复执行/累计失败: result=%v calls=%d failures=%d", joined.err, calls.Load(), app.connectionServices[0].failures)
	}
}

func TestConnectionProcessChangeResetsOfflineEvidence(t *testing.T) {
	for _, connection := range []string{connectTypeUSB, connectTypeNetwork} {
		t.Run(connection, func(t *testing.T) {
			app := newApplication()
			app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connection)}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			defer app.registerActiveDeviceCommand("PHONE", connectTypeNetwork, cancel)()
			app.deviceOfflineSince["PHONE"] = time.Now().Add(-time.Hour)
			app.devicePresenceMissingSince["PHONE"] = time.Now().Add(-time.Hour)
			app.connectionProcessStarted(connectionWiFi)
			online := make(map[string]*device)
			if connection == connectTypeUSB {
				online["PHONE"] = cloneDevice(app.devices["PHONE"])
			}
			app.applyPresenceSnapshotWithNetwork(online, nil, nowBeijing())
			if ctx.Err() != nil || !app.devices["PHONE"].IsOnline {
				t.Fatal("进程更替中断观测，不能沿用旧代次缺失时间取消任务或发布离线")
			}
			if !app.connectionRefreshPending {
				t.Fatal("进程监督自动拉起也必须安排完整刷新")
			}
		})
	}
}

func TestConnectionQueryFailurePreservesLastKnownDevice(t *testing.T) {
	app := newApplication()
	app.devices["USB-1"] = &device{UDID: "USB-1", Name: "Phone", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte("ERROR: Unable to retrieve device list!"), errors.New("exit status 255")
	}
	app.refreshPresence()
	if !app.devices["USB-1"].IsOnline {
		t.Fatal("查询失败不能把最后确认在线的设备改为离线")
	}
}

func TestConnectionQueryFailureDoesNotCancelNetworkTask(t *testing.T) {
	app := newApplication()
	app.devices["WIFI-1"] = &device{UDID: "WIFI-1", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	defer app.registerActiveDeviceCommand("WIFI-1", connectTypeNetwork, cancel)()
	app.deviceOfflineSince["WIFI-1"] = time.Now().Add(-time.Hour)
	app.devicePresenceMissingSince["WIFI-1"] = time.Now().Add(-time.Hour)
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("service unavailable")
	}
	app.refreshPresence()
	if ctx.Err() != nil {
		t.Fatal("连接服务查询失败不能取消仍在运行的 Wi-Fi 任务")
	}
	if _, exists := app.deviceOfflineSince["WIFI-1"]; exists {
		t.Fatal("无效观测必须重置连续缺失计时")
	}
}

func TestConnectionHealthyBackendStillUpdates(t *testing.T) {
	app := newApplication()
	app.devices["USB-1"] = &device{UDID: "USB-1", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices["WIFI-1"] = &device{UDID: "WIFI-1", IsOnline: false, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.configs["WIFI-1"] = app.defaultBackupConfig("WIFI-1", "Phone")
	app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "-n") {
			return []byte("WIFI-1 (Network)\n"), nil
		}
		return nil, errors.New("USB unavailable")
	}
	app.refreshPresence()
	if !app.devices["USB-1"].IsOnline || !app.devices["WIFI-1"].IsOnline {
		t.Fatal("USB 状态应保留，同时正常发布健康 Wi-Fi 后端的观测")
	}
}

func TestConnectionEmptyListIsHealthy(t *testing.T) {
	app := newApplication()
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	for i := 0; i < 5; i++ {
		app.refreshPresence()
	}
	if got := app.dueConnectionRecovery(time.Now()); len(got) != 0 {
		t.Fatalf("成功空列表不应重启: %v", got)
	}
	for _, s := range app.connectionServices {
		if s.health != "healthy" || s.failures != 0 {
			t.Fatalf("空列表健康状态错误: %+v", s)
		}
	}
}

func TestConnectionFailureThresholdAndRollingLimit(t *testing.T) {
	app := newApplication()
	now := nowBeijing()
	failure := connectionScan{err: errors.New("query failed"), code: "query_failed", completed: now}
	for i := 1; i <= 3; i++ {
		failure.sequence = uint64(i)
		app.publishConnectionScan(connectionUSB, failure)
		if got := len(app.dueConnectionRecovery(now)); (i < 3 && got != 0) || (i == 3 && got != 1) {
			t.Fatalf("第 %d 次失败后的恢复数 %d", i, got)
		}
	}
	app.publishConnectionScan(connectionUSB, failure)
	if app.connectionServices[0].failures != 3 {
		t.Fatal("同一观测被重复计数")
	}
	app.connectionServices[0].attempts = []time.Time{now.Add(-time.Minute), now.Add(-30 * time.Second), now}
	if len(app.dueConnectionRecovery(now)) != 0 || app.connectionServices[0].phase != "manual_required" {
		t.Fatal("未遵守滚动恢复额度")
	}
	app.publishConnectionScan(0, connectionScan{sequence: 4, completed: now, devices: map[string]*device{}})
	for i := 5; i <= 7; i++ {
		failure.sequence = uint64(i)
		app.publishConnectionScan(0, failure)
	}
	if len(app.dueConnectionRecovery(now)) != 0 {
		t.Fatal("短暂成功不应清除滚动额度")
	}
	if len(app.dueConnectionRecovery(now.Add(16*time.Minute))) != 1 {
		t.Fatal("过期额度应释放")
	}
}

func TestConnectionGraceBackoffAndInstallationError(t *testing.T) {
	app := newApplication()
	now := nowBeijing()
	s := &app.connectionServices[0]
	s.failures = 3
	s.code = "query_timeout"
	s.graceUntil = now.Add(15 * time.Second)
	if len(app.dueConnectionRecovery(now)) != 0 {
		t.Fatal("启动宽限内不得重启")
	}
	s.graceUntil = time.Time{}
	s.nextAttempt = now.Add(30 * time.Second)
	if len(app.dueConnectionRecovery(now)) != 0 || s.phase != "backoff" {
		t.Fatal("退避内不得重启")
	}
	s.nextAttempt = time.Time{}
	s.code = "tool_unavailable"
	if len(app.dueConnectionRecovery(now)) != 0 {
		t.Fatal("工具安装错误不得重启服务")
	}
}

func TestConnectionRejectsOldGenerationAndMalformedOutput(t *testing.T) {
	app := newApplication()
	app.connectionProcessStarted(connectionUSB)
	app.publishConnectionScan(0, connectionScan{sequence: 1, generation: 0, err: errors.New("old"), completed: nowBeijing()})
	if app.connectionServices[0].failures != 0 {
		t.Fatal("旧进程观测覆盖了新代次")
	}
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte("ERROR: Unable to retrieve device list!"), nil
	}
	result := app.scanConnection(app.rootCtx, 0, false)
	if result.err == nil || result.code != "invalid_response" || len(result.devices) != 0 {
		t.Fatalf("错误文本不能解析为设备: %+v", result)
	}
}

func TestConnectionBackendPublishesBeforeOtherBackendCompletes(t *testing.T) {
	app := newApplication()
	app.devices["USB-1"] = &device{UDID: "USB-1", Connection: connectionTypeDesc(connectTypeUSB)}
	app.configs["USB-1"] = app.defaultBackupConfig("USB-1", "Phone")
	networkStarted, finishNetwork, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	app.cmdRunner = func(ctx context.Context, _ string, args, _ []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "-n") {
			close(networkStarted)
			select {
			case <-finishNetwork:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte("USB-1\n"), nil
	}
	go func() { app.scanConnections(app.rootCtx); close(done) }()
	<-networkStarted
	waitFor(t, func() bool { app.mu.RLock(); defer app.mu.RUnlock(); return app.devices["USB-1"].IsOnline }, time.Second)
	select {
	case <-done:
		t.Fatal("网络查询应仍在等待")
	default:
	}
	close(finishNetwork)
	<-done
}

func TestConnectionCancelledScanDoesNotCountFailure(t *testing.T) {
	app := newApplication()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	app.cmdRunner = func(ctx context.Context, _ string, _, _ []string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan struct{})
	go func() { app.scanConnection(ctx, 0, false); close(done) }()
	<-started
	cancel()
	<-done
	if app.connectionServices[0].failures != 0 {
		t.Fatal("请求取消不应计为服务故障")
	}
}

func TestConnectionSharedBackendPreservesBothTransports(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiBackend = wifiBackendUSBMuxd2
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices["PHONE"] = &device{UDID: "PHONE", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true}
	app.configs["PHONE"] = app.defaultBackupConfig("PHONE", "Phone")
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte("PHONE (USB)\nPHONE (Network)\n"), nil
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	defer app.registerActiveDeviceCommand("PHONE", connectTypeNetwork, cancel)()
	app.deviceOfflineSince["PHONE"] = time.Now().Add(-time.Hour)
	app.refreshPresence()
	if ctx.Err() != nil {
		t.Fatal("共享服务同时枚举 USB/Wi-Fi 时不得丢失活动 Wi-Fi 流的在线证据")
	}
	if app.devices["PHONE"].Connection != connectionTypeDesc(connectTypeUSB) {
		t.Fatal("页面仍应优先显示 USB")
	}
}
