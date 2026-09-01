package app

import (
	"context"
	"fmt"
	"time"
)

// DeviceStatus 设备状态信息
type deviceStatus struct {
	UDID       string
	Connection string // "USB" 或 "Network"
	IsOnline   bool
}

// getOnlineDevicesWithStatus 通过双 socket 查询获取在线设备状态（USB 优先）。
// USB 列表来自 usbmuxd2 默认 socket（不加 -n）；网络列表来自 netmuxd socket（-l -n）。
func (app *application) getOnlineDevicesWithStatus() map[string]*deviceStatus {
	usbList, netList := app.listDevicesFromBothSockets()
	merged := mergeDeviceLists(usbList, netList)

	devices := make(map[string]*deviceStatus, len(merged))
	for udid, dev := range merged {
		devices[udid] = &deviceStatus{
			UDID:       udid,
			Connection: dev.Connection,
			IsOnline:   true,
		}
	}
	return devices
}

// autoBackupScheduler 自动备份调度器
func (app *application) autoBackupScheduler(ctx context.Context) {
	interval := app.runtimeConfig.SchedulerInterval
	if interval <= 0 {
		interval = defaultRuntimeConfig().SchedulerInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	app.addLog("SYSTEM", fmt.Sprintf("自动备份调度器已启动，每%s检查一次设备状态", interval))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// 获取当前在线设备列表
		onlineDevices := app.getOnlineDevicesWithStatus()

		configs := app.autoBackupCandidates()

		// 检查每个启用自动备份的设备
		for udid, config := range configs {
			// 检查设备是否在备份中或正在检查中，如果是则跳过
			// 检查设备是否在线
			if deviceStatus, exists := onlineDevices[udid]; exists {
				if app.tryBeginAutoBackupCheck(udid) {
					go app.checkAndBackup(udid, config, *deviceStatus)
				}
			}
		}
	}
}

func (app *application) autoBackupCandidates() map[string]backupConfig {
	app.mu.RLock()
	defer app.mu.RUnlock()
	configs := make(map[string]backupConfig, len(app.configs))
	for udid, config := range app.configs {
		if config != nil && config.AutoBackupEnabled && config.RemovedAt == nil && !app.deviceRemovalPending[udid] {
			configs[udid] = *config
		}
	}
	return configs
}

// tryBeginAutoBackupCheck 把最终状态校验与占用合并在同一个锁区间，避免移除和调度的 TOCTOU。
func (app *application) tryBeginAutoBackupCheck(udid string) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.deviceRemovalBlockedUnsafe(udid) != nil || app.deviceRemovalBusyUnsafe(udid) {
		return false
	}
	config := app.configs[udid]
	if config == nil || !config.AutoBackupEnabled {
		return false
	}
	app.checkInProgress[udid] = true
	return true
}

// checkAndBackup 检查并执行备份
func (app *application) checkAndBackup(udid string, config backupConfig, deviceStatus deviceStatus) {
	// 确保函数结束时清理检查状态
	defer func() {
		app.mu.Lock()
		delete(app.checkInProgress, udid)
		app.mu.Unlock()
	}()

	app.mu.RLock()
	if app.deviceRemovalBlockedUnsafe(udid) != nil {
		app.mu.RUnlock()
		return
	}
	device, exists := app.devices[udid]
	device = cloneDevice(device)
	app.mu.RUnlock()

	if !exists {
		return
	}

	now := nowBeijing()

	// 检查是否在备份时间段内
	startTime, _ := time.Parse("15:04", config.StartTime)
	endTime, _ := time.Parse("15:04", config.EndTime)

	currentTime := time.Date(0, 1, 1, now.Hour(), now.Minute(), 0, 0, beijingLocation)
	startTime = time.Date(0, 1, 1, startTime.Hour(), startTime.Minute(), 0, 0, beijingLocation)
	endTime = time.Date(0, 1, 1, endTime.Hour(), endTime.Minute(), 0, 0, beijingLocation)

	inBackupWindow := false
	if startTime.Before(endTime) {
		// 同一天内的时间段：开始时间 <= 当前时间 < 结束时间
		inBackupWindow = !currentTime.Before(startTime) && currentTime.Before(endTime)
	} else {
		// 跨天的时间段：当前时间 >= 开始时间 OR 当前时间 < 结束时间
		inBackupWindow = !currentTime.Before(startTime) || currentTime.Before(endTime)
	}

	if !inBackupWindow {
		return
	}

	// 检查距离上次备份的时间（优先使用配置中的时间）
	lastBackupTime := config.LastBackup
	if lastBackupTime.IsZero() {
		lastBackupTime = device.LastBackup
	}
	timeSinceLastBackup := now.Sub(lastBackupTime)
	if timeSinceLastBackup < time.Duration(config.BackupInterval)*time.Hour {
		return
	}

	// 获取设备详细信息（电量、充电状态等）
	updatedDevice := app.updateDeviceStatusForBackup(device, deviceStatus)

	// 检查电量条件
	if updatedDevice.BatteryLevel > 0 && updatedDevice.BatteryLevel < config.MinBatteryLevel {
		return
	}

	// 检查充电条件
	if config.OnlyWhenCharging && !updatedDevice.IsCharging {
		return
	}

	// 全局并发上限：满了就跳过，下个 tick 再试（天然排队，避免多台并发成倍占用资源）。
	if !app.backupSem.tryAcquire() {
		app.addLog(udid, "已达备份并发上限，稍后重试")
		return
	}
	defer app.backupSem.release()

	// 执行备份，传递连接方式信息
	sessionID := newBackupSessionID()
	app.addLog(udid, backupSessionLog(sessionID, fmt.Sprintf("自动备份触发 - 连接方式: %s, 电量: %d%%, 充电: %v",
		deviceStatus.Connection, updatedDevice.BatteryLevel, updatedDevice.IsCharging)))

	app.performBackupWithConnection(udid, deviceStatus.Connection, sessionID)
}

// updateDeviceStatusForBackup 更新设备状态用于备份检查
func (app *application) updateDeviceStatusForBackup(device *device, deviceStatus deviceStatus) *device {
	// 只修改当前 goroutine 拥有的副本；慢命令完成后再短锁提交。
	device.Connection = deviceStatus.Connection
	device.IsOnline = deviceStatus.IsOnline
	app.getDeviceInfo(device)
	app.mu.Lock()
	if current, ok := app.devices[device.UDID]; ok && current.Connection == deviceStatus.Connection {
		current.Name = device.Name
		current.DeviceType = device.DeviceType
		current.BatteryLevel = device.BatteryLevel
		current.IsCharging = device.IsCharging
	}
	app.mu.Unlock()

	return device
}
