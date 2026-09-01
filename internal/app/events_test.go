package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventHubBroadcastDelivers(t *testing.T) {
	h := newEventHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	h.Broadcast([]byte("hello"))

	select {
	case msg := <-ch:
		if string(msg) != "hello" {
			t.Fatalf("期望 hello，得到 %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者应收到广播")
	}
}

func TestEventHubSlowClientDoesNotBlock(t *testing.T) {
	h := newEventHub()
	slow := h.Subscribe() // 不读 → 缓冲很快填满
	fast := h.Subscribe() // 正常读
	defer h.Unsubscribe(slow)
	defer h.Unsubscribe(fast)

	// 广播远超 channel 容量；慢客户端被丢帧但不能阻塞广播
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("慢客户端不应阻塞 Broadcast")
	}

	// fast 至少能收到一帧（缓冲里有）
	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("正常客户端应能收到帧")
	}
}

func TestEventHubUnsubscribeStops(t *testing.T) {
	h := newEventHub()
	ch := h.Subscribe()
	if h.clientCount() != 1 {
		t.Fatalf("订阅后应有 1 个客户端，得到 %d", h.clientCount())
	}
	h.Unsubscribe(ch)
	if h.clientCount() != 0 {
		t.Fatalf("注销后应有 0 个客户端，得到 %d", h.clientCount())
	}
	// 注销后广播不应 panic（channel 已关闭且移出 map）
	h.Broadcast([]byte("after"))
	// 二次注销应安全（幂等）
	h.Unsubscribe(ch)
}

func TestConnCode(t *testing.T) {
	cases := []struct {
		online bool
		conn   string
		want   string
	}{
		{true, connectionTypeDesc(connectTypeUSB), "usb"},
		{true, connectionTypeDesc(connectTypeNetwork), "wifi"},
		{true, "未知连接", "wifi"},
		{false, connectionTypeDesc(connectTypeUSB), "offline"},
	}
	for _, c := range cases {
		d := &device{IsOnline: c.online, Connection: c.conn}
		if got := connCode(d); got != c.want {
			t.Errorf("connCode(online=%v,%q)=%q，期望 %q", c.online, c.conn, got, c.want)
		}
	}
}

func TestBuildStatusSnapshot(t *testing.T) {
	app := newApplication()
	app.devices["U1"] = &device{UDID: "U1", Name: "iPad", DeviceType: "iPad14,1", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true, BatteryLevel: 80, IsCharging: true}
	app.devices["U2"] = &device{UDID: "U2", Name: "iPhone", IsOnline: false}
	app.backupInProgress["U1"] = true

	snap := app.buildStatusSnapshot()
	if len(snap.Devices) != 2 {
		t.Fatalf("应有 2 台设备，得到 %d", len(snap.Devices))
	}
	// 在线优先排序：U1 在前
	if snap.Devices[0].UDID != "U1" {
		t.Errorf("在线设备应排前，得到 %q", snap.Devices[0].UDID)
	}
	d0 := snap.Devices[0]
	if d0.Conn != "usb" || !d0.Online || d0.Battery != 80 || !d0.Charging || !d0.BackingUp {
		t.Errorf("U1 快照字段不符: %+v", d0)
	}
	if snap.BackupInProgress != 1 {
		t.Errorf("BackupInProgress 应为 1，得到 %d", snap.BackupInProgress)
	}
}

func TestBuildStatusSnapshotIncludesBackupProgress(t *testing.T) {
	app := newApplication()
	udid := "U-PROGRESS-DTO"
	updatedAt := time.Date(2026, 8, 14, 10, 30, 0, 0, time.UTC)
	app.devices[udid] = &device{UDID: udid, Name: "iPhone", IsOnline: true}
	app.backupInProgress[udid] = true
	app.backupProgress[udid] = backupProgress{
		State:              backupProgressRunning,
		Phase:              "receiving",
		OverallPercent:     float64Ptr(81),
		CurrentFilePercent: float64Ptr(47.5),
		CurrentBytes:       int64Ptr(209_000_000),
		CurrentTotalBytes:  int64Ptr(440_000_000),
		UpdatedAt:          updatedAt,
	}

	snap := app.buildStatusSnapshot()
	if len(snap.Devices) != 1 || snap.Devices[0].BackupProgress == nil {
		t.Fatalf("运行中的设备应包含备份进度: %+v", snap.Devices)
	}
	got := snap.Devices[0].BackupProgress
	if got.State != "running" || got.Phase != "receiving" || got.UpdatedAt != "2026-08-14T18:30:00+08:00" {
		t.Errorf("进度状态或北京时间不正确: %+v", got)
	}
	assertOptionalFloat(t, "OverallPercent", got.OverallPercent, float64Ptr(81))
	assertOptionalFloat(t, "CurrentFilePercent", got.CurrentFilePercent, float64Ptr(47.5))
	assertOptionalInt64(t, "CurrentBytes", got.CurrentBytes, int64Ptr(209_000_000))
	assertOptionalInt64(t, "CurrentTotalBytes", got.CurrentTotalBytes, int64Ptr(440_000_000))
}

func TestBuildStatusSnapshotOmitsBackupProgressWhenIdle(t *testing.T) {
	app := newApplication()
	app.devices["IDLE"] = &device{UDID: "IDLE", Name: "iPhone"}

	payload, err := json.Marshal(app.buildStatusSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "backup_progress") {
		t.Fatalf("空闲设备不应下发 backup_progress: %s", payload)
	}
}

func TestBackupProgressBroadcastIsThrottledButPhaseChangesAreImmediate(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	udid := "U-PROGRESS-SSE"
	app.devices[udid] = &device{UDID: udid, Name: "iPhone", IsOnline: true}
	ch := app.hub.Subscribe()
	defer app.hub.Unsubscribe(ch)
	t0 := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)

	app.startBackupProgress(udid, t0)
	app.markBackupRunning(udid)
	requireProgressFrame(t, ch, "准备状态应立即广播")

	// 第一次解析到真实传输阶段，必须立即广播。
	app.applyBackupProgressUpdate(udid, backupProgressUpdate{
		Phase:          "receiving",
		OverallPercent: float64Ptr(10),
	}, t0.Add(10*time.Millisecond))
	requireProgressFrame(t, ch, "进入传输阶段应立即广播")

	// 500ms 内只有数值变化时只更新内存，不推送 SSE。
	app.applyBackupProgressUpdate(udid, backupProgressUpdate{OverallPercent: float64Ptr(11)}, t0.Add(100*time.Millisecond))
	requireNoProgressFrame(t, ch, "连续数值更新应被节流")

	app.applyBackupProgressUpdate(udid, backupProgressUpdate{OverallPercent: float64Ptr(12)}, t0.Add(600*time.Millisecond))
	requireProgressFrame(t, ch, "达到节流间隔后应广播最新值")

	// 即使还未到下一间隔，文件/方向阶段变化也不能延迟。
	app.applyBackupProgressUpdate(udid, backupProgressUpdate{Phase: "sending"}, t0.Add(601*time.Millisecond))
	requireProgressFrame(t, ch, "阶段变化应立即广播")
}

func requireProgressFrame(t *testing.T, ch <-chan []byte, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func requireNoProgressFrame(t *testing.T, ch <-chan []byte, message string) {
	t.Helper()
	select {
	case payload := <-ch:
		t.Fatalf("%s，意外收到: %s", message, payload)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestBuildStatusSnapshotIncludesOnboardingStates(t *testing.T) {
	app := newApplication()
	app.devices["U-GUIDE"] = &device{
		UDID:       "U-GUIDE",
		Name:       "iPhone",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeUSB),
	}
	app.deviceOperationStates["U-GUIDE"] = deviceOperationState{
		PairingState:     pairingStateWaitingForTrust,
		PairingErrorCode: "trust_required",
		PairingError:     "请在设备上点击信任",
		BackupState:      backupStateFailed,
		BackupErrorCode:  "backup_failed",
		LastBackupError:  "exit status 1",
	}

	snap := app.buildStatusSnapshot()
	if len(snap.Devices) != 1 {
		t.Fatalf("应有 1 台设备，得到 %d", len(snap.Devices))
	}
	d := snap.Devices[0]
	if d.PairingState != pairingStateWaitingForTrust || d.PairingErrorCode != "trust_required" || d.PairingError == "" {
		t.Errorf("配对状态未完整发布: %+v", d)
	}
	if d.BackupState != backupStateFailed || d.LastBackupErrorCode != "backup_failed" || d.LastBackupError == "" {
		t.Errorf("备份状态未完整发布: %+v", d)
	}
}

func TestBuildStatusSnapshotUsesSafeOnboardingDefaults(t *testing.T) {
	app := newApplication()
	app.devices["NEW"] = &device{UDID: "NEW"}
	app.devices["DONE"] = &device{UDID: "DONE", LastBackup: nowBeijing()}

	snap := app.buildStatusSnapshot()
	states := make(map[string]deviceStatusDTO, len(snap.Devices))
	for _, d := range snap.Devices {
		states[d.UDID] = d
	}
	if got := states["NEW"]; got.PairingState != pairingStateUnknown || got.BackupState != backupStateIdle {
		t.Errorf("新设备默认状态不正确: %+v", got)
	}
	if got := states["DONE"]; got.BackupState != backupStateSucceeded {
		t.Errorf("已有成功备份的设备应恢复为 succeeded: %+v", got)
	}
}

func TestBroadcastStatusOnlyOnChange(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	app.devices["U1"] = &device{UDID: "U1", Name: "iPad", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	ch := app.hub.Subscribe()
	defer app.hub.Unsubscribe(ch)

	app.broadcastStatus() // 首次：应推
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("首次广播应收到")
	}

	app.broadcastStatus() // 无变化：不应推
	select {
	case <-ch:
		t.Fatal("快照无变化不应再次广播")
	case <-time.After(200 * time.Millisecond):
	}

	// 状态变化：应再推
	app.mu.Lock()
	app.devices["U1"].BatteryLevel = 42
	app.mu.Unlock()
	app.broadcastStatus()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("状态变化后应再次广播")
	}
}

func TestApplyPresenceSnapshotKeepsWiFiDeviceOnlineDuringTransientMiss(t *testing.T) {
	app := newApplication()
	cfg := defaultRuntimeConfig()
	cfg.DeviceDisconnectGrace = 30 * time.Second
	app.runtimeConfig = cfg

	const udid = "U-WIFI-PRESENCE"
	app.devices[udid] = &device{
		UDID:       udid,
		Name:       "iPhone",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeNetwork),
	}
	t0 := time.Date(2026, 8, 15, 9, 0, 0, 0, beijingLocation)

	app.applyPresenceSnapshotWithNetwork(map[string]*device{}, map[string]*device{}, t0)
	if !app.devices[udid].IsOnline {
		t.Fatal("Wi-Fi 设备单次扫描缺失时应保持在线")
	}

	// 宽限期内重新发现必须清空本次缺失，下一次缺失重新计时。
	online := map[string]*device{
		udid: {UDID: udid, IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)},
	}
	app.applyPresenceSnapshotWithNetwork(online, online, t0.Add(10*time.Second))
	app.applyPresenceSnapshotWithNetwork(map[string]*device{}, map[string]*device{}, t0.Add(20*time.Second))
	app.applyPresenceSnapshotWithNetwork(map[string]*device{}, map[string]*device{}, t0.Add(49*time.Second))
	if !app.devices[udid].IsOnline {
		t.Fatal("重新发现后的新缺失尚未达到宽限期，不应发布离线")
	}

	app.applyPresenceSnapshotWithNetwork(map[string]*device{}, map[string]*device{}, t0.Add(50*time.Second))
	if app.devices[udid].IsOnline {
		t.Fatal("Wi-Fi 设备连续缺失达到宽限期后应发布离线")
	}
}

func TestApplyPresenceSnapshotMarksMissingUSBDeviceOfflineImmediately(t *testing.T) {
	app := newApplication()
	const udid = "U-USB-PRESENCE"
	app.devices[udid] = &device{
		UDID:       udid,
		Name:       "iPhone",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeUSB),
	}

	app.applyPresenceSnapshotWithNetwork(map[string]*device{}, map[string]*device{}, nowBeijing())
	if app.devices[udid].IsOnline {
		t.Fatal("USB 设备缺失时应立即发布离线")
	}
}

func TestHandleEventsStreamsFirstFrame(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	app.devices["U-EV"] = &device{UDID: "U-EV", Name: "iPad", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}

	req := httptest.NewRequest("GET", "/api/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { app.handleEvents(rr, req); close(done) }()

	// 等订阅建立 + 首帧写出
	deadline := time.Now().Add(2 * time.Second)
	for app.hub.clientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type 应为 text/event-stream，得到 %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data: ") || !strings.Contains(body, "U-EV") {
		t.Errorf("首帧应含快照数据，得到: %q", body)
	}
	// 断开后应注销
	if app.hub.clientCount() != 0 {
		t.Errorf("连接断开后应注销客户端，仍有 %d", app.hub.clientCount())
	}
}
