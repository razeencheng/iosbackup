package app

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// loadPairedDevices 从lockdown目录加载已配对的设备（不获取锁，假设调用者已持有锁）
func (app *application) loadPairedDevices() map[string]*device {
	pairedDevices := make(map[string]*device)
	lockdownDir := dirLockdown

	// 读取lockdown目录
	files, err := os.ReadDir(lockdownDir)
	if err != nil {
		log.Printf("无法读取lockdown目录: %v", err)
		return pairedDevices
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".plist") {
			// 从文件名提取UDID（去掉.plist后缀）
			udid := strings.TrimSuffix(file.Name(), ".plist")

			if udid == "SystemConfiguration" {
				continue
			}

			// 创建离线设备
			device := &device{
				UDID:         udid,
				Name:         "Unknown Device",
				DeviceType:   "iPhone",
				Connection:   "离线",
				IsOnline:     false,
				BatteryLevel: 0,
				IsCharging:   false,
				LastBackup:   time.Time{}, // 零值时间
			}

			// 尝试从已有配置中获取设备名称
			if config, exists := app.configs[udid]; exists {
				device.Name = config.Name
				device.LastBackup = config.LastBackup
			}

			pairedDevices[udid] = device
		}
	}

	return pairedDevices
}

func connectionTypeDesc(connection string) string {
	switch connection {
	case connectTypeUSB:
		return "USB 已连接"
	case connectTypeNetwork:
		return "WI-FI 已连接"
	default:
		return "未知连接"
	}
}

// connTypeFromDesc 将连接类型描述（中文）反向映射回 connectTypeUSB/connectTypeNetwork。
func connTypeFromDesc(desc string) string {
	if strings.Contains(strings.ToLower(desc), "wi-fi") || strings.Contains(strings.ToLower(desc), "network") {
		return connectTypeNetwork
	}
	return connectTypeUSB
}

func isNetworkConnection(connectionDesc string) bool {
	return strings.Contains(strings.ToLower(connectionDesc), "wi-fi")
}

// RefreshDevices 刷新设备列表
func (app *application) RefreshDevices() error {
	release, err := app.beginConnectionTask(nil)
	if err != nil {
		return err
	}
	defer release()
	app.mu.Lock()
	if len(app.backupInProgress) > 0 {
		app.mu.Unlock()
		return fmt.Errorf("设备正在执行任务，请稍后刷新")
	}
	app.deviceRefreshGeneration++
	generation := app.deviceRefreshGeneration
	for udid, d := range app.loadPairedDevices() {
		if app.isDeviceRemovedUnsafe(udid) || app.deviceRemovalPending[udid] {
			continue
		}
		if app.devices[udid] == nil {
			app.devices[udid] = d
		}
		state := app.deviceOperationStates[udid]
		if state.PairingState == "" || state.PairingState == pairingStateUnknown {
			state.PairingState = pairingStatePaired
			app.deviceOperationStates[udid] = state
		}
	}
	app.mu.Unlock()
	app.scanConnections(app.rootCtx)
	app.mu.RLock()
	var queue []device
	for _, d := range app.devices {
		if d.IsOnline && !d.PresenceUnknown {
			queue = append(queue, *d)
		}
	}
	app.mu.RUnlock()
	for _, d := range queue {
		app.pairDeviceAndUpdateInfoInTask(d, generation)
	}
	app.mu.RLock()
	_, _, valid := app.connectionListsUnsafe()
	app.mu.RUnlock()
	if !valid[0] || !valid[1] {
		return errConnectionUnavailable
	}
	return nil
}

// pairDeviceAndUpdateInfo 检查并配对设备，成功后更新设备信息和配置
func (app *application) pairDeviceAndUpdateInfo(device device, generation uint64) {
	release, err := app.beginConnectionTask(&device)
	if err != nil {
		return
	}
	defer release()
	app.pairDeviceAndUpdateInfoInTask(device, generation)
}

func (app *application) pairDeviceAndUpdateInfoInTask(device device, generation uint64) {
	epoch := app.connectionEpoch(&device)
	app.mu.RLock()
	blocked := app.deviceRemovalBlockedUnsafe(device.UDID) != nil
	app.mu.RUnlock()
	if blocked {
		return
	}
	// 先尝试配对
	app.pairDeviceInTask(&device)

	// 配对完成后重新获取设备信息
	app.getDeviceInfoInTask(&device)

	// 更新配置中的设备名称
	var needSaveConfig bool
	app.mu.Lock()
	if app.deviceRefreshGeneration == generation && app.connectionEpochUnsafe(&device) == epoch {
		if current, exists := app.devices[device.UDID]; exists && current.Connection == device.Connection {
			current.Name = device.Name
			current.DeviceType = device.DeviceType
			current.BatteryLevel = device.BatteryLevel
			current.IsCharging = device.IsCharging
		}
	}
	if config, exists := app.configs[device.UDID]; exists &&
		app.deviceRefreshGeneration == generation && app.connectionEpochUnsafe(&device) == epoch &&
		config.RemovedAt == nil &&
		!app.deviceRemovalPending[device.UDID] {
		if device.Name != "Unknown Device" && device.Name != "" {
			config.Name = device.Name
			needSaveConfig = true
		}
		if device.DeviceType != "" && config.DeviceType != device.DeviceType {
			config.DeviceType = device.DeviceType
			needSaveConfig = true
		}
	}
	app.mu.Unlock()

	// 在锁外保存配置和记录日志
	if needSaveConfig {
		app.saveConfigs()
		app.addLog(device.UDID, fmt.Sprintf("设备信息已更新：%s", device.Name))
	}
}

func (app *application) setPairingState(udid, stateCode, errorCode, message string) {
	app.mu.Lock()
	state := app.deviceOperationStates[udid]
	app.pairingCheckSequence++
	state.pairingCheck = app.pairingCheckSequence
	state.PairingState = stateCode
	if stateCode == pairingStatePaired {
		state.pairingAlert = pairingAlertState{}
	}
	if stateCode != pairingStateChecking {
		state.PairingErrorCode = errorCode
		state.PairingError = message
	}
	app.deviceOperationStates[udid] = state
	app.mu.Unlock()
	app.broadcastStatus()
}

// getDeviceInfo 获取设备详细信息（通过 runIdeviceCmd，自动注入 socket 和 -n）
func (app *application) getDeviceInfo(device *device) {
	release, err := app.beginConnectionTask(device)
	if err != nil {
		return
	}
	defer release()
	app.getDeviceInfoInTask(device)
}

func (app *application) getDeviceInfoInTask(device *device) {
	// 获取设备名称（短命令）
	if output, err := app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdeviceInfo, "-k", "DeviceName"); err == nil {
		deviceName := strings.TrimSpace(string(output))
		if deviceName != "" && deviceName != device.Name {
			device.Name = deviceName
			app.addDebugLog(device.UDID, fmt.Sprintf("获取设备名称成功: %s", deviceName))
		} else if deviceName == "" {
			app.addDebugLog(device.UDID, "获取设备名称为空")
		}
	} else {
		app.addWarnLog(device.UDID, fmt.Sprintf("获取设备名称失败: %v", err))
	}

	// 获取设备类型
	if output, err := app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdeviceInfo, "-k", "ProductType"); err == nil {
		device.DeviceType = strings.TrimSpace(string(output))
	}

	// 获取电池信息（使用正确的 domain，不变）
	if output, err := app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdeviceInfo, "-q", "com.apple.mobile.battery", "-k", "BatteryCurrentCapacity"); err == nil {
		if level, err := strconv.Atoi(strings.TrimSpace(string(output))); err == nil {
			device.BatteryLevel = level
		}
	}

	// 获取充电状态（使用正确的 domain，不变）
	if output, err := app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdeviceInfo, "-q", "com.apple.mobile.battery", "-k", "ExternalConnected"); err == nil {
		device.IsCharging = strings.TrimSpace(string(output)) == "true"
	}

	// 默认值
	if device.Name == "" {
		device.Name = "Unknown Device"
	}
	if device.DeviceType == "" {
		device.DeviceType = "iPhone"
	}
	if device.Connection == "" {
		device.Connection = "未知连接"
	}
}
func (app *application) RefreshDevicesStatus() {
	app.refreshPresence()
	app.refreshDetails()
}
