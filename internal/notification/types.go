package notification

import "time"

// Type 是通知事件类型。
type Type string

const (
	BackupStart   Type = "backup_start"
	BackupSuccess Type = "backup_success"
	BackupFailed  Type = "backup_failed"
	DeviceOnline  Type = "device_online"
	DeviceOffline Type = "device_offline"
	SystemError   Type = "system_error"
)

// Level 是通知级别。
type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Message 是发送给通知器的消息。
type Message struct {
	Type        Type              `json:"type"`
	Level       Level             `json:"level"`
	Title       string            `json:"title"`
	Content     string            `json:"content"`
	DeviceName  string            `json:"device_name,omitempty"`
	DeviceUDID  string            `json:"device_udid,omitempty"`
	Timestamp   time.Time         `json:"timestamp"`
	ExtraFields map[string]string `json:"extra_fields,omitempty"`
}

// Notifier 是通知适配器的最小接口。
type Notifier interface {
	Send(message *Message) error
	GetName() string
	IsEnabled() bool
	Validate() error
}

// Delivery 是单个通知器的一次实际发送结果。
type Delivery struct {
	Notifier string `json:"notifier"`
	Error    string `json:"error,omitempty"`
}

// SendResult 汇总同步测试发送的真实结果。
type SendResult struct {
	Attempted  int        `json:"attempted"`
	Succeeded  int        `json:"succeeded"`
	Failed     int        `json:"failed"`
	Deliveries []Delivery `json:"deliveries"`
}

// TelegramConfig 是 Telegram 通知配置。
type TelegramConfig struct {
	Name             string `json:"name"`
	BotToken         string `json:"bot_token,omitempty"`
	ChatID           string `json:"chat_id"`
	Enabled          bool   `json:"enabled"`
	SecretConfigured bool   `json:"configured,omitempty"`
	ReplaceSecret    bool   `json:"replace_secret,omitempty"`
	ClearSecret      bool   `json:"clear_secret,omitempty"`
}

// EmailConfig 是 SMTP 邮件通知配置。
type EmailConfig struct {
	Name             string `json:"name"`
	SMTPHost         string `json:"smtp_host"`
	SMTPPort         int    `json:"smtp_port"`
	Username         string `json:"username"`
	Password         string `json:"password,omitempty"`
	From             string `json:"from"`
	To               string `json:"to"`
	Enabled          bool   `json:"enabled"`
	SecretConfigured bool   `json:"configured,omitempty"`
	ReplaceSecret    bool   `json:"replace_secret,omitempty"`
	ClearSecret      bool   `json:"clear_secret,omitempty"`
}

// WecomConfig 是企业微信通知配置。
type WecomConfig struct {
	Name             string `json:"name"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	Enabled          bool   `json:"enabled"`
	SecretConfigured bool   `json:"configured,omitempty"`
	ReplaceSecret    bool   `json:"replace_secret,omitempty"`
	ClearSecret      bool   `json:"clear_secret,omitempty"`
}

// BarkConfig 是 Bark 推送配置。
type BarkConfig struct {
	Name             string `json:"name"`
	ServerURL        string `json:"server_url"`
	DeviceKey        string `json:"device_key,omitempty"`
	Enabled          bool   `json:"enabled"`
	SecretConfigured bool   `json:"configured,omitempty"`
	ReplaceSecret    bool   `json:"replace_secret,omitempty"`
	ClearSecret      bool   `json:"clear_secret,omitempty"`
}

// WebhookConfig 是通用 Webhook 通知配置。
type WebhookConfig struct {
	Name             string            `json:"name"`
	URL              string            `json:"url,omitempty"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	Enabled          bool              `json:"enabled"`
	SecretConfigured bool              `json:"configured,omitempty"`
	ReplaceSecret    bool              `json:"replace_secret,omitempty"`
	ClearSecret      bool              `json:"clear_secret,omitempty"`
}

// Config 是完整通知配置。文件读写与秘密持久化留在应用层。
type Config struct {
	Enabled               bool                `json:"enabled"`
	TelegramConfigs       []TelegramConfig    `json:"telegram_configs"`
	EmailConfigs          []EmailConfig       `json:"email_configs"`
	WecomConfigs          []WecomConfig       `json:"wecom_configs"`
	BarkConfigs           []BarkConfig        `json:"bark_configs"`
	WebhookConfigs        []WebhookConfig     `json:"webhook_configs"`
	NotificationRules     map[string][]string `json:"notification_rules"`
	NotificationTemplates map[string]string   `json:"notification_templates"`
}

// CloneMessage 返回不共享 ExtraFields 的消息副本。
func CloneMessage(message Message) Message {
	extraFields := message.ExtraFields
	message.ExtraFields = make(map[string]string, len(extraFields))
	for key, value := range extraFields {
		message.ExtraFields[key] = value
	}
	return message
}

// CloneRules 返回不共享 map 或 slice 的规则副本。
func CloneRules(rules map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(rules))
	for key, values := range rules {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

// CloneStringMap 返回字符串 map 的副本。
func CloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
