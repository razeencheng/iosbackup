package config

import "path/filepath"

// Paths 是从已校验 Runtime 派生的全部可写路径。
type Paths struct {
	ConfigsRoot            string
	BackupsRoot            string
	BackupBase             string
	BackupConfigFile       string
	NotificationConfigFile string
	SecretsFile            string
	AuthCredentialsFile    string
	CSRFSecretFile         string
}

// NewPaths 从运行配置派生全部可写路径。
func NewPaths(cfg Runtime) Paths {
	return Paths{
		ConfigsRoot:            cfg.ConfigsRoot,
		BackupsRoot:            cfg.BackupsRoot,
		BackupBase:             cfg.BackupsRoot,
		BackupConfigFile:       filepath.Join(cfg.ConfigsRoot, "backup_configs.json"),
		NotificationConfigFile: filepath.Join(cfg.ConfigsRoot, "notification_configs.json"),
		SecretsFile:            filepath.Join(cfg.ConfigsRoot, "secrets.enc"),
		AuthCredentialsFile:    filepath.Join(cfg.ConfigsRoot, "auth_credentials.json"),
		CSRFSecretFile:         filepath.Join(cfg.ConfigsRoot, "csrf_secret.json"),
	}
}
