package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"iosbackup/internal/buildinfo"
)

var errDeviceDisconnected = errors.New("设备 Wi-Fi 连接持续中断，操作已取消")

type activeDeviceCommand struct {
	connection string
	cancel     context.CancelCauseFunc
	cancelled  bool
}

const (
	presencePollInterval = 4 * time.Second  // 设备在线/连接类型轮询（便宜）
	detailPollInterval   = 30 * time.Second // 电量/名称等明细刷新（较贵，低频）
	ssePingInterval      = 20 * time.Second // SSE 心跳，保活长连接
)

// ---- eventHub：SSE 广播总线（非阻塞，慢客户端丢帧） ----

type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[chan []byte]struct{})}
}

func (h *eventHub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 向所有客户端发送；缓冲满的（慢）客户端直接丢帧，绝不阻塞。
func (h *eventHub) Broadcast(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- payload:
		default: // 慢客户端：丢帧
		}
	}
}

func (h *eventHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// ---- 状态快照 ----

type deviceStatusDTO struct {
	PresenceKnown       bool               `json:"presence_known"`
	LastSeen            string             `json:"last_seen,omitempty"`
	OperationsAvailable bool               `json:"operations_available"`
	UDID                string             `json:"udid"`
	Name                string             `json:"name"`
	DeviceType          string             `json:"device_type"`
	Conn                string             `json:"conn"` // usb | wifi | offline
	Online              bool               `json:"online"`
	Battery             int                `json:"battery"`
	Charging            bool               `json:"charging"`
	LastBackup          string             `json:"last_backup"` // 北京时间 "2006-01-02 15:04" 或空
	BackingUp           bool               `json:"backing_up"`
	PairingState        string             `json:"pairing_state"`
	PairingErrorCode    string             `json:"pairing_error_code"`
	PairingError        string             `json:"pairing_error"`
	BackupState         string             `json:"backup_state"`
	LastBackupErrorCode string             `json:"last_backup_error_code"`
	LastBackupError     string             `json:"last_backup_error"`
	BackupProgress      *backupProgressDTO `json:"backup_progress,omitempty"`
}

type backupProgressDTO struct {
	State              string   `json:"state"`
	Phase              string   `json:"phase"`
	OverallPercent     *float64 `json:"overall_percent,omitempty"`
	CurrentFilePercent *float64 `json:"current_file_percent,omitempty"`
	CurrentBytes       *int64   `json:"current_bytes,omitempty"`
	CurrentTotalBytes  *int64   `json:"current_total_bytes,omitempty"`
	UpdatedAt          string   `json:"updated_at"`
	LastActivityAt     string   `json:"last_activity_at,omitempty"`
}

type statusSnapshot struct {
	ConnectionServices []connectionServiceDTO `json:"connection_services"`
	Devices            []deviceStatusDTO      `json:"devices"`
	BackupInProgress   int                    `json:"backup_in_progress"`
	RemovedDeviceCount int                    `json:"removed_device_count"`
	Version            string                 `json:"version"` // 构建版本；前端比对以检测「已部署新版本」（SSE 重连即拿到新版本）
}

// connCode 把中文连接串映射为机器码（USB 优先规则已体现在 Connection 上）。
func connCode(d *device) string {
	if d.PresenceUnknown {
		return "unknown"
	}
	if !d.IsOnline {
		return "offline"
	}
	if d.Connection == connectionTypeDesc(connectTypeUSB) {
		return "usb"
	}
	return "wifi"
}

func lastBackupStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return toBeijingTime(t).Format("2006-01-02 15:04")
}

// buildStatusSnapshot 在读锁下生成当前快照（排序与 handleHome 一致）。
func (app *application) buildStatusSnapshot() statusSnapshot {
	app.mu.RLock()
	defer app.mu.RUnlock()
	devs := make([]deviceStatusDTO, 0, len(app.devices))
	removedDeviceCount := 0
	for _, config := range app.configs {
		if config != nil && config.RemovedAt != nil {
			removedDeviceCount++
		}
	}
	for udid, d := range app.devices {
		if app.isDeviceRemovedUnsafe(udid) || app.deviceRemovalPending[udid] {
			continue
		}
		operationState := app.deviceOperationStates[udid]
		if operationState.PairingState == "" {
			operationState.PairingState = pairingStateUnknown
		}
		if operationState.BackupState == "" {
			operationState.BackupState = backupStateIdle
			if !d.LastBackup.IsZero() {
				operationState.BackupState = backupStateSucceeded
			}
		}
		var progressDTO *backupProgressDTO
		if progress, ok := app.backupProgress[udid]; ok {
			updatedAt := ""
			if !progress.UpdatedAt.IsZero() {
				updatedAt = toBeijingTime(progress.UpdatedAt).Format(time.RFC3339)
			}
			lastActivityAt := ""
			if !progress.LastActivityAt.IsZero() {
				lastActivityAt = toBeijingTime(progress.LastActivityAt).Format(time.RFC3339)
			}
			progressDTO = &backupProgressDTO{
				LastActivityAt:     lastActivityAt,
				State:              string(progress.State),
				Phase:              progress.Phase,
				OverallPercent:     cloneOptionalFloat64(progress.OverallPercent),
				CurrentFilePercent: cloneOptionalFloat64(progress.CurrentFilePercent),
				CurrentBytes:       cloneOptionalInt64(progress.CurrentBytes),
				CurrentTotalBytes:  cloneOptionalInt64(progress.CurrentTotalBytes),
				UpdatedAt:          updatedAt,
			}
		}
		lastSeen := ""
		if !d.LastSeen.IsZero() {
			lastSeen = toBeijingTime(d.LastSeen).Format(time.RFC3339)
		}
		devs = append(devs, deviceStatusDTO{
			PresenceKnown: !d.PresenceUnknown, LastSeen: lastSeen, OperationsAvailable: d.IsOnline && app.connectionAdmissionUnsafe(d) == nil,
			UDID:                udid,
			Name:                d.Name,
			DeviceType:          d.DeviceType,
			Conn:                connCode(d),
			Online:              d.IsOnline,
			Battery:             d.BatteryLevel,
			Charging:            d.IsCharging,
			LastBackup:          lastBackupStr(d.LastBackup),
			BackingUp:           app.backupInProgress[udid],
			PairingState:        operationState.PairingState,
			PairingErrorCode:    operationState.PairingErrorCode,
			PairingError:        operationState.PairingError,
			BackupState:         operationState.BackupState,
			LastBackupErrorCode: operationState.BackupErrorCode,
			LastBackupError:     operationState.LastBackupError,
			BackupProgress:      progressDTO,
		})
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].Online != devs[j].Online {
			return devs[i].Online
		}
		if devs[i].Name == devs[j].Name {
			return devs[i].UDID < devs[j].UDID
		}
		return devs[i].Name < devs[j].Name
	})
	return statusSnapshot{
		ConnectionServices: app.connectionServicesUnsafe(),
		Devices:            devs,
		BackupInProgress:   len(app.backupInProgress),
		RemovedDeviceCount: removedDeviceCount,
		Version:            buildinfo.Version,
	}
}

// broadcastStatus 仅在快照相对上次发生变化时广播（事件化，不刷屏）。
func (app *application) broadcastStatus() {
	if app.hub == nil {
		return
	}
	snapshot := app.buildStatusSnapshot()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	for i := range snapshot.ConnectionServices {
		snapshot.ConnectionServices[i].LastSuccess = ""
	}
	for i := range snapshot.Devices {
		snapshot.Devices[i].LastSeen = ""
	}
	comparison, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	app.mu.Lock()
	changed := string(comparison) != app.lastSnapshotJSON
	if changed {
		app.lastSnapshotJSON = string(comparison)
	}
	app.mu.Unlock()
	if changed {
		app.hub.Broadcast(payload)
	}
}

// ---- 中央状态轮询 ----

// statusPoller 后端集中轮询设备状态，变化时经 SSE 广播。
// presence（在线/连接类型）每 presencePollInterval 刷新；明细（电量/名称）低频刷新。
func (app *application) statusPoller(ctx context.Context) {
	// 一个明细 worker；队列容量为一，慢设备不会阻塞健康扫描或积累 goroutine。
	details := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-details:
				app.mu.RLock()
				pending := app.connectionRefreshPending
				app.mu.RUnlock()
				if pending {
					app.refreshRecoveredConnections()
				} else {
					app.refreshDetails()
				}
				app.broadcastStatus()
			}
		}
	}()
	defer func() { <-done }()
	scheduleDetails := func() {
		select {
		case details <- struct{}{}:
		default:
		}
	}
	app.refreshPresence()
	scheduleDetails()
	app.broadcastStatus()
	interval := app.runtimeConfig.PresenceInterval
	if interval <= 0 {
		interval = presencePollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastDetail := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newOnline := app.refreshPresence()
			app.mu.RLock()
			pending := app.connectionRefreshPending
			app.mu.RUnlock()
			if newOnline || pending || time.Since(lastDetail) >= detailPollInterval {
				scheduleDetails()
				lastDetail = time.Now()
			}
			app.broadcastStatus()
		}
	}

}

// refreshPresence 轻量刷新在线状态与连接类型（不跑 getDeviceInfo）。
// 返回是否有设备「新上线」（用于触发一次明细刷新）。
func (app *application) refreshPresence() bool {
	app.mu.RLock()
	before := make(map[string]bool, len(app.devices))
	for udid, d := range app.devices {
		before[udid] = d.IsOnline
	}
	app.mu.RUnlock()
	app.scanConnections(app.rootCtx)
	app.mu.RLock()
	defer app.mu.RUnlock()
	for udid, d := range app.devices {
		if d.IsOnline && !before[udid] {
			return true
		}
	}
	return false
}

// registerActiveDeviceCommand 登记可由在线状态监控取消的长设备命令。
// release 只移除本次登记，避免旧命令退出时误删同 UDID 的新命令。
func (app *application) registerActiveDeviceCommand(udid, connection string, cancel context.CancelCauseFunc) func() {
	command := &activeDeviceCommand{connection: connection, cancel: cancel}
	app.mu.Lock()
	app.activeDeviceCommands[udid] = command
	delete(app.deviceOfflineSince, udid)
	app.mu.Unlock()
	return func() {
		app.mu.Lock()
		if app.activeDeviceCommands[udid] == command {
			delete(app.activeDeviceCommands, udid)
			delete(app.deviceOfflineSince, udid)
		}
		app.mu.Unlock()
	}
}

// applyPresenceSnapshot 发布一次轻量设备快照，并在网络长命令连续离线超过宽限期后取消它。
// cancel 在锁外执行；单次扫描缺失不会误杀正常的 mDNS/netmuxd 抖动。
func (app *application) applyPresenceSnapshot(online map[string]*device, now time.Time) (newOnline, configCreated bool) {
	networkOnline := make(map[string]*device)
	for udid, device := range online {
		if connTypeFromDesc(device.Connection) == connectTypeNetwork {
			networkOnline[udid] = device
		}
	}
	return app.applyPresenceSnapshotWithNetwork(online, networkOnline, now)
}

// applyPresenceSnapshotWithNetwork 同时接收原始 netmuxd 列表。合并后的设备列表会按 USB
// 优先覆盖同 UDID 的 Wi-Fi 记录，但正在运行的 Wi-Fi 命令仍必须以 netmuxd 是否看见设备
// 为准，不能因为用户中途插入 USB 就把健康的 Wi-Fi 任务误判为离线。
func (app *application) applyPresenceSnapshotWithNetwork(online, networkOnline map[string]*device, now time.Time) (newOnline, configCreated bool) {
	return app.applyPresenceSnapshotValid(online, networkOnline, now, [2]bool{true, true}, 0)
}

func (app *application) applyPresenceSnapshotValid(online, networkOnline map[string]*device, now time.Time, valid [2]bool, revision uint64) (newOnline, configCreated bool) {
	grace := app.runtimeConfig.DeviceDisconnectGrace
	if grace <= 0 {
		grace = defaultRuntimeConfig().DeviceDisconnectGrace
	}
	type pendingCancel struct {
		udid   string
		cancel context.CancelCauseFunc
	}
	type pendingPresenceNotification struct {
		udid   string
		name   string
		online bool
	}
	var pending []pendingCancel
	var presenceNotifications []pendingPresenceNotification

	app.mu.Lock()
	if revision != 0 && revision != app.connectionRevision {
		app.mu.Unlock()
		return
	}
	for udid, od := range online {
		if app.isDeviceRemovedUnsafe(udid) {
			delete(app.devices, udid)
			delete(app.devicePresenceMissingSince, udid)
			continue
		}
		if app.deviceRemovalPending[udid] {
			continue
		}
		if d, ok := app.devices[udid]; ok {
			if !d.IsOnline {
				newOnline = true
				presenceNotifications = append(presenceNotifications, pendingPresenceNotification{udid: udid, name: d.Name, online: true})
			}
			d.IsOnline = true
			d.PresenceUnknown = false
			d.LastSeen = now
			d.Connection = od.Connection
		} else {
			app.devices[udid] = &device{UDID: udid, Name: udid, IsOnline: true, Connection: od.Connection, LastSeen: now}
			newOnline = true
			presenceNotifications = append(presenceNotifications, pendingPresenceNotification{udid: udid, name: udid, online: true})
		}
		delete(app.devicePresenceMissingSince, udid)
		// 发现即建默认配置，保证「立即备份」可用（首页提速后不再在请求路径上建配置）
		if app.ensureConfigUnsafe(udid, app.devices[udid].Name) {
			configCreated = true
		}
	}
	for udid, d := range app.devices {
		if app.isDeviceRemovedUnsafe(udid) {
			delete(app.devices, udid)
			delete(app.deviceOfflineSince, udid)
			delete(app.devicePresenceMissingSince, udid)
			continue
		}
		_, listed := online[udid]
		backend := connectionUSB
		if isNetworkConnection(d.Connection) {
			backend = connectionWiFi
		}
		known := listed || valid[backend]
		d.PresenceUnknown = !known
		if !known {
			delete(app.devicePresenceMissingSince, udid)
		}
		if known && !listed && d.IsOnline {
			if connTypeFromDesc(d.Connection) != connectTypeNetwork {
				d.IsOnline = false
				presenceNotifications = append(presenceNotifications, pendingPresenceNotification{udid: udid, name: d.Name, online: false})
				delete(app.devicePresenceMissingSince, udid)
			} else if since, tracked := app.devicePresenceMissingSince[udid]; !tracked {
				// iOS 休眠或 mDNS/netmuxd 刷新会造成短时发现空窗。Wi-Fi 设备先保持
				// 上一帧状态；只有连续缺失达到宽限期后才对页面发布离线。
				app.devicePresenceMissingSince[udid] = now
			} else if now.Sub(since) >= grace {
				d.IsOnline = false
				presenceNotifications = append(presenceNotifications, pendingPresenceNotification{udid: udid, name: d.Name, online: false})
				delete(app.devicePresenceMissingSince, udid)
			}
		}
		command := app.activeDeviceCommands[udid]
		commandOnline := listed
		commandKnown := known
		if command != nil && command.connection == connectTypeNetwork {
			// 运行中的命令固定在 netmuxd socket；合并视图即使显示 USB，也不能替代网络在线证据。
			_, commandOnline = networkOnline[udid]
			commandKnown = valid[connectionWiFi]
		}
		if !commandKnown {
			delete(app.deviceOfflineSince, udid)
			continue
		}
		if commandOnline {
			app.setBackupProgressConnectionUnsafe(udid, true, now)
			delete(app.deviceOfflineSince, udid)
			continue
		}
		if command == nil || command.connection != connectTypeNetwork || command.cancelled {
			delete(app.deviceOfflineSince, udid)
			continue
		}
		since, tracked := app.deviceOfflineSince[udid]
		if !tracked {
			app.deviceOfflineSince[udid] = now
			app.setBackupProgressConnectionUnsafe(udid, false, now)
			continue
		}
		if now.Sub(since) >= grace {
			command.cancelled = true
			pending = append(pending, pendingCancel{udid: udid, cancel: command.cancel})
		}
	}
	app.mu.Unlock()

	for _, item := range pending {
		app.addWarnLog(item.udid, fmt.Sprintf("Wi-Fi 设备连续离线 %s，正在取消长任务", grace))
		item.cancel(errDeviceDisconnected)
	}
	if manager := app.notificationManagerSnapshot(); manager != nil {
		for _, item := range presenceNotifications {
			if item.online {
				manager.SendDeviceOnline(item.name, item.udid)
			} else {
				manager.SendDeviceOffline(item.name, item.udid)
			}
		}
	}
	return newOnline, configCreated
}

// refreshDetails 在副本上跑 getDeviceInfo（无锁、慢），再回写（短锁），避免长时间持锁与数据竞争。
func (app *application) refreshDetails() {
	if !app.detailRefreshMu.TryLock() {
		return
	}
	defer app.detailRefreshMu.Unlock()
	release, err := app.beginConnectionTask(nil)
	if err != nil {
		return
	}
	defer release()
	app.mu.RLock()
	copies := make([]device, 0, len(app.devices))
	epochs := make(map[string]uint64)
	for _, d := range app.devices {
		if d.IsOnline && !d.PresenceUnknown {
			copies = append(copies, *d)
			epochs[d.UDID] = app.connectionEpochUnsafe(d)
		}
	}
	app.mu.RUnlock()
	for i := range copies {
		if app.rootCtx.Err() != nil {
			return
		}
		app.getDeviceInfoInTask(&copies[i])
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, copy := range copies {
		if d := app.devices[copy.UDID]; d != nil && d.IsOnline && !d.PresenceUnknown && d.Connection == copy.Connection && app.connectionEpochUnsafe(d) == epochs[d.UDID] {
			d.Name, d.DeviceType, d.BatteryLevel, d.IsCharging = copy.Name, copy.DeviceType, copy.BatteryLevel, copy.IsCharging
		}
	}
}

// ---- SSE 端点 ----

func (app *application) handleEvents(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if app.sseLimiter == nil || !app.sseLimiter.Acquire(ip) {
		writeError(w, http.StatusServiceUnavailable, "SSE 连接数已达上限")
		return
	}
	defer app.sseLimiter.Release(ip)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 关反代缓冲

	ch := app.hub.Subscribe()
	defer app.hub.Unsubscribe(ch)

	// 首帧：当前快照（首屏对齐）
	if payload, err := json.Marshal(app.buildStatusSnapshot()); err == nil {
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
