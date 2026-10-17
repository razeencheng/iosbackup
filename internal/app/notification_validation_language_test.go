package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"iosbackup/internal/i18n"
)

func TestNotificationValidationPreservesActionableLanguageWithoutSecrets(t *testing.T) {
	for _, test := range []struct {
		name, code, en, zh string
		config             notificationConfig
	}{
		{"duplicate", "api.notification_name_duplicate", "already exists", "已存在", notificationConfig{TelegramConfigs: []telegramConfig{{Name: "main", BotToken: "do-not-display-secret"}, {Name: "main"}}}},
		{"missing secret", "api.notification_secret_required", "before enabling", "请先配置凭据", notificationConfig{TelegramConfigs: []telegramConfig{{Name: "main", Enabled: true}}}},
		{"conflicting actions", "api.notification_secret_actions_conflict", "together", "同时", notificationConfig{TelegramConfigs: []telegramConfig{{Name: "main", BotToken: "do-not-display-secret", ReplaceSecret: true, ClearSecret: true}}}},
	} {
		for _, method := range []string{"preview", "save"} {
			t.Run(test.name+"/"+method, func(t *testing.T) {
				store := newNotificationConfigStore(filepath.Join(t.TempDir(), "notifications.json"), nil)
				var err error
				if method == "preview" {
					_, err = store.Preview(&test.config)
				} else {
					err = store.Save(&test.config)
				}
				var translated *i18n.Error
				if !errors.As(err, &translated) || translated.Code != test.code {
					t.Fatalf("validation is not a specific typed error: %v", err)
				}
				for locale, word := range map[string]string{"en": test.en, "zh-CN": test.zh} {
					rr := httptest.NewRecorder()
					rr.Header().Set("Content-Language", locale)
					writeOperationError(rr, http.StatusBadRequest, err, "api.notification_config_invalid")
					var payload map[string]any
					if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					message, _ := payload["error"].(string)
					if payload["code"] != test.code || !strings.Contains(message, word) || strings.Contains(rr.Body.String(), "do-not-display-secret") {
						t.Fatalf("%s validation response: %s", locale, rr.Body.String())
					}
				}
			})
		}
	}
}
