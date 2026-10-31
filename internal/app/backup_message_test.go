package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	"iosbackup/internal/i18n"
)

func TestBackupTerminalMessagePreservesTypedError(t *testing.T) {
	for _, test := range []struct {
		name     string
		battery  int
		charging bool
		code     string
		params   map[string]any
	}{
		{"low battery", 9, true, "api.low_battery", map[string]any{"battery": float64(9), "min_battery": float64(20)}},
		{"not charging", 80, false, "api.not_charging", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newBackupControlTestApp(t)
			app.devices["CONTROL"].BatteryLevel = test.battery
			app.devices["CONTROL"].IsCharging = test.charging
			app.configs["CONTROL"] = &backupConfig{UDID: "CONTROL", MinBatteryLevel: 20, OnlyWhenCharging: true, BackupDirectory: t.TempDir()}

			// 调用真实二次校验及收尾链；条件失败时不启动备份工具。
			err := app.PerformBackup("CONTROL")
			var message *i18n.Error
			if !errors.As(err, &message) || message.Code != test.code {
				t.Fatalf("二次校验错误 = %v，期望 %s", err, test.code)
			}
			payload := backupMessagePayload(t, app)
			if payload["last_backup_message_code"] != test.code {
				t.Fatalf("备份终态丢失原因码：%v", payload)
			}
			params, _ := payload["last_backup_message_params"].(map[string]any)
			if !reflect.DeepEqual(params, test.params) {
				t.Fatalf("终态参数 = %v，期望 %v", params, test.params)
			}
			if payload["backup_state"] != backupStateFailed || payload["last_backup_error_code"] != "backup_failed" || payload["last_backup_error"] != err.Error() {
				t.Fatalf("既有终态或兼容错误字段被改写：%v", payload)
			}
			if app.backupInProgress["CONTROL"] {
				t.Fatal("失败后未释放备份占用")
			}
		})
	}
}

func TestBackupTerminalMessageClearsOnNewTaskAndSuccess(t *testing.T) {
	for name, transition := range map[string]func(*testing.T, *application){
		"starting":          func(t *testing.T, app *application) { app.markBackupStarting("CONTROL") },
		"running":           func(t *testing.T, app *application) { app.markBackupRunning("CONTROL") },
		"success state":     func(t *testing.T, app *application) { app.finishBackupState("CONTROL", nil) },
		"success operation": func(t *testing.T, app *application) { finishBackupMessageTestJob(t, app, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			app := newBackupControlTestApp(t)
			app.deviceOperationStates["CONTROL"] = deviceOperationState{
				PairingState: pairingStatePaired, LastBackupMessageCode: "api.low_battery",
				LastBackupMessageParams: map[string]any{"battery": 9, "min_battery": 20},
			}
			transition(t, app)
			payload := backupMessagePayload(t, app)
			if _, exists := payload["last_backup_message_code"]; exists {
				t.Fatalf("新任务或成功状态仍发布旧消息码：%v", payload)
			}
			if _, exists := payload["last_backup_message_params"]; exists {
				t.Fatalf("新任务或成功状态仍发布旧参数：%v", payload)
			}
			if payload["pairing_state"] != pairingStatePaired {
				t.Fatalf("备份状态更新改写配对结果：%v", payload)
			}
		})
	}
}

func TestBackupTerminalMessageFallbackPreservesClassification(t *testing.T) {
	for _, test := range []struct {
		name, state, code, messageCode string
		err                            error
	}{
		{"typed wrapped", backupStateFailed, "backup_failed", "api.not_charging", fmt.Errorf("backup check: %w", i18n.NewError("api.not_charging", nil))},
		{"unknown typed", backupStateFailed, "backup_failed", "api.backup_failed", i18n.NewError("api.future_unknown", nil)},
		{"unknown diagnostic", backupStateFailed, "backup_failed", "api.backup_failed", errors.New("unrecognized 原始诊断")},
		{"stalled", backupStateFailed, "backup_stalled", "api.backup_stalled", fmt.Errorf("backup: %w", errBackupStalled)},
		{"disconnected", backupStateInterrupted, "device_disconnected", "api.device_disconnected", errDeviceDisconnected},
		{"cancelled", backupStateInterrupted, "backup_cancelled", "api.backup_cancelled", context.Canceled},
		{"deadline", backupStateInterrupted, "backup_cancelled", "api.backup_cancelled", context.DeadlineExceeded},
		{"storage", backupStateFailed, "storage_unavailable", "api.storage_unavailable", errors.New("no space left on device")},
		{"locked", backupStateFailed, "device_locked", "api.device_locked", errors.New("device locked")},
		{"user stopped", backupStateStopped, "backup_stopped_by_user", "", errBackupStoppedByUser},
	} {
		for _, finishOperation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/operation=%t", test.name, finishOperation), func(t *testing.T) {
				app := newBackupControlTestApp(t)
				if finishOperation {
					finishBackupMessageTestJob(t, app, test.err)
				} else {
					app.finishBackupState("CONTROL", test.err)
				}
				got := app.buildStatusSnapshot().Devices[0]
				if got.BackupState != test.state || got.LastBackupErrorCode != test.code || got.LastBackupError != test.err.Error() {
					t.Fatalf("旧分类或诊断被改写：%+v", got)
				}
				if got.LastBackupMessageCode != test.messageCode || len(got.LastBackupMessageParams) != 0 {
					t.Fatalf("失败消息未正确映射：%+v", got)
				}
				if app.backupInProgress["CONTROL"] {
					t.Fatal("收尾没有释放占用")
				}
			})
		}
	}
}

func TestStatusSnapshotOwnsBackupMessageParams(t *testing.T) {
	app := newBackupControlTestApp(t)
	params := map[string]any{"battery": 9, "min_battery": 20, "device_name": "我的 ${battery} iPhone"}
	finishBackupMessageTestJob(t, app, i18n.NewError("api.low_battery", params))
	params["battery"] = 100
	delete(params, "min_battery")
	first := app.buildStatusSnapshot().Devices[0]
	if first.LastBackupMessageParams["battery"] != 9 || first.LastBackupMessageParams["min_battery"] != 20 || first.LastBackupMessageParams["device_name"] != "我的 ${battery} iPhone" {
		t.Fatalf("调用方参数修改污染终态：%+v", first)
	}
	first.LastBackupMessageParams["battery"] = 200
	second := app.buildStatusSnapshot().Devices[0]
	if second.LastBackupMessageParams["battery"] != 9 {
		t.Fatalf("快照参数修改污染内存状态：%+v", second)
	}
	app.markBackupStarting("CONTROL")
	finishBackupMessageTestJob(t, app, nil)
	third := app.buildStatusSnapshot().Devices[0]
	if third.LastBackupMessageCode != "" || len(third.LastBackupMessageParams) != 0 || second.LastBackupMessageParams["battery"] != 9 {
		t.Fatalf("相邻任务与旧快照互相污染：old=%+v new=%+v", second, third)
	}
}

func TestBackupTerminalMessageParamsOnlyContainJSONScalars(t *testing.T) {
	app := newBackupControlTestApp(t)
	params := map[string]any{
		"battery": 9, "min_battery": 20, "text": "路径 {battery}", "flag": true, "empty": nil,
		"float": 1.5, "float32": float32(2.5), "large": uint64(1 << 63),
		"map": map[string]any{"secret": "nested"}, "slice": []string{"mutable"},
		"pointer": new(int), "nan": math.NaN(), "infinity": math.Inf(1), "float32nan": float32(math.NaN()),
		"function": func() {},
	}
	app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", params))
	got := app.buildStatusSnapshot().Devices[0]
	want := map[string]any{"battery": 9, "min_battery": 20, "text": "路径 {battery}", "flag": true, "empty": nil, "float": 1.5, "float32": float32(2.5), "large": uint64(1 << 63)}
	if !reflect.DeepEqual(got.LastBackupMessageParams, want) {
		t.Fatalf("消息参数没有限制为 JSON 标量：%v", got.LastBackupMessageParams)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("消息参数导致状态无法序列化：%v", err)
	}
}

func TestBackupTerminalMessageConcurrentSnapshots(t *testing.T) {
	app := newBackupControlTestApp(t)
	app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", map[string]any{"battery": 9, "min_battery": 20}))
	var group sync.WaitGroup
	group.Add(3)
	go func() {
		defer group.Done()
		for i := 0; i < 100; i++ {
			params := map[string]any{"battery": i, "min_battery": 20}
			app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", params))
			params["battery"] = -1
		}
	}()
	for reader := 0; reader < 2; reader++ {
		go func() {
			defer group.Done()
			for i := 0; i < 100; i++ {
				got := app.buildStatusSnapshot().Devices[0]
				if got.LastBackupMessageCode != "api.low_battery" {
					t.Errorf("并发快照丢失消息码：%+v", got)
					return
				}
				battery, ok := got.LastBackupMessageParams["battery"].(int)
				if !ok || battery < 0 || got.LastBackupMessageParams["min_battery"] != 20 {
					t.Errorf("快照观察到调用方或其他快照修改的参数：%v", got.LastBackupMessageParams)
				}
				got.LastBackupMessageParams["battery"] = -2
			}
		}()
	}
	group.Wait()
	finishBackupMessageTestJob(t, app, nil)
	if got := app.buildStatusSnapshot().Devices[0]; got.LastBackupMessageCode != "" || len(got.LastBackupMessageParams) != 0 {
		t.Fatalf("并发读取后未能清除消息：%+v", got)
	}
}

func TestBackupTerminalMessageNewJobClearsBeforeCommand(t *testing.T) {
	app := newBackupControlTestApp(t)
	app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", map[string]any{"battery": 9, "min_battery": 20}))
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	starting := app.buildStatusSnapshot().Devices[0]
	if starting.BackupState != backupStateStarting || starting.BackupJobID != job.id || !starting.BackingUp {
		t.Fatalf("新任务登记不完整: %+v", starting)
	}
	if starting.LastBackupMessageCode != "" || len(starting.LastBackupMessageParams) != 0 {
		t.Errorf("新任务准备阶段仍保留上次失败: %+v", starting)
	}
	commandStarted := false
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		commandStarted = true
		running := app.buildStatusSnapshot().Devices[0]
		if running.BackupState != backupStateRunning || running.LastBackupMessageCode != "" || len(running.LastBackupMessageParams) != 0 {
			t.Errorf("运行阶段仍发布上次失败: %+v", running)
		}
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	if err := app.runBackupJob(job); err != nil {
		t.Fatal(err)
	}
	got := app.buildStatusSnapshot().Devices[0]
	if !commandStarted || got.BackupState != backupStateSucceeded || got.LastBackupMessageCode != "" || len(got.LastBackupMessageParams) != 0 || got.BackupJobID != job.id || got.BackingUp || app.backupSem.inUse() != 0 {
		t.Fatalf("成功后元数据、任务标识或占用错误: %+v", got)
	}
}

func TestBackupTerminalMessageStopKeepsControlState(t *testing.T) {
	for _, earlierFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(earlierFailure), func(t *testing.T) {
			app := newBackupControlTestApp(t)
			app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", map[string]any{"battery": 9, "min_battery": 20}))
			job, err := app.beginBackupJob("CONTROL", "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if earlierFailure {
				job.cancel(errDeviceDisconnected)
			}
			if err := app.stopBackup("CONTROL", job.id); err != nil {
				t.Fatal(err)
			}
			wantErr, wantState, wantCode, wantMessage := errBackupStoppedByUser, backupStateStopped, "backup_stopped_by_user", ""
			if earlierFailure {
				wantErr, wantState, wantCode, wantMessage = errDeviceDisconnected, backupStateInterrupted, "device_disconnected", "api.device_disconnected"
			}
			if err := app.runBackupJob(job); !errors.Is(err, wantErr) {
				t.Fatalf("停止改写了优先终态: %v", err)
			}
			got := app.buildStatusSnapshot().Devices[0]
			if got.BackupState != wantState || got.LastBackupErrorCode != wantCode || got.LastBackupMessageCode != wantMessage || len(got.LastBackupMessageParams) != 0 {
				t.Fatalf("停止消息错误: %+v", got)
			}
			if !got.AutoBackupHold || got.AutoBackupHoldVersion != job.id || got.AutoBackupHoldPersistence != "saved" || got.BackupJobID != job.id || got.BackingUp || app.backupSem.inUse() != 0 {
				t.Fatalf("消息更新破坏停止、暂缓或任务占用: %+v", got)
			}
		})
	}
}

func TestBackupTerminalMessageUsesFrozenJobResult(t *testing.T) {
	for _, outcome := range []string{"typed failure", "success", "stopped"} {
		t.Run(outcome, func(t *testing.T) {
			app := newBackupControlTestApp(t)
			job, err := app.beginBackupJob("CONTROL", "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			var result error
			wantState, wantMessage := backupStateSucceeded, ""
			if outcome == "typed failure" {
				result = i18n.NewError("api.low_battery", map[string]any{"battery": 9, "min_battery": 20})
				wantState, wantMessage = backupStateFailed, "api.low_battery"
			} else if outcome == "stopped" {
				result = errBackupStoppedByUser
				wantState = backupStateStopped
			}
			app.recordBackupResult(job, result)
			app.finishBackupJob(job, context.DeadlineExceeded)
			got := app.buildStatusSnapshot().Devices[0]
			if got.BackupState != wantState || got.LastBackupMessageCode != wantMessage || got.BackingUp || app.backupSem.inUse() != 0 {
				t.Fatalf("元数据未跟随已经冻结的任务结果: %+v", got)
			}
			if outcome == "typed failure" && (got.LastBackupMessageParams["battery"] != 9 || got.LastBackupMessageParams["min_battery"] != 20) {
				t.Fatalf("冻结结果丢失参数: %+v", got)
			}
		})
	}
}

func TestBackupTerminalMessageMissingDeviceReplacesOldError(t *testing.T) {
	app := newBackupControlTestApp(t)
	app.finishBackupState("CONTROL", i18n.NewError("api.low_battery", map[string]any{"battery": 9, "min_battery": 20}))
	delete(app.devices, "CONTROL")
	if err := app.PerformBackup("CONTROL"); !errors.Is(err, errDeviceNotFound) {
		t.Fatalf("设备不存在时错误改变: %v", err)
	}
	got := operationStateForTest(app, "CONTROL")
	if got.LastBackupMessageCode != "api.backup_failed" || len(got.LastBackupMessageParams) != 0 || got.BackupErrorCode != "backup_failed" {
		t.Fatalf("设备不存在仍保留旧原因: %+v", got)
	}
}

func finishBackupMessageTestJob(t *testing.T, app *application, result error) {
	t.Helper()
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	app.finishBackupJob(job, result)
}

func backupMessagePayload(t *testing.T, app *application) map[string]any {
	t.Helper()
	data, err := json.Marshal(app.buildStatusSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 {
		t.Fatalf("期望一个设备快照，得到 %s", data)
	}
	return snapshot.Devices[0]
}
