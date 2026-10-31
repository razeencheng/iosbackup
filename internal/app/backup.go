package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
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
	state.LastBackupMessageCode = ""
	state.LastBackupMessageParams = nil
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
	if errors.Is(err, errBackupStoppedByUser) {
		return backupStateStopped, "backup_stopped_by_user"
	}
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
	app.mu.Lock()
	app.finishBackupStateUnsafe(udid, err)
	app.mu.Unlock()
	app.broadcastStatus()
}

// finishBackupStateUnsafe 统一终态消息写入；调用者必须持有 app.mu。
func (app *application) finishBackupStateUnsafe(udid string, err error) {
	state := app.deviceOperationStates[udid]
	state.BackupState = backupStateSucceeded
	state.BackupErrorCode = ""
	state.LastBackupError = ""
	state.LastBackupMessageCode = ""
	state.LastBackupMessageParams = nil
	if err != nil {
		state.BackupState, state.BackupErrorCode = backupFailureState(err)
		state.LastBackupError = err.Error()
		state.LastBackupMessageCode, state.LastBackupMessageParams = backupFailureMessage(err, state.BackupErrorCode)
	}
	app.deviceOperationStates[udid] = state
}

// backupFailureMessage 保留已知错误的原因与参数，旧分类继续供旧客户端使用。
func backupFailureMessage(err error, errorCode string) (string, map[string]any) {
	// 主动停止使用独立状态与提示，不作为失败消息发布。
	if errors.Is(err, errBackupStoppedByUser) {
		return "", nil
	}
	var message *i18n.Error
	if errors.As(err, &message) && i18n.HasCode(message.Code) {
		return message.Code, cloneBackupMessageParams(message.Params)
	}
	code := "api." + errorCode
	if !i18n.HasCode(code) {
		code = i18n.BackupFailed
	}
	return code, nil
}

// cloneBackupMessageParams 只复制 JSON 标量，阻止参数携带可变引用或使快照编码失败的值。
func cloneBackupMessageParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	copy := make(map[string]any, len(params))
	for key, value := range params {
		switch number := value.(type) {
		case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		case float64:
			if math.IsNaN(number) || math.IsInf(number, 0) {
				continue
			}
		case float32:
			if math.IsNaN(float64(number)) || math.IsInf(float64(number), 0) {
				continue
			}
		default:
			continue
		}
		copy[key] = value
	}
	if len(copy) == 0 {
		return nil
	}
	return copy
}

// PerformBackup 执行一次手动备份，暂缓自动计划不限制此入口。
func (app *application) PerformBackup(udid string) error {
	job, err := app.beginBackupJob(udid, "", "", false)
	if err != nil {
		return err
	}
	return app.runBackupJob(job)
}

// runBackupJob 共用执行、通知和收束，所有资源在终态发布前释放。
func (app *application) runBackupJob(job *backupJob) (retErr error) {
	defer func() { app.finishBackupJob(job, retErr) }()
	udid, cfg, device := job.udid, job.config, job.device
	backupLog := func(message string) { app.addLog(udid, backupSessionLog(job.id, message)) }
	defer func() {
		app.recordBackupResult(job, retErr)
		if retErr == nil {
			return
		}
		if errors.Is(retErr, errBackupStoppedByUser) {
			backupLog("event=backup.stopped")
			return
		}
		backupLog(fmt.Sprintf("event=backup.failed error=%v", retErr))
		if manager := app.notificationManagerSnapshot(); manager != nil {
			_, code := backupFailureState(retErr)
			messageCode, params := backupFailureMessage(retErr, code)
			manager.SendBackupFailureWithReason(device.Name, udid, code, retErr.Error(), messageCode, params)
		}
	}()
	app.broadcastStatus()
	if err := context.Cause(job.ctx); err != nil {
		return err
	}
	if !job.automatic {
		if cfg.OnlyWhenCharging && !device.IsCharging {
			return i18n.NewError(i18n.NotCharging, nil)
		}
		if device.BatteryLevel < cfg.MinBatteryLevel {
			return i18n.NewError(i18n.LowBattery, map[string]any{"battery": device.BatteryLevel, "min_battery": cfg.MinBatteryLevel})
		}
	}
	backupDir := cfg.BackupDirectory
	if backupDir == "" {
		backupDir = app.paths.BackupsRoot
	}
	// 先停止旧索引扫描，备份进程才能修改这份备份。
	app.beginBackupIndexWrite(udid, backupDir, true)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}
	if err := context.Cause(job.ctx); err != nil {
		return err
	}
	backupLog(fmt.Sprintf("event=backup.started automatic=%t", job.automatic))
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendBackupStart(device.Name, udid)
	}
	network := isNetworkConnection(device.Connection)
	releasePowerAssertion, err := app.guardWiFiBackupPowerAssertion(job.ctx, job.cancel, udid, network)
	if err == nil {
		defer releasePowerAssertion()
		app.mu.Lock()
		if app.backupJobs[udid] == job && !job.stopAccepted {
			state := app.deviceOperationStates[udid]
			state.BackupState = backupStateRunning
			state.LastBackupMessageCode, state.LastBackupMessageParams = "", nil
			app.deviceOperationStates[udid] = state
		}
		app.mu.Unlock()
		app.broadcastStatus()
		err = app.runBackupCommand(job.ctx, job.cancel, udid, job.id, backupDir, network)
	}
	app.recordBackupResult(job, err)
	if err != nil {
		return err
	}
	// 与配置编辑串行保存成功时间，避免旧候选覆盖本次结果。
	app.configPersistMu.Lock()
	app.mu.Lock()
	at := nowBeijing()
	if d := app.devices[udid]; d != nil {
		d.LastBackup = at
	}
	if cfg := app.configs[udid]; cfg != nil {
		cfg.LastBackup = at
		cfg.LastBackupConnection = connTypeFromDesc(device.Connection)
		if current := app.devices[udid]; current != nil {
			if name := current.knownName(); name != "" {
				cfg.Name = name
			}
		}
	}
	configs := app.backupConfigsSnapshotUnsafe()
	app.mu.Unlock()
	saveErr := app.configStore.Replace(configs)
	app.configPersistMu.Unlock()
	if saveErr != nil {
		backupLog(fmt.Sprintf("event=backup.metadata_save_failed error=%v", saveErr))
	}
	backupLog("event=backup.completed")
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendBackupSuccess(device.Name, udid)
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
				app.applyBackupProgressUpdate(udid, update, nowBeijing(), sessionID)
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

// PerformBackupWithConnection 显式选择连接执行单次备份，不改变自动计划。
func (app *application) PerformBackupWithConnection(udid, connectionType string) error {
	job, err := app.beginBackupJob(udid, connectionType, "", false)
	if err != nil {
		return err
	}
	return app.runBackupJob(job)
}

func (app *application) performBackupWithConnection(udid, connectionType, sessionID string) error {
	job, err := app.beginBackupJob(udid, connectionType, sessionID, true)
	if err != nil {
		return err
	}
	return app.runBackupJob(job)
}
