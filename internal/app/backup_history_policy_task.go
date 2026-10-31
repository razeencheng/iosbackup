package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
)

type historyPolicyRun struct {
	ID        string `json:"id,omitempty"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
	cancel    context.CancelFunc
	done      chan struct{}
}

// 状态只暴露任务结果；目标策略与 pending 标记在配置中持久化，重启后可重试。
func (app *application) historyPolicyStatus(udid string, cfg *backupConfig) historyPolicyRun {
	view := historyPolicyRun{State: "idle"}
	if cfg != nil && cfg.HistoryPolicyPending {
		view.State = "interrupted"
	}
	service := app.historyService()
	service.mu.Lock()
	defer service.mu.Unlock()
	if run := service.policies[udid]; run != nil {
		view.ID, view.State, view.ErrorCode = run.ID, run.State, run.ErrorCode
	}
	return view
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
	go func() {
		defer cancel()
		err := app.runHistoryPolicy(work)
		service.mu.Lock()
		run.State = "succeeded"
		if err != nil {
			run.State, run.ErrorCode = "failed", i18n.HistoryUnavailable
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				run.State = "interrupted"
			}
			if errors.Is(err, backupstore.ErrCapacity) {
				run.ErrorCode = i18n.HistoryCapacity
			}
		}
		close(run.done)
		service.mu.Unlock()
		app.broadcastStatus()
	}()
	return historyPolicyRun{ID: run.ID, State: "running"}, nil
}
