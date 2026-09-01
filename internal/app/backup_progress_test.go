package app

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseBackupProgressLine(t *testing.T) {
	tests := []struct {
		name             string
		line             string
		wantMatch        bool
		wantPhase        string
		wantOverall      *float64
		wantFilePercent  *float64
		wantCurrentBytes *int64
		wantTotalBytes   *int64
	}{
		{
			name:        "overall percentage with ansi and carriage refresh",
			line:        "stale\r\x1b[2K\x1b[1GBackup     [########........]  81%",
			wantMatch:   true,
			wantOverall: float64Ptr(81),
		},
		{
			name:             "current file megabytes",
			line:             "           [====>           ]  47.5% 209 MB / 440 MB",
			wantMatch:        true,
			wantFilePercent:  float64Ptr(47.5),
			wantCurrentBytes: int64Ptr(209_000_000),
			wantTotalBytes:   int64Ptr(440_000_000),
		},
		{
			name:             "decimal gigabytes and kilobytes",
			line:             "[========>] 75.0% 1.5 GB / 2000 MB",
			wantMatch:        true,
			wantFilePercent:  float64Ptr(75),
			wantCurrentBytes: int64Ptr(1_500_000_000),
			wantTotalBytes:   int64Ptr(2_000_000_000),
		},
		{
			name:             "bytes and lowercase kilobytes",
			line:             "[=>] 25% 512 Bytes / 2 kB",
			wantMatch:        true,
			wantFilePercent:  float64Ptr(25),
			wantCurrentBytes: int64Ptr(512),
			wantTotalBytes:   int64Ptr(2_000),
		},
		{
			name:      "new receiving file keeps last file counters until replacement",
			line:      "Receiving Library/Private/secret.db",
			wantMatch: true,
			wantPhase: "receiving",
		},
		{
			name:      "new sending file keeps last file counters until replacement",
			line:      "Sending   Snapshot/Manifest.db",
			wantMatch: true,
			wantPhase: "sending",
		},
		{
			name:      "percentage over one hundred is rejected",
			line:      "Backup [########] 101%",
			wantMatch: false,
		},
		{
			name:      "negative bytes are rejected",
			line:      "[=>] 25% -1 MB / 2 MB",
			wantMatch: false,
		},
		{
			name:      "ordinary log does not match",
			line:      "Backup command started successfully",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched := parseBackupProgressLine(tt.line)
			if matched != tt.wantMatch {
				t.Fatalf("matched = %v, want %v (update: %+v)", matched, tt.wantMatch, got)
			}
			if !matched {
				return
			}
			if got.Phase != tt.wantPhase {
				t.Errorf("Phase = %q, want %q", got.Phase, tt.wantPhase)
			}
			assertOptionalFloat(t, "OverallPercent", got.OverallPercent, tt.wantOverall)
			assertOptionalFloat(t, "CurrentFilePercent", got.CurrentFilePercent, tt.wantFilePercent)
			assertOptionalInt64(t, "CurrentBytes", got.CurrentBytes, tt.wantCurrentBytes)
			assertOptionalInt64(t, "CurrentTotalBytes", got.CurrentTotalBytes, tt.wantTotalBytes)
		})
	}
}

func assertOptionalFloat(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if math.Abs(*got-*want) > 0.0001 {
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func assertOptionalInt64(t *testing.T, name string, got, want *int64) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func TestBackupProgressLifecycle(t *testing.T) {
	app := newApplication()
	udid := "U-PROGRESS"
	t0 := time.Date(2026, 8, 14, 18, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))

	app.startBackupProgress(udid, t0)
	assertBackupProgressState(t, app, udid, backupProgressPreparing, "preparing", nil, nil)

	app.applyBackupProgressUpdate(udid, backupProgressUpdate{
		Phase:              "receiving",
		OverallPercent:     float64Ptr(42),
		CurrentFilePercent: float64Ptr(50),
		CurrentBytes:       int64Ptr(5_000_000),
		CurrentTotalBytes:  int64Ptr(10_000_000),
	}, t0.Add(time.Second))
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(42), float64Ptr(50))

	// 新文件只有方向行时继续展示上一个文件的完成值，直到下一个文件进度到达后原位替换。
	app.applyBackupProgressUpdate(udid, backupProgressUpdate{Phase: "receiving"}, t0.Add(2*time.Second))
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(42), float64Ptr(50))

	app.applyBackupProgressUpdate(udid, backupProgressUpdate{
		CurrentFilePercent: float64Ptr(10),
		CurrentBytes:       int64Ptr(2_000_000),
		CurrentTotalBytes:  int64Ptr(20_000_000),
	}, t0.Add(2500*time.Millisecond))
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(42), float64Ptr(10))

	app.finishBackupProgress(udid, nil, t0.Add(3*time.Second))
	assertBackupProgressState(t, app, udid, backupProgressCompleted, "completed", float64Ptr(100), nil)

	// 新任务必须清掉上一轮的完成值和文件进度。
	app.startBackupProgress(udid, t0.Add(4*time.Second))
	assertBackupProgressState(t, app, udid, backupProgressPreparing, "preparing", nil, nil)
}

func TestBackupProgressFailureAndInterruptionKeepLastOverall(t *testing.T) {
	app := newApplication()
	t0 := nowBeijing()

	app.startBackupProgress("FAILED", t0)
	app.applyBackupProgressUpdate("FAILED", backupProgressUpdate{OverallPercent: float64Ptr(63)}, t0.Add(time.Second))
	app.finishBackupProgress("FAILED", errors.New("disk unavailable"), t0.Add(2*time.Second))
	assertBackupProgressState(t, app, "FAILED", backupProgressFailed, "failed", float64Ptr(63), nil)

	app.startBackupProgress("INTERRUPTED", t0)
	app.applyBackupProgressUpdate("INTERRUPTED", backupProgressUpdate{OverallPercent: float64Ptr(27)}, t0.Add(time.Second))
	app.finishBackupProgress("INTERRUPTED", errDeviceDisconnected, t0.Add(2*time.Second))
	assertBackupProgressState(t, app, "INTERRUPTED", backupProgressInterrupted, "interrupted", float64Ptr(27), nil)
}

func TestReadBackupOutputUpdatesProgressOnlyFromStdout(t *testing.T) {
	app := newApplication()
	udid := "U-OUTPUT"
	app.startBackupProgress(udid, nowBeijing())

	app.readBackupOutput(udid, "test-session", strings.NewReader("Receiving private/path.db\n[====>] 47.5% 209 MB / 440 MB\nBackup [########] 81%\n"), "STDOUT")
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(81), float64Ptr(47.5))

	app.readBackupOutput(udid, "test-session", strings.NewReader("Backup [########] 99%\n"), "STDERR")
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(81), float64Ptr(47.5))
}

func TestNetworkBackupProgressEntersAndLeavesReconnectGrace(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.DeviceDisconnectGrace = 30 * time.Second
	app := newApplicationWithRuntime(nil, cfg)
	udid := "U-WIFI-PROGRESS"
	app.devices[udid] = &device{UDID: udid, IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.startBackupProgress(udid, nowBeijing())
	app.applyBackupProgressUpdate(udid, backupProgressUpdate{Phase: "receiving", OverallPercent: float64Ptr(31)}, nowBeijing())

	_, cancel := contextWithCancelCauseForProgressTest()
	release := app.registerActiveDeviceCommand(udid, connectTypeNetwork, cancel)
	defer release()

	t0 := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	app.applyPresenceSnapshot(map[string]*device{}, t0)
	assertBackupProgressState(t, app, udid, backupProgressReconnecting, "receiving", float64Ptr(31), nil)

	online := map[string]*device{
		udid: {UDID: udid, IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)},
	}
	app.applyPresenceSnapshot(online, t0.Add(5*time.Second))
	assertBackupProgressState(t, app, udid, backupProgressRunning, "receiving", float64Ptr(31), nil)
}

func contextWithCancelCauseForProgressTest() (done <-chan struct{}, cancel func(error)) {
	ctx, cancelCause := context.WithCancelCause(context.Background())
	return ctx.Done(), cancelCause
}

func assertBackupProgressState(t *testing.T, app *application, udid string, wantState backupProgressState, wantPhase string, wantOverall, wantFile *float64) {
	t.Helper()
	app.mu.RLock()
	got, ok := app.backupProgress[udid]
	app.mu.RUnlock()
	if !ok {
		t.Fatalf("设备 %s 缺少备份进度状态", udid)
	}
	if got.State != wantState || got.Phase != wantPhase {
		t.Errorf("state/phase = %q/%q, want %q/%q", got.State, got.Phase, wantState, wantPhase)
	}
	assertOptionalFloat(t, "OverallPercent", got.OverallPercent, wantOverall)
	assertOptionalFloat(t, "CurrentFilePercent", got.CurrentFilePercent, wantFile)
	if wantFile == nil && (got.CurrentBytes != nil || got.CurrentTotalBytes != nil) {
		t.Errorf("当前文件进度应已清空: %+v", got)
	}
}

func float64Ptr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64       { return &v }
