package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
)

type historyPolicyRun struct {
	Progress      *backupLocalProgress `json:"progress,omitempty"`
	ID            string               `json:"id,omitempty"`
	State         string               `json:"state"`
	ErrorCode     string               `json:"error_code,omitempty"`
	SettingsSaved bool                 `json:"settings_saved"`
	CleanupState  string               `json:"cleanup_state,omitempty"`
	cancel        context.CancelFunc
	done          chan struct{}
}

// 状态只暴露任务结果；目标策略与 pending 标记在配置中持久化，重启后可重试。
func (app *application) historyPolicyStatus(udid string, cfg *backupConfig) historyPolicyRun {
	view := historyPolicyRun{State: "idle"}
	if cfg != nil && cfg.HistoryPolicyPending {
		view.State = "interrupted"
		view.SettingsSaved, view.CleanupState = true, "pending"
	}
	service := app.historyService()
	service.mu.Lock()
	defer service.mu.Unlock()
	if run := service.policies[udid]; run != nil {
		view.ID, view.State, view.ErrorCode = run.ID, run.State, run.ErrorCode
		view.SettingsSaved, view.CleanupState = run.SettingsSaved, run.CleanupState
		view.Progress = run.Progress
	}
	return view
}

func historyPolicyErrorCode(err error, fallback string) string {
	var translated *i18n.Error
	switch {
	case errors.As(err, &translated):
		return translated.Code
	case errors.Is(err, backupstore.ErrCapacity), errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return i18n.HistoryCapacity
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EROFS):
		return i18n.StorageUnavailable
	default:
		return fallback
	}
}

func (app *application) startHistoryPolicy(requestCtx context.Context, udid string, policy backupstore.Policy, confirmed *historyPolicyPreview) (historyPolicyRun, error) {
	service := app.historyService()
	ctx, cancel := context.WithCancel(app.rootCtx)
	run := &historyPolicyRun{ID: newBackupSessionID(), State: "running", cancel: cancel, done: make(chan struct{})}
	service.mu.Lock()
	if service.closed || service.policies[udid] != nil && service.policies[udid].State == "running" {
		service.mu.Unlock()
		cancel()
		return historyPolicyRun{}, backupstore.ErrBusy
	}
	service.policies[udid] = run
	service.mu.Unlock()
	work, err := app.prepareHistoryPolicyChange(requestCtx, udid, policy, confirmed)
	if err != nil {
		service.mu.Lock()
		delete(service.policies, udid)
		close(run.done)
		service.mu.Unlock()
		cancel()
		return historyPolicyRun{}, err
	}
	work.job.ctx = ctx
	work.job.id = run.ID
	service.mu.Lock()
	run.SettingsSaved, run.CleanupState = true, "pending"
	service.mu.Unlock()
	go func() {
		defer cancel()
		err := app.runHistoryPolicy(work)
		if err != nil {
			app.addWarnLog(udid, fmt.Sprintf("event=backup.history_policy_failed job_id=%s error=%v", run.ID, err))
		}
		service.mu.Lock()
		run.CleanupState = work.job.historyCleanup
		if run.CleanupState == "" {
			run.CleanupState = "not_needed"
		}
		run.State = "succeeded"
		if err != nil {
			run.State, run.ErrorCode = "failed", historyPolicyErrorCode(err, i18n.HistoryUnavailable)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				run.State = "interrupted"
			}
		}
		close(run.done)
		service.mu.Unlock()
		app.broadcastStatus()
	}()
	return historyPolicyRun{ID: run.ID, State: "running", SettingsSaved: true, CleanupState: "pending"}, nil
}
