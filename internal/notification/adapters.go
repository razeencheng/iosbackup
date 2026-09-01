package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"strings"
	"time"
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
				lastErr = fmt.Errorf("目标 %s 没有可用地址", host)
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
			return fmt.Errorf("重定向次数过多")
		}
		return validateOutboundURL(req.URL.String(), allowPrivate, allowUnsafe)
	}
	return client
}

func validateOutboundIP(address netip.Addr, allowPrivate []netip.Prefix) error {
	address = address.Unmap()
	if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return fmt.Errorf("目标地址 %s 属于禁止的本机、组播或链路本地范围", address)
	}
	if address.IsPrivate() {
		for _, prefix := range allowPrivate {
			if prefix.Contains(address) {
				return nil
			}
		}
		return fmt.Errorf("私网目标 %s 未列入 IOSBK_WEBHOOK_ALLOW_CIDRS", address)
	}
	return nil
}

func validateOutboundURL(rawURL string, allowPrivate []netip.Prefix, allowUnsafe bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("URL 无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL 仅支持 http 或 https")
	}
	if parsed.Scheme != "https" && !allowUnsafe {
		address, parseErr := netip.ParseAddr(parsed.Hostname())
		if parseErr != nil || !address.Unmap().IsPrivate() {
			return fmt.Errorf("公网通知 URL 必须使用 https")
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
	text := fmt.Sprintf("📱 *%s*\n\n%s\n\n⏰ %s", message.Title, message.Content, message.Timestamp.Format("2006-01-02 15:04:05"))
	data := map[string]interface{}{"chat_id": notifier.config.ChatID, "text": text, "parse_mode": "Markdown"}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("构建请求数据失败: %v", err)
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", notifier.config.BotToken)
	response, err := notifier.client.Post(endpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("Telegram 发送请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram api 返回错误状态码: %d", response.StatusCode)
	}
	return nil
}

func (notifier *TelegramNotifier) GetName() string {
	return fmt.Sprintf("Telegram_%s", notifier.config.Name)
}
func (notifier *TelegramNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *TelegramNotifier) Validate() error {
	if notifier.config.BotToken == "" {
		return fmt.Errorf("bot token 不能为空")
	}
	if notifier.config.ChatID == "" {
		return fmt.Errorf("chat id 不能为空")
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
	subject := fmt.Sprintf("iOS备份通知: %s", sanitizeMailHeader(message.Title))
	body := fmt.Sprintf(`
设备: %s
类型: %s
级别: %s
时间: %s

%s
`, message.DeviceName, message.Type, message.Level, message.Timestamp.Format("2006-01-02 15:04:05"), message.Content)
	mailMessage := fmt.Sprintf("To: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", notifier.config.To, subject, body)
	address := fmt.Sprintf("%s:%d", notifier.config.SMTPHost, notifier.config.SMTPPort)
	if err := notifier.sendSMTP(address, []byte(mailMessage)); err != nil {
		return fmt.Errorf("发送邮件失败: %v", err)
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
			return fmt.Errorf("SMTP 服务未提供 STARTTLS，拒绝发送凭据")
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
		return fmt.Errorf("SMTP主机不能为空")
	}
	if notifier.config.SMTPPort <= 0 || notifier.config.SMTPPort > 65535 {
		return fmt.Errorf("SMTP端口必须在 1 到 65535 之间")
	}
	if notifier.config.Username == "" {
		return fmt.Errorf("用户名不能为空")
	}
	if notifier.config.Password == "" {
		return fmt.Errorf("密码不能为空")
	}
	if notifier.config.From == "" {
		return fmt.Errorf("发件人不能为空")
	}
	if notifier.config.To == "" {
		return fmt.Errorf("收件人不能为空")
	}
	for field, address := range map[string]string{"发件人": notifier.config.From, "收件人": notifier.config.To} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != strings.TrimSpace(address) || strings.ContainsAny(address, "\r\n") {
			return fmt.Errorf("%s地址无效", field)
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
	content := fmt.Sprintf("%s\n\n%s\n\n时间: %s", message.Title, message.Content, message.Timestamp.Format("2006-01-02 15:04:05"))
	data := map[string]interface{}{"msgtype": "text", "text": map[string]string{"content": content}}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("构建请求数据失败: %v", err)
	}
	client := notifier.client
	if notifier.allowUnsafeTestTarget {
		client = newNotificationHTTPClientPolicy(nil, true)
	}
	response, err := client.Post(notifier.config.WebhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("企业微信发送请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("企业微信API返回错误状态码: %d", response.StatusCode)
	}
	return nil
}

func (notifier *WecomNotifier) GetName() string {
	return fmt.Sprintf("Wecom_%s", notifier.config.Name)
}
func (notifier *WecomNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *WecomNotifier) Validate() error {
	if notifier.config.WebhookURL == "" {
		return fmt.Errorf("webhook url 不能为空")
	}
	if !notifier.allowUnsafeTestTarget && !strings.HasPrefix(notifier.config.WebhookURL, "https://") {
		return fmt.Errorf("webhook url 必须以 https:// 开头")
	}
	parsed, err := url.Parse(notifier.config.WebhookURL)
	if err != nil || (!notifier.allowUnsafeTestTarget && !strings.EqualFold(parsed.Hostname(), "qyapi.weixin.qq.com")) {
		return fmt.Errorf("企业微信 webhook 必须使用 qyapi.weixin.qq.com")
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
		return fmt.Errorf("构建Bark请求数据失败: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, notifier.endpoint(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("创建Bark请求失败: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := notifier.client
	if notifier.allowUnsafeTestTarget {
		client = newNotificationHTTPClientPolicy(nil, true)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Bark发送请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bark返回错误状态码: %d", response.StatusCode)
	}
	return nil
}

func (notifier *BarkNotifier) GetName() string {
	return fmt.Sprintf("Bark_%s", notifier.config.Name)
}
func (notifier *BarkNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *BarkNotifier) Validate() error {
	if notifier.config.ServerURL == "" {
		return fmt.Errorf("Bark服务器地址不能为空")
	}
	if len(notifier.config.ServerURL) > 4096 {
		return fmt.Errorf("Bark服务器地址过长")
	}
	if notifier.config.DeviceKey == "" {
		return fmt.Errorf("Bark设备Key不能为空")
	}
	if strings.ContainsAny(notifier.config.DeviceKey, "\r\n") || len(notifier.config.DeviceKey) > 256 {
		return fmt.Errorf("Bark设备Key无效")
	}
	if err := validateOutboundURL(notifier.config.ServerURL, notifier.allowPrivate, notifier.allowUnsafeTestTarget); err != nil {
		return err
	}
	parsed, err := url.Parse(notifier.config.ServerURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("Bark服务器地址无效")
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
		return fmt.Errorf("构建请求数据失败: %v", err)
	}
	method := strings.ToUpper(notifier.config.Method)
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequest(method, notifier.config.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
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
		return fmt.Errorf("Webhook 发送请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook 返回错误状态码: %d", response.StatusCode)
	}
	return nil
}

func (notifier *WebhookNotifier) GetName() string {
	return fmt.Sprintf("Webhook_%s", notifier.config.Name)
}
func (notifier *WebhookNotifier) IsEnabled() bool { return notifier.config.Enabled }
func (notifier *WebhookNotifier) Validate() error {
	if notifier.config.URL == "" {
		return fmt.Errorf("URL不能为空")
	}
	if len(notifier.config.URL) > 4096 {
		return fmt.Errorf("URL 过长")
	}
	if err := validateOutboundURL(notifier.config.URL, notifier.allowPrivate, notifier.allowUnsafeTestTarget); err != nil {
		return err
	}
	method := strings.ToUpper(notifier.config.Method)
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return fmt.Errorf("webhook method 仅支持 POST、PUT 或 PATCH")
	}
	return validateWebhookHeaders(notifier.config.Headers)
}

func validateWebhookHeaders(headers map[string]string) error {
	if len(headers) > 20 {
		return fmt.Errorf("webhook headers 最多 20 个")
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
			return fmt.Errorf("webhook header %q 不允许", name)
		}
	}
	if total > 8192 {
		return fmt.Errorf("webhook headers 总大小超过 8 KiB")
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
