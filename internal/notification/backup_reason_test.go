package notification

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestBackupFailureSpecificReasonPrecedesClassification(t *testing.T) {
	const diagnostics = "原始诊断 ${reason} /backups/家庭"
	const device = "我的 ${reason} iPhone"
	for _, language := range []string{"en", "zh-CN"} {
		for _, tc := range []struct {
			name, classification, messageCode, english, chinese string
			params                                              map[string]any
		}{
			{"specific before known classification", "device_locked", "api.low_battery", "Battery is at ${device_name}%; at least 20% is required.", "当前电量为 ${device_name}%，至少需要 20%。", map[string]any{"battery": "${device_name}", "min_battery": 20}},
			{"known reason copy", "backup_failed", "api.backup_stalled", "No backup activity was detected within the time limit, so the job stopped.", "长时间未检测到备份活动，任务已停止。", nil},
			{"unknown specific keeps classification", "device_locked", "api.future_unknown", "Device authorization is required. Check the phone and enter its passcode.", "设备需要授权，请查看手机并输入锁屏密码。", nil},
			{"generic reason stays compatible", "backup_failed", "api.backup_failed", "The backup could not be completed. Check the diagnostic logs.", "备份未能完成，请查看诊断日志。", nil},
			{"unknown reason stays conservative", "future_unknown", "api.future_unknown", "The backup could not be completed. Check the diagnostic logs.", "备份未能完成，请查看诊断日志。", nil},
		} {
			t.Run(language+"/"+tc.name, func(t *testing.T) {
				manager, recorder := localizedTestManager(t, language)
				if err := manager.SetTemplates(map[string]string{string(BackupFailed): "用户模板 ${reason} | ${diagnostics} | ${device_name}"}); err != nil {
					t.Fatal(err)
				}
				message := &Message{Type: BackupFailed, EventCode: "backup_failed", ReasonCode: tc.classification, ReasonMessageCode: tc.messageCode, ReasonMessageParams: tc.params, DeviceName: device, ExtraFields: map[string]string{"reason": diagnostics, "diagnostics": diagnostics}}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if result := manager.SendAndWait(ctx, message); result.Succeeded != 1 {
					t.Fatalf("未送达: %+v", result)
				}
				want := tc.english
				if language == "zh-CN" {
					want = tc.chinese
				}
				got := recorder.last.Load()
				if got.ReasonCode != tc.classification || got.ExtraFields["reason"] != want || got.ExtraFields["diagnostics"] != diagnostics || got.Content != "用户模板 "+want+" | "+diagnostics+" | "+device {
					t.Fatalf("原因优先级或单遍插值错误: %+v", got)
				}
				data, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				for key := range fields {
					switch key {
					case "locale", "event_code", "reason_code", "type", "level", "title", "content", "device_name", "device_udid", "timestamp", "extra_fields":
					default:
						t.Errorf("内部元数据不应新增 Webhook 字段: %s", key)
					}
				}
			})
		}
	}
}

func TestBackupFailureReasonAPIAliasCompatibility(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, code := range []string{"backup_stalled", "api.backup_stalled"} {
			t.Run(language+"/"+code, func(t *testing.T) {
				manager, recorder := localizedTestManager(t, language)
				recorder.started = make(chan struct{})
				manager.SendBackupFailure("我的 iPhone", "PHONE", code, "原始诊断")
				select {
				case <-recorder.started:
				case <-time.After(time.Second):
					t.Fatal("兼容入口未送达")
				}
				want := "No backup activity was detected within the time limit, so the job stopped."
				if language == "zh-CN" {
					want = "长时间未检测到备份活动，任务已停止。"
				}
				if got := recorder.last.Load(); got.ReasonCode != code || got.ExtraFields["reason"] != want || got.ExtraFields["diagnostics"] != "原始诊断" {
					t.Fatalf("旧原因码或 api 别名不兼容: %+v", got)
				}
			})
		}
	}
}

func TestCloneMessageOwnsScalarReasonParams(t *testing.T) {
	params := map[string]any{"battery": 9, "min_battery": 20, "text": "${reason}", "flag": true, "empty": nil, "float": 1.5, "float32": float32(2.5), "large": uint64(1 << 63), "map": map[string]any{"nested": true}, "slice": []string{"mutable"}, "pointer": new(int), "nan": math.NaN(), "inf": math.Inf(1), "float32nan": float32(math.NaN()), "function": func() {}}
	message := Message{ReasonMessageCode: "api.low_battery", ReasonMessageParams: params}
	clone := CloneMessage(message)
	params["battery"] = 100
	want := map[string]any{"battery": 9, "min_battery": 20, "text": "${reason}", "flag": true, "empty": nil, "float": 1.5, "float32": float32(2.5), "large": uint64(1 << 63)}
	if !reflect.DeepEqual(clone.ReasonMessageParams, want) {
		t.Fatalf("消息参数未复制或保留了可变引用: %v", clone.ReasonMessageParams)
	}
	clone.ReasonMessageParams["min_battery"] = 30
	if params["min_battery"] != 20 {
		t.Fatal("消息副本改写了调用方参数")
	}
}

func TestQueuedBackupFailureKeepsSpecificReasonSnapshot(t *testing.T) {
	manager, err := NewManagerFromConfig(&Config{Enabled: true, Language: "en"}, nil, Options{WorkerCount: 1, QueueSize: 4, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); manager.Close() })
	blocker := &recordingNotifier{name: "blocker", enabled: true, started: make(chan struct{}), unblock: release}
	queued := &recordingNotifier{name: "queued", enabled: true, started: make(chan struct{})}
	for _, n := range []Notifier{blocker, queued} {
		if err := manager.AddNotifier(n); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocker"}, string(BackupFailed): {"queued"}})
	manager.SendBackupSuccess("blocker", "BLOCKER")
	select {
	case <-blocker.started:
	case <-time.After(time.Second):
		t.Fatal("未能阻塞工作线程")
	}
	if err := manager.SetTemplates(map[string]string{string(BackupFailed): "旧模板 ${reason} | ${diagnostics}"}); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"battery": 9, "min_battery": 20}
	message := &Message{Type: BackupFailed, ReasonCode: "backup_failed", ReasonMessageCode: "api.low_battery", ReasonMessageParams: params, ExtraFields: map[string]string{"reason": "原始 ${reason}"}}
	manager.Send(message)
	if len(manager.queue) != 1 {
		t.Fatal("待检验消息没有留在队列中")
	}
	params["battery"] = 99
	message.ReasonMessageCode = "api.not_charging"
	message.ExtraFields["reason"] = "changed"
	manager.mu.Lock()
	manager.language = "zh-CN"
	manager.mu.Unlock()
	if err := manager.SetTemplates(map[string]string{string(BackupFailed): "新模板 ${reason}"}); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	select {
	case <-queued.started:
	case <-time.After(time.Second):
		t.Fatal("排队消息未送达")
	}
	got := queued.last.Load()
	if got.Locale != "en" || got.ReasonCode != "backup_failed" || got.ReasonMessageCode != "api.low_battery" || got.ReasonMessageParams["battery"] != 9 || got.Content != "旧模板 Battery is at 9%; at least 20% is required. | 原始 ${reason}" {
		t.Fatalf("排队原因被调用方或设置更改: %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result := manager.SendAndWait(ctx, message); result.Succeeded != 1 {
		t.Fatalf("新消息未送达: %+v", result)
	}
	if got := queued.last.Load(); got.Locale != "zh-CN" || got.Content != "新模板 请为设备接通电源后再备份。" {
		t.Fatalf("新消息未采用新原因和设置: %+v", got)
	}
}
