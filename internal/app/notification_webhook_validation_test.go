package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"iosbackup/internal/i18n"
)

func TestWebhookSecretValidationConsistentAcrossPreviewSaveAndHTTP(t *testing.T) {
	const name = "Family {reason} 通知"
	for _, test := range []struct {
		name, url, code string
		clear           bool
	}{
		{"missing replacement", "", "api.notification_replacement_secret_required", false},
		{"conflict without URL", "", "api.notification_secret_actions_conflict", true},
		{"conflict with URL", "https://example.com/never-disclose-secret", "api.notification_secret_actions_conflict", true},
	} {
		for _, locale := range []string{"en", "zh-CN"} {
			for _, method := range []string{"preview", "save", "http"} {
				t.Run(test.name+"/"+locale+"/"+method, func(t *testing.T) {
					store := newNotificationConfigStore(filepath.Join(t.TempDir(), "notifications.json"), nil)
					config := notificationConfig{WebhookConfigs: []webhookConfig{
						{Name: "valid"},
						{Name: name, Enabled: true, ReplaceSecret: true, ClearSecret: test.clear, URL: test.url, Headers: map[string]string{"Authorization": "never-disclose-secret"}},
					}}
					rr := httptest.NewRecorder()
					if method == "http" {
						app := newLoginTestApp(t)
						app.notificationConfigStore = store
						body, err := json.Marshal(config)
						if err != nil {
							t.Fatal(err)
						}
						req := httptest.NewRequest(http.MethodPost, "/api/notifications/config", bytes.NewReader(body))
						req.SetBasicAuth("iosbackup", "correct-password")
						req.Header.Set(csrfHeader, app.csrfManager.Token())
						req.Header.Set("Content-Type", "application/json")
						req.AddCookie(&http.Cookie{Name: "iosbk_lang", Value: locale})
						app.setupRoutes().ServeHTTP(rr, req)
					} else {
						var err error
						if method == "preview" {
							_, err = store.Preview(&config)
						} else {
							err = store.Save(&config)
						}
						if err == nil {
							t.Fatal("无效的凭据更新必须失败")
						}
						rr.Header().Set("Content-Language", locale)
						writeOperationError(rr, http.StatusBadRequest, err, i18n.NotificationConfigInvalid)
					}
					if rr.Code != http.StatusBadRequest || rr.Header().Get("Content-Language") != locale {
						t.Fatalf("校验状态或语言错误: %d %v %s", rr.Code, rr.Header(), rr.Body.String())
					}
					var payload map[string]any
					if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					if payload["code"] != test.code || payload["message_code"] != test.code || !reflect.DeepEqual(payload["params"], map[string]any{}) {
						t.Fatalf("预览、保存和 HTTP 必须保留同一原因码及参数: %s", rr.Body.String())
					}
					wantContext := map[string]any{"kind": "notification_channel", "channel": "Webhook", "name": name}
					if !reflect.DeepEqual(payload["message_context"], wantContext) {
						t.Fatalf("渠道上下文改变: %s", rr.Body.String())
					}
					want := i18n.Text(locale, i18n.NotificationChannelContext, map[string]any{"channel": "Webhook", "name": name, "reason": i18n.Text(locale, test.code, nil)})
					message, _ := payload["error"].(string)
					if message != want || strings.Count(message, name) != 1 || strings.Contains(rr.Body.String(), "never-disclose-secret") {
						t.Fatalf("渠道名称应只出现一次且不得解释用户内容或泄露凭据: %s", rr.Body.String())
					}
				})
			}
		}
	}
}

func TestWebhookSecretUpdatesPreserveURLAndHeaders(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	const originalURL = "https://example.com/original-fixture"
	const replacementURL = "https://example.com/replacement-fixture"
	originalHeaders := map[string]string{"Authorization": "original-fixture"}
	replacementHeaders := map[string]string{"X-Token": "replacement-fixture"}
	for _, test := range []struct {
		name, url, wantURL   string
		headers, wantHeaders map[string]string
		replace, clear       bool
	}{
		{"initial replacement", originalURL, originalURL, originalHeaders, originalHeaders, true, false},
		{"omitted credentials", "", originalURL, nil, originalHeaders, false, false},
		{"new replacement", replacementURL, replacementURL, replacementHeaders, replacementHeaders, true, false},
		{"explicit clear", "", "", nil, nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := store.Public()
			if err != nil {
				t.Fatal(err)
			}
			config.WebhookConfigs = []webhookConfig{{Name: "Family webhook", URL: test.url, Headers: test.headers, ReplaceSecret: test.replace, ClearSecret: test.clear}}
			preview, err := store.Preview(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(config); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			for method, result := range map[string]*notificationConfig{"preview": preview, "saved": loaded} {
				got := result.WebhookConfigs[0]
				if got.URL != test.wantURL || !reflect.DeepEqual(got.Headers, test.wantHeaders) || got.SecretConfigured != (test.wantURL != "") {
					t.Fatalf("%s 的 URL、请求头或凭据状态改变: %+v", method, got)
				}
			}
		})
	}
}
