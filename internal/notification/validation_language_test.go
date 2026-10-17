package notification

import (
	"errors"
	"strings"
	"testing"

	"iosbackup/internal/i18n"
)

func TestKnownNotificationValidationKeepsLocalizedActionAndParameters(t *testing.T) {
	for _, test := range []struct {
		name, code, context string
		validate            func() error
	}{
		{"template variable", "api.notification_template_variable", "unsupported", func() error { return ValidateTemplates(map[string]string{"backup_failed": "${unsupported}"}) }},
		{"missing rule channel", "api.notification_rule_target_missing", "Telegram_missing", func() error { return ValidateRules(map[string][]string{"backup_failed": {"Telegram_missing"}}, nil) }},
		{"duplicate rule channel", "api.notification_rule_target_duplicate", "Telegram_main", func() error {
			return ValidateRules(map[string][]string{"backup_failed": {"Telegram_main", "Telegram_main"}}, []map[string]interface{}{{"name": "Telegram_main"}})
		}},
		{"telegram chat", "api.notification_field_required", "chat_id", func() error { return NewTelegramNotifier(TelegramConfig{BotToken: "secret-never-in-error"}).Validate() }},
		{"smtp port", "api.notification_smtp_port", "smtp_port", func() error {
			return NewEmailNotifier(EmailConfig{SMTPHost: "smtp.example.com", SMTPPort: 70000, Password: "secret-never-in-error"}).Validate()
		}},
		{"wecom https", "api.notification_url_https", "HTTPS", func() error {
			return NewWecomNotifier(WecomConfig{WebhookURL: "http://qyapi.weixin.qq.com/?key=secret-never-in-error"}).Validate()
		}},
		{"bark key", "api.notification_bark_key_invalid", "device_key", func() error {
			return NewBarkNotifier(BarkConfig{ServerURL: "https://api.day.app", DeviceKey: "secret-never-in-error\n"}).Validate()
		}},
		{"webhook method", "api.notification_webhook_method", "method", func() error {
			return NewWebhookNotifier(WebhookConfig{URL: "https://example.com/secret-never-in-error", Method: "GET"}).Validate()
		}},
		{"webhook headers", "api.notification_webhook_header", "Authorization", func() error {
			return validateWebhookHeaders(map[string]string{"Authorization": "secret-never-in-error\n"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.validate()
			var typed *i18n.Error
			if !errors.As(err, &typed) || typed.Code != test.code {
				t.Fatalf("known validation is not typed: %v", err)
			}
			for _, locale := range []string{"en", "zh-CN"} {
				text := i18n.Text(locale, typed.Code, typed.Params)
				if !strings.Contains(text, test.context) || strings.Contains(text, "secret-never-in-error") || text == i18n.Text(locale, "api.operation_failed", nil) {
					t.Fatalf("%s action lost or secret leaked: %q", locale, text)
				}
			}
		})
	}
}

func TestManagerPreservesTypedAdapterValidation(t *testing.T) {
	manager := NewManager(Options{Logf: func(string, ...any) {}})
	defer manager.Close()
	err := manager.AddNotifier(NewTelegramNotifier(TelegramConfig{Name: "main", BotToken: "configured"}))
	var typed *i18n.Error
	if !errors.As(err, &typed) || typed.Code != "api.notification_field_required" {
		t.Fatalf("manager erased typed validation: %v", err)
	}
}
