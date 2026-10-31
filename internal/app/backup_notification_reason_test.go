package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"iosbackup/internal/i18n"
	notificationpkg "iosbackup/internal/notification"
)

func TestBackupNotificationPreservesSpecificFailureReason(t *testing.T) {
	const deviceName = "我的 ${reason} iPhone"
	for _, failure := range []struct {
		name, code, english, chinese string
		battery                      int
		charging                     bool
	}{
		{"low battery", "api.low_battery", "Battery is at 9%; at least 20% is required.", "当前电量为 9%，至少需要 20%。", 9, true},
		{"not charging", "api.not_charging", "Connect the device to power before backing up.", "请为设备接通电源后再备份。", 80, false},
	} {
		for _, language := range []string{"en", "zh-CN"} {
			t.Run(failure.name+"/"+language, func(t *testing.T) {
				app := newBackupControlTestApp(t)
				app.devices["CONTROL"].Name = deviceName
				app.devices["CONTROL"].BatteryLevel = failure.battery
				app.devices["CONTROL"].IsCharging = failure.charging
				app.configs["CONTROL"].OnlyWhenCharging = true
				app.configs["CONTROL"].MinBatteryLevel = 20
				app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
					t.Error("条件校验失败不应启动备份工具")
					return newExecCmd(ctx, "/bin/sh", "-c", "exit 1")
				}
				manager, err := notificationpkg.NewManagerFromConfig(&notificationConfig{Enabled: true, Language: language}, nil, notificationpkg.Options{WorkerCount: 1, Logf: func(string, ...any) {}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(manager.Close)
				recorder := &MockNotifier{name: "reason", enabled: true}
				if err := manager.AddNotifier(recorder); err != nil {
					t.Fatal(err)
				}
				manager.SetRules(map[string][]string{string(notificationBackupFailed): {recorder.name}})
				app.replaceNotificationManager(manager)
				wantReason := failure.english
				if language == "zh-CN" {
					wantReason = failure.chinese
				}
				for _, custom := range []bool{false, true} {
					if custom {
						if err := manager.SetTemplates(map[string]string{string(notificationBackupFailed): "用户模板 ${device_name} | ${reason} | ${diagnostics}"}); err != nil {
							t.Fatal(err)
						}
					}
					err := app.PerformBackup("CONTROL")
					var message *i18n.Error
					if !errors.As(err, &message) || message.Code != failure.code {
						t.Fatalf("错误没有经过真实备份条件校验: %v", err)
					}
					got := recorder.WaitForSends(t, 1)
					if got.Locale != language || got.ReasonCode != "backup_failed" || got.EventCode != "backup_failed" || got.Type != notificationBackupFailed || got.Level != notificationLevelError || got.DeviceName != deviceName || got.DeviceUDID != "CONTROL" {
						t.Fatalf("通知公开字段改变: %+v", got)
					}
					if got.ExtraFields["reason"] != wantReason || got.ExtraFields["diagnostics"] != err.Error() {
						t.Errorf("具体失败原因丢失或诊断被改写: reason=%q diagnostics=%q，期望 reason=%q diagnostics=%q", got.ExtraFields["reason"], got.ExtraFields["diagnostics"], wantReason, err.Error())
					}
					wantBody := "Backup failed for device " + deviceName + "\nReason: " + wantReason
					if language == "zh-CN" {
						wantBody = "设备 " + deviceName + " 备份失败\n原因: " + wantReason
					}
					if custom {
						wantBody = "用户模板 " + deviceName + " | " + wantReason + " | " + err.Error()
					}
					if got.Content != wantBody {
						t.Errorf("通知正文未使用具体原因或单遍插值: %q，期望 %q", got.Content, wantBody)
					}
					data, marshalErr := json.Marshal(got)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					if strings.Contains(string(data), "reason_message") || strings.Contains(string(data), failure.code) || !strings.Contains(string(data), `"reason_code":"backup_failed"`) {
						t.Errorf("内部具体原因元数据泄漏到 Webhook 协议: %s", data)
					}
				}
			})
		}
	}
}
