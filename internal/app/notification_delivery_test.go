package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNotificationConfigRejectsInvalidEnabledNotifierWithoutPersisting(t *testing.T) {
	store, secretStore, configPath := newNotificationSecurityStore(t)
	app := newApplication()
	app.notificationConfigStore = store
	app.secretStore = secretStore

	body := `{
		"enabled": true,
		"telegram_configs": [],
		"email_configs": [],
		"wecom_configs": [],
		"webhook_configs": [{
			"name": "unsafe",
			"url": "http://127.0.0.1:1/hook",
			"method": "POST",
			"headers": {},
			"enabled": true,
			"replace_secret": true
		}],
		"notification_rules": {"backup_success": ["Webhook_unsafe"]}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/config", strings.NewReader(body))
	rr := httptest.NewRecorder()
	app.handleNotificationConfig(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法且启用的 webhook 应 400，得 %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("验证失败不得写通知配置，stat err=%v", err)
	}
	public, err := store.Public()
	if err != nil {
		t.Fatal(err)
	}
	if public.Enabled || len(public.WebhookConfigs) != 0 {
		t.Fatalf("验证失败不得发布配置: %+v", public)
	}
}

func TestNotificationTestReportsNoEligibleNotifier(t *testing.T) {
	app := newApplication()
	manager := newNotificationManager()
	defer manager.Close()
	manager.SetNotificationRules(map[string][]string{
		string(notificationBackupSuccess): {"Webhook_missing"},
	})
	app.replaceNotificationManager(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", strings.NewReader(`{"message_type":"backup_success"}`))
	rr := httptest.NewRecorder()
	app.handleNotificationTest(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("没有可用通知器不能报成功，应 409，得 %d: %s", rr.Code, rr.Body.String())
	}
	assertNotificationDeliveryCounts(t, rr, 0, 0, 0)
}

func TestNotificationTestWaitsForDeliveryAndReportsFailure(t *testing.T) {
	app := newApplication()
	manager := newNotificationManager()
	defer manager.Close()
	failing := &MockNotifier{name: "test_notifier", enabled: true, shouldError: true}
	if err := manager.AddNotifier(failing); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{
		string(notificationBackupSuccess): {"test_notifier"},
	})
	app.replaceNotificationManager(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", strings.NewReader(`{"message_type":"backup_success"}`))
	rr := httptest.NewRecorder()
	app.handleNotificationTest(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("实际发送全部失败应 502，得 %d: %s", rr.Code, rr.Body.String())
	}
	assertNotificationDeliveryCounts(t, rr, 1, 0, 1)
	if failing.Count() != 1 {
		t.Fatalf("测试接口返回前应完成一次发送，实际 %d", failing.Count())
	}
}

func TestNotificationSendAndWaitReportsSuccess(t *testing.T) {
	manager := newNotificationManager()
	defer manager.Close()
	notifier := &MockNotifier{name: "ok", enabled: true}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{
		string(notificationBackupSuccess): {"ok"},
	})
	result := manager.SendAndWait(context.Background(), &notificationMessage{Type: notificationBackupSuccess, Title: "test"})
	if result.Attempted != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("发送结果不准确: %+v", result)
	}
}

func assertNotificationDeliveryCounts(t *testing.T, rr *httptest.ResponseRecorder, attempted, succeeded, failed int) {
	t.Helper()
	var body struct {
		Attempted int `json:"attempted"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v: %s", err, rr.Body.String())
	}
	if body.Attempted != attempted || body.Succeeded != succeeded || body.Failed != failed {
		t.Fatalf("发送计数错误: %+v", body)
	}
}
