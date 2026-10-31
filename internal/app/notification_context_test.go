package app

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	notificationpkg "iosbackup/internal/notification"
)

func TestNotificationSecretValidationIdentifiesChannel(t *testing.T) {
	const name = "Family {reason} 邮箱"
	cases := []struct {
		channel string
		config  notificationConfig
	}{
		{"Email", notificationConfig{EmailConfigs: []emailConfig{{Name: "valid"}, {Name: name, ReplaceSecret: true}}}},
		{"Telegram", notificationConfig{TelegramConfigs: []telegramConfig{{Name: "valid"}, {Name: name, ReplaceSecret: true}}}},
		{"WeCom", notificationConfig{WecomConfigs: []wecomConfig{{Name: "valid"}, {Name: name, ReplaceSecret: true}}}},
		{"Bark", notificationConfig{BarkConfigs: []barkConfig{{Name: "valid"}, {Name: name, ReplaceSecret: true}}}},
		{"Webhook", notificationConfig{WebhookConfigs: []webhookConfig{{Name: "valid"}, {Name: name, ReplaceSecret: true}}}},
	}
	for _, tc := range cases {
		for _, method := range []string{"preview", "save"} {
			t.Run(tc.channel+"/"+method, func(t *testing.T) {
				store := newNotificationConfigStore(filepath.Join(t.TempDir(), "notifications.json"), nil)
				var err error
				if method == "preview" {
					_, err = store.Preview(&tc.config)
				} else {
					err = store.Save(&tc.config)
				}
				code := "api.notification_replacement_secret_required"
				assertNotificationErrorContext(t, err, code, tc.channel, name)
			})
		}
	}
}

func TestNotificationManagerValidationIdentifiesChannel(t *testing.T) {
	const name = "Family {reason} 邮箱"
	for _, tc := range []struct {
		channel, code string
		config        notificationConfig
	}{
		{"Email", "api.notification_smtp_port", notificationConfig{Enabled: true, EmailConfigs: []emailConfig{{Name: name, Enabled: true, SMTPHost: "smtp.example.com", SMTPPort: 70000, Password: "never-disclose-secret"}}}},
		{"Telegram", "api.notification_field_required", notificationConfig{Enabled: true, TelegramConfigs: []telegramConfig{{Name: name, Enabled: true, BotToken: "never-disclose-secret"}}}},
		{"WeCom", "api.notification_url_https", notificationConfig{Enabled: true, WecomConfigs: []wecomConfig{{Name: name, Enabled: true, WebhookURL: "http://qyapi.weixin.qq.com/?key=never-disclose-secret"}}}},
		{"Bark", "api.notification_bark_key_invalid", notificationConfig{Enabled: true, BarkConfigs: []barkConfig{{Name: name, Enabled: true, ServerURL: "https://api.day.app", DeviceKey: "never-disclose-secret\n"}}}},
		{"Webhook", "api.notification_webhook_method", notificationConfig{Enabled: true, WebhookConfigs: []webhookConfig{{Name: name, Enabled: true, URL: "https://example.com/never-disclose-secret", Method: "GET"}}}},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			manager, err := notificationpkg.NewManagerFromConfig(&tc.config, nil, notificationpkg.Options{Logf: func(string, ...any) {}})
			if manager != nil {
				manager.Close()
			}
			assertNotificationErrorContext(t, err, tc.code, tc.channel, name)
		})
	}
}

func assertNotificationErrorContext(t *testing.T, err error, code, channel, name string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, locale := range []string{"en", "zh-CN"} {
		rr := httptest.NewRecorder()
		rr.Header().Set("Content-Language", locale)
		payload := operationErrorPayload(rr, err, "api.notification_config_invalid")
		data, _ := json.Marshal(payload)
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		context, ok := decoded["message_context"].(map[string]any)
		if !ok || context["kind"] != "notification_channel" || context["channel"] != channel || context["name"] != name {
			t.Fatalf("channel context lost: %s", data)
		}
		if decoded["code"] != code || decoded["message_code"] != code {
			t.Fatalf("machine code changed: %s", data)
		}
		message, _ := decoded["error"].(string)
		if strings.Count(message, name) != 1 || !strings.Contains(message, channel) || strings.Contains(string(data), "never-disclose-secret") {
			t.Fatalf("context missing or secret leaked: %s", data)
		}
		if locale == "en" && !strings.Contains(message, "Notification channel") {
			t.Fatalf("English context: %s", data)
		}
		if locale == "zh-CN" && !strings.Contains(message, "通知渠道") {
			t.Fatalf("Chinese context: %s", data)
		}
	}
}
