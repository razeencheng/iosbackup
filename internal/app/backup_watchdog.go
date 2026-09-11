package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errBackupStalled = errors.New("备份长时间没有活动，已停止；请检查手机授权和网络后重试")

// backupActivity 只属于一次命令。设备发现、防休眠续租、普通日志都不能续期。
// 配套工具报告实际协议字节/消息计数；旧工具仅以可确认的输出进度变化为后备。
type backupActivity struct {
	mu           sync.Mutex
	last         time.Time
	phase        string
	phaseStarted time.Time
	native       bool
	sequence     uint64
	lastFile     string
	lastProgress backupProgressUpdate
	success      bool
}

func (a *backupActivity) observe(line, stream string, at time.Time) (backupProgressUpdate, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	line = strings.TrimSpace(ansiRE.ReplaceAllString(line, ""))
	if stream == "STDERR" && strings.HasPrefix(line, "IOSBK_ACTIVITY ") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return backupProgressUpdate{}, true
		}
		seq, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return backupProgressUpdate{}, true
		}
		switch parts[2] {
		case "preparing", "waiting_authorization", "waiting_device", "sending", "receiving":
		default:
			return backupProgressUpdate{}, true
		}
		if seq < a.sequence {
			return backupProgressUpdate{}, true
		}
		a.native = true
		if a.phase != parts[2] {
			a.phaseStarted = at
		}
		a.phase = parts[2]
		update := backupProgressUpdate{Phase: a.phase}
		if seq > a.sequence {
			a.sequence = seq
			a.last = at
			update.LastActivityAt = at
		}
		return update, true
	}
	if stream != "STDOUT" {
		return backupProgressUpdate{}, false
	}
	if line == "Backup Successful." {
		a.success = true
	}
	update, ok := parseBackupProgressLine(line)
	if a.native {
		update.Phase = "" // stderr 的协议阶段优先；延迟的显示刷新不能覆盖它。
		return update, false
	}
	if strings.Contains(line, "Waiting for passcode") || strings.Contains(line, "entering the passcode on the device") {
		update.Phase = "waiting_authorization"
		ok = true
	}
	if !ok {
		return update, false
	}
	moved := false
	if backupDirectionRE.MatchString(line) && line == a.lastFile {
		update.Phase = ""
	}
	if update.Phase != "" && update.Phase != a.phase {
		a.phase = update.Phase
		a.phaseStarted = at
		moved = true
	}
	if backupDirectionRE.MatchString(line) && line != a.lastFile {
		a.lastFile = line
		moved = true
	}
	if update.OverallPercent != nil && (a.lastProgress.OverallPercent == nil || *update.OverallPercent > *a.lastProgress.OverallPercent) {
		moved = true
	}
	if update.CurrentBytes != nil && (a.lastProgress.CurrentBytes == nil || *update.CurrentBytes != *a.lastProgress.CurrentBytes) {
		moved = true
	}
	if update.OverallPercent != nil {
		if a.lastProgress.OverallPercent == nil || *update.OverallPercent > *a.lastProgress.OverallPercent {
			a.lastProgress.OverallPercent = cloneOptionalFloat64(update.OverallPercent)
		}
	}
	if update.CurrentBytes != nil {
		a.lastProgress.CurrentBytes = cloneOptionalInt64(update.CurrentBytes)
	}
	if update.CurrentBytes != nil && update.CurrentTotalBytes != nil && *update.CurrentBytes == *update.CurrentTotalBytes {
		a.phase = "waiting_device"
		update.Phase = a.phase
	}
	if moved {
		a.last = at
		update.LastActivityAt = at
	}
	return update, false
}

func (a *backupActivity) timeout(cfg runtimeConfig, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit := cfg.BackupPreparationTimeout
	switch a.phase {
	case "waiting_authorization":
		limit = cfg.BackupAuthorizationTimeout
	case "sending", "receiving":
		limit = cfg.BackupInactivityTimeout
	}
	since := a.last
	if a.phaseStarted.After(since) {
		since = a.phaseStarted
	}
	if at.Sub(since) < limit {
		return nil
	}
	return fmt.Errorf("%w（阶段 %s，无活动 %s）", errBackupStalled, a.phase, limit)
}

func (app *application) runBackupCommand(ctx context.Context, cancel context.CancelCauseFunc, udid, sessionID, directory string, network bool) error {
	args := []string{"-u", udid, "backup", directory}
	if network {
		args = append([]string{"-n"}, args...)
	}
	factory := app.backupCommand
	if factory == nil {
		factory = newExecCmd
	}
	cmd := factory(ctx, cmdIdevicebackup2, args...)
	cmd.SetGracefulCancel(3 * time.Second)
	cmd.cmd.Dir = app.paths.BackupBase
	env := append(os.Environ(), "IOSBK_BACKUP_ACTIVITY=1")
	if network {
		env = append(env, app.networkIdeviceEnvVars()...)
	}
	cmd.SetEnv(env)
	if network {
		defer app.registerActiveDeviceCommand(udid, connectTypeNetwork, cancel)()
	}

	activity := &backupActivity{last: time.Now(), phase: "preparing"}
	var readers sync.WaitGroup
	var writers []*io.PipeWriter
	for _, stream := range []string{"STDOUT", "STDERR"} {
		r, w := io.Pipe()
		writers = append(writers, w)
		if stream == "STDOUT" {
			cmd.SetStdout(w)
		} else {
			cmd.SetStderr(w)
		}
		readers.Add(1)
		go func(stream string) {
			defer readers.Done()
			app.readBackupOutput(udid, sessionID, r, stream, func(line, kind string) bool {
				at := time.Now()
				update, internal := activity.observe(line, kind, at)
				if update.Phase != "" || update.OverallPercent != nil || update.CurrentBytes != nil || !update.LastActivityAt.IsZero() {
					app.applyBackupProgressUpdate(udid, update, at)
				}
				return internal
			})
		}(stream)
	}
	interval := time.Second
	for _, d := range []time.Duration{app.runtimeConfig.BackupPreparationTimeout, app.runtimeConfig.BackupInactivityTimeout, app.runtimeConfig.BackupAuthorizationTimeout} {
		if d/4 < interval {
			interval = d / 4
		}
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				if err := activity.timeout(app.runtimeConfig, at); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	err := cmd.Run()
	close(done)
	<-stopped
	for _, w := range writers {
		_ = w.Close()
	}
	readers.Wait() // 旧输出必须在终态与下一任务前全部收束。
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err != nil {
		return err
	}
	if !activity.success {
		return errors.New("备份工具已退出，但未确认本次备份成功")
	}
	return nil
}
