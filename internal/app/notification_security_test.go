package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type blockingNotifier struct {
	name    string
	blocked <-chan struct{}
}

func (n *blockingNotifier) Send(*notificationMessage) error { <-n.blocked; return nil }
func (n *blockingNotifier) GetName() string                 { return n.name }
func (n *blockingNotifier) IsEnabled() bool                 { return true }
func (n *blockingNotifier) Validate() error                 { return nil }

func TestNotificationQueueIsBounded(t *testing.T) {
	blocked := make(chan struct{})
	manager := newNotificationManagerWithLimits(4, 100)
	defer manager.Close()
	if err := manager.AddNotifier(&blockingNotifier{name: "blocked", blocked: blocked}); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{string(notificationBackupSuccess): {"blocked"}})
	before := runtime.NumGoroutine()
	for i := 0; i < 1000; i++ {
		manager.Send(&notificationMessage{Type: notificationBackupSuccess, Title: "bounded"})
	}
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if delta := after - before; delta > 4 {
		t.Fatalf("1000 个阻塞通知最多只能占用 worker 数量的 goroutine，新增 %d", delta)
	}
	if manager.Dropped() == 0 {
		t.Fatal("队列满时必须累计 dropped 指标")
	}
	close(blocked)
}

func TestNotificationQueueLimitsClampNonPositiveValues(t *testing.T) {
	blocked := make(chan struct{})
	manager := newNotificationManagerWithLimits(0, 0)
	defer manager.Close()
	if err := manager.AddNotifier(&blockingNotifier{name: "blocked", blocked: blocked}); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{string(notificationBackupSuccess): {"blocked"}})
	manager.Send(&notificationMessage{Type: notificationBackupSuccess})
	time.Sleep(20 * time.Millisecond)
	manager.Send(&notificationMessage{Type: notificationBackupSuccess})
	manager.Send(&notificationMessage{Type: notificationBackupSuccess})
	if manager.Dropped() == 0 {
		close(blocked)
		t.Fatal("非正 worker/queue 限制必须与旧行为一致地收敛为 1")
	}
	close(blocked)
}

func newNotificationSecurityStore(t *testing.T) (*notificationConfigStore, *aesSecretStore, string) {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	secretStore, err := newAESSecretStore(key, filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notification_configs.json")
	return newNotificationConfigStore(path, secretStore), secretStore, path
}

func secretNotificationConfig() *notificationConfig {
	return &notificationConfig{
		Enabled: true,
		TelegramConfigs: []telegramConfig{{
			Name: "telegram", BotToken: "tg-super-secret", ChatID: "chat", Enabled: true, ReplaceSecret: true,
		}},
		EmailConfigs: []emailConfig{{
			Name: "email", SMTPHost: "smtp.example.com", SMTPPort: 587, Username: "user", Password: "smtp-super-secret", From: "a@example.com", To: "b@example.com", Enabled: true, ReplaceSecret: true,
		}},
		WecomConfigs: []wecomConfig{{
			Name: "wecom", WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=wecom-secret", Enabled: true, ReplaceSecret: true,
		}},
		BarkConfigs: []barkConfig{{
			Name: "bark", ServerURL: "https://api.day.app", DeviceKey: "bark-device-secret", Enabled: true, ReplaceSecret: true,
		}},
		WebhookConfigs: []webhookConfig{{
			Name: "webhook", URL: "https://hooks.example.com/secret-path", Method: "POST", Headers: map[string]string{"Authorization": "Bearer header-secret"}, Enabled: true, ReplaceSecret: true,
		}},
		NotificationRules: defaultNotificationRules(),
	}
}

func TestNotificationConfigGETNeverReturnsSecrets(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	if err := store.Save(secretNotificationConfig()); err != nil {
		t.Fatal(err)
	}
	public, err := store.Public()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(public)
	for _, secret := range []string{"tg-super-secret", "smtp-super-secret", "wecom-secret", "bark-device-secret", "secret-path", "header-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("GET DTO 泄露秘密 %q: %s", secret, data)
		}
	}
	if !public.TelegramConfigs[0].SecretConfigured || !public.EmailConfigs[0].SecretConfigured || !public.WecomConfigs[0].SecretConfigured || !public.BarkConfigs[0].SecretConfigured || !public.WebhookConfigs[0].SecretConfigured {
		t.Fatal("GET 应仅返回 configured 状态")
	}
}

func TestNotificationConfigOmittedSecretPreservesExisting(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	if err := store.Save(secretNotificationConfig()); err != nil {
		t.Fatal(err)
	}
	update, err := store.Public()
	if err != nil {
		t.Fatal(err)
	}
	update.TelegramConfigs[0].ChatID = "new-chat"
	if err := store.Save(update); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TelegramConfigs[0].BotToken != "tg-super-secret" || loaded.TelegramConfigs[0].ChatID != "new-chat" {
		t.Fatal("省略 secret 时必须保留旧值，同时更新非秘密字段")
	}
}

func TestNotificationConfigDefaultsTemplates(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.NotificationTemplates[string(notificationBackupSuccess)] != "设备 ${device_name} 备份成功完成" {
		t.Fatalf("成功模板默认值不正确: %+v", config.NotificationTemplates)
	}
	if config.NotificationTemplates[string(notificationBackupFailed)] == "" {
		t.Fatal("失败模板默认值不应为空")
	}
}

func TestNotificationConfigRejectsUnknownTemplateVariable(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	config := defaultNotificationConfig()
	config.NotificationTemplates = map[string]string{
		string(notificationBackupSuccess): "${not_defined}",
	}
	if err := store.Save(config); err == nil {
		t.Fatal("未知模板变量应该拒绝保存")
	}
}

func TestNotificationSecretsAreEncryptedAtRest(t *testing.T) {
	store, _, configPath := newNotificationSecurityStore(t)
	if err := store.Save(secretNotificationConfig()); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(filepath.Dir(configPath), "secrets.enc")
	for _, path := range []string{configPath, secretPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"tg-super-secret", "smtp-super-secret", "wecom-secret", "bark-device-secret", "secret-path", "header-secret"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("文件 %s 含明文秘密 %q", path, secret)
			}
		}
	}
}

func TestNotificationLegacySecretsMigrateOnlyAfterEncryption(t *testing.T) {
	store, _, path := newNotificationSecurityStore(t)
	legacy, _ := json.Marshal(secretNotificationConfig())
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TelegramConfigs[0].BotToken != "tg-super-secret" {
		t.Fatal("迁移后应可解密原秘密")
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("tg-super-secret")) {
		t.Fatal("成功加密后必须从通知配置清除明文")
	}
}

func TestNotificationLegacyMigrationFailureKeepsPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notification_configs.json")
	legacy, _ := json.Marshal(secretNotificationConfig())
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	store := newNotificationConfigStore(path, newUnavailableSecretStore(filepath.Join(dir, "secrets.enc")))
	if _, err := store.Load(); err == nil {
		t.Fatal("没有加密密钥时迁移必须失败")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("tg-super-secret")) {
		t.Fatal("加密写入失败前不得清除旧明文，避免秘密丢失")
	}
}

func TestNotificationSecretExplicitClear(t *testing.T) {
	store, _, _ := newNotificationSecurityStore(t)
	if err := store.Save(secretNotificationConfig()); err != nil {
		t.Fatal(err)
	}
	update, _ := store.Public()
	update.TelegramConfigs[0].Enabled = false
	update.TelegramConfigs[0].ClearSecret = true
	if err := store.Save(update); err != nil {
		t.Fatal(err)
	}
	public, _ := store.Public()
	if public.TelegramConfigs[0].SecretConfigured {
		t.Fatal("clear_secret 后 configured 必须为 false")
	}
}

func TestWebhookRejectsUnsafeTargetsAndHeaders(t *testing.T) {
	tests := []webhookConfig{
		{Name: "loopback", URL: "http://127.0.0.1/hook", Method: "POST", Enabled: true},
		{Name: "metadata", URL: "http://169.254.169.254/latest", Method: "POST", Enabled: true},
		{Name: "method", URL: "https://example.com", Method: "TRACE", Enabled: true},
		{Name: "header", URL: "https://example.com", Method: "POST", Headers: map[string]string{"Host": "evil"}, Enabled: true},
	}
	for _, cfg := range tests {
		if err := newWebhookNotifier(cfg).Validate(); err == nil {
			t.Fatalf("应拒绝不安全 webhook: %+v", cfg)
		}
	}
}

func TestNotificationConfigRouteUsesPublicDTO(t *testing.T) {
	store, secretStore, _ := newNotificationSecurityStore(t)
	if err := store.Save(secretNotificationConfig()); err != nil {
		t.Fatal(err)
	}
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.notificationConfigStore = store
	app.secretStore = secretStore
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/config", nil)
	rr := httptest.NewRecorder()
	app.handleNotificationConfig(rr, req)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "super-secret") || strings.Contains(rr.Body.String(), "secret-path") {
		t.Fatalf("通知配置 GET 泄密或失败: %d %s", rr.Code, rr.Body.String())
	}
}
