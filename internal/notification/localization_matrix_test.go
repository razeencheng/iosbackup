package notification

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotificationEventLanguageMatrix(t *testing.T) {
	const device = "我的 ${reason} iPhone <&>"
	const diagnostics = "原始工具输出: /backups/家庭 ${title}"
	events := []struct {
		event                            Type
		level                            Level
		titleEN, titleZH, bodyEN, bodyZH string
		send                             func(*Manager)
	}{
		{BackupStart, LevelInfo, "✅ Backup started", "✅ 设备备份开始", "Backup started for device " + device, "设备 " + device + " 开始备份", func(m *Manager) { m.SendBackupStart(device, "PHONE") }},
		{BackupSuccess, LevelInfo, "✅ Backup completed", "✅ 设备备份完成", "Device " + device + " was backed up successfully", "设备 " + device + " 备份成功完成", func(m *Manager) { m.SendBackupSuccess(device, "PHONE") }},
		{BackupFailed, LevelError, "❌ Backup failed", "❌ 设备备份失败", "Backup failed for device " + device + "\nReason: No backup activity was detected within the time limit, so the job stopped.", "设备 " + device + " 备份失败\n原因: 长时间未检测到备份活动，任务已停止。", func(m *Manager) { m.SendBackupFailure(device, "PHONE", "api.backup_stalled", diagnostics) }},
		{DeviceOnline, LevelInfo, "✅ Device connected", "✅ 设备上线", "Device " + device + " connected", "设备 " + device + " 已连接", func(m *Manager) { m.SendDeviceOnline(device, "PHONE") }},
		{DeviceOffline, LevelWarning, "❌ Device disconnected", "❌ 设备离线", "Device " + device + " disconnected", "设备 " + device + " 已断开连接", func(m *Manager) { m.SendDeviceOffline(device, "PHONE") }},
		{SystemError, LevelError, "⚠️ System error", "⚠️ 系统错误", "The operation could not be completed. Check the application status and diagnostic logs.", "操作未能完成。请查看应用状态和日志中的诊断信息。", func(m *Manager) {
			m.SendSystemEvent("system_error", map[string]string{"device_name": device, "device_udid": "PHONE", "diagnostics": diagnostics})
		}},
	}
	for _, event := range events {
		for _, language := range []string{"en", "zh-CN"} {
			for _, kind := range []string{"background", "test"} {
				t.Run(string(event.event)+"/"+language+"/"+kind, func(t *testing.T) {
					manager, recorder := localizedTestManager(t, language)
					recorder.started = make(chan struct{})
					manager.SetRules(map[string][]string{string(event.event): {recorder.name}})
					title, body := event.titleEN, event.bodyEN
					wantDevice, wantUDID := device, "PHONE"
					if language == "zh-CN" {
						title, body = event.titleZH, event.bodyZH
					}
					if kind == "test" {
						wantUDID = "test-device"
						wantDevice, title, body = "Test device", "🧪 Test notification: "+title, "This is a test notification to verify notification settings."
						if language == "zh-CN" {
							wantDevice, title, body = "测试设备", "🧪 测试通知: "+event.titleZH, "这是一条测试通知，用于验证通知配置。"
						}
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						if result := manager.SendAndWait(ctx, manager.NewTestMessage(event.event)); result.Succeeded != 1 || result.Failed != 0 {
							t.Fatalf("测试发送失败: %+v", result)
						}
					} else {
						event.send(manager)
					}
					select {
					case <-recorder.started:
					case <-time.After(time.Second):
						t.Fatal("后台通知未送达")
					}
					got := recorder.last.Load()
					if got.Locale != language || got.Type != event.event || got.EventCode != string(event.event) || got.Level != event.level || got.Timestamp.IsZero() {
						t.Fatalf("机器字段或语言错误: %+v", got)
					}
					if got.Title != title || got.Content != body || got.DeviceName != wantDevice || got.DeviceUDID != wantUDID {
						t.Fatalf("通知文案不符:\ngot %+v\nwant title=%q body=%q device=%q udid=%q", got, title, body, wantDevice, wantUDID)
					}
					if kind == "background" && (event.event == BackupFailed || event.event == SystemError) && got.ExtraFields["diagnostics"] != diagnostics {
						t.Fatalf("诊断原文被修改: %+v", got)
					}
					if event.event == BackupFailed {
						if kind == "background" && got.ReasonCode != "api.backup_stalled" {
							t.Fatalf("原因码改变: %+v", got)
						}
						if kind == "test" {
							reason := "Simulated failure for notification testing"
							if language == "zh-CN" {
								reason = "用于测试通知的模拟故障"
							}
							if got.ExtraFields["reason"] != reason {
								t.Fatalf("测试失败原因未本地化: %+v", got)
							}
						}
					}
				})
			}
		}
	}
}

func TestNotificationChannelLanguageMatrix(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, channel := range []string{"telegram", "wecom", "bark", "webhook", "email"} {
			t.Run(channel+"/"+language, func(t *testing.T) {
				message := &Message{Locale: language, EventCode: "backup_failed", ReasonCode: "api.device_locked", Type: BackupFailed, Level: LevelError, Title: "❌ Backup failed", Content: "用户正文 ${title} /backups/家庭 <&>\nsecond line", DeviceName: "我的 ${reason} iPhone", DeviceUDID: "PHONE", Timestamp: time.Date(2026, 10, 31, 0, 1, 2, 0, time.FixedZone("CST", 8*60*60)), ExtraFields: map[string]string{"diagnostics": "原始诊断 ${content}"}}
				label, subject, deviceLabel, typeLabel, levelLabel := "Time", "iOS Backup notification", "Device", "Type", "Level"
				if language == "zh-CN" {
					message.Title = "❌ 设备备份失败"
					label, subject, deviceLabel, typeLabel, levelLabel = "时间", "iOS备份通知", "设备", "类型", "级别"
				}
				stamp := "2026-10-31T00:01:02+08:00"
				if channel == "email" {
					config, captured := localizationSMTPReceiver(t)
					if err := NewEmailNotifier(config).Send(message); err != nil {
						t.Fatal(err)
					}
					var data string
					select {
					case data = <-captured:
					case <-time.After(3 * time.Second):
						t.Fatal("本机 SMTP 未收到邮件")
					}
					parsed, err := mail.ReadMessage(strings.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					gotSubject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(parsed.Body)
					if err != nil {
						t.Fatal(err)
					}
					wantBody := fmt.Sprintf("%s: %s\n%s: backup_failed\n%s: error\n%s: %s\n\n%s\n", deviceLabel, message.DeviceName, typeLabel, levelLabel, label, stamp, message.Content)
					if gotSubject != subject+": "+message.Title || strings.ReplaceAll(string(body), "\r\n", "\n") != wantBody {
						t.Fatalf("邮件格式错误: subject=%q body=%q", gotSubject, body)
					}
					return
				}
				var payload map[string]any
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
						t.Errorf("请求格式错误: %s %v", r.Method, r.Header)
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				})}
				var adapter Notifier
				switch channel {
				case "telegram":
					n := NewTelegramNotifier(TelegramConfig{BotToken: "test-only", ChatID: "chat-machine-id"})
					n.client = client
					adapter = n
				case "wecom":
					n := NewWecomNotifier(WecomConfig{WebhookURL: "https://qyapi.weixin.qq.com/test-only"})
					n.client = client
					adapter = n
				case "bark":
					n := NewBarkNotifier(BarkConfig{ServerURL: "https://example.invalid", DeviceKey: "test-only"})
					n.client = client
					adapter = n
				case "webhook":
					n := NewWebhookNotifier(WebhookConfig{URL: "https://example.invalid/test-only", Method: http.MethodPost})
					n.client = client
					adapter = n
				}
				if err := adapter.Send(message); err != nil {
					t.Fatal(err)
				}
				switch channel {
				case "telegram":
					want := "📱 *" + message.Title + "*\n\n" + message.Content + "\n\n⏰ " + stamp
					if payload["text"] != want || payload["chat_id"] != "chat-machine-id" || payload["parse_mode"] != "Markdown" {
						t.Fatalf("Telegram正文或机器字段改变: %+v", payload)
					}
				case "wecom":
					want := map[string]any{"msgtype": "text", "text": map[string]any{"content": message.Title + "\n\n" + message.Content + "\n\n" + label + ": " + stamp}}
					if !reflect.DeepEqual(payload, want) {
						t.Fatalf("WeCom格式错误: %+v", payload)
					}
				case "bark":
					if !reflect.DeepEqual(payload, map[string]any{"title": message.Title, "body": message.Content}) {
						t.Fatalf("Bark正文改变: %+v", payload)
					}
				case "webhook":
					want := map[string]any{"locale": language, "event_code": "backup_failed", "reason_code": "api.device_locked", "type": "backup_failed", "level": "error", "title": message.Title, "content": message.Content, "device_name": message.DeviceName, "device_udid": "PHONE", "timestamp": stamp, "extra_fields": map[string]any{"diagnostics": "原始诊断 ${content}"}}
					if !reflect.DeepEqual(payload, want) {
						t.Fatalf("Webhook机器字段或用户原文改变: %+v", payload)
					}
				}
			})
		}
	}
}

// localizationSMTPReceiver 只监听回环地址，测试不使用外部邮件服务。
func localizationSMTPReceiver(t *testing.T) (EmailConfig, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	captured := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(conn, "220 localhost ESMTP\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "DATA") {
				fmt.Fprint(conn, "354 continue\r\n")
				var data strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					data.WriteString(line)
				}
				captured <- data.String()
			}
			fmt.Fprint(conn, "250 OK\r\n")
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return EmailConfig{SMTPHost: host, SMTPPort: portNumber, From: "sender@example.invalid", To: "receiver@example.invalid"}, captured
}

func TestQueuedNotificationKeepsLanguageAndTemplateSnapshot(t *testing.T) {
	manager, err := NewManagerFromConfig(&Config{Enabled: true, Language: "en"}, nil, Options{WorkerCount: 1, QueueSize: 4, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); manager.Close() })
	blocker := &recordingNotifier{name: "blocker", enabled: true, started: make(chan struct{}), unblock: release}
	queued := &recordingNotifier{name: "queued", enabled: true, started: make(chan struct{})}
	for _, notifier := range []Notifier{blocker, queued} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocker"}})
	manager.SendBackupSuccess("blocker", "BLOCKER")
	select {
	case <-blocker.started:
	case <-time.After(time.Second):
		t.Fatal("未能阻塞工作线程")
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"queued"}})
	if err := manager.SetTemplates(map[string]string{string(BackupSuccess): "旧模板 ${title}: ${device_name}"}); err != nil {
		t.Fatal(err)
	}
	manager.SendBackupSuccess("我的 ${title} iPhone", "PHONE")
	if len(manager.queue) != 1 {
		t.Fatal("待检验消息尚未留在队列中")
	}
	// 应用保存配置时替换管理器；这里还直接改变原管理器，验证队列持有完整消息快照。
	manager.mu.Lock()
	manager.language = "zh-CN"
	manager.mu.Unlock()
	if err := manager.SetTemplates(map[string]string{string(BackupSuccess): "新模板 ${title}: ${device_name}"}); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	select {
	case <-queued.started:
	case <-time.After(time.Second):
		t.Fatal("排队消息未送达")
	}
	old := CloneMessage(*queued.last.Load())
	if old.Locale != "en" || old.Title != "✅ Backup completed" || old.Content != "旧模板 ✅ Backup completed: 我的 ${title} iPhone" {
		t.Fatalf("排队消息被新设置改写: %+v", old)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result := manager.SendAndWait(ctx, &Message{Type: BackupSuccess, EventCode: string(BackupSuccess), DeviceName: "我的 ${title} iPhone"}); result.Succeeded != 1 {
		t.Fatalf("新消息发送失败: %+v", result)
	}
	if got := queued.last.Load(); got.Locale != "zh-CN" || got.Content != "新模板 ✅ 设备备份完成: 我的 ${title} iPhone" {
		t.Fatalf("新消息未采用新设置: %+v", got)
	}
}
