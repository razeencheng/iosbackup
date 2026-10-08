package app

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"

	notificationpkg "iosbackup/internal/notification"
)

// 应用层保留原有名称，避免 HTTP、配置和备份调用方发生兼容性变化。
type notificationType = notificationpkg.Type
type notificationLevel = notificationpkg.Level
type notificationMessage = notificationpkg.Message
type notifier = notificationpkg.Notifier
type notificationManager = notificationpkg.Manager
type notificationDelivery = notificationpkg.Delivery
type notificationSendResult = notificationpkg.SendResult
type notificationConfig = notificationpkg.Config
type telegramConfig = notificationpkg.TelegramConfig
type emailConfig = notificationpkg.EmailConfig
type wecomConfig = notificationpkg.WecomConfig
type barkConfig = notificationpkg.BarkConfig
type webhookConfig = notificationpkg.WebhookConfig

const (
	notificationBackupStart   = notificationpkg.BackupStart
	notificationBackupSuccess = notificationpkg.BackupSuccess
	notificationBackupFailed  = notificationpkg.BackupFailed
	notificationDeviceOnline  = notificationpkg.DeviceOnline
	notificationDeviceOffline = notificationpkg.DeviceOffline
	notificationSystemError   = notificationpkg.SystemError

	notificationLevelInfo    = notificationpkg.LevelInfo
	notificationLevelWarning = notificationpkg.LevelWarning
	notificationLevelError   = notificationpkg.LevelError
)

// 业务调用方明确决定是否告警，日志级别本身不再触发外部通知。
func (app *application) notifySystemError(title, content string) {
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendSystemError(title, content)
	}
}

func notificationManagerOptions(workerCount, queueSize int) notificationpkg.Options {
	return notificationpkg.Options{
		Now:         nowBeijing,
		Logf:        log.Printf,
		WorkerCount: workerCount,
		QueueSize:   queueSize,
	}
}

func newNotificationManager() *notificationManager {
	return notificationpkg.NewManager(notificationManagerOptions(4, 100))
}

func newNotificationManagerWithLimits(workerCount, queueSize int) *notificationManager {
	options := notificationManagerOptions(workerCount, queueSize)
	// leaf 的零值表示“采用默认值”；此兼容入口的历史语义是非正数收敛为 1。
	if workerCount == 0 {
		options.WorkerCount = -1
	}
	if queueSize == 0 {
		options.QueueSize = -1
	}
	return notificationpkg.NewManager(options)
}

func cloneNotificationMessage(message notificationMessage) notificationMessage {
	return notificationpkg.CloneMessage(message)
}

func cloneNotificationRules(rules map[string][]string) map[string][]string {
	return notificationpkg.CloneRules(rules)
}

func defaultNotificationTemplates() map[string]string {
	return notificationpkg.DefaultTemplates()
}

func mergeNotificationTemplates(templates map[string]string) map[string]string {
	return notificationpkg.MergeTemplates(templates)
}

func validateNotificationTemplates(templates map[string]string) error {
	return notificationpkg.ValidateTemplates(templates)
}

// 通知配置文件路径仍由应用运行时目录决定。
var notificationConfigFile = dirConfigs + "/notification_configs.json"

func loadNotificationConfig() (*notificationConfig, error) {
	store, err := notificationStoreFromEnvironment()
	if err != nil {
		return nil, err
	}
	return store.Load()
}

func saveNotificationConfig(config *notificationConfig) error {
	store, err := notificationStoreFromEnvironment()
	if err != nil {
		return err
	}
	return store.Save(config)
}

func notificationStoreFromEnvironment() (*notificationConfigStore, error) {
	var secrets secretStore
	if encoded := os.Getenv(secretsEnvKey); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("%s 不是合法 base64: %w", secretsEnvKey, err)
		}
		store, err := newAESSecretStore(key, dirConfigs+"/secrets.enc")
		if err != nil {
			return nil, err
		}
		secrets = store
	}
	return newNotificationConfigStore(notificationConfigFile, secrets), nil
}
