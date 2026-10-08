package app

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type backupProgressState string

const (
	backupProgressPreparing    backupProgressState = "preparing"
	backupProgressRunning      backupProgressState = "running"
	backupProgressReconnecting backupProgressState = "reconnecting"
	backupProgressInterrupted  backupProgressState = "interrupted"
	backupProgressCompleted    backupProgressState = "completed"
	backupProgressFailed       backupProgressState = "failed"
)

type backupProgress struct {
	State              backupProgressState
	Phase              string
	OverallPercent     *float64
	CurrentFilePercent *float64
	CurrentBytes       *int64
	CurrentTotalBytes  *int64
	UpdatedAt          time.Time
	LastActivityAt     time.Time
}

// backupProgressUpdate 只包含一行输出能够确认的事实。文件路径不会进入结构化状态，
// 避免通过状态接口暴露照片、应用或其他私有目录名称。
type backupProgressUpdate struct {
	LastActivityAt     time.Time
	Phase              string
	OverallPercent     *float64
	CurrentFilePercent *float64
	CurrentBytes       *int64
	CurrentTotalBytes  *int64
}

const backupProgressBroadcastInterval = 500 * time.Millisecond

var (
	backupOverallProgressRE = regexp.MustCompile(`\bBackup\s+\[[^\]]*\]\s*([0-9]+(?:\.[0-9]+)?)%`)
	backupFileProgressRE    = regexp.MustCompile(`\[[^\]]*\]\s*([0-9]+(?:\.[0-9]+)?)%\s+([0-9]+(?:\.[0-9]+)?)\s*(Bytes|kB|KB|MB|GB)\s*/\s*([0-9]+(?:\.[0-9]+)?)\s*(Bytes|kB|KB|MB|GB)\b`)
	backupDirectionRE       = regexp.MustCompile(`^(Receiving|Sending)\s+\S`)
)

// parseBackupProgressLine 从 idevicebackup2 的规范化输出中提取进度。
// 不认识或数值越界的行直接忽略，不能影响实际备份命令。
func parseBackupProgressLine(line string) (backupProgressUpdate, bool) {
	line = ansiRE.ReplaceAllString(line, "")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return backupProgressUpdate{}, false
	}

	if matches := backupOverallProgressRE.FindStringSubmatch(line); matches != nil {
		percent, ok := parseProgressPercent(matches[1])
		if !ok {
			return backupProgressUpdate{}, false
		}
		return backupProgressUpdate{OverallPercent: &percent}, true
	}

	if matches := backupFileProgressRE.FindStringSubmatch(line); matches != nil {
		percent, ok := parseProgressPercent(matches[1])
		if !ok {
			return backupProgressUpdate{}, false
		}
		current, ok := parseDisplayedBytes(matches[2], matches[3])
		if !ok {
			return backupProgressUpdate{}, false
		}
		total, ok := parseDisplayedBytes(matches[4], matches[5])
		if !ok || current > total {
			return backupProgressUpdate{}, false
		}
		return backupProgressUpdate{
			CurrentFilePercent: &percent,
			CurrentBytes:       &current,
			CurrentTotalBytes:  &total,
		}, true
	}

	if matches := backupDirectionRE.FindStringSubmatch(line); matches != nil {
		return backupProgressUpdate{
			Phase: strings.ToLower(matches[1]),
		}, true
	}

	return backupProgressUpdate{}, false
}

func parseProgressPercent(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return 0, false
	}
	return value, true
}

func parseDisplayedBytes(raw, unit string) (int64, bool) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	multiplier := float64(1)
	switch strings.ToUpper(unit) {
	case "BYTES":
	case "KB":
		multiplier = 1_000
	case "MB":
		multiplier = 1_000_000
	case "GB":
		multiplier = 1_000_000_000
	default:
		return 0, false
	}
	bytes := value * multiplier
	if bytes > math.MaxInt64 {
		return 0, false
	}
	return int64(math.Round(bytes)), true
}

func cloneOptionalFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOptionalInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (app *application) startBackupProgress(udid string, at time.Time) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.backupProgress == nil {
		app.backupProgress = make(map[string]backupProgress)
	}
	if app.backupProgressBroadcast == nil {
		app.backupProgressBroadcast = make(map[string]time.Time)
	}
	app.backupProgress[udid] = backupProgress{
		State:     backupProgressPreparing,
		Phase:     "preparing",
		UpdatedAt: toBeijingTime(at),
	}
	app.backupProgressBroadcast[udid] = at
}

// applyBackupProgressUpdate 合并一行输出提供的局部事实。终态不会被迟到的输出协程覆盖。
func (app *application) applyBackupProgressUpdate(udid string, update backupProgressUpdate, at time.Time) bool {
	app.mu.Lock()

	progress, ok := app.backupProgress[udid]
	if !ok || progress.State == backupProgressCompleted || progress.State == backupProgressFailed || progress.State == backupProgressInterrupted {
		app.mu.Unlock()
		return false
	}

	changed := false
	if !update.LastActivityAt.IsZero() && update.LastActivityAt.After(progress.LastActivityAt) {
		progress.LastActivityAt = toBeijingTime(update.LastActivityAt)
		changed = true
	}
	forceBroadcast := progress.State != backupProgressRunning || (update.Phase != "" && progress.Phase != update.Phase)
	if progress.State != backupProgressRunning {
		progress.State = backupProgressRunning
		changed = true
	}
	if update.Phase != "" && progress.Phase != update.Phase {
		progress.Phase = update.Phase
		changed = true
	}
	if update.OverallPercent != nil && (progress.OverallPercent == nil || *update.OverallPercent >= *progress.OverallPercent) {
		if progress.OverallPercent == nil || *progress.OverallPercent != *update.OverallPercent {
			changed = true
		}
		progress.OverallPercent = cloneOptionalFloat64(update.OverallPercent)
	}
	if update.CurrentFilePercent != nil {
		if progress.CurrentFilePercent == nil || *progress.CurrentFilePercent != *update.CurrentFilePercent {
			changed = true
		}
		progress.CurrentFilePercent = cloneOptionalFloat64(update.CurrentFilePercent)
	}
	if update.CurrentBytes != nil {
		if progress.CurrentBytes == nil || *progress.CurrentBytes != *update.CurrentBytes {
			changed = true
		}
		progress.CurrentBytes = cloneOptionalInt64(update.CurrentBytes)
	}
	if update.CurrentTotalBytes != nil {
		if progress.CurrentTotalBytes == nil || *progress.CurrentTotalBytes != *update.CurrentTotalBytes {
			changed = true
		}
		progress.CurrentTotalBytes = cloneOptionalInt64(update.CurrentTotalBytes)
	}
	if changed {
		progress.UpdatedAt = toBeijingTime(at)
		app.backupProgress[udid] = progress
	}
	lastBroadcast := app.backupProgressBroadcast[udid]
	shouldBroadcast := changed && (forceBroadcast || lastBroadcast.IsZero() || at.Sub(lastBroadcast) >= backupProgressBroadcastInterval)
	if shouldBroadcast {
		app.backupProgressBroadcast[udid] = at
	}
	app.mu.Unlock()

	if shouldBroadcast {
		app.broadcastStatus()
	}
	return shouldBroadcast
}

func (app *application) finishBackupProgress(udid string, err error, at time.Time) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.finishBackupProgressUnsafe(udid, err, at)
}

// 调用者持有 app.mu，以便终态与任务占用一起提交。
func (app *application) finishBackupProgressUnsafe(udid string, err error, at time.Time) {
	progress, ok := app.backupProgress[udid]
	if !ok {
		return
	}

	progress.CurrentFilePercent = nil
	progress.CurrentBytes = nil
	progress.CurrentTotalBytes = nil
	progress.UpdatedAt = toBeijingTime(at)
	app.backupProgressBroadcast[udid] = at
	if err == nil {
		complete := float64(100)
		progress.State = backupProgressCompleted
		progress.Phase = "completed"
		progress.OverallPercent = &complete
	} else if errors.Is(err, errDeviceDisconnected) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		progress.State = backupProgressInterrupted
		progress.Phase = "interrupted"
	} else {
		progress.State = backupProgressFailed
		progress.Phase = "failed"
	}
	app.backupProgress[udid] = progress
}

// setBackupProgressConnectionUnsafe 记录 Wi-Fi 任务处于断线宽限期或已恢复。
// 调用者必须持有 app.mu；原传输阶段保留在 Phase 中，便于恢复后继续展示。
func (app *application) setBackupProgressConnectionUnsafe(udid string, online bool, at time.Time) {
	progress, ok := app.backupProgress[udid]
	if !ok {
		return
	}
	if online {
		if progress.State != backupProgressReconnecting {
			return
		}
		progress.State = backupProgressRunning
	} else {
		if progress.State != backupProgressPreparing && progress.State != backupProgressRunning {
			return
		}
		progress.State = backupProgressReconnecting
	}
	progress.UpdatedAt = toBeijingTime(at)
	app.backupProgress[udid] = progress
}
