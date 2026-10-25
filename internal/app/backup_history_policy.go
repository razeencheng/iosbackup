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
	if m == nil {
		return historyPolicyPreview{}, backupstore.ErrUncertain
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return historyPolicyPreview{}, err
	}
	preview := historyPolicyPreview{EpochID: st.EpochID, Root: cfg.BackupDirectory, Policy: policy, Stores: map[string]backupstore.RetentionPlan{}}
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
	if a.EpochID != b.EpochID || a.GenerationID != b.GenerationID || a.Policy != b.Policy || len(a.Stores) != len(b.Stores) {
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
func (app *application) applyHistoryPolicy(ctx context.Context, udid string, policy backupstore.Policy, confirmed *historyPolicyPreview) (retErr error) {
	policy, err := policy.Normalize()
	if err != nil {
		return err
	}
	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.Lock()
	if err = app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	cfg := cloneBackupConfig(app.configs[udid])
	if cfg == nil {
		app.mu.Unlock()
		return errDeviceNotFound
	}
	if app.backupInProgress[udid] || app.checkInProgress[udid] {
		app.mu.Unlock()
		return errDeviceBusy
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
		return err
	}
	app.beginBackupIndexWrite(udid, root, true)
	defer func() {
		manager := app.lockBackupIndexFinish()
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		var lease *backupIndexWrite
		if manager != nil {
			lease = manager.releaseWriteUnsafe(udid)
			manager.mu.Unlock()
		}
		app.afterBackupIndexWrite(manager, lease, retErr == nil)
		app.broadcastStatus()
	}()
	preview, err := app.previewHistoryPolicy(ctx, udid, policy)
	if err != nil {
		return err
	}
	st, err := app.backupStorage.deviceState(udid)
	if err != nil {
		return err
	}
	if policy.Enabled && st.Phase == "active" && policy.Days < st.Days {
		if confirmed == nil || !sameHistoryPreview(preview, *confirmed) {
			return backupstore.ErrConflict
		}
		st.Days = policy.Days
		if err = app.backupStorage.saveDevice(udid, st); err != nil {
			return err
		}
	}
	cfg.History = policy
	job := &backupJob{udid: udid, id: newBackupSessionID(), config: cfg, ctx: ctx, request: backupRequest{Mode: "auto"}, historyPolicyOnly: true}
	if _, err = app.prepareBackupHistory(job, root); err != nil {
		return err
	}
	if err = app.configStore.Put(*cfg); err != nil {
		return err
	}
	app.mu.Lock()
	app.overlayBackupHoldUnsafe(cfg)
	app.configs[udid] = cfg
	app.mu.Unlock()
	return nil
}
