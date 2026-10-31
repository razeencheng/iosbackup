package app

import (
	"context"
	"iosbackup/internal/backupstore"
	"path/filepath"
	"slices"
)

// This is the backend contract for the T11 policy API. The browser never supplies
// filesystem paths or a deletion list; it confirms a server-computed preview.
type historyPolicyPreview struct {
	Previous     backupstore.Policy                   `json:"previous"`
	EpochID      string                               `json:"epoch_id"`
	Root         string                               `json:"-"`
	GenerationID string                               `json:"generation_id"`
	Policy       backupstore.Policy                   `json:"policy"`
	Stores       map[string]backupstore.RetentionPlan `json:"stores"`
}

func (app *application) previewHistoryPolicy(ctx context.Context, udid string, policy backupstore.Policy) (historyPolicyPreview, error) {
	policy, err := policy.Normalize()
	if err != nil {
		return historyPolicyPreview{}, err
	}
	app.mu.RLock()
	m := app.backupStorage
	cfg := cloneBackupConfig(app.configs[udid])
	blocked := app.deviceRemovalBlockedUnsafe(udid)
	app.mu.RUnlock()
	if blocked != nil {
		return historyPolicyPreview{}, blocked
	}
	if cfg == nil {
		return historyPolicyPreview{}, errDeviceNotFound
	}
	if cfg.HistoryPolicyPending && cfg.History != policy {
		return historyPolicyPreview{}, errDeviceBusy
	}
	if m == nil {
		return historyPolicyPreview{}, backupstore.ErrUncertain
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return historyPolicyPreview{}, err
	}
	previous, _ := cfg.History.Normalize()
	preview := historyPolicyPreview{Previous: previous, EpochID: st.EpochID, Root: cfg.BackupDirectory, Policy: policy, Stores: map[string]backupstore.RetentionPlan{}}
	if st.EpochID == "" {
		return preview, nil
	}
	generations, err := app.generationRegistry().load(udid)
	if err != nil {
		return preview, err
	}
	preview.GenerationID = generations.Current
	stores, err := m.deviceStores(udid)
	if err != nil {
		return preview, err
	}
	for _, s := range stores {
		if err = ctx.Err(); err != nil {
			return preview, err
		}
		// Disabling preserves every point; re-enabling leaves old manual points alone.
		effective := policy
		effective.Enabled = policy.Enabled && st.Phase == "active"
		plan, err := s.PlanGC(backupstore.RetentionRequest{Policy: effective, EpochID: st.EpochID, GenerationID: generations.Current, Now: app.nowTime()})
		if err != nil {
			return preview, err
		}
		c, err := s.Snapshot()
		if err != nil {
			return preview, err
		}
		if c.Revision != plan.Revision {
			return preview, backupstore.ErrConflict
		}
		preview.Stores[c.StoreID] = plan
	}
	return preview, nil
}
func sameHistoryPreview(a, b historyPolicyPreview) bool {
	if a.Previous != b.Previous || a.EpochID != b.EpochID || a.GenerationID != b.GenerationID || a.Policy != b.Policy || len(a.Stores) != len(b.Stores) {
		return false
	}
	for id, p := range a.Stores {
		q, ok := b.Stores[id]
		if !ok || p.Revision != q.Revision || !slices.Equal(p.Delete, q.Delete) {
			return false
		}
	}
	return true
}

type historyPolicyWork struct {
	job     *backupJob
	root    string
	release func()
}

// 先持久化目标策略并占用设备，再把耗时转换交给任务执行；配置锁不跨越复制。
func (app *application) prepareHistoryPolicyChange(ctx context.Context, udid string, policy backupstore.Policy, confirmed *historyPolicyPreview) (_ *historyPolicyWork, retErr error) {
	policy, err := policy.Normalize()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.Lock()
	if err = app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return nil, err
	}
	cfg := cloneBackupConfig(app.configs[udid])
	if cfg == nil {
		app.mu.Unlock()
		return nil, errDeviceNotFound
	}
	if cfg.HistoryPolicyPending && cfg.History != policy {
		app.mu.Unlock()
		return nil, errDeviceBusy
	}
	if app.backupInProgress[udid] || app.checkInProgress[udid] {
		app.mu.Unlock()
		return nil, errDeviceBusy
	}
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	root := cfg.BackupDirectory
	if root == "" {
		root = app.paths.BackupsRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		return nil, err
	}
	app.beginBackupIndexWrite(udid, root, true)
	release := func() {
		manager := app.lockBackupIndexFinish()
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		var lease *backupIndexWrite
		if manager != nil {
			lease = manager.releaseWriteUnsafe(udid)
			manager.mu.Unlock()
		}
		app.afterBackupIndexWrite(manager, lease, false)
		app.broadcastStatus()
	}
	defer func() {
		if retErr != nil {
			release()
		}
	}()
	// 仅重试已持久化且未改变的目标可跳过预览；故障中的存储由后台任务恢复。
	if !cfg.HistoryPolicyPending {
		preview, err := app.previewHistoryPolicy(ctx, udid, policy)
		if err != nil {
			return nil, err
		}
		if confirmed != nil && (!sameHistoryPreview(preview, *confirmed) || preview.Root != confirmed.Root) {
			return nil, backupstore.ErrConflict
		}
		st, err := app.backupStorage.deviceState(udid)
		if err != nil {
			return nil, err
		}
		if policy.Enabled && st.Phase == "active" && policy.Days < st.Days && confirmed == nil {
			return nil, backupstore.ErrConflict
		}
	}
	cfg.History = policy
	cfg.HistoryPolicyPending = true
	if err = app.configStore.Put(*cfg); err != nil {
		return nil, err
	}
	app.mu.Lock()
	app.overlayBackupHoldUnsafe(cfg)
	app.configs[udid] = cfg
	app.mu.Unlock()
	job := &backupJob{udid: udid, id: newBackupSessionID(), config: cloneBackupConfig(cfg), ctx: ctx, request: backupRequest{Mode: "auto"}, historyPolicyOnly: true}
	return &historyPolicyWork{job: job, root: root, release: release}, nil
}

func (app *application) completeHistoryPolicy(job *backupJob) error {
	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.RLock()
	cfg := cloneBackupConfig(app.configs[job.udid])
	app.mu.RUnlock()
	if cfg == nil || cfg.History != job.config.History {
		return backupstore.ErrConflict
	}
	if !cfg.HistoryPolicyPending {
		return nil
	}
	cfg.HistoryPolicyPending = false
	if err := app.configStore.Put(*cfg); err != nil {
		return err
	}
	app.mu.Lock()
	app.overlayBackupHoldUnsafe(cfg)
	app.configs[job.udid] = cfg
	app.mu.Unlock()
	return nil
}

func (app *application) runHistoryPolicy(work *historyPolicyWork) error {
	defer work.release()
	if _, err := app.prepareBackupHistory(work.job, work.root); err != nil {
		return err
	}
	return app.completeHistoryPolicy(work.job)
}

func (app *application) applyHistoryPolicy(ctx context.Context, udid string, policy backupstore.Policy, confirmed *historyPolicyPreview) error {
	work, err := app.prepareHistoryPolicyChange(ctx, udid, policy, confirmed)
	if err != nil {
		return err
	}
	return app.runHistoryPolicy(work)
}
