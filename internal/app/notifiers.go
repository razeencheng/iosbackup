package app

import (
	"fmt"
	"net/netip"

	notificationpkg "iosbackup/internal/notification"
)

type telegramNotifier = notificationpkg.TelegramNotifier
type emailNotifier = notificationpkg.EmailNotifier
type wecomNotifier = notificationpkg.WecomNotifier
type barkNotifier = notificationpkg.BarkNotifier
type webhookNotifier = notificationpkg.WebhookNotifier

func newTelegramNotifier(config telegramConfig) *telegramNotifier {
	return notificationpkg.NewTelegramNotifier(config)
}

func newEmailNotifier(config emailConfig) *emailNotifier {
	return notificationpkg.NewEmailNotifier(config)
}

func newWecomNotifier(config wecomConfig) *wecomNotifier {
	return notificationpkg.NewWecomNotifier(config)
}

func newBarkNotifier(config barkConfig) *barkNotifier {
	return notificationpkg.NewBarkNotifier(config)
}

func newBarkNotifierWithAllowlist(config barkConfig, allowPrivate []netip.Prefix) *barkNotifier {
	return notificationpkg.NewBarkNotifierWithAllowlist(config, allowPrivate)
}

func newWebhookNotifier(config webhookConfig) *webhookNotifier {
	return notificationpkg.NewWebhookNotifier(config)
}

func newWebhookNotifierWithAllowlist(config webhookConfig, allowPrivate []netip.Prefix) *webhookNotifier {
	return notificationpkg.NewWebhookNotifierWithAllowlist(config, allowPrivate)
}

func initNotificationManager() (*notificationManager, error) {
	config, err := loadNotificationConfig()
	if err != nil {
		return nil, fmt.Errorf("加载通知配置失败: %v", err)
	}
	return newNotificationManagerFromConfig(config, nil)
}

func initNotificationManagerFromStore(store *notificationConfigStore, allowPrivate []netip.Prefix) (*notificationManager, error) {
	config, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("加载通知配置失败: %v", err)
	}
	return newNotificationManagerFromConfig(config, allowPrivate)
}

func newNotificationManagerFromConfig(config *notificationConfig, allowPrivate []netip.Prefix) (*notificationManager, error) {
	return notificationpkg.NewManagerFromConfig(config, allowPrivate, notificationManagerOptions(4, 100))
}

func validateNotificationRules(rules map[string][]string, notifiers []map[string]interface{}) error {
	return notificationpkg.ValidateRules(rules, notifiers)
}
