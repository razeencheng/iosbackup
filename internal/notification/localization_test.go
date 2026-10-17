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
	"sort"
	"strings"
	"testing"
	"time"
)

func localizedTestManager(t *testing.T, language string) (*Manager, *recordingNotifier) {
	t.Helper()
	var config Config
	if err := json.Unmarshal([]byte(`{"enabled":true,"language":"`+language+`"}`), &config); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerFromConfig(&config, nil, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	recorder := &recordingNotifier{name: "capture", enabled: true}
	if err := manager.AddNotifier(recorder); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"capture"}, string(BackupFailed): {"capture"}, string(SystemError): {"capture"}})
	return manager, recorder
}

func TestTestMessagesAndKnownReasonsUseInstanceLanguage(t *testing.T) {
	manager, recorder := localizedTestManager(t, "en")
	message := manager.NewTestMessage(BackupFailed)
	manager.SendAndWait(context.Background(), message)
	got := recorder.last.Load()
	if got.DeviceName != "Test device" || !strings.Contains(got.Title, "Test") || !strings.Contains(got.Content, "test notification") {
		t.Fatalf("test message: %+v", got)
	}
	if !strings.Contains(got.Title, "Backup failed") || got.ExtraFields["reason"] == "" {
		t.Fatalf("test event lost its selected type or reason: %+v", got)
	}
	if manager.NewTestMessage(Type("unknown")) != nil {
		t.Fatal("unknown test event accepted")
	}
	manager.SendAndWait(context.Background(), &Message{Type: BackupFailed, ReasonCode: "api.backup_stalled", DeviceName: "Phone", ExtraFields: map[string]string{"reason": "原始错误"}})
	got = recorder.last.Load()
	if !strings.Contains(got.Content, "No backup activity") || got.ExtraFields["diagnostics"] != "原始错误" {
		t.Fatalf("known reason diagnostics: %+v", got)
	}
	manager.SendAndWait(context.Background(), &Message{Type: BackupFailed, EventCode: string(BackupFailed), DeviceName: "Phone", ExtraFields: map[string]string{"reason": "未知错误：xyz"}})
	got = recorder.last.Load()
	if !strings.Contains(got.Content, "Check the diagnostic logs") || got.ExtraFields["diagnostics"] != "未知错误：xyz" {
		t.Fatalf("unknown reason: %+v", got)
	}
}

func TestEnglishChannelShellsUseEnglishAndExplicitOffset(t *testing.T) {
	message := &Message{Locale: "en", Type: BackupSuccess, Level: LevelInfo, Title: "✅ Backup completed", Content: "Done", DeviceName: "我的 iPhone", Timestamp: time.Date(2026, 10, 11, 12, 34, 56, 0, time.FixedZone("CST", 8*60*60))}
	t.Run("wecom", func(t *testing.T) {
		notifier := NewWecomNotifier(WecomConfig{WebhookURL: "https://qyapi.weixin.qq.com/test"})
		notifier.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), "Time:") || strings.Contains(string(data), "时间:") || !strings.Contains(string(data), "+08:00") {
				t.Errorf("shell: %s", data)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}
		if err := notifier.Send(message); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("email", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		captured := make(chan string, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				captured <- ""
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprint(conn, "220 localhost ESMTP\r\n")
			r := bufio.NewReader(conn)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.HasPrefix(line, "DATA") {
					fmt.Fprint(conn, "354 continue\r\n")
					var data strings.Builder
					for {
						line, err = r.ReadString('\n')
						if err != nil {
							return
						}
						if line == ".\r\n" {
							break
						}
						data.WriteString(line)
					}
					captured <- data.String()
					fmt.Fprint(conn, "250 OK\r\n")
				} else {
					fmt.Fprint(conn, "250 OK\r\n")
				}
			}
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		var portNumber int
		fmt.Sscan(port, &portNumber)
		notifier := NewEmailNotifier(EmailConfig{SMTPHost: host, SMTPPort: portNumber, From: "sender@example.com", To: "receiver@example.com"})
		if err := notifier.Send(message); err != nil {
			t.Fatal(err)
		}
		select {
		case data := <-captured:
			parsed, err := mail.ReadMessage(strings.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(parsed.Body)
			if subject != "iOS Backup notification: ✅ Backup completed" || !strings.Contains(string(body), "Device: 我的 iPhone") || !strings.Contains(string(body), "+08:00") {
				t.Fatalf("mail subject=%q body=%s", subject, body)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("mail capture timed out")
		}
	})
}

func TestInstanceLanguageRendersBackgroundNotifications(t *testing.T) {
	manager, recorder := localizedTestManager(t, "en")
	manager.SendBackupSuccess("我的 iPhone", "test")
	result := manager.SendAndWait(context.Background(), &Message{Type: BackupSuccess, DeviceName: "我的 iPhone"})
	if result.Succeeded != 1 {
		t.Fatalf("delivery: %+v", result)
	}
	got := recorder.last.Load()
	if !strings.Contains(got.Title, "Backup completed") || got.Content != "Device 我的 iPhone was backed up successfully" {
		t.Fatalf("English notification lost instance locale or user name: %+v", got)
	}
}

func TestUnknownNotificationLanguageIsRejectedEvenWhenDisabled(t *testing.T) {
	var config Config
	_ = json.Unmarshal([]byte(`{"language":"fr","enabled":false}`), &config)
	manager, err := NewManagerFromConfig(&config, nil, Options{})
	if manager != nil {
		manager.Close()
	}
	if err == nil {
		t.Fatal("invalid language accepted")
	}
}

func TestEmptyTemplatesRemainEmptyAndCustomTemplatesRemainVerbatim(t *testing.T) {
	manager, recorder := localizedTestManager(t, "en")
	if err := manager.SetTemplates(map[string]string{string(BackupSuccess): ""}); err != nil {
		t.Fatal(err)
	}
	if manager.Templates()[string(BackupSuccess)] != "" {
		t.Fatal("default text materialized as a custom template")
	}
	manager.SendAndWait(context.Background(), &Message{Type: BackupSuccess, DeviceName: "Phone"})
	if got := recorder.last.Load().Content; got != "Device Phone was backed up successfully" {
		t.Fatalf("default: %q", got)
	}
	const custom = "用户自己的模板 ${device_name} ${title}"
	if err := manager.SetTemplates(map[string]string{string(BackupSuccess): custom}); err != nil {
		t.Fatal(err)
	}
	manager.SendAndWait(context.Background(), &Message{Type: BackupSuccess, DeviceName: "Phone"})
	if got := recorder.last.Load().Content; got != "用户自己的模板 Phone ✅ Backup completed" {
		t.Fatalf("custom template changed: %q", got)
	}
}

func TestSystemEventsHaveActionableEnglishCopy(t *testing.T) {
	manager, recorder := localizedTestManager(t, "en")
	for _, event := range []string{"pairing_required", "connection_service_unavailable", "encryption_password_store_failed", "password_store_failed", "encryption_failed", "password_change_failed", "restore_failed", "unpack_failed"} {
		t.Run(event, func(t *testing.T) {
			manager.SendAndWait(context.Background(), &Message{Type: SystemError, EventCode: event, DeviceName: "Phone", Params: map[string]string{"backend": "netmuxd"}})
			got := recorder.last.Load()
			if got.Title == "⚠️ System error" || got.Content == copyFor("en", "body.system_error") {
				t.Fatalf("specific system event fell back: %+v", got)
			}
		})
	}
	manager.SendAndWait(context.Background(), &Message{Type: BackupFailed, ReasonCode: "storage_unavailable", DeviceName: "Phone"})
	if got := recorder.last.Load().Content; !strings.Contains(got, "Storage is unavailable") {
		t.Fatalf("storage reason: %q", got)
	}
}

func TestNotificationCatalogKeysAndParametersStayAligned(t *testing.T) {
	parameters := func(text string) []string {
		values := templateVariablePattern.FindAllString(text, -1)
		sort.Strings(values)
		return values
	}
	if len(notificationCopy["en"]) != len(notificationCopy["zh-CN"]) {
		t.Fatal("catalog key counts differ")
	}
	for key, english := range notificationCopy["en"] {
		chinese, ok := notificationCopy["zh-CN"][key]
		if !ok || english == "" || chinese == "" {
			t.Errorf("missing translation: %s", key)
		}
		if !reflect.DeepEqual(parameters(english), parameters(chinese)) {
			t.Errorf("translation parameters differ: %s", key)
		}
	}
}

func TestNotificationPreservesLiteralUnknownDeviceName(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			manager, recorder := localizedTestManager(t, language)
			manager.SendAndWait(context.Background(), &Message{Type: BackupSuccess, DeviceName: "Unknown Device", DeviceUDID: "PHONE"})
			got := recorder.last.Load()
			if got.DeviceName != "Unknown Device" || !strings.Contains(got.Content, "Unknown Device") {
				t.Fatalf("user's device name translated: %+v", got)
			}
		})
	}
}

func TestCodedUnknownFailureKeepsDiagnosticsAndLocalizesReason(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			manager, recorder := localizedTestManager(t, language)
			const raw = "raw diagnostic with 原始输出"
			manager.SendAndWait(context.Background(), &Message{Type: BackupFailed, EventCode: string(BackupFailed), ExtraFields: map[string]string{"reason": raw}})
			got := recorder.last.Load()
			if got.ExtraFields["reason"] != copyFor(language, "reason.unknown") || got.ExtraFields["diagnostics"] != raw || strings.Contains(got.Content, raw) {
				t.Fatalf("untranslated reason or lost diagnostics: %+v", got)
			}
			if err := manager.SetTemplates(map[string]string{string(BackupFailed): "${reason}\n${diagnostics}"}); err != nil {
				t.Fatal(err)
			}
			manager.SendAndWait(context.Background(), &Message{Type: BackupFailed, EventCode: string(BackupFailed), ExtraFields: map[string]string{"reason": raw}})
			if got := recorder.last.Load().Content; got != copyFor(language, "reason.unknown")+"\n"+raw {
				t.Fatalf("custom diagnostics template: %q", got)
			}
		})
	}
}
