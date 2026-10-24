package app

import (
	"context"
	"errors"
	"time"

	"iosbackup/internal/i18n"
)

var errBackupStoppedByUser = errors.New("backup stopped by user")

// backupJob 的可变字段由 app.mu 保护；一次登记同时占用设备、连接与并发容量。
type backupJob struct {
	id, udid                          string
	automatic                         bool
	ctx                               context.Context
	cancel                            context.CancelCauseFunc
	device                            *device
	config                            *backupConfig
	release                           func()
	result                            error
	resultSet, finished, stopAccepted bool
	commandFinished                   bool
}

func (app *application) beginBackupJob(udid, connection, sessionID string, automatic bool) (*backupJob, error) {
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return nil, err
	}
	d := cloneDevice(app.devices[udid])
	if d == nil {
		state := app.deviceOperationStates[udid]
		state.BackupState, state.BackupErrorCode, state.LastBackupError = backupStateFailed, "backup_failed", errDeviceNotFound.Error()
		app.deviceOperationStates[udid] = state
		app.mu.Unlock()
		app.broadcastStatus()
		return nil, errDeviceNotFound
	}
	app.mu.Unlock()
	if connection != "" {
		d.Connection = connection
	}
	if !app.backupSem.tryAcquire() {
		return nil, i18n.NewError(i18n.BackupBusy, nil)
	}
	releaseConnection, err := app.beginConnectionTask(d)
	if err != nil {
		app.backupSem.release()
		return nil, err
	}
	release := func() { releaseConnection(); app.backupSem.release() }
	accepted := false
	defer func() {
		if !accepted {
			release()
		}
	}()
	app.mu.Lock()
	defer app.mu.Unlock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		return nil, err
	}
	if app.backupInProgress[udid] {
		return nil, errDeviceBusy
	}
	if err := app.connectionAdmissionUnsafe(d); err != nil {
		return nil, err
	}
	cfg := cloneBackupConfig(app.configs[udid])
	if cfg == nil {
		return nil, i18n.NewError(i18n.ConfigInvalid, nil)
	}
	if automatic && !cfg.AutoBackupEnabled {
		return nil, i18n.NewError(i18n.Busy, nil)
	}
	if automatic && app.backupHoldUnsafe(udid).Held {
		return nil, i18n.NewError(i18n.BackupAutoHeld, nil)
	}
	if sessionID == "" {
		sessionID = newBackupSessionID()
	}
	ctx, cancel := context.WithCancelCause(app.rootCtx)
	job := &backupJob{id: sessionID, udid: udid, automatic: automatic, ctx: ctx, cancel: cancel, device: d, config: cfg, release: release}
	if app.backupJobs == nil {
		app.backupJobs = make(map[string]*backupJob)
	}
	app.backupJobs[udid] = job
	app.backupInProgress[udid] = true
	state := app.deviceOperationStates[udid]
	state.BackupState, state.BackupErrorCode, state.LastBackupError = backupStateStarting, "", ""
	app.deviceOperationStates[udid] = state
	app.backupProgress[udid] = backupProgress{State: backupProgressPreparing, Phase: "preparing", UpdatedAt: nowBeijing()}
	accepted = true
	return job, nil
}

// recordBackupResult 冻结执行结果，后到的停止不能改写已经确认的成功或故障。
func (app *application) recordBackupResult(job *backupJob, err error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.backupJobs[job.udid] == job && !job.resultSet {
		job.result, job.resultSet = err, true
	}
}

func (app *application) finishBackupJob(job *backupJob, err error) {
	app.recordBackupResult(job, err)
	job.cancel(nil)
	job.release()
	// 先锁住索引收束，再释放设备占用，旧任务不能清理下一次备份的写入租约。
	manager := app.lockBackupIndexFinish()
	finished, success := false, false
	app.mu.Lock()
	if app.backupJobs[job.udid] == job {
		job.finished = true
		finished, success = true, job.result == nil
		app.finishBackupOperationUnsafe(job.udid, job.result, nowBeijing())
	}
	app.mu.Unlock()
	var lease *backupIndexWrite
	if manager != nil {
		if finished {
			lease = manager.releaseWriteUnsafe(job.udid)
		}
		manager.mu.Unlock()
	}
	app.afterBackupIndexWrite(manager, lease, success)
	app.broadcastStatus()
}

func (app *application) finishBackupOperationUnsafe(udid string, err error, at time.Time) {
	app.finishBackupProgressUnsafe(udid, err, at)
	state := app.deviceOperationStates[udid]
	state.BackupState, state.BackupErrorCode, state.LastBackupError = backupStateSucceeded, "", ""
	if err != nil {
		state.BackupState, state.BackupErrorCode = backupFailureState(err)
		state.LastBackupError = err.Error()
	}
	app.deviceOperationStates[udid] = state
	delete(app.backupInProgress, udid)
}
