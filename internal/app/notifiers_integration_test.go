//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

const realNotificationConfirmation = "YES_I_KNOW_THIS_SENDS_MESSAGES"

type realNotificationResult struct {
	notifier string
	title    string
	err      error
}

func TestRealNotice(t *testing.T) {
	if os.Getenv("IOSBK_RUN_REAL_NOTIFICATIONS") != realNotificationConfirmation {
		t.Skip("set explicit confirmation to send real notifications")
	}

	configPath := os.Getenv("IOSBK_INTEGRATION_NOTIFICATION_CONFIG")
	if configPath == "" {
		t.Fatal("IOSBK_INTEGRATION_NOTIFICATION_CONFIG is required")
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read integration notification config: %v", err)
	}

	var config notificationConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatalf("decode integration notification config: %v", err)
	}
	if !config.Enabled {
		t.Fatal("integration notification config must be enabled")
	}

	notifiers, err := integrationNotifiers(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifiers) == 0 {
		t.Fatal("integration notification config has no enabled notifier")
	}

	messages := []*notificationMessage{
		{Type: notificationBackupStart, Level: notificationLevelInfo, Title: "✅ 设备备份开始", Content: "iOS Backup integration test", DeviceName: "integration-device", DeviceUDID: "integration-udid", Timestamp: nowBeijing()},
		{Type: notificationBackupSuccess, Level: notificationLevelInfo, Title: "✅ 设备备份完成", Content: "iOS Backup integration test", DeviceName: "integration-device", DeviceUDID: "integration-udid", Timestamp: nowBeijing()},
		{Type: notificationBackupFailed, Level: notificationLevelError, Title: "❌ 设备备份失败", Content: "iOS Backup integration test", DeviceName: "integration-device", DeviceUDID: "integration-udid", Timestamp: nowBeijing()},
		{Type: notificationDeviceOnline, Level: notificationLevelInfo, Title: "✅ 设备上线", Content: "iOS Backup integration test", DeviceName: "integration-device", DeviceUDID: "integration-udid", Timestamp: nowBeijing()},
		{Type: notificationDeviceOffline, Level: notificationLevelWarning, Title: "❌ 设备离线", Content: "iOS Backup integration test", DeviceName: "integration-device", DeviceUDID: "integration-udid", Timestamp: nowBeijing()},
		{Type: notificationSystemError, Level: notificationLevelError, Title: "⚠️ 系统错误", Content: "iOS Backup integration test", Timestamp: nowBeijing()},
	}

	results := make(chan realNotificationResult, len(notifiers)*len(messages))
	var wg sync.WaitGroup
	for _, notifier := range notifiers {
		for _, message := range messages {
			notifier := notifier
			messageCopy := *message
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- realNotificationResult{
					notifier: notifier.GetName(),
					title:    messageCopy.Title,
					err:      notifier.Send(&messageCopy),
				}
			}()
		}
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return
			}
			if result.err != nil {
				t.Errorf("%s send %q: %v", result.notifier, result.title, result.err)
			}
		case <-timeout.C:
			t.Fatal("real notification integration test timed out")
		}
	}
}

func integrationNotifiers(config notificationConfig) ([]notifier, error) {
	var notifiers []notifier
	for _, item := range config.TelegramConfigs {
		if item.Enabled {
			notifiers = append(notifiers, newTelegramNotifier(item))
		}
	}
	for _, item := range config.EmailConfigs {
		if item.Enabled {
			notifiers = append(notifiers, newEmailNotifier(item))
		}
	}
	for _, item := range config.WecomConfigs {
		if item.Enabled {
			notifiers = append(notifiers, newWecomNotifier(item))
		}
	}
	for _, item := range config.WebhookConfigs {
		if item.Enabled {
			notifiers = append(notifiers, newWebhookNotifier(item))
		}
	}
	for _, notifier := range notifiers {
		if err := notifier.Validate(); err != nil {
			return nil, fmt.Errorf("validate notifier %q: %w", notifier.GetName(), err)
		}
	}
	return notifiers, nil
}
