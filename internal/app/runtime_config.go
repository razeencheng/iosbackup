package app

import "iosbackup/internal/config"

const (
	wifiBackendNetmuxd  = config.WiFiBackendNetmuxd
	wifiBackendUSBMuxd2 = config.WiFiBackendUSBMuxD2
)

// RuntimeConfig 是进程启动时一次性解析并校验的运行配置。
// 此别名在应用迁移至 internal/app 期间保持现有调用方兼容。
type runtimeConfig = config.Runtime

// AppPaths 是从已校验 RuntimeConfig 派生的全部可写路径。
type appPaths = config.Paths

var (
	initialAppPaths = newAppPaths(defaultRuntimeConfig())
	dirConfigs      = initialAppPaths.ConfigsRoot
	dirBackups      = initialAppPaths.BackupsRoot
	dirBackupBase   = initialAppPaths.BackupBase
	configFile      = initialAppPaths.BackupConfigFile
)

func defaultRuntimeConfig() runtimeConfig {
	return config.Default()
}

func loadRuntimeConfig(getenv func(string) string) (runtimeConfig, error) {
	return config.Load(getenv)
}

func newAppPaths(cfg runtimeConfig) appPaths {
	return config.NewPaths(cfg)
}

// applyRuntimeConfig 只允许在 run 启动任何 goroutine 之前调用。
// 这些兼容变量将在持久化 store 迁移后逐步移除。
func applyRuntimeConfig(cfg runtimeConfig) appPaths {
	paths := config.NewPaths(cfg)
	dirConfigs = paths.ConfigsRoot
	dirBackups = paths.BackupsRoot
	dirBackupBase = paths.BackupBase
	configFile = paths.BackupConfigFile
	notificationConfigFile = paths.NotificationConfigFile
	return paths
}
