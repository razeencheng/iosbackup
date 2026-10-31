package app

import (
	"iosbackup/internal/backupstore"
	"math"
	"time"
)

const backupJobPointLinkVersion = 1

type backupJobSummary struct {
	PointLinkVersion     int                `json:"point_link_version,omitempty"`
	HistoryPhase         string             `json:"history_phase,omitempty"`
	HistoryCommitted     bool               `json:"history_committed"`
	PointID              string             `json:"point_id,omitempty"`
	ExecutionState       string             `json:"execution_state,omitempty"`
	HistoryState         string             `json:"history_state,omitempty"`
	Operation            string             `json:"operation,omitempty"`
	Check                *backupCheckResult `json:"check,omitempty"`
	ExecutionCompletedAt time.Time          `json:"execution_completed_at,omitempty"`
	ID                   string             `json:"id"`
	UDID                 string             `json:"udid"`
	DeviceName           string             `json:"device_name"`
	Source               string             `json:"source"`
	RequestedMode        string             `json:"requested_mode"`
	ObservedMode         string             `json:"observed_mode"`
	State                string             `json:"state"`
	ErrorCode            string             `json:"error_code,omitempty"`
	StartedAt            time.Time          `json:"started_at"`
	CompletedAt          time.Time          `json:"completed_at,omitempty"`
	LastActivityAt       time.Time          `json:"last_activity_at,omitempty"`
	FirstCursor          uint64             `json:"first_cursor"`
	LastCursor           uint64             `json:"last_cursor"`
	Dropped              uint64             `json:"dropped"`
	Truncated            bool               `json:"truncated"`
	Persistence          string             `json:"persistence"`
	Revision             uint64             `json:"revision"`
}

// 事件只包含封闭字段。原始工具输出、路径、密码及令牌没有序列化入口。
type backupJobEvent struct {
	Cursor    uint64    `json:"cursor"`
	At        time.Time `json:"at"`
	Code      string    `json:"code"`
	Phase     string    `json:"phase,omitempty"`
	Percent   *float64  `json:"percent,omitempty"`
	Bytes     *int64    `json:"bytes,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
}

func validJobEvent(e backupJobEvent) bool {
	switch e.Code {
	case "started", "progress", "phase_changed", "stop_requested", "succeeded", "failed", "stopped", "interrupted":
	default:
		return false
	}
	switch e.Phase {
	case "", "preparing", "waiting_authorization", "waiting_device", "sending", "receiving", "completed":
	default:
		return false
	}
	switch e.ErrorCode {
	case "", "backup_failed", "backup_stalled", "device_disconnected", "backup_cancelled", "storage_unavailable", "device_locked", "backup_stopped_by_user", "process_restarted":
	default:
		return false
	}
	return (e.Percent == nil || (!math.IsNaN(*e.Percent) && !math.IsInf(*e.Percent, 0) && *e.Percent >= 0 && *e.Percent <= 100)) && (e.Bytes == nil || *e.Bytes >= 0)
}

func (app *application) recordBackupActivity(id string, update backupProgressUpdate) {
	if app.backupJobRecords != nil {
		update.LastActivityAt = app.inDeploymentTime(update.LastActivityAt)
		app.backupJobRecords.activity(id, update, app.nowTime())
	}
}

func validBackupJobSummary(summary backupJobSummary) bool {
	if summary.PointLinkVersion != 0 && summary.PointLinkVersion != backupJobPointLinkVersion {
		return false
	}
	switch summary.HistoryPhase {
	case "", "commit", "reference", "cleanup", "completed":
	default:
		return false
	}
	if summary.HistoryCommitted && summary.PointID == "" {
		return false
	}
	if summary.PointID != "" && !backupstore.ValidID(summary.PointID) {
		return false
	}
	if summary.Operation != "" && summary.Operation != "backup" && summary.Operation != "check" {
		return false
	}
	switch summary.ExecutionState {
	case "", "running", "succeeded", "failed", "stopped", "interrupted", "not_run":
	default:
		return false
	}
	switch summary.HistoryState {
	case "", "not_enabled", "preparing", "saved", "pending", "failed", "uncertain", "not_saved":
	default:
		return false
	}
	if summary.Check != nil && !validBackupCheckResult(*summary.Check) {
		return false
	}

	if !udidPattern.MatchString(summary.UDID) || len(summary.DeviceName) > 256 || summary.StartedAt.IsZero() {
		return false
	}
	if summary.Source != "manual" && summary.Source != "automatic" {
		return false
	}
	if summary.RequestedMode != "auto" && summary.RequestedMode != "full" {
		return false
	}
	if summary.ObservedMode != "unknown" && summary.ObservedMode != "full" && summary.ObservedMode != "incremental" {
		return false
	}
	switch summary.State {
	case backupStateStarting, backupStateRunning, backupStateStopping, backupStateStopped, backupStateSucceeded, backupStateFailed, backupStateInterrupted:
	default:
		return false
	}
	return validJobEvent(backupJobEvent{Code: "failed", ErrorCode: summary.ErrorCode})
}

// 记录模块只接收不可变任务信息，不持有控制器或设备状态。
type backupJobSeed struct {
	ID, UDID, DeviceName, Source, RequestedMode, Operation, PointID string
	StartedAt                                                       time.Time
}
