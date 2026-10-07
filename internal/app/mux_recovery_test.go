package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionManualRestartRejectsActiveTasks(t *testing.T) {
	for _, task := range []string{"backup", "precheck", "registration"} {
		t.Run(task, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app := newApplicationWithRuntime(ctx, defaultRuntimeConfig())
			app.muxProcFactory = func(ctx context.Context, _ string, _ ...string) muxProcess { return &fakeMuxProc{ctx: ctx} }
			app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
			switch task {
			case "backup":
				app.backupInProgress["PHONE"] = true
			case "precheck":
				app.checkInProgress["PHONE"] = true
			case "registration":
				app.networkRegistrations = map[string]bool{"PHONE": true}
			}
			w := httptest.NewRecorder()
			app.handleRestartUSBMuxD(w, httptest.NewRequest(http.MethodPost, "/api/restart-usbmuxd", nil))
			if w.Code != http.StatusConflict {
				t.Errorf("任务执行期间重启应返回 409，实际 %d", w.Code)
			}
		})
	}
}

func TestConnectionTaskAndRecoveryAdmissionAreAtomic(t *testing.T) {
	for i := 0; i < 100; i++ {
		app := newApplication()
		start := make(chan struct{})
		taskDone, recoveryDone := make(chan bool, 1), make(chan bool, 1)
		releaseTask := make(chan struct{})
		go func() {
			<-start
			release, err := app.beginConnectionTask(nil)
			taskDone <- err == nil
			if err == nil {
				<-releaseTask
				release()
			}
		}()
		go func() { <-start; recoveryDone <- app.reserveConnectionRecovery([]int{connectionUSB}, true) == nil }()
		close(start)
		task, recovery := <-taskDone, <-recoveryDone
		if task && recovery {
			t.Fatal("新任务和重启不能同时取得准入")
		}
		close(releaseTask)
		if recovery {
			app.mu.Lock()
			app.connectionRecoveryActive = false
			app.mu.Unlock()
			app.connectionRecoveryWorkers.Done()
		}
	}
}

func TestConnectionPendingRecoveryDrainsExistingTask(t *testing.T) {
	app := newApplication()
	app.connectionServices[0].failures = 3
	app.connectionServices[0].code = "query_failed"
	release, err := app.beginConnectionTask(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.reserveConnectionRecovery([]int{0}, false); err == nil {
		t.Fatal("任务占用时不能重启")
	}
	if _, err := app.beginConnectionTask(nil); err == nil {
		t.Fatal("待恢复时不能继续接纳普通任务")
	}
	commandRelease, err := app.beginConnectionCommand()
	if err != nil {
		t.Fatal("现有任务必须能继续执行收尾命令")
	}
	commandRelease()
	release()
	release()
	if err := app.reserveConnectionRecovery([]int{0}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.beginConnectionCommand(); err == nil {
		t.Fatal("重启独占期间不能进入设备命令")
	}
	app.connectionRecoveryWorkers.Done()
}

func TestConnectionOldPairingResultCannotOverwriteNewProcess(t *testing.T) {
	app := newApplication()
	d := &device{UDID: "PHONE", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	app.devices[d.UDID] = d
	publish := app.pairingStatePublisher(d)
	app.connectionProcessStarted(0)
	app.setPairingState(d.UDID, pairingStatePaired, "", "")
	publish(d.UDID, pairingStateFailed, "pair_failed", "old error")
	if app.deviceOperationStates[d.UDID].PairingState != pairingStatePaired {
		t.Fatal("旧配对结果覆盖新状态")
	}
}

func TestConnectionPairingKeepsOldErrorUntilValidation(t *testing.T) {
	app := newApplication()
	d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[d.UDID] = d
	app.deviceOperationStates[d.UDID] = deviceOperationState{PairingState: pairingStateFailed, PairingErrorCode: "old_error", PairingError: "旧错误"}
	app.setPairingState(d.UDID, pairingStateChecking, "", "")
	if app.deviceOperationStates[d.UDID].PairingErrorCode != "old_error" {
		t.Error("接纳配对请求不能提前清除旧错误")
	}
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		if app.deviceOperationStates[d.UDID].PairingErrorCode != "old_error" {
			t.Error("验证返回成功之前必须保留旧错误")
		}
		return nil, nil
	}
	app.pairDevice(d)
	if state := app.deviceOperationStates[d.UDID]; state.PairingState != pairingStatePaired || state.PairingErrorCode != "" {
		t.Fatal("成功验证后应清除旧错误")
	}
}

func TestConnectionPairingSuccessRequiresValidate(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, alreadyPaired := range []bool{false, true} {
			app := newApplication()
			d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
			app.devices[d.UDID] = d
			app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
				if args[len(args)-1] == "validate" {
					return nil, errors.New("validation unavailable")
				}
				if alreadyPaired {
					return []byte("already paired"), errors.New("exit status 1")
				}
				return nil, nil
			}
			if retry {
				app.retryPairAfterTrustWithSchedule(d.UDID, time.Millisecond, 1)
			} else {
				app.pairDevice(d)
			}
			if app.deviceOperationStates[d.UDID].PairingState == pairingStatePaired {
				t.Fatalf("配对命令输出不能替代真实 validate 成功: retry=%v already=%v", retry, alreadyPaired)
			}
		}
	}
}

func TestConnectionRecoveryRequiresNewProcessProtocolSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newApplicationWithRuntime(ctx, defaultRuntimeConfig())
	started := make(chan struct{})
	app.muxProcFactory = func(ctx context.Context, _ string, _ ...string) muxProcess { return &fakeMuxProc{ctx: ctx} }
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	if err := app.reserveConnectionRecovery([]int{0}, true); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(started)
		if err := app.executeConnectionRecovery(ctx, []int{0}, true); err != nil {
			t.Error(err)
		}
	}()
	<-started
	app.mu.RLock()
	s := app.connectionServices[0]
	app.mu.RUnlock()
	app.StopUSBMuxD()
	if s.scan.generation != s.generation || s.generation == 0 || s.health != "healthy" {
		t.Fatalf("必须验证新进程，实际 %+v", s)
	}
}

func TestConnectionSchedulerDiscardsDetailsFromOldProcess(t *testing.T) {
	app := newApplication()
	d := &device{UDID: "PHONE", Name: "Old", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	app.devices[d.UDID] = cloneDevice(d)
	first := true
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		if first {
			first = false
			app.connectionProcessStarted(0)
			app.mu.Lock()
			app.devices[d.UDID].Name = "Fresh"
			app.mu.Unlock()
		}
		return []byte("Stale"), nil
	}
	result := app.updateDeviceStatusForBackup(cloneDevice(d), deviceStatus{UDID: d.UDID, Connection: d.Connection, IsOnline: true})
	if app.devices[d.UDID].Name != "Fresh" || result != nil {
		t.Fatal("换代后的预检必须丢弃旧明细并延期备份")
	}
}

func TestConnectionRecoveryRechecksEligibilityAfterProcessChange(t *testing.T) {
	app := newApplication()
	app.connectionServices[0].failures = 3
	app.connectionServices[0].code = "query_failed"
	targets := app.dueConnectionRecovery(nowBeijing())
	app.connectionProcessStarted(0)
	if err := app.reserveConnectionRecovery(targets, false); err == nil {
		app.connectionRecoveryWorkers.Done()
		t.Fatal("发现故障后进程已换代，不能继续重启新进程")
	}
}

func TestConnectionWiFiRecoveryRegistersBeforePairing(t *testing.T) {
	app := newApplication()
	app.paths.ConfigsRoot = t.TempDir()
	app.configStore = newBackupConfigStore(filepath.Join(app.paths.ConfigsRoot, "backup.json"), []string{t.TempDir()})
	app.devices["PHONE"] = &device{UDID: "PHONE", Name: "Phone", Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: false}
	app.configs["PHONE"] = app.defaultBackupConfig("PHONE", "Phone")
	app.configs["PHONE"].NetworkAddress = "192.0.2.11"
	app.deviceOperationStates["PHONE"] = deviceOperationState{PairingState: pairingStateFailed, PairingErrorCode: "pair_failed", BackupState: backupStateFailed, BackupErrorCode: "backup_failed"}
	app.reachProbe = func(string) (bool, string) { return true, "" }
	var registered atomic.Bool
	var validated atomic.Bool
	app.cmdRunner = func(_ context.Context, name string, args, env []string) ([]byte, error) {
		if name == cmdAddDevice {
			registered.Store(true)
			return []byte("Success"), nil
		}
		if name == cmdIdeviceID && envHas(env, "USBMUXD_SOCKET_ADDRESS") && registered.Load() {
			return []byte("PHONE (Network)\n"), nil
		}
		if name == cmdIdevicePair {
			if !registered.Load() {
				t.Error("注册前不能验证配对")
			}
			validated.Store(true)
		}
		return nil, nil
	}
	app.connectionRefreshPending = true
	app.refreshRecoveredConnections()
	if !validated.Load() || app.deviceOperationStates["PHONE"].PairingState != pairingStatePaired {
		t.Fatal("恢复后必须自动注册并完成配对验证")
	}
	if !app.connectionRefreshPending {
		t.Fatal("注册发生在刷新期间，必须保留后续枚举请求以排除旧查询")
	}
	app.refreshRecoveredConnections()
	if app.connectionRefreshPending {
		t.Fatal("已注册设备不能不断新增刷新请求")
	}
	if app.deviceOperationStates["PHONE"].BackupState != backupStateFailed {
		t.Fatal("连接恢复不能清除或重跑失败备份")
	}
}

func TestConnectionConcurrentRegistrationKeepsPairingRefresh(t *testing.T) {
	for _, duringRefresh := range []bool{false, true} {
		name := "注册晚于刷新"
		if duringRefresh {
			name = "注册与刷新收尾重叠"
		}
		t.Run(name, func(t *testing.T) {
			app := newApplication()
			app.paths.ConfigsRoot = t.TempDir()
			app.configStore = newBackupConfigStore(filepath.Join(app.paths.ConfigsRoot, "backup.json"), []string{t.TempDir()})
			for _, udid := range []string{"PHONE", "USB"} {
				app.devices[udid] = &device{UDID: udid, Name: udid, Connection: connectionTypeDesc(connectTypeUSB)}
				app.configs[udid] = app.defaultBackupConfig(udid, udid)
			}
			app.configs["PHONE"].NetworkAddress = "192.0.2.11"
			app.devices["PHONE"].Connection = connectionTypeDesc(connectTypeNetwork)
			app.deviceOperationStates["PHONE"] = deviceOperationState{PairingState: pairingStateFailed, PairingErrorCode: "pair_failed"}
			app.reachProbe = func(string) (bool, string) { return true, "" }
			started, finish := make(chan struct{}), make(chan struct{})
			pairStarted, finishPair := make(chan struct{}), make(chan struct{})
			var registered, pairBlocked atomic.Bool
			app.cmdRunner = func(_ context.Context, bin string, args, env []string) ([]byte, error) {
				if bin == cmdAddDevice {
					close(started)
					<-finish
					registered.Store(true)
					return []byte("Success"), nil
				}
				if bin == cmdIdeviceID {
					if !envHas(env, "USBMUXD_SOCKET_ADDRESS") {
						return []byte("USB\n"), nil
					}
					if registered.Load() {
						return []byte("PHONE (Network)\n"), nil
					}
				}
				if bin == cmdIdevicePair && duringRefresh && pairBlocked.CompareAndSwap(false, true) {
					close(pairStarted)
					<-finishPair
				}
				return nil, nil
			}
			registrationDone := make(chan error, 1)
			go func() { registrationDone <- app.callAddDevice("PHONE", "192.0.2.11") }()
			<-started
			app.mu.Lock()
			app.connectionRefreshPending = true
			app.mu.Unlock()
			refreshDone := make(chan struct{})
			go func() { app.refreshRecoveredConnections(); close(refreshDone) }()
			if duringRefresh {
				<-pairStarted
			} else {
				<-refreshDone
			}
			close(finish)
			if err := <-registrationDone; err != nil {
				t.Fatal(err)
			}
			if duringRefresh {
				close(finishPair)
				<-refreshDone
			}
			if !app.connectionRefreshPending {
				t.Fatal("并发注册完成后必须保留一次配对刷新，不能被旧刷新清除")
			}
			app.refreshRecoveredConnections()
			if app.connectionRefreshPending || app.deviceOperationStates["PHONE"].PairingState != pairingStatePaired {
				t.Fatal("后续刷新必须清除旧配对错误并完成请求")
			}
		})
	}
}

func TestConnectionFailedVerificationBacksOffAndReleasesAdmission(t *testing.T) {
	app := newApplication()
	app.connectionServices[0].failures = 3
	app.connectionServices[0].code = "query_failed"
	app.muxProcFactory = func(ctx context.Context, _ string, _ ...string) muxProcess { return &fakeMuxProc{ctx: ctx} }
	app.cmdRunner = func(ctx context.Context, _ string, _, _ []string) ([]byte, error) {
		return nil, errors.New("protocol stuck")
	}
	if err := app.reserveConnectionRecovery([]int{0}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := app.executeConnectionRecovery(ctx, []int{0}, false)
	app.StopUSBMuxD()
	if err == nil {
		t.Fatal("进程启动不能代表恢复成功")
	}
	if app.connectionRecoveryActive || app.connectionServices[0].phase != "backoff" || len(app.connectionServices[0].attempts) != 1 {
		t.Fatal("失败必须释放占用、记录一次尝试并退避")
	}
	if len(app.dueConnectionRecovery(nowBeijing())) != 0 {
		t.Fatal("验证失败后不能立刻再次重启")
	}
}

func TestConnectionSharedBackendHasOnePhysicalRecovery(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiBackend = wifiBackendUSBMuxd2
	app := newApplicationWithRuntime(context.Background(), cfg)
	var starts atomic.Int32
	app.muxProcFactory = func(ctx context.Context, name string, _ ...string) muxProcess {
		if name != cmdUSBMuxd {
			t.Errorf("不应启动 netmuxd")
		}
		starts.Add(1)
		return &fakeMuxProc{ctx: ctx}
	}
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	targets := app.connectionTargets()
	if err := app.reserveConnectionRecovery(targets, true); err != nil {
		t.Fatal(err)
	}
	if err := app.executeConnectionRecovery(app.rootCtx, targets, true); err != nil {
		t.Fatal(err)
	}
	app.StopUSBMuxD()
	if starts.Load() != 1 {
		t.Fatalf("共享服务应只恢复一次，实际 %d", starts.Load())
	}
}
