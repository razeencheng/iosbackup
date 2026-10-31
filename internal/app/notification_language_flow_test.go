package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type notificationLanguageRecorder struct {
	messages chan notificationMessage
}

func (recorder *notificationLanguageRecorder) Send(message *notificationMessage) error {
	recorder.messages <- cloneNotificationMessage(*message)
	return nil
}

func (*notificationLanguageRecorder) GetName() string { return "language_capture" }
func (*notificationLanguageRecorder) IsEnabled() bool { return true }
func (*notificationLanguageRecorder) Validate() error { return nil }

func TestNotificationLanguageSavedRouteReplacesManagerAndControlsDelivery(t *testing.T) {
	app := newLoginTestApp(t)
	path := filepath.Join(t.TempDir(), "notifications.json")
	app.notificationConfigStore = newNotificationConfigStore(path, nil)
	app.replaceNotificationManager(newNotificationManager())
	t.Cleanup(func() { app.notificationManagerSnapshot().Close() })
	handler := app.setupRoutes()
	const userName = "我的 ${title} iPhone <&>"
	const diagnostics = "原始诊断 /backups/家庭 ${reason}"
	const custom = "用户模板 ${title}\n${content}\n${diagnostics}"
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			browserLanguage := "zh-CN"
			if language == "zh-CN" {
				browserLanguage = "en"
			}
			request := func(path string, body any) *httptest.ResponseRecorder {
				t.Helper()
				data, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
				req.SetBasicAuth("iosbackup", "correct-password")
				req.Header.Set(csrfHeader, app.csrfManager.Token())
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(&http.Cookie{Name: "iosbk_lang", Value: browserLanguage})
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				if rr.Code != http.StatusOK || rr.Header().Get("Content-Language") != browserLanguage {
					t.Fatalf("请求失败或响应语言错误: %d %v %s", rr.Code, rr.Header(), rr.Body.String())
				}
				return rr
			}
			previous := app.notificationManagerSnapshot()
			config := notificationConfig{Enabled: true, Language: language, NotificationTemplates: map[string]string{string(notificationBackupFailed): "", string(notificationSystemError): custom}}
			request("/api/notifications/config", config)
			loaded, err := newNotificationConfigStore(path, nil).Load()
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Language != language || loaded.NotificationTemplates[string(notificationBackupFailed)] != "" || loaded.NotificationTemplates[string(notificationSystemError)] != custom {
				t.Fatalf("保存改变了语言或模板: %+v", loaded)
			}
			current := app.notificationManagerSnapshot()
			if current == nil || current == previous {
				t.Fatal("保存后没有发布新的通知管理器")
			}
			// 仅将最后的外发接口替换为记录器；保存、加载、管理器和测试路由仍走生产实现。
			recorder := &notificationLanguageRecorder{messages: make(chan notificationMessage, 2)}
			if err := current.AddNotifier(recorder); err != nil {
				t.Fatal(err)
			}
			current.SetRules(map[string][]string{string(notificationBackupFailed): {recorder.GetName()}, string(notificationSystemError): {recorder.GetName()}})
			response := request("/api/notifications/test", map[string]string{"message_type": "backup_failed"})
			assertNotificationDeliveryCounts(t, response, 1, 1, 0)
			var testMessage notificationMessage
			select {
			case testMessage = <-recorder.messages:
			case <-time.After(time.Second):
				t.Fatal("测试路由没有发送消息")
			}
			wantTitle, wantBody, wantDevice, wantReason := "🧪 Test notification: ❌ Backup failed", "This is a test notification to verify notification settings.", "Test device", "Simulated failure for notification testing"
			if language == "zh-CN" {
				wantTitle, wantBody, wantDevice, wantReason = "🧪 测试通知: ❌ 设备备份失败", "这是一条测试通知，用于验证通知配置。", "测试设备", "用于测试通知的模拟故障"
			}
			if testMessage.Locale != language || testMessage.Title != wantTitle || testMessage.Content != wantBody || testMessage.DeviceName != wantDevice || testMessage.DeviceUDID != "test-device" || testMessage.Type != notificationBackupFailed || testMessage.Level != notificationLevelError || testMessage.EventCode != "backup_failed" || testMessage.ExtraFields["reason"] != wantReason {
				t.Fatalf("测试发送未采用保存的实例语言: %+v", testMessage)
			}
			app.notifySystemEvent("pairing_required", map[string]string{"device_name": userName, "device_udid": "PHONE", "diagnostics": diagnostics})
			var background notificationMessage
			select {
			case background = <-recorder.messages:
			case <-time.After(time.Second):
				t.Fatal("后台消息没有送达新管理器")
			}
			wantTitle = "⚠️ Device needs pairing: " + userName
			wantBody = "Automatic backup checks for " + userName + " repeatedly failed pairing validation. Connect the device over USB, unlock it and confirm Trust This Computer, then retry."
			if language == "zh-CN" {
				wantTitle = "⚠️ 设备需要重新配对: " + userName
				wantBody = "设备 " + userName + " 的自动备份检查持续无法通过配对校验。请通过 USB 连接设备，解锁并确认“信任此电脑”，然后重试。"
			}
			wantContent := "用户模板 " + wantTitle + "\n" + wantBody + "\n" + diagnostics
			if background.Locale != language || background.Title != wantTitle || background.Content != wantContent || background.DeviceName != userName || background.DeviceUDID != "PHONE" || background.Type != notificationSystemError || background.Level != notificationLevelError || background.EventCode != "pairing_required" || background.ExtraFields["diagnostics"] != diagnostics {
				t.Fatalf("后台发送未采用保存的语言/模板，或用户原文改变: %+v", background)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if result := previous.SendAndWait(ctx, previous.NewTestMessage(notificationBackupFailed)); result.Attempted != 0 {
				t.Fatalf("旧管理器仍接受发送: %+v", result)
			}
		})
	}
}
