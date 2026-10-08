package app

import (
	"errors"
	"fmt"
	"time"
)

var (
	errDeviceNotFound       = errors.New("设备不存在")
	errDeviceBusy           = errors.New("设备正在执行任务")
	errDeviceRemoved        = errors.New("设备已移除")
	errDeviceRemovalPending = errors.New("设备正在移除")
)

// isDeviceRemovedUnsafe 仅可在持有 app.mu 的情况下调用。
func (app *application) isDeviceRemovedUnsafe(udid string) bool {
	config := app.configs[udid]
	return config != nil && config.RemovedAt != nil
}

// deviceRemovalBlockedUnsafe 返回会阻止新设备操作的归档状态。
// 调用方必须已持有 app.mu。
func (app *application) deviceRemovalBlockedUnsafe(udid string) error {
	if app.deviceRemovalPending[udid] {
		return errDeviceRemovalPending
	}
	if app.isDeviceRemovedUnsafe(udid) {
		return errDeviceRemoved
	}
	return nil
}

func (app *application) deviceRemovalBlock(udid string) error {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.deviceRemovalBlockedUnsafe(udid)
}

// deviceRemovalBusyUnsafe 判断是否已有设备任务占用该 UDID。
// 调用方必须已持有 app.mu。
func (app *application) deviceRemovalBusyUnsafe(udid string) bool {
	return app.backupInProgress[udid] ||
		app.networkRegistrations[udid] ||
		app.checkInProgress[udid] ||
		app.activeDeviceCommands[udid] != nil
}

// removeDevice 将设备从活动视图归档。它不删除备份、配置、密码或配对记录。
// 配置必须先成功落盘，内存状态才会发布，避免重启后“设备重新出现”的半成功状态。
func (app *application) removeDevice(udid string) (time.Time, error) {
	if !udidPattern.MatchString(udid) {
		return time.Time{}, fmt.Errorf("无效的设备UDID")
	}

	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()

	app.mu.Lock()
	config := app.configs[udid]
	device := app.devices[udid]
	if config == nil && device == nil {
		app.mu.Unlock()
		return time.Time{}, errDeviceNotFound
	}
	if config != nil && config.RemovedAt != nil {
		removedAt := *config.RemovedAt
		app.mu.Unlock()
		return removedAt, nil
	}
	if app.deviceRemovalPending[udid] {
		app.mu.Unlock()
		return time.Time{}, errDeviceRemovalPending
	}
	if app.deviceRemovalBusyUnsafe(udid) {
		app.mu.Unlock()
		return time.Time{}, errDeviceBusy
	}
	if app.deviceRemovalPending == nil {
		app.deviceRemovalPending = make(map[string]bool)
	}
	app.deviceRemovalPending[udid] = true

	var candidate backupConfig
	if config != nil {
		candidate = *config
	} else {
		name := ""
		if device != nil {
			name = device.Name
		}
		candidate = *app.defaultBackupConfig(udid, name)
	}
	if device != nil {
		if candidate.Name == "" {
			candidate.Name = device.Name
		}
	}
	app.mu.Unlock()

	removedAt := nowBeijing()
	candidate.RemovedAt = &removedAt
	if err := app.configStore.Put(candidate); err != nil {
		app.mu.Lock()
		delete(app.deviceRemovalPending, udid)
		app.mu.Unlock()
		return time.Time{}, fmt.Errorf("保存设备移除状态: %w", err)
	}

	app.mu.Lock()
	app.configs[udid] = cloneBackupConfig(&candidate)
	delete(app.devices, udid)
	delete(app.deviceOperationStates, udid)
	delete(app.deviceOfflineSince, udid)
	delete(app.devicePresenceMissingSince, udid)
	delete(app.deviceRemovalPending, udid)
	app.mu.Unlock()
	app.broadcastStatus()
	return removedAt, nil
}

// restoreRemovedDevice 清除归档标记并恢复一张离线设备卡片；真实在线状态由后续刷新更新。
func (app *application) restoreRemovedDevice(udid string) error {
	if !udidPattern.MatchString(udid) {
		return fmt.Errorf("无效的设备UDID")
	}

	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()

	app.mu.Lock()
	config := app.configs[udid]
	if config == nil {
		app.mu.Unlock()
		return errDeviceNotFound
	}
	if app.deviceRemovalPending[udid] {
		app.mu.Unlock()
		return errDeviceRemovalPending
	}
	if config.RemovedAt == nil {
		if app.devices[udid] == nil {
			app.devices[udid] = restoredOfflineDevice(*config)
		}
		app.mu.Unlock()
		return nil
	}
	candidate := *config
	candidate.RemovedAt = nil
	app.mu.Unlock()

	if err := app.configStore.Put(candidate); err != nil {
		return fmt.Errorf("保存设备恢复状态: %w", err)
	}

	app.mu.Lock()
	app.configs[udid] = cloneBackupConfig(&candidate)
	app.devices[udid] = restoredOfflineDevice(candidate)
	app.mu.Unlock()
	app.broadcastStatus()
	return nil
}

func restoredOfflineDevice(config backupConfig) *device {
	return &device{
		UDID:       config.UDID,
		Name:       config.Name,
		DeviceType: config.DeviceType,
		LastBackup: config.LastBackup,
		IsOnline:   false,
	}
}
