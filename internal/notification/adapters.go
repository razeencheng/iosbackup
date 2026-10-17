package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"iosbackup/internal/i18n"
)

const networkTimeout = 10 * time.Second

func newNotificationHTTPClient(allowPrivate []netip.Prefix) *http.Client {
	return newNotificationHTTPClientPolicy(allowPrivate, false)
}

func newNotificationHTTPClientPolicy(allowPrivate []netip.Prefix, allowUnsafe bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if allowUnsafe {
				return dialer.DialContext(ctx, network, address)
			}
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range addresses {
				if err := validateOutboundIP(ip, allowPrivate); err != nil {
					lastErr = err
					continue
				}
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("destination %s has no usable address", host)
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: transport, Timeout: networkTimeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return validateOutboundURL(req.URL.String(), allowPrivate, allowUnsafe)
	}
	return client
}

func validateOutboundIP(address netip.Addr, allowPrivate []netip.Prefix) error {
	address = address.Unmap()
	if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return i18n.NewError(i18n.NotificationTargetDisallowed, nil)
	}
	if address.IsPrivate() {
		for _, prefix := range allowPrivate {
			if prefix.Contains(address) {
				return nil
			}
		}
		return i18n.NewError(i18n.NotificationPrivateTargetUnlisted, nil)
	}
	return nil
}

func validateOutboundURL(rawURL string, allowPrivate []netip.Prefix, allowUnsafe bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return i18n.NewError(i18n.NotificationURLInvalid, nil)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return i18n.NewError(i18n.NotificationURLInvalid, nil)
	}
	if parsed.Scheme != "https" && !allowUnsafe {
		address, parseErr := netip.ParseAddr(parsed.Hostname())
		if parseErr != nil || !address.Unmap().IsPrivate() {
			return i18n.NewError(i18n.NotificationPublicURLHTTPS, nil)
		}
	}
	if allowUnsafe {
		return nil
	}
	if address, err := netip.ParseAddr(parsed.Hostname()); err == nil {
		return validateOutboundIP(address, allowPrivate)
	}
	return nil
}

// TelegramNotifier 通过 Telegram Bot API 发送通知。
type TelegramNotifier struct {
	config TelegramConfig
	client *http.Client
}

func NewTelegramNotifier(config TelegramConfig) *TelegramNotifier {
	return &TelegramNotifier{config: config, client: newNotificationHTTPClient(nil)}
}

func (notifier *TelegramNotifier) Send(message *Message) error {
	text := fmt.Sprintf("📱 *%s*\n\n%s\n\n⏰ %s", message.Title, message.Content, message.Timestamp.Format(time.RFC3339))
	data := map[string]interface{}{"chat_id": notifier.config.ChatID, "text": text, "parse_mode": "Markdown"}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode request data: %v", err)
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", notifier.config.BotToken)
	response, err := notifier.client.Post(endpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("Telegram request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Telegram API returned status %d", response.StatusCode)
	}
	return nil
}

func (notifier *TelegramNotifier) GetName() string {
	return fmt.Sprintf("Telegram_%s", notifier.config.Name)
}
func (notifier *TelegramNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *TelegramNotifier) Validate() error {
	if notifier.config.BotToken == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Telegram", "field": "bot_token"})
	}
	if notifier.config.ChatID == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Telegram", "field": "chat_id"})
	}
	return nil
}

// EmailNotifier 通过 SMTP 发送通知。
type EmailNotifier struct {
	config  EmailConfig
	timeout time.Duration
}

func NewEmailNotifier(config EmailConfig) *EmailNotifier {
	return &EmailNotifier{config: config, timeout: networkTimeout}
}

func (notifier *EmailNotifier) Send(message *Message) error {
	subject := mime.QEncoding.Encode("UTF-8", copyFor(message.Locale, "label.subject")+": "+sanitizeMailHeader(message.Title))
	body := fmt.Sprintf("%s: %s\n%s: %s\n%s: %s\n%s: %s\n\n%s\n",
		copyFor(message.Locale, "label.device"), message.DeviceName,
		copyFor(message.Locale, "label.type"), message.Type,
		copyFor(message.Locale, "label.level"), message.Level,
		copyFor(message.Locale, "label.time"), message.Timestamp.Format(time.RFC3339), message.Content)
	mailMessage := fmt.Sprintf("To: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s", notifier.config.To, subject, body)
	address := fmt.Sprintf("%s:%d", notifier.config.SMTPHost, notifier.config.SMTPPort)
	if err := notifier.sendSMTP(address, []byte(mailMessage)); err != nil {
		return fmt.Errorf("send email: %v", err)
	}
	return nil
}

func (notifier *EmailNotifier) sendSMTP(address string, message []byte) error {
	deadline := time.Now().Add(notifier.timeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	connection, err := (&net.Dialer{Timeout: notifier.timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	tlsConfig := smtpTLSConfig(notifier.config.SMTPHost)
	if notifier.config.SMTPPort == 465 {
		connection = tls.Client(connection, tlsConfig)
		if err := connection.(*tls.Conn).HandshakeContext(ctx); err != nil {
			return err
		}
	}
	client, err := smtp.NewClient(connection, notifier.config.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if notifier.config.SMTPPort != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return err
			}
		} else if notifier.config.Username != "" {
			return fmt.Errorf("SMTP server does not offer STARTTLS; refusing to send credentials")
		}
	}
	if notifier.config.Username != "" {
		auth := smtp.PlainAuth("", notifier.config.Username, notifier.config.Password, notifier.config.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(notifier.config.From); err != nil {
		return err
	}
	if err := client.Rcpt(notifier.config.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	return writer.Close()
}

func smtpTLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func (notifier *EmailNotifier) GetName() string {
	return fmt.Sprintf("Email_%s", notifier.config.Name)
}
func (notifier *EmailNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *EmailNotifier) Validate() error {
	if notifier.config.SMTPHost == "" || strings.ContainsAny(notifier.config.SMTPHost, "\r\n\x00") {
		return i18n.NewError(i18n.NotificationSMTPHost, nil)
	}
	if notifier.config.SMTPPort <= 0 || notifier.config.SMTPPort > 65535 {
		return i18n.NewError(i18n.NotificationSMTPPort, nil)
	}
	if notifier.config.Username == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Email", "field": "username"})
	}
	if notifier.config.Password == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Email", "field": "password"})
	}
	if notifier.config.From == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Email", "field": "from"})
	}
	if notifier.config.To == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Email", "field": "to"})
	}
	for field, address := range map[string]string{"from": notifier.config.From, "to": notifier.config.To} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != strings.TrimSpace(address) || strings.ContainsAny(address, "\r\n") {
			return i18n.NewError(i18n.NotificationEmailAddress, map[string]any{"field": field})
		}
	}
	return nil
}

func sanitizeMailHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

// WecomNotifier 通过企业微信机器人发送通知。
type WecomNotifier struct {
	config                WecomConfig
	client                *http.Client
	allowUnsafeTestTarget bool
}

func NewWecomNotifier(config WecomConfig) *WecomNotifier {
	return &WecomNotifier{config: config, client: newNotificationHTTPClient(nil)}
}

func (notifier *WecomNotifier) Send(message *Message) error {
	content := fmt.Sprintf("%s\n\n%s\n\n%s: %s", message.Title, message.Content, copyFor(message.Locale, "label.time"), message.Timestamp.Format(time.RFC3339))
	data := map[string]interface{}{"msgtype": "text", "text": map[string]string{"content": content}}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode request data: %v", err)
	}
	client := notifier.client
	if notifier.allowUnsafeTestTarget {
		client = newNotificationHTTPClientPolicy(nil, true)
	}
	response, err := client.Post(notifier.config.WebhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("WeCom request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("WeCom API returned status %d", response.StatusCode)
	}
	return nil
}

func (notifier *WecomNotifier) GetName() string {
	return fmt.Sprintf("Wecom_%s", notifier.config.Name)
}
func (notifier *WecomNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *WecomNotifier) Validate() error {
	if notifier.config.WebhookURL == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "WeCom", "field": "webhook_url"})
	}
	if !notifier.allowUnsafeTestTarget && !strings.HasPrefix(notifier.config.WebhookURL, "https://") {
		return i18n.NewError(i18n.NotificationURLHTTPS, nil)
	}
	parsed, err := url.Parse(notifier.config.WebhookURL)
	if err != nil || (!notifier.allowUnsafeTestTarget && !strings.EqualFold(parsed.Hostname(), "qyapi.weixin.qq.com")) {
		return i18n.NewError(i18n.NotificationWeComURL, nil)
	}
	return validateOutboundURL(notifier.config.WebhookURL, nil, notifier.allowUnsafeTestTarget)
}

// BarkNotifier 通过 Bark HTTP API 发送通知。
type BarkNotifier struct {
	config                BarkConfig
	client                *http.Client
	allowPrivate          []netip.Prefix
	allowUnsafeTestTarget bool
}

func NewBarkNotifier(config BarkConfig) *BarkNotifier {
	return NewBarkNotifierWithAllowlist(config, nil)
}

func NewBarkNotifierWithAllowlist(config BarkConfig, allowPrivate []netip.Prefix) *BarkNotifier {
	ownedAllowPrivate := append([]netip.Prefix(nil), allowPrivate...)
	return &BarkNotifier{config: config, client: newNotificationHTTPClient(ownedAllowPrivate), allowPrivate: ownedAllowPrivate}
}

func (notifier *BarkNotifier) endpoint() string {
	return strings.TrimRight(notifier.config.ServerURL, "/") + "/" + url.PathEscape(notifier.config.DeviceKey)
}

func (notifier *BarkNotifier) Send(message *Message) error {
	data, err := json.Marshal(map[string]string{"title": message.Title, "body": message.Content})
	if err != nil {
		return fmt.Errorf("encode Bark request: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, notifier.endpoint(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create Bark request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := notifier.client
	if notifier.allowUnsafeTestTarget {
		client = newNotificationHTTPClientPolicy(nil, true)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Bark request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bark returned status %d", response.StatusCode)
	}
	return nil
}

func (notifier *BarkNotifier) GetName() string {
	return fmt.Sprintf("Bark_%s", notifier.config.Name)
}
func (notifier *BarkNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *BarkNotifier) Validate() error {
	if notifier.config.ServerURL == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Bark", "field": "server_url"})
	}
	if len(notifier.config.ServerURL) > 4096 {
		return i18n.NewError(i18n.NotificationURLLength, map[string]any{"channel": "Bark", "field": "server_url", "max": 4096})
	}
	if notifier.config.DeviceKey == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Bark", "field": "device_key"})
	}
	if strings.ContainsAny(notifier.config.DeviceKey, "\r\n") || len(notifier.config.DeviceKey) > 256 {
		return i18n.NewError(i18n.NotificationBarkKeyInvalid, nil)
	}
	if err := validateOutboundURL(notifier.config.ServerURL, notifier.allowPrivate, notifier.allowUnsafeTestTarget); err != nil {
		return err
	}
	parsed, err := url.Parse(notifier.config.ServerURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return i18n.NewError(i18n.NotificationBarkURL, nil)
	}
	return nil
}

// WebhookNotifier 通过通用 Webhook 发送通知。
type WebhookNotifier struct {
	config                WebhookConfig
	client                *http.Client
	allowPrivate          []netip.Prefix
	allowUnsafeTestTarget bool
}

func NewWebhookNotifier(config WebhookConfig) *WebhookNotifier {
	return NewWebhookNotifierWithAllowlist(config, nil)
}

func NewWebhookNotifierWithAllowlist(config WebhookConfig, allowPrivate []netip.Prefix) *WebhookNotifier {
	ownedAllowPrivate := append([]netip.Prefix(nil), allowPrivate...)
	config.Headers = CloneStringMap(config.Headers)
	return &WebhookNotifier{config: config, client: newNotificationHTTPClient(ownedAllowPrivate), allowPrivate: ownedAllowPrivate}
}

func (notifier *WebhookNotifier) Send(message *Message) error {
	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode request data: %v", err)
	}
	method := strings.ToUpper(notifier.config.Method)
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequest(method, notifier.config.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range notifier.config.Headers {
		request.Header.Set(key, value)
	}
	client := notifier.client
	if notifier.allowUnsafeTestTarget {
		client = newNotificationHTTPClientPolicy(nil, true)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Webhook request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Webhook returned status %d", response.StatusCode)
	}
	return nil
}

func (notifier *WebhookNotifier) GetName() string {
	return fmt.Sprintf("Webhook_%s", notifier.config.Name)
}
func (notifier *WebhookNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *WebhookNotifier) Validate() error {
	if notifier.config.URL == "" {
		return i18n.NewError(i18n.NotificationFieldRequired, map[string]any{"channel": "Webhook", "field": "url"})
	}
	if len(notifier.config.URL) > 4096 {
		return i18n.NewError(i18n.NotificationURLLength, map[string]any{"channel": "Webhook", "field": "url", "max": 4096})
	}
	if err := validateOutboundURL(notifier.config.URL, notifier.allowPrivate, notifier.allowUnsafeTestTarget); err != nil {
		return err
	}
	method := strings.ToUpper(notifier.config.Method)
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return i18n.NewError(i18n.NotificationWebhookMethod, nil)
	}
	return validateWebhookHeaders(notifier.config.Headers)
}

func validateWebhookHeaders(headers map[string]string) error {
	if len(headers) > 20 {
		return i18n.NewError(i18n.NotificationWebhookHeaderCount, nil)
	}
	forbidden := map[string]bool{
		"host": true, "content-length": true, "connection": true, "keep-alive": true,
		"proxy-authenticate": true, "proxy-authorization": true, "te": true,
		"trailer": true, "transfer-encoding": true, "upgrade": true,
	}
	total := 0
	for name, value := range headers {
		lower := strings.ToLower(name)
		total += len(name) + len(value)
		if forbidden[lower] || !validHTTPToken(name) || strings.ContainsAny(value, "\r\n") {
			return i18n.NewError(i18n.NotificationWebhookHeader, map[string]any{"header": name})
		}
	}
	if total > 8192 {
		return i18n.NewError(i18n.NotificationWebhookHeaderSize, nil)
	}
	return nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character > 127 || !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", character)) {
			return false
		}
	}
	return true
}
