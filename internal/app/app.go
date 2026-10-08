package app

import (
	"context"
	"embed"
	"html/template"
	"sync"
	"time"
)

const (
	cmdIdeviceID      = "/usr/local/bin/idevice_id"
	cmdIdevicePair    = "/usr/local/bin/idevicepair"
	cmdIdeviceInfo    = "/usr/local/bin/ideviceinfo"
	cmdIdevicebackup2 = "/usr/local/bin/idevicebackup2"
	cmdUSBMuxd        = "/usr/local/bin/usbmuxd"

	dirLockdown = "/var/lib/lockdown"
	dirRun      = "/var/run"
)

const (
	connectTypeUSB     = "usb"
	connectTypeNetwork = "network"
)

//go:embed templates/* static/*
var templateFS embed.FS

// Device 设备信息结构
type device struct {
	UDID            string    `json:"udid"`
	Name            string    `json:"name"`
	DeviceType      string    `json:"device_type"`
	Connection      string    `json:"connection"`
	LastBackup      time.Time `json:"last_backup"`
	IsOnline        bool      `json:"is_online"`
	PresenceUnknown bool      `json:"presence_unknown"`
	LastSeen        time.Time `json:"last_seen"`
	BatteryLevel    int       `json:"battery_level"`
	IsCharging      bool      `json:"is_charging"`
}

// BackupConfig 备份配置结构
type backupConfig struct {
	UDID                 string     `json:"udid"`
	Name                 string     `json:"name"`
	DeviceType           string     `json:"device_type,omitempty"` // 移除设备后保留型号，供恢复面板展示
	StartTime            string     `json:"start_time"`
	EndTime              string     `json:"end_time"`
	BackupInterval       int        `json:"backup_interval"` // 小时
	MinBatteryLevel      int        `json:"min_battery_level"`
	OnlyWhenCharging     bool       `json:"only_when_charging"`
	BackupDirectory      string     `json:"backup_directory"`
	AutoBackupEnabled    bool       `json:"auto_backup_enabled"`
	LastBackup           time.Time  `json:"last_backup"`
	NetworkAddress       string     `json:"network_address,omitempty"`        // 可选：Wi-Fi/Tailscale IP（跨子网用）；零值安全
	LastBackupConnection string     `json:"last_backup_connection,omitempty"` // 上次备份的连接类型（usb/network）；零值安全
	RestoreEnabled       bool       `json:"restore_enabled,omitempty"`        // 每设备恢复开关，默认 false（关时 restore 端点 403）
	RemovedAt            *time.Time `json:"removed_at,omitempty"`             // 可恢复移除时间；非空时不发现、不展示、不执行任务
}

// BackupLog 备份日志结构
type backupLogEntry struct {
	UDID      string    `json:"udid"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
}

const (
	pairingStateUnknown         = "unknown"
	pairingStateChecking        = "checking"
	pairingStateWaitingForTrust = "waiting_for_trust"
	pairingStatePaired          = "paired"
	pairingStateFailed          = "failed"

	backupStateIdle        = "idle"
	backupStateStarting    = "starting"
	backupStateRunning     = "running"
	backupStateSucceeded   = "succeeded"
	backupStateFailed      = "failed"
	backupStateInterrupted = "interrupted"
)

// deviceOperationState 是配对与备份操作的内存态。它通过 /api/events 发布，
// 不写入配置文件；已有成功备份可由 LastBackup 恢复，瞬时错误在进程重启后清空。
type deviceOperationState struct {
	PairingState     string
	PairingErrorCode string
	PairingError     string
	BackupState      string
	BackupErrorCode  string
	LastBackupError  string
}

type muxLifecycleState uint8

const (
	muxStopped muxLifecycleState = iota
	muxStarting
	muxRunning
	muxStopping
)

// App 应用结构
type application struct {
	devices map[string]*device
	configs map[string]*backupConfig
	// configPersistMu 串行化“快照 -> 落盘 -> 发布”事务，避免并发保存丢失更新。
	configPersistMu sync.Mutex
	configStore     *backupConfigStore
	authManager     *authManager
	csrfManager     *csrfManager
	sseLimiter      *sseLimiter
	// rootCtx 是所有后台 worker 与进程 supervisor 的共同父生命周期。
	rootCtx                      context.Context
	runtimeConfig                runtimeConfig
	enableExperimentalOperations bool
	paths                        appPaths
	// usbmuxd2 / netmuxd 进程监督句柄（均由 superviseMux 管理，单 goroutine + 单 cancel）
	usbMuxLifecycleMu         sync.Mutex
	usbMuxState               muxLifecycleState
	usbMuxGeneration          uint64
	usbmuxdCancel             context.CancelFunc // usbmuxd2 supervisor 取消
	usbmuxdDone               chan struct{}      // usbmuxd2 supervisor 退出信号
	netMuxLifecycleMu         sync.Mutex
	netMuxState               muxLifecycleState
	netMuxGeneration          uint64
	netmuxdCancel             context.CancelFunc // netmuxd supervisor 取消
	netmuxdDone               chan struct{}      // netmuxd supervisor 退出信号
	muxRestartMu              sync.Mutex
	connectionClosed          bool
	connectionRecoveryWorkers sync.WaitGroup
	connectionRefreshPending  bool
	connectionRefreshRevision uint64
	connectionPublishMu       sync.Mutex
	connectionServices        [2]connectionService
	connectionFlights         [2]*connectionFlight
	connectionRevision        uint64
	connectionRecoveryActive  bool
	connectionTasks           int
	detailRefreshMu           sync.Mutex
	mu                        sync.RWMutex
	htmlTemplate              *template.Template
	loginTemplate             *template.Template
	onboardingTemplate        *template.Template
	backupInProgress          map[string]bool // 跟踪正在备份的设备
	checkInProgress           map[string]bool // 跟踪正在检查备份条件的设备
	deviceOperationStates     map[string]deviceOperationState
	backupProgress            map[string]backupProgress
	backupProgressBroadcast   map[string]time.Time
	notificationManager       *notificationManager // 通知管理器
	notificationConfigStore   *notificationConfigStore
	secretStore               secretStore // 备份密码加密存储（缺密钥时为降级 store；测试中可能为 nil）
	// cmdRunner 可注入的 exec runner（nil 时使用 defaultCmdRunner）；测试时注入 mock
	cmdRunner cmdRunner
	// streamCmdRunner 用于长时间/大输出命令；nil 时使用真实流式 exec。
	streamCmdRunner streamCmdRunner
	// backupCommand 是外部备份工具的构造边界；nil 时执行固定路径的真实工具。
	backupCommand func(context.Context, string, ...string) *execCmd
	// reachProbe 可注入的 IP 可达性探测（nil 时使用真实 wifiReachable）；测试时注入避免真实拨号
	reachProbe func(ip string) (bool, string)
	// networkIPLookup 测试可注入 netmuxd 的地址快照；生产读取 ListDevices。
	networkIPLookup      func(context.Context) (map[string]string, error)
	networkRecoveryMu    sync.Mutex                      // 单次恢复循环串行，网络 I/O 不占用 app.mu
	networkRecovery      map[string]networkRecoveryState // app.mu 保护；仅内存缓存
	networkRegistrations map[string]bool                 // app.mu 保护；同一设备禁止重复注册
	// muxProcFactory 可注入的进程工厂（nil 时使用真实 exec）；测试时注入 fake
	muxProcFactory func(ctx context.Context, name string, args ...string) muxProcess
	// powerAssertionStarter 启动 Wi-Fi WirelessSync assertion helper；测试时注入 fake。
	powerAssertionStarter       powerAssertionStarter
	powerAssertionRenewInterval time.Duration
	// watchdogBaseDelay 看门狗退避基数（0 时使用 watchdogInitDelay）；测试时调小以加速
	watchdogBaseDelay time.Duration
	// hub SSE 广播总线；lastSnapshotJSON 上次广播的快照（用于 diff，仅变化时推）
	hub                        *eventHub
	lastSnapshotJSON           string
	deviceRefreshGeneration    uint64
	backupSem                  *backupSem // 全局备份并发闸：限制同时运行的 idevicebackup2 数量
	activeDeviceCommands       map[string]*activeDeviceCommand
	deviceOfflineSince         map[string]time.Time
	devicePresenceMissingSince map[string]time.Time
	deviceRemovalPending       map[string]bool
	// manifestRows 可注入，生产默认使用 sqlite3 流式读取 Manifest.db。
	manifestRows backupManifestRowStreamer
}

// NewApp 创建新的应用实例
func newApplication() *application {
	return newApplicationWithRuntime(context.Background(), defaultRuntimeConfig())
}

// NewAppWithRuntime 构造使用已校验配置和明确根生命周期的应用实例。
func newApplicationWithRuntime(ctx context.Context, cfg runtimeConfig) *application {
	if ctx == nil {
		ctx = context.Background()
	}
	paths := newAppPaths(cfg)
	return &application{
		devices:                      make(map[string]*device),
		configs:                      make(map[string]*backupConfig),
		rootCtx:                      ctx,
		runtimeConfig:                cfg,
		enableExperimentalOperations: cfg.EnableExperimentalOperations,
		paths:                        paths,
		configStore:                  newBackupConfigStore(paths.BackupConfigFile, []string{paths.BackupsRoot}),
		notificationConfigStore:      newNotificationConfigStore(paths.NotificationConfigFile, nil),
		authManager:                  newBootstrapAuthManager(cfg),
		csrfManager:                  newEphemeralCSRFManager(),
		sseLimiter:                   newSSELimiter(defaultSSEGlobalLimit, defaultSSEPerIPLimit),
		backupInProgress:             make(map[string]bool),
		checkInProgress:              make(map[string]bool),
		deviceOperationStates:        make(map[string]deviceOperationState),
		backupProgress:               make(map[string]backupProgress),
		backupProgressBroadcast:      make(map[string]time.Time),
		backupSem:                    newBackupSem(cfg.MaxHeavyJobs),
		activeDeviceCommands:         make(map[string]*activeDeviceCommand),
		deviceOfflineSince:           make(map[string]time.Time),
		devicePresenceMissingSince:   make(map[string]time.Time),
		deviceRemovalPending:         make(map[string]bool),
	}
}

func cloneDevice(device *device) *device {
	if device == nil {
		return nil
	}
	copy := *device
	return &copy
}

// devicesSnapshot 返回完全由调用方拥有的设备快照，禁止泄露内部可变指针。
func (app *application) devicesSnapshot() map[string]*device {
	app.mu.RLock()
	defer app.mu.RUnlock()
	snapshot := make(map[string]*device, len(app.devices))
	for udid, device := range app.devices {
		snapshot[udid] = cloneDevice(device)
	}
	return snapshot
}

func cloneBackupConfig(config *backupConfig) *backupConfig {
	if config == nil {
		return nil
	}
	copy := *config
	return &copy
}

func (app *application) notificationManagerSnapshot() *notificationManager {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.notificationManager
}

func (app *application) replaceNotificationManager(manager *notificationManager) *notificationManager {
	app.mu.Lock()
	defer app.mu.Unlock()
	old := app.notificationManager
	app.notificationManager = manager
	return old
}

// RefreshDevicesStatus 轻量级设备状态刷新（不执行配对操作）
// defaultBackupConfig 返回新设备的默认备份配置（唯一来源，避免多处漂移）。
func (app *application) defaultBackupConfig(udid, name string) *backupConfig {
	return &backupConfig{
		UDID:              udid,
		Name:              name,
		StartTime:         "18:00",
		EndTime:           "06:00",
		BackupInterval:    24,
		MinBatteryLevel:   20,
		OnlyWhenCharging:  true,
		BackupDirectory:   app.paths.BackupsRoot,
		AutoBackupEnabled: false,
		LastBackup:        time.Time{},
	}
}

// applyBackupConfigDefaults 补齐旧版或部分配置缺少的必填字段。
func (app *application) applyBackupConfigDefaults(cfg *backupConfig) {
	defaults := app.defaultBackupConfig(cfg.UDID, cfg.Name)
	if cfg.StartTime == "" {
		cfg.StartTime = defaults.StartTime
	}
	if cfg.EndTime == "" {
		cfg.EndTime = defaults.EndTime
	}
	if cfg.BackupInterval == 0 {
		cfg.BackupInterval = defaults.BackupInterval
	}
	if cfg.BackupDirectory == "" {
		cfg.BackupDirectory = defaults.BackupDirectory
	}
}

// ensureConfigUnsafe 设备无配置时建默认配置，返回是否新建（幂等，不覆盖已有）。
// 调用者须持有 app.mu（写锁）。设备一被发现就应有配置，否则「立即备份」会报「没有配置」。
func (app *application) ensureConfigUnsafe(udid, name string) bool {
	if _, exists := app.configs[udid]; exists {
		return false
	}
	app.configs[udid] = app.defaultBackupConfig(udid, name)
	return true
}
