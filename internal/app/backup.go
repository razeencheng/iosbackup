package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
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

func (app *application) PerformBackup(udid string) (retErr error) {
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	device, exists := app.devices[udid]
	config, configExists := app.configs[udid]

	// 检查设备是否已经在备份中
	if app.backupInProgress[udid] {
		app.mu.Unlock()
		return fmt.Errorf("设备 %s 已经在备份中", udid)
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
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		app.finishBackupProgress(udid, retErr, nowBeijing())
		app.finishBackupState(udid, retErr) // 备份结束：即时推送结构化结果
	}()

	if !exists {
		return fmt.Errorf("设备 %s 不存在", udid)
	}

	if !configExists {
		return fmt.Errorf("设备 %s 没有配置", udid)
	}

	// 检查备份条件
	if config.OnlyWhenCharging && !device.IsCharging {
		return fmt.Errorf("设备未充电，跳过备份")
	}

	if device.BatteryLevel < config.MinBatteryLevel {
		return fmt.Errorf("电量不足（%d%%），跳过备份", device.BatteryLevel)
	}

	backupLog("开始备份...")

	// 确保备份目录存在
	os.MkdirAll(config.BackupDirectory, 0755)
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendBackupStart(device.Name, udid)
	}

	connType := device.Connection
	if connType == connectionTypeDesc(connectTypeNetwork) {
		backupLog("使用 Wi-Fi 连接进行备份")
	} else {
		backupLog("使用 USB 连接进行备份")
	}

	// 构建备份命令（通过 runIdeviceCmd 注入 socket 和 -n）
	// 长命令使用可取消 context，与 backupInProgress 守卫联动
	backupCtx, backupCancel := context.WithCancelCause(app.rootCtx)
	defer backupCancel(nil)

	// 直接构建 exec.Cmd（需要 StdoutPipe/StderrPipe），手动注入 socket env
	isNetwork := device.Connection == connectionTypeDesc(connectTypeNetwork)
	releasePowerAssertion, err := app.guardWiFiBackupPowerAssertion(backupCtx, backupCancel, udid, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("备份失败: %v", err))
		return err
	}
	defer releasePowerAssertion()

	var cmdArgs []string
	if isNetwork {
		cmdArgs = append(cmdArgs, "-n")
	}
	cmdArgs = append(cmdArgs, "-u", udid, "backup", config.BackupDirectory)

	cmd := newExecCmd(backupCtx, cmdIdevicebackup2, cmdArgs...)
	cmd.SetGracefulCancel(3 * time.Second)
	if isNetwork {
		releaseActive := app.registerActiveDeviceCommand(udid, connectTypeNetwork, backupCancel)
		defer releaseActive()
	}
	cmdEnv := os.Environ()
	if isNetwork {
		cmdEnv = append(cmdEnv, app.networkIdeviceEnvVars()...)
	}
	cmd.cmd.Env = cmdEnv

	// 实时输出备份日志
	stdout, err := cmd.cmd.StdoutPipe()
	if err != nil {
		backupLog(fmt.Sprintf("备份失败: %v", err))
		return err
	}

	stderr, err := cmd.cmd.StderrPipe()
	if err != nil {
		backupLog(fmt.Sprintf("备份失败: %v", err))
		return err
	}

	if err := cmd.Start(); err != nil {
		backupLog(fmt.Sprintf("备份失败: %v", err))
		return err
	}

	// 启动日志读取协程
	go app.readBackupOutput(udid, sessionID, stdout, "STDOUT")
	go app.readBackupOutput(udid, sessionID, stderr, "STDERR")

	err = cmd.Wait()
	if errors.Is(context.Cause(backupCtx), errDeviceDisconnected) {
		err = errDeviceDisconnected
	} else if errors.Is(context.Cause(backupCtx), errPowerAssertionLost) {
		err = context.Cause(backupCtx)
	}
	if err != nil {
		backupLog(fmt.Sprintf("备份失败: %v", err))

		// 发送备份失败通知
		if manager := app.notificationManagerSnapshot(); manager != nil {
			deviceName := udid
			app.mu.RLock()
			if device, exists := app.devices[udid]; exists {
				deviceName = device.Name
			}
			app.mu.RUnlock()
			manager.SendBackupFailed(deviceName, udid, err.Error())
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
			config.Name = device.Name
		}
	}
	app.mu.Unlock()

	// 保存配置到文件
	app.saveConfigs()

	backupLog("备份完成")

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
func (app *application) readBackupOutput(udid, sessionID string, pipe interface{}, logType string) {
	defer func() {
		// 确保goroutine能够正常退出
		if r := recover(); r != nil {
			app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("[%s] 读取输出协程发生panic: %v", logType, r)))
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
		if logType == "STDOUT" {
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
		app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("[%s] 读取输出错误: %v", logType, err)))
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
	_, exists := app.devices[udid]
	config, configExists := app.configs[udid]

	// 检查设备是否已经在备份中
	if app.backupInProgress[udid] {
		app.mu.Unlock()
		return fmt.Errorf("设备 %s 已经在备份中", udid)
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
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		app.finishBackupProgress(udid, retErr, nowBeijing())
		app.finishBackupState(udid, retErr) // 备份结束：即时推送结构化结果
	}()

	if !exists {
		return fmt.Errorf("设备 %s 不存在", udid)
	}

	if !configExists {
		return fmt.Errorf("设备 %s 没有配置", udid)
	}

	backupDir := config.BackupDirectory
	if backupDir == "" {
		backupDir = dirBackups
	}

	backupLog(fmt.Sprintf("开始自动备份到 %s", backupDir))

	err := os.MkdirAll(backupDir, 0755)
	if err != nil {
		backupLog(fmt.Sprintf("创建备份目录失败: %v", err))
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
		backupLog("使用 Wi-Fi 连接进行自动备份")
	} else {
		backupLog("使用 USB 连接进行自动备份")
	}

	backupCtx, backupCancel := context.WithCancelCause(app.rootCtx)
	defer backupCancel(nil)
	releasePowerAssertion, err := app.guardWiFiBackupPowerAssertion(backupCtx, backupCancel, udid, isNetwork)
	if err != nil {
		backupLog(fmt.Sprintf("自动备份失败: %v", err))
		return err
	}
	defer releasePowerAssertion()

	var cmdArgs []string
	if isNetwork {
		cmdArgs = append(cmdArgs, "-n")
	}
	cmdArgs = append(cmdArgs, "-u", udid, "backup", backupDir)

	cmd := newExecCmd(backupCtx, cmdIdevicebackup2, cmdArgs...)
	cmd.SetGracefulCancel(3 * time.Second)
	if isNetwork {
		releaseActive := app.registerActiveDeviceCommand(udid, connectTypeNetwork, backupCancel)
		defer releaseActive()
	}
	cmdEnv := os.Environ()
	if isNetwork {
		cmdEnv = append(cmdEnv, app.networkIdeviceEnvVars()...)
	}
	cmd.cmd.Env = cmdEnv
	cmd.cmd.Dir = dirBackupBase

	stdout, err := cmd.cmd.StdoutPipe()
	if err != nil {
		backupLog(fmt.Sprintf("创建输出管道失败: %v", err))
		return err
	}

	stderr, err := cmd.cmd.StderrPipe()
	if err != nil {
		backupLog(fmt.Sprintf("创建错误管道失败: %v", err))
		return err
	}

	if err := cmd.Start(); err != nil {
		backupLog(fmt.Sprintf("启动备份命令失败: %v", err))
		return err
	}

	go app.readBackupOutput(udid, sessionID, stdout, "STDOUT")
	go app.readBackupOutput(udid, sessionID, stderr, "STDERR")

	err = cmd.Wait()
	if errors.Is(context.Cause(backupCtx), errDeviceDisconnected) {
		err = errDeviceDisconnected
	} else if errors.Is(context.Cause(backupCtx), errPowerAssertionLost) {
		err = context.Cause(backupCtx)
	}
	if err != nil {
		backupLog(fmt.Sprintf("自动备份失败: %v", err))

		if manager := app.notificationManagerSnapshot(); manager != nil {
			deviceName := udid
			app.mu.RLock()
			if device, exists := app.devices[udid]; exists {
				deviceName = device.Name
			}
			app.mu.RUnlock()
			manager.SendBackupFailed(deviceName, udid, err.Error())
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
			config.Name = device.Name
		}
	}
	app.mu.Unlock()

	app.saveConfigs()
	backupLog("自动备份完成")

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
