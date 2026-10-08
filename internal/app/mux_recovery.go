package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// connectionAdmissionUnsafe 和重启占用在同一个 app.mu 边界内判定。
// 长流程必须在首个设备命令前进入，最后一个命令及收尾完成后释放。
func (app *application) connectionAdmissionUnsafe(d *device) error {
	if app.connectionClosed || app.rootCtx.Err() != nil {
		return context.Canceled
	}
	if app.connectionRecoveryActive {
		return errConnectionRestarting
	}
	for _, s := range app.connectionServices {
		if s.phase == "waiting_busy" {
			return errConnectionRestarting
		}
	}
	if d != nil {
		backend := connectionUSB
		if isNetworkConnection(d.Connection) {
			backend = connectionWiFi
		}
		s := app.connectionServices[app.connectionBackend(backend)]
		if d.PresenceUnknown || (s.scan.sequence != 0 && (s.scan.err != nil || s.scan.generation != s.generation)) {
			return errConnectionUnavailable
		}
	}
	return nil
}

func (app *application) beginConnectionTask(d *device) (func(), error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if err := app.connectionAdmissionUnsafe(d); err != nil {
		return nil, err
	}
	return app.retainConnectionTaskUnsafe(), nil
}

func (app *application) retainConnectionTaskUnsafe() func() {
	app.connectionTasks++
	var once sync.Once
	return func() { once.Do(func() { app.mu.Lock(); app.connectionTasks--; app.mu.Unlock() }) }
}

// 底层命令也计数，但不拒绝等待中的恢复：已准入的多命令任务需要能完成收尾。
// 普通后台工作在流程入口使用 beginConnectionTask，避免持续接纳造成恢复饥饿。
func (app *application) beginConnectionCommand() (func(), error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.connectionClosed || app.rootCtx.Err() != nil {
		return nil, context.Canceled
	}
	if app.connectionRecoveryActive {
		return nil, errConnectionRestarting
	}
	return app.retainConnectionTaskUnsafe(), nil
}

func (app *application) connectionBusyUnsafe() bool {
	return app.connectionTasks > 0 || len(app.backupInProgress) > 0 || len(app.checkInProgress) > 0 || len(app.networkRegistrations) > 0 || len(app.activeDeviceCommands) > 0 || app.connectionFlights[0] != nil || app.connectionFlights[1] != nil
}

func (app *application) connectionTargets() []int {
	if app.usesUSBMuxd2WiFi() {
		return []int{connectionUSB}
	}
	return []int{connectionUSB, connectionWiFi}
}

func (app *application) reserveConnectionRecovery(targets []int, manual bool) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.connectionClosed || app.rootCtx.Err() != nil {
		return context.Canceled
	}
	if app.connectionRecoveryActive {
		return errConnectionRestarting
	}
	if app.connectionBusyUnsafe() {
		if !manual {
			for _, target := range targets {
				app.connectionServices[target].phase = "waiting_busy"
			}
		}
		return errDeviceBusy
	}
	if !manual {
		now := nowBeijing()
		for _, target := range targets {
			s := &app.connectionServices[target]
			if !s.configRestart && (s.failures < 3 || s.code == "tool_unavailable" || now.Before(s.graceUntil) || now.Before(s.nextAttempt) || len(trimConnectionAttempts(s.attempts, now)) >= 3) {
				return errConnectionUnavailable
			}
		}
	}
	app.connectionRecoveryActive = true
	app.connectionRecoveryWorkers.Add(1)
	for _, target := range targets {
		app.connectionServices[target].phase = "restarting"
	}
	return nil
}

func trimConnectionAttempts(attempts []time.Time, now time.Time) []time.Time {
	first := 0
	for first < len(attempts) && !attempts[first].After(now.Add(-15*time.Minute)) {
		first++
	}
	return append([]time.Time(nil), attempts[first:]...)
}

func (app *application) dueConnectionRecovery(now time.Time) []int {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.connectionClosed || app.connectionRecoveryActive || app.rootCtx.Err() != nil {
		return nil
	}
	var targets []int
	for _, target := range app.connectionTargets() {
		s := &app.connectionServices[target]
		s.attempts = trimConnectionAttempts(s.attempts, now)
		if s.configRestart {
			targets = append(targets, target)
			continue
		}
		if s.failures < 3 || s.code == "tool_unavailable" || now.Before(s.graceUntil) {
			continue
		}
		if len(s.attempts) >= 3 {
			s.phase = "manual_required"
			continue
		}
		if now.Before(s.nextAttempt) {
			s.phase = "backoff"
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func (app *application) connectionRecoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			targets := app.dueConnectionRecovery(now)
			if len(targets) > 0 {
				if err := app.reserveConnectionRecovery(targets, false); err == nil {
					_ = app.executeConnectionRecovery(ctx, targets, false)
				}
			}
			app.broadcastStatus()
		}
	}
}

// Wi-Fi 地址修改只登记待处理请求，不能从保存配置路径直接停止进程。
func (app *application) queueNetworkServiceRestart() {
	if app.usesUSBMuxd2WiFi() {
		return
	}
	app.mu.Lock()
	app.connectionServices[connectionWiFi].configRestart = true
	if !app.connectionRecoveryActive {
		app.connectionServices[connectionWiFi].phase = "waiting_busy"
	}
	app.mu.Unlock()
	app.broadcastStatus()
}

func (app *application) executeConnectionRecovery(ctx context.Context, targets []int, manual bool) (retErr error) {
	defer app.connectionRecoveryWorkers.Done()
	defer func() {
		app.mu.Lock()
		app.connectionRecoveryActive = false
		app.mu.Unlock()
		app.broadcastStatus()
	}()
	app.broadcastStatus()
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		app.mu.RLock()
		configRestart := app.connectionServices[target].configRestart
		app.mu.RUnlock()
		// 自动恢复先复核，忙期间服务自行恢复时不消耗额度。
		if !manual && !configRestart {
			result := app.scanConnection(ctx, target, true)
			if result.code == "tool_unavailable" {
				app.mu.Lock()
				app.connectionServices[target].phase = "manual_required"
				app.mu.Unlock()
				retErr = result.err
				continue
			}
			if result.err == nil {
				app.mu.Lock()
				app.connectionServices[target].phase = "idle"
				app.mu.Unlock()
				continue
			}
		}
		app.mu.Lock()
		s := &app.connectionServices[target]
		minimumGeneration := s.generation + 1
		s.configRestart = false
		if !manual {
			s.attempts = append(trimConnectionAttempts(s.attempts, nowBeijing()), nowBeijing())
		}
		app.mu.Unlock()
		app.addWarnLog("SYSTEM", fmt.Sprintf("开始恢复连接服务：backend=%d manual=%t", target, manual))
		var err error
		if target == connectionUSB {
			err = app.restartUSBMuxDProcess()
		} else {
			err = app.restartNetmuxdProcess()
		}
		if err == nil {
			verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = app.verifyConnectionService(verifyCtx, target, minimumGeneration)
			cancel()
		}
		app.mu.Lock()
		s = &app.connectionServices[target]
		if err == nil {
			s.phase, s.code, s.failures = "idle", "", 0
			s.nextAttempt = time.Time{}
			app.requestConnectionRefreshUnsafe()
		} else {
			s.health, s.phase = "unhealthy", "backoff"
			if s.code != "tool_unavailable" {
				s.code = "recovery_failed"
			}
			if s.failures < 3 {
				s.failures = 3
			}
			delay := 30 * time.Second
			if s.backoff > 0 {
				delay = 2 * time.Minute
			}
			s.backoff++
			s.nextAttempt = nowBeijing().Add(delay)
			if len(s.attempts) >= 3 || s.code == "tool_unavailable" {
				s.phase = "manual_required"
			}
			retErr = err
		}
		app.mu.Unlock()
		if err != nil {
			app.addWarnLog("SYSTEM", fmt.Sprintf("连接服务恢复验证失败：backend=%d", target))
		} else {
			app.addInfoLog("SYSTEM", fmt.Sprintf("连接服务协议验证成功：backend=%d", target))
		}
	}
	return retErr
}

func (app *application) verifyConnectionService(ctx context.Context, target int, minimumGeneration uint64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result := app.scanConnection(ctx, target, true)
		if result.err == nil {
			app.mu.RLock()
			current := result.generation >= minimumGeneration && result.generation == app.connectionServices[target].generation
			app.mu.RUnlock()
			if current {
				return nil
			}
		}
		if result.code == "tool_unavailable" {
			return result.err
		}
		if !sleepOrDone(ctx, 200*time.Millisecond) {
			return ctx.Err()
		}
	}
}

// 独立于 supervisor 的生命周期编号，覆盖监督循环内部每次物理进程启动。
func (app *application) connectionProcessStarted(target int) {
	app.mu.Lock()
	s := &app.connectionServices[target]
	s.generation++
	s.graceUntil = nowBeijing().Add(connectionStartupGrace)
	s.healthySince = time.Time{}
	s.failures = 0
	s.health = "unknown"
	app.connectionRevision++
	app.requestConnectionRefreshUnsafe()
	for udid, d := range app.devices {
		logical := connectionUSB
		if isNetworkConnection(d.Connection) {
			logical = connectionWiFi
		}
		if app.connectionBackend(logical) == target {
			d.PresenceUnknown = true
			delete(app.devicePresenceMissingSince, udid)
			delete(app.deviceOfflineSince, udid)
		}
		// 页面可能已切换为 USB，但在途 Wi-Fi 命令仍属于网络服务。
		if command := app.activeDeviceCommands[udid]; command != nil && command.connection == connectTypeNetwork && app.connectionBackend(connectionWiFi) == target {
			delete(app.deviceOfflineSince, udid)
		}
	}
	app.mu.Unlock()
}

func (app *application) restartConnectionsSync(targets []int) error {
	app.muxRestartMu.Lock()
	defer app.muxRestartMu.Unlock()
	if err := app.reserveConnectionRecovery(targets, true); err != nil {
		return err
	}
	return app.executeConnectionRecovery(app.rootCtx, targets, true)
}

func (app *application) RestartUSBMuxD() error {
	return app.restartConnectionsSync([]int{connectionUSB})
}
func (app *application) RestartNetmuxd() {
	if app.usesUSBMuxd2WiFi() {
		return
	}
	if err := app.restartConnectionsSync([]int{connectionWiFi}); err != nil && !errors.Is(err, context.Canceled) {
		app.addWarnLog("SYSTEM", err.Error())
	}
}

func (app *application) connectionEpochUnsafe(d *device) uint64 {
	logical := connectionUSB
	if d != nil && isNetworkConnection(d.Connection) {
		logical = connectionWiFi
	}
	return app.connectionServices[app.connectionBackend(logical)].generation
}
func (app *application) connectionEpoch(d *device) uint64 {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.connectionEpochUnsafe(d)
}
func (app *application) pairingStatePublisher(d *device) func(string, string, string, string) bool {
	return app.pairingPublisher(d, false)
}

func (app *application) pairingPublisher(d *device, automatic bool) func(string, string, string, string) bool {
	app.mu.Lock()
	epoch := app.connectionEpochUnsafe(d)
	app.pairingCheckSequence++
	check := app.pairingCheckSequence
	state := app.deviceOperationStates[d.UDID]
	state.pairingCheck = check
	app.deviceOperationStates[d.UDID] = state
	app.mu.Unlock()
	return func(udid, stateCode, code, message string) bool {
		app.mu.Lock()
		current := app.devices[udid]
		state := app.deviceOperationStates[udid]
		if state.pairingCheck != check || app.connectionEpochUnsafe(d) != epoch || app.deviceRemovalBlockedUnsafe(udid) != nil ||
			(current != nil && (current.Connection != d.Connection || current.PresenceUnknown)) ||
			(automatic && (current == nil || !current.IsOnline || app.connectionAdmissionUnsafe(current) != nil)) {
			app.mu.Unlock()
			return false
		}
		state.PairingState = stateCode
		if stateCode == pairingStatePaired {
			state.pairingAlert = pairingAlertState{}
		}
		if stateCode != pairingStateChecking {
			state.PairingErrorCode, state.PairingError = code, message
		}
		var notification *notificationMessage
		if automatic {
			notification = app.pairingNotificationUnsafe(d, &state, nowBeijing())
		}
		app.deviceOperationStates[udid] = state
		app.mu.Unlock()
		if notification != nil {
			if manager := app.notificationManagerSnapshot(); manager != nil {
				manager.Send(notification)
			}
		}
		app.broadcastStatus()
		return true
	}
}

func (app *application) requestConnectionRefreshUnsafe() {
	app.connectionRefreshPending = true
	app.connectionRefreshRevision++
}

// 注册成功可能晚于恢复刷新；通过序号防止旧刷新吞掉新的配对请求。
func (app *application) requestConnectionRefresh() {
	app.mu.Lock()
	app.requestConnectionRefreshUnsafe()
	app.mu.Unlock()
}

// 完整恢复顺序为注册重放 -> 枚举 -> 配对。失败、换代或新请求保留待刷新状态。
func (app *application) refreshRecoveredConnections() {
	release, err := app.beginConnectionTask(nil)
	if err != nil {
		return
	}
	defer release()
	app.mu.RLock()
	epochs := [2]uint64{app.connectionServices[0].generation, app.connectionServices[1].generation}
	revision := app.connectionRefreshRevision
	app.mu.RUnlock()
	app.replayAddDevice()
	if err := app.RefreshDevices(); err != nil {
		return
	}
	app.mu.Lock()
	// 重放期间新注册也保留下一轮请求：随后枚举可能复用注册前启动的查询。
	if revision == app.connectionRefreshRevision && epochs == [2]uint64{app.connectionServices[0].generation, app.connectionServices[1].generation} {
		app.connectionRefreshPending = false
	}
	app.mu.Unlock()
}
