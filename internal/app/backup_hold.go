package app

import (
	"fmt"
	"os"

	"iosbackup/internal/i18n"
)

// backupHold 是当前进程的有效意图；在途配置发布旧快照不能清除此屏障。
type backupHold struct {
	Held        bool
	Version     string
	Persistence string
}

func (app *application) backupHoldUnsafe(udid string) backupHold {
	if h, ok := app.backupHolds[udid]; ok {
		return h
	}
	h := backupHold{Persistence: "saved"}
	if c := app.configs[udid]; c != nil {
		h.Held, h.Version = c.AutoBackupHold, c.AutoBackupHoldVersion
	}
	return h
}

func (app *application) setBackupHoldUnsafe(udid string, h backupHold) {
	if app.backupHolds == nil {
		app.backupHolds = make(map[string]backupHold)
	}
	app.backupHolds[udid] = h
}

func (app *application) overlayBackupHoldUnsafe(c *backupConfig) {
	if h, ok := app.backupHolds[c.UDID]; ok {
		c.AutoBackupHold, c.AutoBackupHoldVersion = h.Held, h.Version
	}
}

func (app *application) stopBackup(udid, jobID string) error {
	app.mu.Lock()
	if app.configs[udid] == nil {
		app.mu.Unlock()
		return errDeviceNotFound
	}
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	job := app.backupJobs[udid]
	if job == nil || job.id != jobID {
		app.mu.Unlock()
		return i18n.NewError(i18n.BackupJobStale, nil)
	}
	var cancel func(error)
	first := !job.stopAccepted
	if first {
		job.stopAccepted = true
		app.setBackupHoldUnsafe(udid, backupHold{Held: true, Version: job.id, Persistence: "pending"})
		if !job.resultSet && !job.commandFinished {
			state := app.deviceOperationStates[udid]
			state.BackupState = backupStateStopping
			if app.backupJobRecords != nil {
				app.backupJobRecords.transition(job.id, backupStateStopping)
				app.backupJobRecords.emit(job.id, backupJobEvent{At: app.nowTime(), Code: "stop_requested"})
			}
			app.deviceOperationStates[udid] = state
			cancel = job.cancel
		}
	}
	h := app.backupHoldUnsafe(udid)
	app.mu.Unlock()
	if cancel != nil {
		cancel(errBackupStoppedByUser)
	}
	if first {
		app.addInfoLog(udid, backupSessionLog(jobID, "event=backup.stop_requested"))
	}
	app.broadcastStatus()
	if h.Held && h.Persistence != "saved" {
		return app.persistBackupHold(udid, h.Version, false)
	}
	return nil
}

// persistBackupHold 可靠保存后才解除暂缓；落盘失败不能回滚已发出的进程取消。
func (app *application) persistBackupHold(udid, version string, resume bool) error {
	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.Lock()
	cfg := app.configs[udid]
	if cfg == nil {
		app.mu.Unlock()
		return errDeviceNotFound
	}
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	h := app.backupHoldUnsafe(udid)
	if h.Version != version {
		app.mu.Unlock()
		return i18n.NewError(i18n.BackupHoldStale, nil)
	}
	if resume {
		if job := app.backupJobs[udid]; job != nil && job.stopAccepted && !job.finished {
			app.mu.Unlock()
			return i18n.NewError(i18n.BackupStopping, nil)
		}
	}
	if !h.Held || (!resume && h.Persistence == "saved") {
		app.mu.Unlock()
		return nil
	}
	candidate := *cfg
	candidate.AutoBackupHold, candidate.AutoBackupHoldVersion = !resume, h.Version
	app.mu.Unlock()
	err := app.configStore.Put(candidate)
	if err != nil && resume {
		// Put 可能已经替换其中一个文件，补写暂缓；失败不声称磁盘已回滚。
		candidate.AutoBackupHold = true
		if repairErr := app.configStore.Put(candidate); repairErr != nil {
			app.addErrorLog(udid, fmt.Sprintf("event=backup.auto_hold_repair_failed error=%v", repairErr))
		}
	}
	if err != nil {
		// 读取只能确认当前可见内容，不能把同步失败重新解释为可靠保存。
		primary := app.readBackupHoldOnDisk(app.configStore.path, udid, version)
		lastGood := app.readBackupHoldOnDisk(app.configStore.lastGoodPath, udid, version)
		app.addWarnLog(udid, fmt.Sprintf("event=backup.auto_hold_persistence_uncertain primary=%s last_good=%s", primary, lastGood))
	}
	app.mu.Lock()
	current := app.backupHoldUnsafe(udid)
	if current.Version != version {
		app.mu.Unlock()
		return i18n.NewError(i18n.BackupHoldStale, nil)
	}
	if err != nil {
		current.Persistence = "error"
	} else {
		current.Held, current.Persistence = !resume, "saved"
		if c := app.configs[udid]; c != nil {
			c.AutoBackupHold, c.AutoBackupHoldVersion = current.Held, current.Version
		}
	}
	app.setBackupHoldUnsafe(udid, current)
	app.mu.Unlock()
	app.broadcastStatus()
	if err != nil {
		app.addErrorLog(udid, fmt.Sprintf("event=backup.auto_hold_save_failed error=%v", err))
		return i18n.NewError(i18n.BackupHoldSaveFailed, nil)
	}
	event := "event=backup.auto_hold_set"
	if resume {
		event = "event=backup.auto_hold_cleared"
	}
	app.addInfoLog(udid, event)
	return nil
}

func (app *application) readBackupHoldOnDisk(path, udid, version string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	configs, _, err := app.configStore.decode(data)
	if err != nil {
		return "unknown"
	}
	c, ok := configs[udid]
	if !ok || c.AutoBackupHoldVersion != version {
		return "different_version"
	}
	if c.AutoBackupHold {
		return "held"
	}
	return "released"
}
