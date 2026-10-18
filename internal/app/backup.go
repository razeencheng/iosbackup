package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"iosbackup/internal/i18n"
)

// PerformBackup 执行备份
// maxConcurrentBackups 全局同时运行的备份数上限。
// 每个 idevicebackup2 备份在传输阶段会吃掉约「一个核」并把写入的数据全量灌进页缓存，
// 多台并发会成倍占用 CPU/内存。此上限封顶并发数：超出的设备自动排队
// （调度器下轮重试，手动备份返回 backup_busy 提示稍后再试）。
// 默认 3：兼顾多设备吞吐与资源占用；想更保守可设 1（完全串行），想更激进可调大。
const maxConcurrentBackups = 3

// backupSem 全局备份并发闸（带缓冲 channel 实现的信号量，非阻塞 tryAcquire）。
type backupSem struct{ ch chan struct{} }

func newBackupSem(n int) *backupSem { return &backupSem{ch: make(chan struct{}, n)} }

// tryAcquire 非阻塞占用一个槽；满了返回 false。
func (s *backupSem) tryAcquire() bool {
	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *backupSem) release()   { <-s.ch }
func (s *backupSem) inUse() int { return len(s.ch) }

func (app *application) setBackupOperationState(udid, stateCode, errorCode, message string) {
	app.mu.Lock()
	state := app.deviceOperationStates[udid]
	state.BackupState = stateCode
	state.BackupErrorCode = errorCode
	state.LastBackupError = message
	app.deviceOperationStates[udid] = state
	app.mu.Unlock()
	app.broadcastStatus()
}

func (app *application) markBackupStarting(udid string) {
	app.setBackupOperationState(udid, backupStateStarting, "", "")
}

func (app *application) markBackupRunning(udid string) {
	app.setBackupOperationState(udid, backupStateRunning, "", "")
}

func backupFailureState(err error) (stateCode, errorCode string) {
	if errors.Is(err, errBackupStalled) {
		return backupStateFailed, "backup_stalled"
	}
	if errors.Is(err, errDeviceDisconnected) {
		return backupStateInterrupted, "device_disconnected"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return backupStateInterrupted, "backup_cancelled"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "no space"), strings.Contains(message, "read-only file system"), strings.Contains(message, "permission denied"):
		return backupStateFailed, "storage_unavailable"
	case strings.Contains(message, "device locked"), strings.Contains(message, "passcode"):
		return backupStateFailed, "device_locked"
	default:
		return backupStateFailed, "backup_failed"
	}
}

func (app *application) finishBackupState(udid string, err error) {
	if err == nil {
		app.setBackupOperationState(udid, backupStateSucceeded, "", "")
		return
	}
	stateCode, errorCode := backupFailureState(err)
	app.setBackupOperationState(udid, stateCode, errorCode, err.Error())
}

// finishBackupOperation 原子提交终态并释放占用，下一任务不能被旧终态覆盖。
func (app *application) finishBackupOperation(udid string, err error, at time.Time) {
	manager := app.lockBackupIndexFinish()
	app.mu.Lock()
	app.finishBackupProgressUnsafe(udid, err, at)
	state := app.deviceOperationStates[udid]
	state.BackupState = backupStateSucceeded
	state.BackupErrorCode = ""
	state.LastBackupError = ""
	if err != nil {
		state.BackupState, state.BackupErrorCode = backupFailureState(err)
		state.LastBackupError = err.Error()
	}
	app.deviceOperationStates[udid] = state
	delete(app.backupInProgress, udid)
	app.mu.Unlock()
	var lease *backupIndexWrite
	if manager != nil {
		lease = manager.releaseWriteUnsafe(udid)
		manager.mu.Unlock()
	}
	app.afterBackupIndexWrite(manager, lease, err == nil)
	app.broadcastStatus()
}

func (app *application) PerformBackup(udid string) error {
	app.mu.RLock()
	d := cloneDevice(app.devices[udid])
	app.mu.RUnlock()
	release, err := app.beginConnectionTask(d)
	if err != nil {
		return err
	}
	defer release()
	return app.performBackupInTask(udid)
}

func (app *application) performBackupInTask(udid string) (retErr error) {
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	device, exists := app.devices[udid]
	device = cloneDevice(device)
	config, configExists := app.configs[udid]
	config = cloneBackupConfig(config)

	// 检查设备是否已经在备份中
	if app.backupInProgress[udid] {
		app.mu.Unlock()
		return i18n.NewError(i18n.Busy, nil)
	}

	// 标记设备为备份中状态
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	app.startBackupProgress(udid, nowBeijing())
	app.markBackupRunning(udid) // 备份开始：即时推送（不必等下一轮询）
	sessionID := newBackupSessionID()
	backupLog := func(message string) {
		app.addLog(udid, backupSessionLog(sessionID, message))
	}

	// 确保在函数退出时清理备份状态
	defer func() {
		app.finishBackupOperation(udid, retErr, nowBeijing())
	}()

	if !exists {
		return i18n.NewError(i18n.DeviceNotFound, nil)
	}

	if !configExists {
		return i18n.NewError(i18n.ConfigInvalid, nil)
	}

	// 检查备份条件
	if config.OnlyWhenCharging && !device.IsCharging {
		return i18n.NewError(i18n.NotCharging, nil)
	}

	if device.BatteryLevel < config.MinBatteryLevel {
		return i18n.NewError(i18n.LowBattery, map[string]any{"battery": device.BatteryLevel, "min_battery": config.MinBatteryLevel})
	}

	backupLog("event=backup.started")

	// 预留写入后先停止索引扫描，避免把变更中的目录发布为完整清单。
	app.beginBackupIndexWrite(udid, config.BackupDirectory, true)
	// 确保备份目录存在
	os.MkdirAll(config.BackupDirectory, 0755)
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendBackupStart(device.Name, udid)
	}

	connType := device.Connection
	if connType == connectionTypeDesc(connectTypeNetwork) {
		backupLog("event=backup.transport connection=network")
	} else {
		backupLog("event=backup.transport connection=usb")
	}

	// 构建备份命令（通过 runIdeviceCmd 注入 socket 和 -n）
	// 长命令使用可取消 context，与 backupInProgress 守卫联动
	backupCtx, backupCancel := context.WithCancelCause(app.rootCtx)
	defer backupCancel(nil)

	// 直接构建 exec.Cmd（需要 StdoutPipe/StderrPipe），手动注入 socket env
	isNetwork := device.Connection == connectionTypeDesc(connectTypeNetwork)
	releasePowerAssertion, err := app.guardWiFiBackupPowerAssertion(backupCtx, backupCancel, udid, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("event=backup.failed error=%v", err))
		return err
	}
	defer releasePowerAssertion()

	err = app.runBackupCommand(backupCtx, backupCancel, udid, sessionID, config.BackupDirectory, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("event=backup.failed error=%v", err))

		// 发送备份失败通知
		if manager := app.notificationManagerSnapshot(); manager != nil {
			deviceName := udid
			app.mu.RLock()
			if device, exists := app.devices[udid]; exists {
				deviceName = device.Name
			}
			app.mu.RUnlock()
			_, code := backupFailureState(err)
			manager.SendBackupFailure(deviceName, udid, code, err.Error())
		}

		return err
	}

	// 更新最后备份时间和连接类型
	app.mu.Lock()
	backupTime := nowBeijing()
	if device, exists := app.devices[udid]; exists {
		device.LastBackup = backupTime
	}
	if config, exists := app.configs[udid]; exists {
		config.LastBackup = backupTime
		config.LastBackupConnection = connTypeFromDesc(connType)
		if device, exists := app.devices[udid]; exists {
			if name := device.knownName(); name != "" {
				config.Name = name
			}
		}
	}
	app.mu.Unlock()

	// 保存配置到文件
	app.saveConfigs()

	backupLog("event=backup.completed")

	// 发送备份成功通知
	if manager := app.notificationManagerSnapshot(); manager != nil {
		deviceName := udid
		app.mu.RLock()
		if device, exists := app.devices[udid]; exists {
			deviceName = device.Name
		}
		app.mu.RUnlock()
		manager.SendBackupSuccess(deviceName, udid)
	}

	return nil
}

// readBackupOutput 读取备份输出
func (app *application) readBackupOutput(udid, sessionID string, pipe interface{}, logType string, observers ...func(string, string) bool) {
	defer func() {
		// 确保goroutine能够正常退出
		if r := recover(); r != nil {
			app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("event=backup.output_panic stream=%s error=%v", logType, r)))
		}
	}()

	var reader io.Reader

	switch p := pipe.(type) {
	case io.ReadCloser:
		reader = p
		defer p.Close()
	case io.Reader:
		reader = p
	default:
		return
	}

	var lastProgressUpdate time.Time
	err := consumeBoundedLines(reader, func(line string, carriageReturn bool) error {
		if len(observers) > 0 {
			if observers[0](line, logType) {
				return nil
			}
		} else if logType == "STDOUT" {
			if update, ok := parseBackupProgressLine(line); ok {
				app.applyBackupProgressUpdate(udid, update, nowBeijing())
			}
		}
		shouldLog := !carriageReturn
		if carriageReturn {
			isProgress := strings.Contains(line, "%") || strings.Contains(line, "[")
			now := nowBeijing()
			shouldLog = !isProgress || now.Sub(lastProgressUpdate) > 5*time.Second ||
				strings.Contains(line, "Finished") || strings.Contains(line, "Receiving") || strings.Contains(line, "Sending")
			if shouldLog && isProgress {
				lastProgressUpdate = now
			}
		}
		if shouldLog {
			app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("[%s] %s", logType, line)))
		}
		return nil
	})
	if err != nil {
		app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("event=backup.output_read_failed stream=%s error=%v", logType, err)))
	}
}

// PerformBackupWithConnection 根据连接方式执行备份
func (app *application) PerformBackupWithConnection(udid string, connectionType string) (retErr error) {
	return app.performBackupWithConnection(udid, connectionType, newBackupSessionID())
}

func (app *application) performBackupWithConnection(udid, connectionType, sessionID string) (retErr error) {
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	device, exists := app.devices[udid]
	device = cloneDevice(device)
	if device != nil {
		device.Connection = connectionType
	}
	if err := app.connectionAdmissionUnsafe(device); err != nil {
		app.mu.Unlock()
		return err
	}
	config, configExists := app.configs[udid]
	config = cloneBackupConfig(config)

	// 检查设备是否已经在备份中
	if app.backupInProgress[udid] {
		app.mu.Unlock()
		return i18n.NewError(i18n.Busy, nil)
	}

	// 标记设备为备份中状态
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	app.startBackupProgress(udid, nowBeijing())
	app.markBackupRunning(udid) // 备份开始：即时推送（不必等下一轮询）
	backupLog := func(message string) {
		app.addLog(udid, backupSessionLog(sessionID, message))
	}

	// 确保在函数退出时清理备份状态
	defer func() {
		app.finishBackupOperation(udid, retErr, nowBeijing())
	}()

	if !exists {
		return i18n.NewError(i18n.DeviceNotFound, nil)
	}

	if !configExists {
		return i18n.NewError(i18n.ConfigInvalid, nil)
	}

	backupDir := config.BackupDirectory
	if backupDir == "" {
		backupDir = dirBackups
	}

	backupLog(fmt.Sprintf("event=backup.started source=scheduler directory=%s", backupDir))

	app.beginBackupIndexWrite(udid, backupDir, true)
	err := os.MkdirAll(backupDir, 0755)
	if err != nil {
		backupLog(fmt.Sprintf("event=backup.directory_create_failed error=%v", err))
		return err
	}
	if manager := app.notificationManagerSnapshot(); manager != nil {
		deviceName := udid
		app.mu.RLock()
		if device, exists := app.devices[udid]; exists {
			deviceName = device.Name
		}
		app.mu.RUnlock()
		manager.SendBackupStart(deviceName, udid)
	}

	// 根据连接方式构建备份命令（手动注入 socket，因为需要 StdoutPipe/StderrPipe）
	isNetwork := isNetworkConnection(connectionType)
	if isNetwork {
		backupLog("event=backup.transport source=scheduler connection=network")
	} else {
		backupLog("event=backup.transport source=scheduler connection=usb")
	}

	backupCtx, backupCancel := context.WithCancelCause(app.rootCtx)
	defer backupCancel(nil)
	releasePowerAssertion, err := app.guardWiFiBackupPowerAssertion(backupCtx, backupCancel, udid, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("event=backup.failed source=scheduler error=%v", err))
		return err
	}
	defer releasePowerAssertion()

	err = app.runBackupCommand(backupCtx, backupCancel, udid, sessionID, backupDir, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("event=backup.failed source=scheduler error=%v", err))

		if manager := app.notificationManagerSnapshot(); manager != nil {
			deviceName := udid
			app.mu.RLock()
			if device, exists := app.devices[udid]; exists {
				deviceName = device.Name
			}
			app.mu.RUnlock()
			_, code := backupFailureState(err)
			manager.SendBackupFailure(deviceName, udid, code, err.Error())
		}

		return err
	}

	// 更新最后备份时间和连接类型
	app.mu.Lock()
	backupTime := nowBeijing()
	if device, exists := app.devices[udid]; exists {
		device.LastBackup = backupTime
	}
	if config, exists := app.configs[udid]; exists {
		config.LastBackup = backupTime
		if isNetwork {
			config.LastBackupConnection = connectTypeNetwork
		} else {
			config.LastBackupConnection = connectTypeUSB
		}
		if device, exists := app.devices[udid]; exists {
			if name := device.knownName(); name != "" {
				config.Name = name
			}
		}
	}
	app.mu.Unlock()

	app.saveConfigs()
	backupLog("event=backup.completed source=scheduler")

	if manager := app.notificationManagerSnapshot(); manager != nil {
		deviceName := udid
		app.mu.RLock()
		if device, exists := app.devices[udid]; exists {
			deviceName = device.Name
		}
		app.mu.RUnlock()
		manager.SendBackupSuccess(deviceName, udid)
	}

	return nil
}
