package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// 真实备份入口 + 真实子进程，替换的只有外部设备工具。
func TestBackupSilentOnlineDeviceStopsAndCanRetry(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "manual"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			cfg := defaultRuntimeConfig()
			cfg.ConfigsRoot = t.TempDir()
			cfg.BackupsRoot = t.TempDir()
			cfg.WiFiPowerAssertion = false
			cfg.BackupPreparationTimeout = 150 * time.Millisecond
			cfg.BackupInactivityTimeout = 150 * time.Millisecond
			cfg.BackupAuthorizationTimeout = 150 * time.Millisecond
			app := newApplicationWithRuntime(ctx, cfg)
			app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", Name: "Test phone", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork), BatteryLevel: 90}
			app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot}
			script := filepath.Join(t.TempDir(), "backup-tool.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 1' INT\nprintf 'IOSBK_ACTIVITY 189 waiting_device\\n' >&2\nwhile :; do sleep 0.05; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd { return newExecCmd(ctx, "/bin/sh", script) }
			run := func() error {
				if automatic {
					return app.PerformBackupWithConnection("TEST-DEVICE", connectionTypeDesc(connectTypeNetwork))
				}
				return app.PerformBackup("TEST-DEVICE")
			}
			if err := run(); err == nil {
				t.Fatal("silent backup reported success")
			}
			snapshot := app.buildStatusSnapshot()
			d := snapshot.Devices[0]
			if d.LastBackupErrorCode != "backup_stalled" || d.BackingUp || snapshot.BackupInProgress != 0 || d.LastBackup != "" {
				t.Fatalf("silent online session must fail, release task and preserve last success: %+v", d)
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("only outer test deadline stopped the task: %v", err)
			}
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'Backup Successful.\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := run(); err != nil {
				t.Fatalf("retry after timeout: %v", err)
			}
			d = app.buildStatusSnapshot().Devices[0]
			if d.BackupState != backupStateSucceeded || d.LastBackup == "" || d.BackingUp {
				t.Fatalf("retry did not complete: %+v", d)
			}
		})
	}
}

func TestBackupRepeatedDisplayDoesNotKeepDeadSessionAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	cfg.WiFiPowerAssertion = false
	cfg.BackupPreparationTimeout = 150 * time.Millisecond
	cfg.BackupInactivityTimeout = 150 * time.Millisecond
	cfg.BackupAuthorizationTimeout = 150 * time.Millisecond
	app := newApplicationWithRuntime(ctx, cfg)
	app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", IsOnline: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot}
	script := `trap 'exit 1' INT
for i in 1 2 3 4 5 6 7 8 9 10; do
printf 'Backup [....] 0%%\nSending TEST-DEVICE/Status.plist\n[====] 100.0%% 189 Bytes / 189 Bytes\n'
sleep 0.04
done
printf 'Backup Successful.\n'
`
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", script)
	}
	_ = app.PerformBackup("TEST-DEVICE")
	d := app.buildStatusSnapshot().Devices[0]
	if d.LastBackupErrorCode != "backup_stalled" {
		t.Fatalf("repeated rendering incorrectly extends liveness: %+v", d)
	}
}

func TestBackupRealActivitySurvivesUnchangedPercentAndDeviceProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	cfg.WiFiPowerAssertion = false
	cfg.BackupInactivityTimeout = 150 * time.Millisecond
	cfg.BackupPreparationTimeout = time.Second
	app := newApplicationWithRuntime(ctx, cfg)
	app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", IsOnline: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot}
	script := `for i in 1 2 3 4 5 6 7 8; do
printf 'IOSBK_ACTIVITY %s receiving\n' "$i" >&2
printf 'Backup [....] 0%%\n'
sleep 0.05
done
printf 'IOSBK_ACTIVITY 8 waiting_device\n' >&2
sleep 0.3
printf 'Backup Successful.\n'
`
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", script)
	}
	if err := app.PerformBackup("TEST-DEVICE"); err != nil {
		t.Fatalf("real transfer or legitimate processing was cancelled: %v", err)
	}
	snapshot := app.buildStatusSnapshot()
	b, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"last_activity_at":"20`) {
		t.Fatalf("completed task must preserve its last real activity timestamp: %s", b)
	}
}

func TestBackupNativeCounterIgnoresLogsAndRequiresSuccess(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"duplicate protocol count", `trap 'exit 1' INT
for i in 1 2 3 4 5 6 7 8 9 10; do
printf 'IOSBK_ACTIVITY 189 waiting_device\n' >&2
printf 'Backup [....] %s%%\nheartbeat still online\n' "$i"
sleep 0.04
done
printf 'Backup Successful.\n'`, "backup_stalled"},
		{"authorization wait", `trap 'exit 1' INT
printf 'IOSBK_ACTIVITY 0 waiting_authorization\n' >&2
sleep 2`, "backup_stalled"},
		{"exit zero without acknowledgement", `printf 'Backup [====] 100%%\n'`, "backup_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			cfg := defaultRuntimeConfig()
			cfg.ConfigsRoot = t.TempDir()
			cfg.BackupsRoot = t.TempDir()
			cfg.WiFiPowerAssertion = false
			cfg.BackupInactivityTimeout = 150 * time.Millisecond
			cfg.BackupPreparationTimeout = 150 * time.Millisecond
			cfg.BackupAuthorizationTimeout = 150 * time.Millisecond
			app := newApplicationWithRuntime(ctx, cfg)
			old := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
			app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", IsOnline: true, BatteryLevel: 90, LastBackup: old, Connection: connectionTypeDesc(connectTypeNetwork)}
			app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot, LastBackup: old}
			app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
				return newExecCmd(ctx, "/bin/sh", "-c", tc.script)
			}
			if err := app.PerformBackup("TEST-DEVICE"); err == nil {
				t.Fatal("incorrect success")
			}
			d := app.buildStatusSnapshot().Devices[0]
			if d.LastBackupErrorCode != tc.want || d.LastBackup != lastBackupStr(old) || d.BackingUp {
				t.Fatalf("unexpected result: %+v", d)
			}
		})
	}
}

func TestBackupStallKillsUnresponsiveProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	cfg.WiFiPowerAssertion = false
	cfg.BackupPreparationTimeout = 150 * time.Millisecond
	app := newApplicationWithRuntime(ctx, cfg)
	app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", IsOnline: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot}
	process := make(chan *execCmd, 1)
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		cmd := newExecCmd(ctx, "/bin/sh", "-c", `trap '' INT; sleep 30 & wait`)
		process <- cmd
		return cmd
	}
	result := make(chan error, 1)
	go func() { result <- app.PerformBackup("TEST-DEVICE") }()
	cmd := <-process
	select {
	case err := <-result:
		if !errors.Is(err, errBackupStalled) {
			t.Fatalf("wrong cancellation: %v", err)
		}
	case <-time.After(6 * time.Second):
		cancel()
		if cmd.cmd.Process != nil {
			_ = syscall.Kill(-cmd.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-result
		t.Fatal("unresponsive process group was not terminated within grace period")
	}
	if d := app.buildStatusSnapshot().Devices[0]; d.BackingUp || d.LastBackupErrorCode != "backup_stalled" {
		t.Fatalf("task not released: %+v", d)
	}
}

func TestBackupIdleStatusIsAlwaysTerminalDuringConcurrentRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	cfg.WiFiPowerAssertion = false
	app := newApplicationWithRuntime(ctx, cfg)
	app.devices["TEST-DEVICE"] = &device{UDID: "TEST-DEVICE", IsOnline: true, BatteryLevel: 90, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.configs["TEST-DEVICE"] = &backupConfig{UDID: "TEST-DEVICE", BackupDirectory: cfg.BackupsRoot}
	app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	stop := make(chan struct{})
	violations := make(chan deviceStatusDTO, 1)
	var observers sync.WaitGroup
	for i := 0; i < 4; i++ {
		observers.Add(1)
		go func() {
			defer observers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				d := app.buildStatusSnapshot().Devices[0]
				if !d.BackingUp && d.BackupState == backupStateRunning {
					select {
					case violations <- d:
					default:
					}
					return
				}
				runtime.Gosched()
			}
		}()
	}
	for i := 0; i < 150; i++ {
		if err := app.PerformBackup("TEST-DEVICE"); err != nil {
			close(stop)
			observers.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	observers.Wait()
	select {
	case d := <-violations:
		t.Fatalf("idle state exposed before old task finished: %+v", d)
	default:
	}
}
