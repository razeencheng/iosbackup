package app

import (
	"context"
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
	// 锁内只取快照；任何设备命令都在锁外运行。
	app.mu.Lock()
	if len(app.backupInProgress) > 0 {
		var backupDevices []string
		for udid := range app.backupInProgress {
			if deviceName, exists := app.devices[udid]; exists {
				backupDevices = append(backupDevices, deviceName.Name)
			} else {
				// 安全的UDID截取
				udidPrefix := udid
				if len(udid) > 8 {
					udidPrefix = udid[:8]
				}
				backupDevices = append(backupDevices, udidPrefix+"...")
			}
		}
		app.addLog("SYSTEM", fmt.Sprintf("刷新被阻止：%d 台设备正在备份中 (%s)", len(app.backupInProgress), strings.Join(backupDevices, ", ")))
		app.mu.Unlock()
		return fmt.Errorf("有 %d 台设备正在备份中，请等待备份完成后再刷新", len(app.backupInProgress))
	}
	app.deviceRefreshGeneration++
	generation := app.deviceRefreshGeneration
	existing := make(map[string]*device, len(app.devices))
	for udid, device := range app.devices {
		if app.isDeviceRemovedUnsafe(udid) || app.deviceRemovalPending[udid] {
			continue
		}
		existing[udid] = cloneDevice(device)
	}
	removed := make(map[string]struct{})
	for udid, config := range app.configs {
		if (config != nil && config.RemovedAt != nil) || app.deviceRemovalPending[udid] {
			removed[udid] = struct{}{}
		}
	}
	app.mu.Unlock()

	app.mu.RLock()
	pairedDevices := app.loadPairedDevices()
	app.mu.RUnlock()
	for udid := range removed {
		delete(pairedDevices, udid)
	}

	nextDevices := existing
	for _, device := range nextDevices {
		device.IsOnline = false
	}
	for udid, pairedDevice := range pairedDevices {
		if existingDevice, exists := nextDevices[udid]; exists {
			// 保留重要的运行时数据
			pairedDevice.LastBackup = existingDevice.LastBackup
			pairedDevice.Name = existingDevice.Name // 保留已知的设备名称
		}
		nextDevices[udid] = pairedDevice
	}

	// 使用双 socket 查询：USB（usbmuxd2 默认 socket）+ 网络（netmuxd socket）
	// USB 列表用 idevice_id -l（不加 -n），网络列表用 netmuxd env 的 idevice_id -l -n
	// 同 UDID 同时在线时 USB 优先
	usbList, netList := app.listDevicesFromBothSockets()
	onlineMap := mergeDeviceLists(usbList, netList)
	for udid := range removed {
		delete(onlineMap, udid)
	}

	hasNewDevices := false

	for udid, onlineDev := range onlineMap {
		// 如果设备已在列表中，更新为在线状态
		var currentDevice *device
		if existingDevice, exists := nextDevices[udid]; exists {
			currentDevice = existingDevice
			currentDevice.IsOnline = true
		} else {
			currentDevice = &device{
				UDID:     udid,
				IsOnline: true,
			}
			nextDevices[udid] = currentDevice
		}

		currentDevice.Connection = onlineDev.Connection

		// 获取设备详细信息（通过 runIdeviceCmd）
		app.getDeviceInfo(currentDevice)
	}

	app.mu.Lock()
	if app.deviceRefreshGeneration != generation {
		app.mu.Unlock()
		return nil
	}
	// 刷新期间可能刚完成移除；发布前以当前配置再过滤一次，避免旧快照复活设备。
	for udid := range nextDevices {
		if app.isDeviceRemovedUnsafe(udid) || app.deviceRemovalPending[udid] {
			delete(nextDevices, udid)
		}
	}
	app.devices = nextDevices
	for udid := range pairedDevices {
		if app.isDeviceRemovedUnsafe(udid) || app.deviceRemovalPending[udid] {
			continue
		}
		state := app.deviceOperationStates[udid]
		if state.PairingState == "" || state.PairingState == pairingStateUnknown {
			state.PairingState = pairingStatePaired
			state.PairingErrorCode = ""
			state.PairingError = ""
			app.deviceOperationStates[udid] = state
		}
	}
	for udid, device := range nextDevices {
		if app.ensureConfigUnsafe(udid, device.Name) {
			hasNewDevices = true
		}
		if config := app.configs[udid]; config != nil && config.RemovedAt == nil && device.DeviceType != "" && config.DeviceType != device.DeviceType {
			config.DeviceType = device.DeviceType
			hasNewDevices = true
		}
	}
	pairQueue := make([]device, 0, len(nextDevices))
	onlineDeviceCount := 0
	for _, device := range nextDevices {
		if device.IsOnline {
			onlineDeviceCount++
			pairQueue = append(pairQueue, *device)
		}
	}
	totalDevices := len(nextDevices)
	app.mu.Unlock()

	for _, device := range pairQueue {
		go app.pairDeviceAndUpdateInfo(device, generation)
	}

	offlineDevices := totalDevices - onlineDeviceCount

	app.addLog("SYSTEM", fmt.Sprintf("刷新设备列表完成，总计 %d 台设备（在线 %d 台，离线 %d 台）", totalDevices, onlineDeviceCount, offlineDevices))

	if hasNewDevices {
		go app.saveConfigs()
	}

	return nil
}

// pairDeviceAndUpdateInfo 检查并配对设备，成功后更新设备信息和配置
func (app *application) pairDeviceAndUpdateInfo(device device, generation uint64) {
	app.mu.RLock()
	blocked := app.deviceRemovalBlockedUnsafe(device.UDID) != nil
	app.mu.RUnlock()
	if blocked {
		return
	}
	// 先尝试配对
	app.pairDevice(&device)

	// 配对完成后重新获取设备信息
	app.getDeviceInfo(&device)

	// 更新配置中的设备名称
	var needSaveConfig bool
	app.mu.Lock()
	if app.deviceRefreshGeneration == generation {
		if current, exists := app.devices[device.UDID]; exists && current.Connection == device.Connection {
			current.Name = device.Name
			current.DeviceType = device.DeviceType
			current.BatteryLevel = device.BatteryLevel
			current.IsCharging = device.IsCharging
		}
	}
	if config, exists := app.configs[device.UDID]; exists &&
		app.deviceRefreshGeneration == generation &&
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
	state.PairingState = stateCode
	state.PairingErrorCode = errorCode
	state.PairingError = message
	app.deviceOperationStates[udid] = state
	app.mu.Unlock()
	app.broadcastStatus()
}

// pairDevice 检查并配对设备（通过 runIdeviceCmd，自动注入 socket 和 -n）。
// 所有用户可见结果都写入结构化状态，由 /api/events 发布。
func (app *application) pairDevice(device *device) {
	if device == nil || device.UDID == "" {
		return
	}
	if app.deviceRemovalBlock(device.UDID) != nil {
		return
	}
	app.setPairingState(device.UDID, pairingStateChecking, "", "")
	app.addDebugLog(device.UDID, "正在检查设备配对状态...")

	// 首先检查设备配对状态（短命令）
	_, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdevicePair, "validate")
	if err == nil {
		app.addDebugLog(device.UDID, "设备已配对，配对状态正常")
		app.setPairingState(device.UDID, pairingStatePaired, "", "")
		return
	}

	app.addInfoLog(device.UDID, "设备未配对，开始自动配对...")

	// 尝试配对设备（短命令）
	output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdevicePair, "pair")
	outputStr := strings.TrimSpace(string(output))

	if err != nil {
		if strings.Contains(outputStr, "Please accept the trust dialog") {
			app.addWarnLog(device.UDID, "配对需要用户确认：请在设备上点击'信任此电脑'")
			app.setPairingState(device.UDID, pairingStateWaitingForTrust, "trust_required", "请在设备上点击“信任此电脑”并输入设备锁屏密码")
			go app.retryPairAfterTrust(device.UDID)
		} else if strings.Contains(outputStr, "already paired") {
			app.addDebugLog(device.UDID, "设备已经配对")
			app.setPairingState(device.UDID, pairingStatePaired, "", "")
		} else {
			app.addErrorLog(device.UDID, fmt.Sprintf("配对失败: %s", outputStr))
			if outputStr == "" {
				outputStr = err.Error()
			}
			app.setPairingState(device.UDID, pairingStateFailed, "pair_failed", outputStr)
		}
		return
	}

	app.addInfoLog(device.UDID, "设备配对成功")

	// 验证配对结果
	_, err = app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdevicePair, "validate")
	if err == nil {
		app.addLog(device.UDID, "配对验证成功，设备可以正常使用")
		app.setPairingState(device.UDID, pairingStatePaired, "", "")
	} else {
		app.addLog(device.UDID, "配对验证失败，可能需要手动重新配对")
		app.setPairingState(device.UDID, pairingStateFailed, "pair_validation_failed", err.Error())
	}
}

// retryPairAfterTrust 等待用户信任后重试配对（通过 runIdeviceCmd）
func (app *application) retryPairAfterTrust(udid string) {
	app.retryPairAfterTrustWithSchedule(udid, 10*time.Second, 6)
}

func (app *application) retryPairAfterTrustWithSchedule(udid string, interval time.Duration, maxRetries int) {
	app.addLog(udid, "等待用户在设备上确认信任...")

	// 获取设备信息以确定连接类型
	app.mu.RLock()
	device, exists := app.devices[udid]
	device = cloneDevice(device)
	app.mu.RUnlock()

	if !exists {
		app.addLog(udid, "设备信息不存在，无法重试配对")
		app.setPairingState(udid, pairingStateFailed, "pair_failed", "设备信息不存在")
		return
	}

	for i := 0; i < maxRetries; i++ {
		timer := time.NewTimer(interval)
		select {
		case <-app.rootCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		// 检查配对状态
		_, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdevicePair, "validate")
		if err == nil {
			app.addLog(udid, "设备信任确认成功，配对完成")
			app.setPairingState(udid, pairingStatePaired, "", "")
			return
		}

		// 重试配对
		output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdevicePair, "pair")
		outputStr := strings.TrimSpace(string(output))

		if err == nil {
			app.addLog(udid, "设备配对成功")
			app.setPairingState(udid, pairingStatePaired, "", "")
			return
		}

		if !strings.Contains(outputStr, "Please accept the trust dialog") {
			if strings.Contains(outputStr, "already paired") {
				app.addLog(udid, "设备已经配对")
				app.setPairingState(udid, pairingStatePaired, "", "")
				return
			}
			app.addLog(udid, fmt.Sprintf("配对失败: %s", outputStr))
			if outputStr == "" {
				outputStr = err.Error()
			}
			app.setPairingState(udid, pairingStateFailed, "pair_failed", outputStr)
			return
		}

		app.addLog(udid, fmt.Sprintf("等待用户确认中... (%d/%d)", i+1, maxRetries))
	}

	app.addLog(udid, "配对超时，请手动在设备上点击'信任此电脑'后重新刷新设备列表")
	app.setPairingState(udid, pairingStateFailed, "pair_timeout", "等待设备确认信任超时，请确认后重试")
}

// getDeviceInfo 获取设备详细信息（通过 runIdeviceCmd，自动注入 socket 和 -n）
func (app *application) getDeviceInfo(device *device) {
	// 获取设备名称（短命令）
	if output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdeviceInfo, "-k", "DeviceName"); err == nil {
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
	if output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdeviceInfo, "-k", "ProductType"); err == nil {
		device.DeviceType = strings.TrimSpace(string(output))
	}

	// 获取电池信息（使用正确的 domain，不变）
	if output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdeviceInfo, "-q", "com.apple.mobile.battery", "-k", "BatteryCurrentCapacity"); err == nil {
		if level, err := strconv.Atoi(strings.TrimSpace(string(output))); err == nil {
			device.BatteryLevel = level
		}
	}

	// 获取充电状态（使用正确的 domain，不变）
	if output, err := app.runIdeviceCmd(context.Background(), cmdKindShort, device, cmdIdeviceInfo, "-q", "com.apple.mobile.battery", "-k", "ExternalConnected"); err == nil {
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
