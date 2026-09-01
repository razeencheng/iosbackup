package notification

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestAdapterNamesPreservePublicPrefixes(t *testing.T) {
	tests := []struct {
		want     string
		notifier Notifier
	}{
		{"Telegram_primary", NewTelegramNotifier(TelegramConfig{Name: "primary"})},
		{"Email_primary", NewEmailNotifier(EmailConfig{Name: "primary"})},
		{"Wecom_primary", NewWecomNotifier(WecomConfig{Name: "primary"})},
		{"Bark_primary", NewBarkNotifier(BarkConfig{Name: "primary"})},
		{"Webhook_primary", NewWebhookNotifier(WebhookConfig{Name: "primary"})},
	}
	for _, test := range tests {
		if got := test.notifier.GetName(); got != test.want {
			t.Errorf("GetName() = %q, want %q", got, test.want)
		}
	}
}

func TestTelegramSendUsesExpectedPayloadAndHidesTransportDetails(t *testing.T) {
	notifier := NewTelegramNotifier(TelegramConfig{Name: "main", BotToken: "top-secret", ChatID: "chat", Enabled: true})
	notifier.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.Contains(req.URL.Path, "/bottop-secret/sendMessage") {
			t.Fatalf("unexpected Telegram request: %s %s", req.Method, req.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["chat_id"] != "chat" || body["parse_mode"] != "Markdown" || !strings.Contains(body["text"].(string), "完成") {
			t.Fatalf("unexpected Telegram body: %+v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	if err := notifier.Send(&Message{Title: "完成", Content: "正文", Timestamp: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	notifier.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial top-secret.example: refused")
	})}
	if err := notifier.Send(&Message{Title: "失败"}); err == nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("transport error must be generic and secret-free: %v", err)
	}
	notifier.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	if err := notifier.Send(&Message{}); err == nil {
		t.Fatal("Telegram accepted an error status")
	}
}

func TestHTTPAdaptersMethodsHeadersStatusesAndValidation(t *testing.T) {
	message := &Message{
		Type: BackupFailed, Level: LevelError, Title: "备份失败", Content: "磁盘空间不足",
		DeviceName: "iPhone 15", DeviceUDID: "test-udid",
		Timestamp:   time.Date(2026, 8, 31, 10, 20, 30, 0, time.FixedZone("CST", 8*60*60)),
		ExtraFields: map[string]string{"reason": "disk_full"},
	}
	handlerErrors := make(chan error, 16)
	recordHandlerError := func(format string, args ...any) {
		select {
		case handlerErrors <- fmt.Errorf(format, args...):
		default:
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/webhook":
			if req.Method != http.MethodPatch || req.Header.Get("Authorization") != "Bearer test" || req.Header.Get("Content-Type") != "application/json" {
				recordHandlerError("unexpected webhook request: %s headers=%v", req.Method, req.Header)
			}
			var got Message
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				recordHandlerError("decode webhook body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got.Type != message.Type || got.Level != message.Level || got.Title != message.Title || got.Content != message.Content || got.DeviceName != message.DeviceName || got.DeviceUDID != message.DeviceUDID || !got.Timestamp.Equal(message.Timestamp) || got.ExtraFields["reason"] != "disk_full" {
				recordHandlerError("webhook message lost fields: %+v", got)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/bad-webhook", "/bark/bad-key", "/bad-wecom":
			w.WriteHeader(http.StatusBadGateway)
		case "/bark/device-key":
			if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
				recordHandlerError("unexpected Bark request: %s headers=%v", req.Method, req.Header)
			}
			var got map[string]string
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				recordHandlerError("decode Bark body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got["title"] != message.Title || got["body"] != message.Content {
				recordHandlerError("Bark body lost fields: %+v", got)
			}
			w.WriteHeader(http.StatusCreated)
		case "/wecom":
			if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
				recordHandlerError("unexpected WeCom request: %s headers=%v", req.Method, req.Header)
			}
			var got struct {
				MsgType string `json:"msgtype"`
				Text    struct {
					Content string `json:"content"`
				} `json:"text"`
			}
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				recordHandlerError("decode WeCom body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got.MsgType != "text" || !strings.Contains(got.Text.Content, message.Title) || !strings.Contains(got.Text.Content, message.Content) || !strings.Contains(got.Text.Content, "2026-08-31 10:20:30") {
				recordHandlerError("WeCom body lost fields: %+v", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()

	webhook := NewWebhookNotifier(WebhookConfig{Name: "hook", URL: server.URL + "/webhook", Method: "PATCH", Headers: map[string]string{"Authorization": "Bearer test"}, Enabled: true})
	webhook.allowUnsafeTestTarget = true
	if err := webhook.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := webhook.Send(message); err != nil {
		t.Fatal(err)
	}
	badWebhook := NewWebhookNotifier(WebhookConfig{Name: "bad", URL: server.URL + "/bad-webhook", Method: "POST", Enabled: true})
	badWebhook.allowUnsafeTestTarget = true
	if err := badWebhook.Send(&Message{}); err == nil {
		t.Fatal("Webhook accepted an error status")
	}

	bark := NewBarkNotifier(BarkConfig{Name: "phone", ServerURL: server.URL + "/bark", DeviceKey: "device-key", Enabled: true})
	bark.allowUnsafeTestTarget = true
	if err := bark.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := bark.Send(message); err != nil {
		t.Fatal(err)
	}
	badBark := NewBarkNotifier(BarkConfig{Name: "bad", ServerURL: server.URL + "/bark", DeviceKey: "bad-key", Enabled: true})
	badBark.allowUnsafeTestTarget = true
	if err := badBark.Send(&Message{}); err == nil {
		t.Fatal("Bark accepted an error status")
	}

	wecom := NewWecomNotifier(WecomConfig{Name: "corp", WebhookURL: server.URL + "/wecom", Enabled: true})
	wecom.allowUnsafeTestTarget = true
	if err := wecom.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := wecom.Send(message); err != nil {
		t.Fatal(err)
	}
	badWecom := NewWecomNotifier(WecomConfig{Name: "bad", WebhookURL: server.URL + "/bad-wecom", Enabled: true})
	badWecom.allowUnsafeTestTarget = true
	if err := badWecom.Send(&Message{}); err == nil {
		t.Fatal("WeCom accepted an error status")
	}
	if !webhook.IsEnabled() || !bark.IsEnabled() || !wecom.IsEnabled() {
		t.Fatal("enabled adapter reported disabled")
	}
	assertNoHandlerErrors(t, handlerErrors)

	invalid := []WebhookConfig{
		{Name: "method", URL: "https://example.com", Method: "TRACE", Enabled: true},
		{Name: "host", URL: "https://example.com", Method: "POST", Headers: map[string]string{"Host": "evil"}, Enabled: true},
		{Name: "newline", URL: "https://example.com", Method: "POST", Headers: map[string]string{"X-Test": "a\r\nb"}, Enabled: true},
	}
	for _, config := range invalid {
		if err := NewWebhookNotifier(config).Validate(); err == nil {
			t.Fatalf("unsafe webhook accepted: %+v", config)
		}
	}
}

func assertNoHandlerErrors(t *testing.T, errors <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-errors:
			t.Error(err)
		default:
			return
		}
	}
}

func TestAdapterValidationMatrix(t *testing.T) {
	validTelegram := NewTelegramNotifier(TelegramConfig{Name: "telegram", BotToken: "token", ChatID: "chat", Enabled: true})
	if err := validTelegram.Validate(); err != nil || !validTelegram.IsEnabled() {
		t.Fatalf("valid enabled Telegram config rejected: enabled=%v err=%v", validTelegram.IsEnabled(), err)
	}
	validEmail := NewEmailNotifier(EmailConfig{
		Name: "email", SMTPHost: "smtp.example.com", SMTPPort: 587,
		Username: "user", Password: "password", From: "a@example.com", To: "b@example.com", Enabled: true,
	})
	if err := validEmail.Validate(); err != nil || !validEmail.IsEnabled() {
		t.Fatalf("valid enabled Email config rejected: enabled=%v err=%v", validEmail.IsEnabled(), err)
	}
	validWecom := NewWecomNotifier(WecomConfig{
		Name: "wecom", WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test", Enabled: true,
	})
	if err := validWecom.Validate(); err != nil || !validWecom.IsEnabled() {
		t.Fatalf("valid production WeCom config rejected: enabled=%v err=%v", validWecom.IsEnabled(), err)
	}

	tests := []struct {
		name     string
		notifier Notifier
	}{
		{"telegram token", NewTelegramNotifier(TelegramConfig{Name: "tg", ChatID: "chat", Enabled: true})},
		{"telegram chat", NewTelegramNotifier(TelegramConfig{Name: "tg", BotToken: "token", Enabled: true})},
		{"smtp host injection", NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: "smtp.example.com\r\nX: y", SMTPPort: 587, Username: "u", Password: "p", From: "a@example.com", To: "b@example.com", Enabled: true})},
		{"smtp port", NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: "smtp.example.com", SMTPPort: 0, Username: "u", Password: "p", From: "a@example.com", To: "b@example.com", Enabled: true})},
		{"smtp username", NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: "smtp.example.com", SMTPPort: 587, Password: "p", From: "a@example.com", To: "b@example.com", Enabled: true})},
		{"smtp password", NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: "smtp.example.com", SMTPPort: 587, Username: "u", From: "a@example.com", To: "b@example.com", Enabled: true})},
		{"smtp recipient injection", NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: "smtp.example.com", SMTPPort: 587, Username: "u", Password: "p", From: "a@example.com", To: "b@example.com\r\nBcc: x@example.com", Enabled: true})},
		{"wecom empty URL", NewWecomNotifier(WecomConfig{Name: "corp", Enabled: true})},
		{"wecom non-HTTPS", NewWecomNotifier(WecomConfig{Name: "corp", WebhookURL: "http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test", Enabled: true})},
		{"wecom host", NewWecomNotifier(WecomConfig{Name: "corp", WebhookURL: "https://example.com/hook", Enabled: true})},
		{"webhook empty URL", NewWebhookNotifier(WebhookConfig{Name: "hook", Method: "POST", Enabled: true})},
		{"webhook invalid URL", NewWebhookNotifier(WebhookConfig{Name: "hook", URL: "invalid-url", Method: "POST", Enabled: true})},
		{"bark key", NewBarkNotifier(BarkConfig{Name: "phone", ServerURL: "https://api.day.app", Enabled: true})},
		{"bark query", NewBarkNotifier(BarkConfig{Name: "phone", ServerURL: "https://api.day.app?token=x", DeviceKey: "key", Enabled: true})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.notifier.Validate(); err == nil {
				t.Fatal("invalid adapter config accepted")
			}
		})
	}
	disabled := NewWebhookNotifier(WebhookConfig{Name: "disabled", URL: "https://example.com", Method: "POST"})
	if disabled.IsEnabled() {
		t.Fatal("disabled adapter reported enabled")
	}
}

func TestOutboundPolicyRejectsLocalAddressesAndBoundsRedirects(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1/hook",
		"http://169.254.169.254/latest/meta-data",
		"ftp://example.com/hook",
		"https://user:password@example.com/hook",
	} {
		if err := validateOutboundURL(rawURL, nil, false); err == nil {
			t.Fatalf("unsafe URL accepted: %s", rawURL)
		}
	}
	allowed := netip.MustParsePrefix("10.20.0.0/16")
	if err := validateOutboundIP(netip.MustParseAddr("10.20.1.2"), []netip.Prefix{allowed}); err != nil {
		t.Fatalf("allowlisted private address rejected: %v", err)
	}
	client := newNotificationHTTPClient(nil)
	if client.Timeout <= 0 || client.Timeout > 30*time.Second {
		t.Fatalf("unbounded client timeout: %s", client.Timeout)
	}
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("HTTP TLS minimum = %#v", transport.TLSClientConfig)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	via := []*http.Request{req, req, req}
	if err := client.CheckRedirect(req, via); err == nil {
		t.Fatal("too many redirects accepted")
	}
	localRedirect, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/internal", nil)
	if err := client.CheckRedirect(localRedirect, []*http.Request{req}); err == nil {
		t.Fatal("redirect to a local address was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transport.DialContext(ctx, "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("cancelled/local dial unexpectedly succeeded")
	}
}

func TestSMTPTLSMinimumAndNoCredentialDowngrade(t *testing.T) {
	if config := smtpTLSConfig("smtp.example.com"); config.MinVersion != tls.VersionTLS12 || config.ServerName != "smtp.example.com" {
		t.Fatalf("SMTP TLS config = %+v", config)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.WriteString(connection, "220 localhost ESMTP\r\n")
		line, _ := bufio.NewReader(connection).ReadString('\n')
		if strings.HasPrefix(line, "EHLO ") {
			_, _ = io.WriteString(connection, "250-localhost\r\n250 AUTH PLAIN\r\n")
		}
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	notifier := NewEmailNotifier(EmailConfig{Name: "mail", SMTPHost: host, SMTPPort: port, Username: "user", Password: "secret", From: "a@example.com", To: "b@example.com", Enabled: true})
	notifier.timeout = time.Second
	err = notifier.Send(&Message{Title: "mail"})
	if err == nil || !strings.Contains(err.Error(), "未提供 STARTTLS") {
		t.Fatalf("SMTP credential downgrade was not rejected: %v", err)
	}
	<-serverDone
}

func TestSMTPReturnsAfterDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			time.Sleep(500 * time.Millisecond)
		}
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	notifier := NewEmailNotifier(EmailConfig{Name: "deadline", SMTPHost: host, SMTPPort: port, Username: "user", Password: "secret", From: "a@example.com", To: "b@example.com", Enabled: true})
	notifier.timeout = 50 * time.Millisecond
	started := time.Now()
	if err := notifier.Send(&Message{Title: "deadline"}); err == nil {
		t.Fatal("half-open SMTP server did not time out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SMTP timeout took %s", elapsed)
	}
	_ = listener.Close()
	<-done
}

func TestWebhookHeaderLimits(t *testing.T) {
	tooMany := make(map[string]string)
	for i := 0; i < 21; i++ {
		tooMany[fmt.Sprintf("X-%d", i)] = "v"
	}
	if err := validateWebhookHeaders(tooMany); err == nil {
		t.Fatal("more than 20 headers accepted")
	}
	if err := validateWebhookHeaders(map[string]string{"X-Large": strings.Repeat("x", 8193)}); err == nil {
		t.Fatal("oversized headers accepted")
	}
}

func TestAllowPrivateConstructorsOwnPolicySlice(t *testing.T) {
	callerAllowlist := []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}
	webhook := NewWebhookNotifierWithAllowlist(WebhookConfig{
		Name: "webhook", URL: "https://hooks.example.com/notify", Method: http.MethodPost, Enabled: true,
	}, callerAllowlist)
	bark := NewBarkNotifierWithAllowlist(BarkConfig{
		Name: "bark", ServerURL: "https://bark.example.com", DeviceKey: "device", Enabled: true,
	}, callerAllowlist)

	// If the HTTP transport retained the caller's backing array, this mutation would
	// silently allow the 10/8 destination. A cancelled context guarantees no dial.
	callerAllowlist[0] = netip.MustParsePrefix("10.0.0.0/8")
	for name, client := range map[string]*http.Client{"webhook": webhook.client, "bark": bark.client} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			transport := client.Transport.(*http.Transport)
			_, err := transport.DialContext(ctx, "tcp", "10.20.30.40:443")
			if err == nil || !strings.Contains(err.Error(), "未列入 IOSBK_WEBHOOK_ALLOW_CIDRS") {
				t.Fatalf("constructor retained caller allowlist storage: %v", err)
			}
		})
	}
}

func TestWebhookConstructorOwnsHeaders(t *testing.T) {
	callerHeaders := map[string]string{"Authorization": "Bearer original"}
	notifier := NewWebhookNotifier(WebhookConfig{
		Name: "direct", URL: "https://hooks.example.com/notify", Method: http.MethodPost,
		Headers: callerHeaders, Enabled: true,
	})
	callerHeaders["Authorization"] = "Bearer mutated"
	callerHeaders["Host"] = "forbidden.example.com"
	if err := notifier.Validate(); err != nil {
		t.Fatalf("caller mutation changed notifier validation: %v", err)
	}
	notifier.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer original" {
			t.Errorf("constructor header isolation failed: %q", got)
		}
		if got := req.Header.Get("Host"); got != "" {
			t.Errorf("caller-added forbidden header reached request: %q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if err := notifier.Send(&Message{Type: BackupSuccess}); err != nil {
		t.Fatal(err)
	}
}

func TestManagerFactoryOwnsWebhookHeaders(t *testing.T) {
	callerHeaders := map[string]string{"Authorization": "Bearer factory-original"}
	config := &Config{
		Enabled: true,
		WebhookConfigs: []WebhookConfig{{
			Name: "factory", URL: "https://hooks.example.com/notify", Method: http.MethodPost,
			Headers: callerHeaders, Enabled: true,
		}},
		NotificationRules: map[string][]string{string(BackupSuccess): {"Webhook_factory"}},
	}
	manager, err := NewManagerFromConfig(config, nil, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	callerHeaders["Authorization"] = "Bearer factory-mutated"
	callerHeaders["Host"] = "forbidden.example.com"
	config.WebhookConfigs[0].Headers["X-After-Factory"] = "mutated"

	notifier, ok := manager.notifiers[0].(*WebhookNotifier)
	if !ok {
		t.Fatalf("factory notifier type = %T", manager.notifiers[0])
	}
	if err := notifier.Validate(); err != nil {
		t.Fatalf("caller mutation changed factory notifier validation: %v", err)
	}
	notifier.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer factory-original" {
			t.Errorf("factory header isolation failed: %q", got)
		}
		if req.Header.Get("Host") != "" || req.Header.Get("X-After-Factory") != "" {
			t.Errorf("post-factory caller headers reached request: %v", req.Header)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if err := notifier.Send(&Message{Type: BackupSuccess}); err != nil {
		t.Fatal(err)
	}
}
