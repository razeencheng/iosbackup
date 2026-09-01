package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

const notificationConfigSchemaVersion = 1

type notificationConfigEnvelope struct {
	SchemaVersion int                `json:"schema_version"`
	Config        notificationConfig `json:"config"`
}

type notificationConfigStore struct {
	mu      sync.Mutex
	path    string
	secrets secretStore
}

func newNotificationConfigStore(path string, secrets secretStore) *notificationConfigStore {
	return &notificationConfigStore{path: path, secrets: secrets}
}

func defaultNotificationRules() map[string][]string {
	return map[string][]string{
		string(notificationBackupStart): {}, string(notificationBackupSuccess): {},
		string(notificationBackupFailed): {}, string(notificationDeviceOnline): {},
		string(notificationDeviceOffline): {}, string(notificationSystemError): {},
	}
}

func defaultNotificationConfig() *notificationConfig {
	return &notificationConfig{
		TelegramConfigs: []telegramConfig{}, EmailConfigs: []emailConfig{},
		WecomConfigs: []wecomConfig{}, BarkConfigs: []barkConfig{}, WebhookConfigs: []webhookConfig{},
		NotificationRules:     defaultNotificationRules(),
		NotificationTemplates: defaultNotificationTemplates(),
	}
}

func (s *notificationConfigStore) Load() (*notificationConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.loadAndMigrateLocked()
	if err != nil {
		return nil, err
	}
	if err := s.hydrateLocked(cfg); err != nil {
		return nil, err
	}
	return cloneNotificationConfig(cfg), nil
}

func (s *notificationConfigStore) Public() (*notificationConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.loadAndMigrateLocked()
	if err != nil {
		return nil, err
	}
	sanitizeNotificationConfig(cfg)
	return cloneNotificationConfig(cfg), nil
}

// Preview 在不写配置文件或秘密存储的前提下，把请求与现有秘密合并成可运行配置。
// HTTP 保存路径先用它做完整 notifier 校验，避免“已落盘但实际没有通知器”的假成功。
func (s *notificationConfigStore) Preview(incoming *notificationConfig) (*notificationConfig, error) {
	if incoming == nil {
		return nil, errors.New("通知配置不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadAndMigrateLocked()
	if err != nil {
		return nil, err
	}
	if err := s.hydrateLocked(current); err != nil {
		return nil, err
	}
	next := cloneNotificationConfig(incoming)
	normalizeNotificationConfig(next)
	if err := validateNotificationNames(next); err != nil {
		return nil, err
	}
	if err := previewTelegramSecrets(current, next); err != nil {
		return nil, err
	}
	if err := previewEmailSecrets(current, next); err != nil {
		return nil, err
	}
	if err := previewWecomSecrets(current, next); err != nil {
		return nil, err
	}
	if err := previewBarkSecrets(current, next); err != nil {
		return nil, err
	}
	if err := previewWebhookSecrets(current, next); err != nil {
		return nil, err
	}
	if next.NotificationRules == nil {
		next.NotificationRules = defaultNotificationRules()
	} else {
		next.NotificationRules = cloneNotificationRules(next.NotificationRules)
	}
	if err := validateNotificationTemplates(next.NotificationTemplates); err != nil {
		return nil, err
	}
	next.NotificationTemplates = mergeNotificationTemplates(next.NotificationTemplates)
	return next, nil
}

func previewTelegramSecrets(current, next *notificationConfig) error {
	existing := make(map[string]telegramConfig, len(current.TelegramConfigs))
	for _, cfg := range current.TelegramConfigs {
		existing[cfg.Name] = cfg
	}
	for i := range next.TelegramConfigs {
		cfg := &next.TelegramConfigs[i]
		old, found := existing[cfg.Name]
		secret, err := previewSecret(cfg.BotToken, cfg.ReplaceSecret, cfg.ClearSecret, found && old.SecretConfigured, old.BotToken)
		if err != nil {
			return fmt.Errorf("Telegram %s: %w", cfg.Name, err)
		}
		cfg.BotToken, cfg.SecretConfigured = secret, secret != ""
		if cfg.Enabled && secret == "" {
			return fmt.Errorf("Telegram %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	return nil
}

func previewBarkSecrets(current, next *notificationConfig) error {
	existing := make(map[string]barkConfig, len(current.BarkConfigs))
	for _, cfg := range current.BarkConfigs {
		existing[cfg.Name] = cfg
	}
	for i := range next.BarkConfigs {
		cfg := &next.BarkConfigs[i]
		old, found := existing[cfg.Name]
		secret, err := previewSecret(cfg.DeviceKey, cfg.ReplaceSecret, cfg.ClearSecret, found && old.SecretConfigured, old.DeviceKey)
		if err != nil {
			return fmt.Errorf("Bark %s: %w", cfg.Name, err)
		}
		cfg.DeviceKey, cfg.SecretConfigured = secret, secret != ""
		if cfg.Enabled && secret == "" {
			return fmt.Errorf("Bark %s: 启用前必须配置设备Key", cfg.Name)
		}
	}
	return nil
}
func previewEmailSecrets(current, next *notificationConfig) error {
	existing := make(map[string]emailConfig, len(current.EmailConfigs))
	for _, cfg := range current.EmailConfigs {
		existing[cfg.Name] = cfg
	}
	for i := range next.EmailConfigs {
		cfg := &next.EmailConfigs[i]
		old, found := existing[cfg.Name]
		secret, err := previewSecret(cfg.Password, cfg.ReplaceSecret, cfg.ClearSecret, found && old.SecretConfigured, old.Password)
		if err != nil {
			return fmt.Errorf("Email %s: %w", cfg.Name, err)
		}
		cfg.Password, cfg.SecretConfigured = secret, secret != ""
		if cfg.Enabled && secret == "" {
			return fmt.Errorf("Email %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	return nil
}

func previewWecomSecrets(current, next *notificationConfig) error {
	existing := make(map[string]wecomConfig, len(current.WecomConfigs))
	for _, cfg := range current.WecomConfigs {
		existing[cfg.Name] = cfg
	}
	for i := range next.WecomConfigs {
		cfg := &next.WecomConfigs[i]
		old, found := existing[cfg.Name]
		secret, err := previewSecret(cfg.WebhookURL, cfg.ReplaceSecret, cfg.ClearSecret, found && old.SecretConfigured, old.WebhookURL)
		if err != nil {
			return fmt.Errorf("WeCom %s: %w", cfg.Name, err)
		}
		cfg.WebhookURL, cfg.SecretConfigured = secret, secret != ""
		if cfg.Enabled && secret == "" {
			return fmt.Errorf("WeCom %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	return nil
}

func previewWebhookSecrets(current, next *notificationConfig) error {
	existing := make(map[string]webhookConfig, len(current.WebhookConfigs))
	for _, cfg := range current.WebhookConfigs {
		existing[cfg.Name] = cfg
	}
	for i := range next.WebhookConfigs {
		cfg := &next.WebhookConfigs[i]
		old, found := existing[cfg.Name]
		secret, err := previewSecret(cfg.URL, cfg.ReplaceSecret, cfg.ClearSecret, found && old.SecretConfigured, old.URL)
		if err != nil {
			return fmt.Errorf("Webhook %s: %w", cfg.Name, err)
		}
		if !cfg.ReplaceSecret && !cfg.ClearSecret && found && old.SecretConfigured {
			cfg.Headers = cloneStringMap(old.Headers)
		}
		if cfg.ClearSecret {
			cfg.Headers = nil
		}
		cfg.URL, cfg.SecretConfigured = secret, secret != ""
		if cfg.Enabled && secret == "" {
			return fmt.Errorf("Webhook %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	return nil
}

func previewSecret(incoming string, replace, clear, oldConfigured bool, oldValue string) (string, error) {
	if replace && clear {
		return "", errors.New("replace_secret 与 clear_secret 不能同时设置")
	}
	if clear {
		return "", nil
	}
	if replace {
		if incoming == "" {
			return "", errors.New("replace_secret 需要新秘密")
		}
		return incoming, nil
	}
	if incoming != "" {
		return "", errors.New("提供秘密时必须显式设置 replace_secret")
	}
	if oldConfigured {
		return oldValue, nil
	}
	return "", nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func (s *notificationConfigStore) Save(incoming *notificationConfig) error {
	if incoming == nil {
		return errors.New("通知配置不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadAndMigrateLocked()
	if err != nil {
		return err
	}
	next := cloneNotificationConfig(incoming)
	if err := validateNotificationNames(next); err != nil {
		return err
	}

	var deleteKeys []string
	if err := s.applyTelegramUpdates(current, next, &deleteKeys); err != nil {
		return err
	}
	if err := s.applyEmailUpdates(current, next, &deleteKeys); err != nil {
		return err
	}
	if err := s.applyWecomUpdates(current, next, &deleteKeys); err != nil {
		return err
	}
	if err := s.applyBarkUpdates(current, next, &deleteKeys); err != nil {
		return err
	}
	if err := s.applyWebhookUpdates(current, next, &deleteKeys); err != nil {
		return err
	}
	if next.NotificationRules == nil {
		next.NotificationRules = defaultNotificationRules()
	} else {
		next.NotificationRules = cloneNotificationRules(next.NotificationRules)
	}
	if err := validateNotificationTemplates(next.NotificationTemplates); err != nil {
		return err
	}
	next.NotificationTemplates = mergeNotificationTemplates(next.NotificationTemplates)
	sanitizeNotificationConfig(next)
	if err := s.writeLocked(next); err != nil {
		return err
	}
	for _, key := range deleteKeys {
		if s.secrets != nil && s.secrets.Available() {
			if err := s.secrets.DeleteSecret(key); err != nil {
				// 配置已安全落盘；遗留密文不再被引用，不把成功更新回报成失败。
				log.Printf("WARN: 清理未引用通知密文 %s 失败: %v", key, err)
			}
		}
	}
	return nil
}

func (s *notificationConfigStore) applyBarkUpdates(current, next *notificationConfig, deletes *[]string) error {
	existing := make(map[string]barkConfig)
	for _, cfg := range current.BarkConfigs {
		existing[cfg.Name] = cfg
	}
	seen := make(map[string]bool)
	for i := range next.BarkConfigs {
		cfg := &next.BarkConfigs[i]
		seen[cfg.Name] = true
		old, ok := existing[cfg.Name]
		if !ok && cfg.SecretConfigured && !cfg.ReplaceSecret && !cfg.ClearSecret {
			return fmt.Errorf("Bark %s: 名称变化后必须重新提供设备Key并设置 replace_secret", cfg.Name)
		}
		configured, err := s.applySecretUpdate(notificationSecretKey("bark", cfg.Name, "device_key"), cfg.DeviceKey, cfg.ReplaceSecret, cfg.ClearSecret, ok && old.SecretConfigured, deletes)
		if err != nil {
			return fmt.Errorf("Bark %s: %w", cfg.Name, err)
		}
		cfg.SecretConfigured = configured
		if cfg.Enabled && !configured {
			return fmt.Errorf("Bark %s: 启用前必须配置设备Key", cfg.Name)
		}
	}
	for name, cfg := range existing {
		if !seen[name] && cfg.SecretConfigured {
			*deletes = append(*deletes, notificationSecretKey("bark", name, "device_key"))
		}
	}
	return nil
}
func (s *notificationConfigStore) loadAndMigrateLocked() (*notificationConfig, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultNotificationConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var probe struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("解析通知配置失败: %w", err)
	}
	if probe.SchemaVersion != nil {
		if *probe.SchemaVersion != notificationConfigSchemaVersion {
			return nil, fmt.Errorf("不支持的通知配置 schema_version: %d", *probe.SchemaVersion)
		}
		var envelope notificationConfigEnvelope
		if err := decodeStrictJSON(data, &envelope); err != nil {
			return nil, err
		}
		normalizeNotificationConfig(&envelope.Config)
		return &envelope.Config, nil
	}

	var legacy notificationConfig
	if err := decodeStrictJSON(data, &legacy); err != nil {
		return nil, fmt.Errorf("解析旧通知配置失败: %w", err)
	}
	normalizeNotificationConfig(&legacy)
	if err := s.migrateLegacySecretsLocked(&legacy); err != nil {
		return nil, err
	}
	sanitizeNotificationConfig(&legacy)
	if err := s.writeLocked(&legacy); err != nil {
		return nil, err
	}
	return &legacy, nil
}

func (s *notificationConfigStore) migrateLegacySecretsLocked(cfg *notificationConfig) error {
	for i := range cfg.TelegramConfigs {
		if cfg.TelegramConfigs[i].BotToken != "" {
			if err := s.setSecret(notificationSecretKey("telegram", cfg.TelegramConfigs[i].Name, "token"), cfg.TelegramConfigs[i].BotToken); err != nil {
				return err
			}
			cfg.TelegramConfigs[i].SecretConfigured = true
		}
	}
	for i := range cfg.EmailConfigs {
		if cfg.EmailConfigs[i].Password != "" {
			if err := s.setSecret(notificationSecretKey("email", cfg.EmailConfigs[i].Name, "password"), cfg.EmailConfigs[i].Password); err != nil {
				return err
			}
			cfg.EmailConfigs[i].SecretConfigured = true
		}
	}
	for i := range cfg.WecomConfigs {
		if cfg.WecomConfigs[i].WebhookURL != "" {
			if err := s.setSecret(notificationSecretKey("wecom", cfg.WecomConfigs[i].Name, "url"), cfg.WecomConfigs[i].WebhookURL); err != nil {
				return err
			}
			cfg.WecomConfigs[i].SecretConfigured = true
		}
	}
	for i := range cfg.BarkConfigs {
		if cfg.BarkConfigs[i].DeviceKey != "" {
			if err := s.setSecret(notificationSecretKey("bark", cfg.BarkConfigs[i].Name, "device_key"), cfg.BarkConfigs[i].DeviceKey); err != nil {
				return err
			}
			cfg.BarkConfigs[i].SecretConfigured = true
		}
	}
	for i := range cfg.WebhookConfigs {
		if cfg.WebhookConfigs[i].URL != "" {
			if err := s.setSecret(notificationSecretKey("webhook", cfg.WebhookConfigs[i].Name, "url"), cfg.WebhookConfigs[i].URL); err != nil {
				return err
			}
			headers, _ := json.Marshal(cfg.WebhookConfigs[i].Headers)
			if err := s.setSecret(notificationSecretKey("webhook", cfg.WebhookConfigs[i].Name, "headers"), string(headers)); err != nil {
				return err
			}
			cfg.WebhookConfigs[i].SecretConfigured = true
		}
	}
	return nil
}

func (s *notificationConfigStore) hydrateLocked(cfg *notificationConfig) error {
	for i := range cfg.TelegramConfigs {
		if cfg.TelegramConfigs[i].SecretConfigured {
			value, err := s.getSecret(notificationSecretKey("telegram", cfg.TelegramConfigs[i].Name, "token"))
			if err != nil {
				return err
			}
			cfg.TelegramConfigs[i].BotToken = value
		}
	}
	for i := range cfg.EmailConfigs {
		if cfg.EmailConfigs[i].SecretConfigured {
			value, err := s.getSecret(notificationSecretKey("email", cfg.EmailConfigs[i].Name, "password"))
			if err != nil {
				return err
			}
			cfg.EmailConfigs[i].Password = value
		}
	}
	for i := range cfg.WecomConfigs {
		if cfg.WecomConfigs[i].SecretConfigured {
			value, err := s.getSecret(notificationSecretKey("wecom", cfg.WecomConfigs[i].Name, "url"))
			if err != nil {
				return err
			}
			cfg.WecomConfigs[i].WebhookURL = value
		}
	}
	for i := range cfg.BarkConfigs {
		if cfg.BarkConfigs[i].SecretConfigured {
			value, err := s.getSecret(notificationSecretKey("bark", cfg.BarkConfigs[i].Name, "device_key"))
			if err != nil {
				return err
			}
			cfg.BarkConfigs[i].DeviceKey = value
		}
	}
	for i := range cfg.WebhookConfigs {
		if cfg.WebhookConfigs[i].SecretConfigured {
			value, err := s.getSecret(notificationSecretKey("webhook", cfg.WebhookConfigs[i].Name, "url"))
			if err != nil {
				return err
			}
			cfg.WebhookConfigs[i].URL = value
			headers, err := s.getSecret(notificationSecretKey("webhook", cfg.WebhookConfigs[i].Name, "headers"))
			if err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(headers), &cfg.WebhookConfigs[i].Headers); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *notificationConfigStore) applyTelegramUpdates(current, next *notificationConfig, deletes *[]string) error {
	existing := make(map[string]telegramConfig)
	for _, cfg := range current.TelegramConfigs {
		existing[cfg.Name] = cfg
	}
	seen := make(map[string]bool)
	for i := range next.TelegramConfigs {
		cfg := &next.TelegramConfigs[i]
		seen[cfg.Name] = true
		old, ok := existing[cfg.Name]
		if !ok && cfg.SecretConfigured && !cfg.ReplaceSecret && !cfg.ClearSecret {
			return fmt.Errorf("Telegram %s: 名称变化后必须重新提供秘密并设置 replace_secret", cfg.Name)
		}
		configured, err := s.applySecretUpdate(notificationSecretKey("telegram", cfg.Name, "token"), cfg.BotToken, cfg.ReplaceSecret, cfg.ClearSecret, ok && old.SecretConfigured, deletes)
		if err != nil {
			return fmt.Errorf("Telegram %s: %w", cfg.Name, err)
		}
		cfg.SecretConfigured = configured
		if cfg.Enabled && !configured {
			return fmt.Errorf("Telegram %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	for name, cfg := range existing {
		if !seen[name] && cfg.SecretConfigured {
			*deletes = append(*deletes, notificationSecretKey("telegram", name, "token"))
		}
	}
	return nil
}

func (s *notificationConfigStore) applyEmailUpdates(current, next *notificationConfig, deletes *[]string) error {
	existing := make(map[string]emailConfig)
	for _, cfg := range current.EmailConfigs {
		existing[cfg.Name] = cfg
	}
	seen := make(map[string]bool)
	for i := range next.EmailConfigs {
		cfg := &next.EmailConfigs[i]
		seen[cfg.Name] = true
		old, ok := existing[cfg.Name]
		if !ok && cfg.SecretConfigured && !cfg.ReplaceSecret && !cfg.ClearSecret {
			return fmt.Errorf("Email %s: 名称变化后必须重新提供秘密并设置 replace_secret", cfg.Name)
		}
		configured, err := s.applySecretUpdate(notificationSecretKey("email", cfg.Name, "password"), cfg.Password, cfg.ReplaceSecret, cfg.ClearSecret, ok && old.SecretConfigured, deletes)
		if err != nil {
			return fmt.Errorf("Email %s: %w", cfg.Name, err)
		}
		cfg.SecretConfigured = configured
		if cfg.Enabled && !configured {
			return fmt.Errorf("Email %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	for name, cfg := range existing {
		if !seen[name] && cfg.SecretConfigured {
			*deletes = append(*deletes, notificationSecretKey("email", name, "password"))
		}
	}
	return nil
}

func (s *notificationConfigStore) applyWecomUpdates(current, next *notificationConfig, deletes *[]string) error {
	existing := make(map[string]wecomConfig)
	for _, cfg := range current.WecomConfigs {
		existing[cfg.Name] = cfg
	}
	seen := make(map[string]bool)
	for i := range next.WecomConfigs {
		cfg := &next.WecomConfigs[i]
		seen[cfg.Name] = true
		old, ok := existing[cfg.Name]
		if !ok && cfg.SecretConfigured && !cfg.ReplaceSecret && !cfg.ClearSecret {
			return fmt.Errorf("WeCom %s: 名称变化后必须重新提供秘密并设置 replace_secret", cfg.Name)
		}
		configured, err := s.applySecretUpdate(notificationSecretKey("wecom", cfg.Name, "url"), cfg.WebhookURL, cfg.ReplaceSecret, cfg.ClearSecret, ok && old.SecretConfigured, deletes)
		if err != nil {
			return fmt.Errorf("WeCom %s: %w", cfg.Name, err)
		}
		cfg.SecretConfigured = configured
		if cfg.Enabled && !configured {
			return fmt.Errorf("WeCom %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	for name, cfg := range existing {
		if !seen[name] && cfg.SecretConfigured {
			*deletes = append(*deletes, notificationSecretKey("wecom", name, "url"))
		}
	}
	return nil
}

func (s *notificationConfigStore) applyWebhookUpdates(current, next *notificationConfig, deletes *[]string) error {
	existing := make(map[string]webhookConfig)
	for _, cfg := range current.WebhookConfigs {
		existing[cfg.Name] = cfg
	}
	seen := make(map[string]bool)
	for i := range next.WebhookConfigs {
		cfg := &next.WebhookConfigs[i]
		seen[cfg.Name] = true
		old, ok := existing[cfg.Name]
		if !ok && cfg.SecretConfigured && !cfg.ReplaceSecret && !cfg.ClearSecret {
			return fmt.Errorf("Webhook %s: 名称变化后必须重新提供秘密并设置 replace_secret", cfg.Name)
		}
		if cfg.ReplaceSecret && cfg.URL == "" {
			return fmt.Errorf("Webhook %s: replace_secret 需要 URL", cfg.Name)
		}
		configured, err := s.applySecretUpdate(notificationSecretKey("webhook", cfg.Name, "url"), cfg.URL, cfg.ReplaceSecret, cfg.ClearSecret, ok && old.SecretConfigured, deletes)
		if err != nil {
			return err
		}
		if cfg.ReplaceSecret {
			headers, _ := json.Marshal(cfg.Headers)
			if err := s.setSecret(notificationSecretKey("webhook", cfg.Name, "headers"), string(headers)); err != nil {
				return err
			}
		}
		if cfg.ClearSecret {
			*deletes = append(*deletes, notificationSecretKey("webhook", cfg.Name, "headers"))
		}
		cfg.SecretConfigured = configured
		if cfg.Enabled && !configured {
			return fmt.Errorf("Webhook %s: 启用前必须配置秘密", cfg.Name)
		}
	}
	for name, cfg := range existing {
		if !seen[name] && cfg.SecretConfigured {
			*deletes = append(*deletes, notificationSecretKey("webhook", name, "url"), notificationSecretKey("webhook", name, "headers"))
		}
	}
	return nil
}

func (s *notificationConfigStore) applySecretUpdate(key, value string, replace, clear, oldConfigured bool, deletes *[]string) (bool, error) {
	if replace && clear {
		return false, errors.New("replace_secret 与 clear_secret 不能同时设置")
	}
	if clear {
		*deletes = append(*deletes, key)
		return false, nil
	}
	if replace {
		if value == "" {
			return false, errors.New("replace_secret 需要新秘密")
		}
		if err := s.setSecret(key, value); err != nil {
			return false, err
		}
		return true, nil
	}
	if value != "" {
		return false, errors.New("提供秘密时必须显式设置 replace_secret")
	}
	return oldConfigured, nil
}

func (s *notificationConfigStore) setSecret(key, value string) error {
	if s.secrets == nil || !s.secrets.Available() {
		return errEncryptionUnavailable
	}
	return s.secrets.SetSecret(key, value)
}
func (s *notificationConfigStore) getSecret(key string) (string, error) {
	if s.secrets == nil || !s.secrets.Available() {
		return "", errEncryptionUnavailable
	}
	return s.secrets.GetSecret(key)
}

func (s *notificationConfigStore) writeLocked(cfg *notificationConfig) error {
	envelope := notificationConfigEnvelope{SchemaVersion: notificationConfigSchemaVersion, Config: *cfg}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0600)
}

func notificationSecretKey(kind, name, field string) string {
	encodedName := base64.RawURLEncoding.EncodeToString([]byte(name))
	return "notification/" + kind + "/" + encodedName + "/" + field
}

func validateNotificationNames(cfg *notificationConfig) error {
	seen := make(map[string]bool)
	check := func(kind, name string) error {
		if name == "" || utf8.RuneCountInString(name) > 64 || strings.ContainsAny(name, "/\\\r\n\x00") {
			return fmt.Errorf("%s 名称无效", kind)
		}
		key := kind + "\x00" + name
		if seen[key] {
			return fmt.Errorf("%s 名称重复: %s", kind, name)
		}
		seen[key] = true
		return nil
	}
	for _, item := range cfg.TelegramConfigs {
		if err := check("Telegram", item.Name); err != nil {
			return err
		}
	}
	for _, item := range cfg.EmailConfigs {
		if err := check("Email", item.Name); err != nil {
			return err
		}
	}
	for _, item := range cfg.WecomConfigs {
		if err := check("WeCom", item.Name); err != nil {
			return err
		}
	}
	for _, item := range cfg.BarkConfigs {
		if err := check("Bark", item.Name); err != nil {
			return err
		}
	}
	for _, item := range cfg.WebhookConfigs {
		if err := check("Webhook", item.Name); err != nil {
			return err
		}
	}
	return nil
}

func normalizeNotificationConfig(cfg *notificationConfig) {
	if cfg.TelegramConfigs == nil {
		cfg.TelegramConfigs = []telegramConfig{}
	}
	if cfg.EmailConfigs == nil {
		cfg.EmailConfigs = []emailConfig{}
	}
	if cfg.WecomConfigs == nil {
		cfg.WecomConfigs = []wecomConfig{}
	}
	if cfg.BarkConfigs == nil {
		cfg.BarkConfigs = []barkConfig{}
	}
	if cfg.WebhookConfigs == nil {
		cfg.WebhookConfigs = []webhookConfig{}
	}
	if cfg.NotificationRules == nil {
		cfg.NotificationRules = defaultNotificationRules()
	}
	if cfg.NotificationTemplates == nil {
		cfg.NotificationTemplates = defaultNotificationTemplates()
	} else {
		cfg.NotificationTemplates = mergeNotificationTemplates(cfg.NotificationTemplates)
	}
}

func sanitizeNotificationConfig(cfg *notificationConfig) {
	for i := range cfg.TelegramConfigs {
		cfg.TelegramConfigs[i].BotToken = ""
		cfg.TelegramConfigs[i].ReplaceSecret = false
		cfg.TelegramConfigs[i].ClearSecret = false
	}
	for i := range cfg.EmailConfigs {
		cfg.EmailConfigs[i].Password = ""
		cfg.EmailConfigs[i].ReplaceSecret = false
		cfg.EmailConfigs[i].ClearSecret = false
	}
	for i := range cfg.WecomConfigs {
		cfg.WecomConfigs[i].WebhookURL = ""
		cfg.WecomConfigs[i].ReplaceSecret = false
		cfg.WecomConfigs[i].ClearSecret = false
	}
	for i := range cfg.BarkConfigs {
		cfg.BarkConfigs[i].DeviceKey = ""
		cfg.BarkConfigs[i].ReplaceSecret = false
		cfg.BarkConfigs[i].ClearSecret = false
	}
	for i := range cfg.WebhookConfigs {
		cfg.WebhookConfigs[i].URL = ""
		cfg.WebhookConfigs[i].Headers = nil
		cfg.WebhookConfigs[i].ReplaceSecret = false
		cfg.WebhookConfigs[i].ClearSecret = false
	}
}

func cloneNotificationConfig(cfg *notificationConfig) *notificationConfig {
	data, _ := json.Marshal(cfg)
	var clone notificationConfig
	_ = json.Unmarshal(data, &clone)
	normalizeNotificationConfig(&clone)
	return &clone
}
