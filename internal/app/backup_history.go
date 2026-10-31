package app

import (
	"context"
	"errors"
	"fmt"
	"iosbackup/internal/backupstore"
	"os"
	"path/filepath"
	"time"
)

const historyIOTimeout = 6 * time.Hour

type historyDeviceState struct {
	WritableCompletedAt time.Time              `json:"writable_completed_at,omitempty"`
	CurrentReset        bool                   `json:"current_reset,omitempty"`
	DeletedAt           time.Time              `json:"deleted_at,omitempty"`
	TransitionSource    string                 `json:"transition_source,omitempty"`
	CurrentDeleted      bool                   `json:"current_deleted,omitempty"`
	EpochID             string                 `json:"epoch_id"`
	Phase               string                 `json:"phase"` // enabling | active | disabling | disabled
	Days                int                    `json:"days"`
	Root                string                 `json:"root"`
	StoreID             string                 `json:"store_id"`
	LatestStore         string                 `json:"latest_store,omitempty"`
	LatestPoint         string                 `json:"latest_point,omitempty"`
	WritableStore       string                 `json:"writable_store,omitempty"`
	WritableID          string                 `json:"writable_id,omitempty"`
	WritableGeneration  backupstore.Generation `json:"writable_generation,omitempty"`
	MigrationDone       bool                   `json:"migration_done"`
	LegacyState         string                 `json:"legacy_state,omitempty"`
	LegacyBytes         int64                  `json:"legacy_bytes,omitempty"`
}
type backupHistoryRun struct {
	forceFull  bool
	state      historyDeviceState
	store      *backupstore.Store
	work       backupstore.Work
	generation backupstore.Generation
	active     bool
}

func (m *backupStorage) deviceState(udid string) (historyDeviceState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uncertain {
		return historyDeviceState{}, backupstore.ErrUncertain
	}
	return m.locations.Devices[udid], nil
}
func (m *backupStorage) saveDevice(udid string, state historyDeviceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uncertain {
		return backupstore.ErrUncertain
	}
	if m.locations.Devices == nil {
		m.locations.Devices = map[string]historyDeviceState{}
	}
	m.locations.Devices[udid] = state
	if err := m.save(); err != nil {
		m.uncertain = true
		return errors.Join(backupstore.ErrUncertain, err)
	}
	return nil
}
func (m *backupStorage) store(id string) (*backupstore.Store, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uncertain {
		return nil, backupstore.ErrUncertain
	}
	if err := m.unavailable[id]; err != nil {
		return nil, err
	}
	if s := m.stores[id]; s != nil {
		return s, nil
	}
	return nil, backupstore.ErrUncertain
}
func (m *backupStorage) deviceStores(udid string) ([]*backupstore.Store, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uncertain {
		return nil, backupstore.ErrUncertain
	}
	var stores []*backupstore.Store
	for _, location := range m.locations.Stores {
		if location.UDID == udid {
			if err := m.unavailable[location.ID]; err != nil {
				return nil, err
			}
			s := m.stores[location.ID]
			if s == nil {
				return nil, backupstore.ErrUncertain
			}
			stores = append(stores, s)
		}
	}
	return stores, nil
}

// 调用方必须独占设备写入；读租约由 Recover 再次检查，不能越过仍在使用的来源。
func (m *backupStorage) recoverDevice(ctx context.Context, udid string) error {
	stores, err := m.deviceStores(udid)
	if err != nil {
		return err
	}
	for _, store := range stores {
		if _, err := store.ReadSnapshot(); errors.Is(err, backupstore.ErrUncertain) {
			if _, err = store.Recover(ctx); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

// sourceRoot is a memory-only lookup. The actual reader still validates identity.
func (m *backupStorage) sourceRoot(udid, root string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.locations.Devices[udid]
	if !ok || filepath.Clean(root) != st.Root {
		return root, nil
	}
	if m.uncertain {
		return "", backupstore.ErrUncertain
	}
	id, object, dir := st.LatestStore, st.LatestPoint, "objects"
	if st.Phase == "disabled" {
		id, object, dir = st.WritableStore, st.WritableID, "work"
	}
	if st.CurrentDeleted {
		return "", os.ErrNotExist
	}
	if st.Phase == "enabling" || st.Phase == "disabling" {
		return "", backupstore.ErrBusy
	}
	if id == "" || object == "" {
		return "", os.ErrNotExist
	}
	if err := m.unavailable[id]; err != nil {
		return "", err
	}
	for _, location := range m.locations.Stores {
		if location.ID == id && location.UDID == udid {
			return filepath.Join(location.Path, dir, object), nil
		}
	}
	return "", backupstore.ErrUncertain
}
func (app *application) historySourceRoot(udid, root string) (string, error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return root, nil
	}
	return m.sourceRoot(udid, filepath.Clean(root))
}

// Called only while the common device admission and index writer are held.
func (app *application) prepareBackupHistory(job *backupJob, root string) (*backupHistoryRun, error) {
	if job.config.HistoryPolicyPending && !job.historyPolicyOnly {
		// 重启或迁移中断后先完成已保存的目标策略，不能按旧来源模式开始写手机数据。
		transition := *job
		transition.historyPolicyOnly = true
		if _, err := app.prepareBackupHistory(&transition, root); err != nil {
			return nil, err
		}
		if err := app.completeHistoryPolicy(&transition); err != nil {
			return nil, err
		}
		job.config.HistoryPolicyPending = false
	}
	policy, err := job.config.History.Normalize()
	if err != nil {
		return nil, err
	}
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		if policy.Enabled {
			return nil, backupstore.ErrUncertain
		}
		return nil, nil
	}
	if err := m.recoverDevice(job.ctx, job.udid); err != nil {
		return nil, err
	}
	st, err := m.reconcileHistoryLatest(job.udid)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled && st.EpochID == "" {
		return nil, nil
	}
	if !job.historyPolicyOnly {
		app.setHistoryJobState(job, "preparing")
	}
	ctx, cancel := context.WithTimeout(job.ctx, historyIOTimeout)
	defer cancel()
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if !safeBackupRoot(root) {
		return nil, backupstore.ErrUnsafe
	}
	if !policy.Enabled {
		if st.Phase != "disabled" || st.Root != root {
			st, err = app.disableBackupHistory(ctx, job, root, st)
			if err != nil {
				return nil, err
			}
		}
		store, err := m.store(st.WritableStore)
		if err != nil {
			return nil, err
		}
		if st.CurrentDeleted && !st.CurrentReset {
			if err = store.AbortWork(ctx, st.WritableID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			work, _, err := m.prepareFrom(ctx, st.StoreID, job.id, "", "", backupstore.CopyOptions{})
			if err != nil {
				return nil, err
			}
			if err = store.SyncWork(ctx, work.ID); err != nil {
				return nil, err
			}
			st.WritableID = work.ID
			st.WritableStore = st.StoreID
			st.CurrentReset = true
			st.WritableGeneration = backupstore.Generation{}
			st.WritableCompletedAt = time.Time{}
			if err = m.saveDevice(job.udid, st); err != nil {
				return nil, err
			}
		}
		work, err := store.WorkLocation(st.WritableID)
		if err != nil {
			return nil, err
		}
		return &backupHistoryRun{state: st, store: store, work: work, forceFull: st.CurrentDeleted}, nil
	}
	if st.Phase != "disabled" && st.EpochID != "" && policy.Days < st.Days && !job.config.HistoryPolicyPending {
		return nil, backupstore.ErrConflict
	}
	if st.Phase == "disabling" {
		st, err = app.disableBackupHistory(ctx, job, root, st)
		if err != nil {
			return nil, err
		}
	}
	id, err := m.create(ctx, root, job.udid)
	if err != nil {
		return nil, err
	}
	if st.EpochID == "" || st.Phase == "disabled" {
		st.EpochID, err = backupstore.NewID()
		if err != nil {
			return nil, err
		}
		st.Phase = "enabling"
		if st.CurrentDeleted {
			st.LatestStore, st.LatestPoint = "", ""
		}
		st.MigrationDone = false
	}
	st.StoreID, st.Root, st.Days = id, root, policy.Days
	if err = m.saveDevice(job.udid, st); err != nil {
		return nil, err
	}
	gen, err := app.generationRegistry().historyGeneration(job.udid, "")
	if err != nil {
		return nil, err
	}
	if !st.MigrationDone {
		st, err = app.migrateHistorySource(ctx, job, st, gen)
		if err != nil {
			return nil, err
		}
	}
	st.Phase = "active"
	if err = m.saveDevice(job.udid, st); err != nil {
		return nil, err
	}
	gen, err = app.generationRegistry().historyGeneration(job.udid, "")
	if err != nil {
		return nil, err
	}
	store, err := m.store(id)
	if err != nil {
		return nil, err
	}
	if job.historyPolicyOnly {
		return &backupHistoryRun{state: st, store: store, generation: gen, active: true}, nil
	}
	// Only already-eligible points may be collected before allocating a work copy.
	if err = app.collectBackupHistory(ctx, job.udid, st, gen); err != nil {
		return nil, err
	}
	baseline, sourceStore := "", ""
	if !st.CurrentDeleted && job.request.Mode != "full" && st.LatestPoint != "" {
		source, err := m.store(st.LatestStore)
		if err != nil {
			return nil, err
		}
		catalog, err := source.Snapshot()
		if err != nil {
			return nil, err
		}
		point, ok := catalog.Points[st.LatestPoint]
		if ok && point.Generation == gen && point.State == "normal" && gen.Kind != "unknown" {
			state, err := app.generationRegistry().load(job.udid)
			if err != nil {
				return nil, err
			}
			if state.compatibleBaseline(point, true) {
				metadataOK := app.historyBaselineMetadata(ctx, source, point, gen)
				if metadataOK {
					baseline, sourceStore = point.ID, point.StoreID
				}
			}
		}
	}
	work, _, err := m.prepareFrom(ctx, id, job.id, sourceStore, baseline, backupstore.CopyOptions{PreferClone: true})
	if err != nil {
		return nil, err
	}
	return &backupHistoryRun{state: st, store: store, work: work, generation: gen, active: true, forceFull: baseline == ""}, nil
}
func (app *application) historyBaselineMetadata(ctx context.Context, store *backupstore.Store, p backupstore.Point, g backupstore.Generation) bool {
	lease, err := store.Acquire(p.ID)
	if err != nil {
		return false
	}
	defer lease.Close()
	metadata, err := readBackupCheckPlist(ctx, lease.Root, "Manifest.plist", nil)
	if err != nil {
		return false
	}
	encrypted, ok := metadata["IsEncrypted"].(bool)
	if !ok || encrypted != (g.Kind == "encrypted") {
		return false
	}
	if encrypted {
		pw, err := app.generationRegistry().password(p.UDID, g.CredentialRef)
		return err == nil && pw != ""
	}
	return true
}
func (app *application) collectBackupHistory(ctx context.Context, udid string, st historyDeviceState, gen backupstore.Generation) error {
	stores, err := app.backupStorage.deviceStores(udid)
	if err != nil {
		return err
	}
	for _, store := range stores {
		req := backupstore.RetentionRequest{Policy: backupstore.Policy{Enabled: st.Phase == "active", Days: st.Days}, EpochID: st.EpochID, GenerationID: gen.ID, Now: app.nowTime()}
		plan, err := store.PlanGC(req)
		if err != nil {
			return err
		}
		if len(plan.Delete) > 0 {
			if _, err = store.GC(ctx, req, plan.Revision); err != nil {
				return err
			}
		}
	}
	return nil
}
func (app *application) disableBackupHistory(ctx context.Context, job *backupJob, root string, st historyDeviceState) (historyDeviceState, error) {
	m := app.backupStorage
	if st.Phase != "disabling" {
		st.TransitionSource = "normal"
		if st.Phase == "enabling" && !st.MigrationDone && st.LatestPoint == "" && st.WritableID == "" {
			st.TransitionSource = "legacy"
		}
		if st.Phase == "disabled" && st.WritableID != "" && !st.CurrentDeleted {
			st.TransitionSource = "current"
		}
	}
	previous := st
	st.Phase = "disabling"
	if err := m.saveDevice(job.udid, st); err != nil {
		return st, err
	}
	stores, err := m.deviceStores(job.udid)
	if err != nil {
		return st, err
	}
	for _, s := range stores {
		if err = s.Freeze(ctx); err != nil {
			return st, err
		}
	}
	id, err := m.create(ctx, root, job.udid)
	if err != nil {
		return st, err
	}
	sourceStore, baseline := st.LatestStore, st.LatestPoint
	if st.CurrentDeleted {
		sourceStore, baseline = "", ""
	}
	var work backupstore.Work
	if previous.TransitionSource == "current" && previous.WritableID != "" && !previous.CurrentDeleted {
		oldStore, e := m.store(previous.WritableStore)
		if e != nil {
			return st, e
		}
		oldWork, e := oldStore.WorkLocation(previous.WritableID)
		if e != nil {
			return st, e
		}
		target, e := m.store(id)
		if e != nil {
			return st, e
		}
		work, e = target.NewWork(job.id)
		if e != nil {
			return st, e
		}
		select {
		case m.slot <- struct{}{}:
		case <-ctx.Done():
			_ = target.AbortWork(context.Background(), work.ID)
			return st, ctx.Err()
		}
		_, err = backupstore.CopyTree(ctx, oldWork.Directory, work.Directory, backupstore.CopyOptions{PreferClone: true})
		<-m.slot
		if err != nil {
			_ = target.AbortWork(context.Background(), work.ID)
			return st, err
		}
	} else if previous.TransitionSource == "legacy" {
		// 兼容旧版本遗留的首次迁移中断：原目录仍是来源，不能改用空副本。
		target, e := m.store(id)
		if e != nil {
			return st, e
		}
		work, e = target.NewWork(job.id)
		if e != nil {
			return st, e
		}
		select {
		case m.slot <- struct{}{}:
		case <-ctx.Done():
			_ = target.AbortWork(context.Background(), work.ID)
			return st, ctx.Err()
		}
		_, err = backupstore.CopyTree(ctx, filepath.Join(previous.Root, job.udid), work.Directory, backupstore.CopyOptions{PreferClone: true})
		<-m.slot
		if err != nil {
			_ = target.AbortWork(context.Background(), work.ID)
			return st, err
		}
	} else {
		work, _, err = m.prepareFrom(ctx, id, job.id, sourceStore, baseline, backupstore.CopyOptions{PreferClone: true})
		if err != nil {
			return st, err
		}
	}
	store, err := m.store(id)
	if err != nil {
		return st, err
	}
	if err = store.SyncWork(ctx, work.ID); err != nil {
		return st, err
	}
	st.Phase, st.Root, st.StoreID = "disabled", root, id
	st.TransitionSource = ""
	st.WritableStore, st.WritableID = id, work.ID
	st.CurrentReset = st.CurrentDeleted
	if previous.TransitionSource != "current" && st.LatestPoint != "" {
		source, e := m.store(st.LatestStore)
		if e != nil {
			return st, e
		}
		c, e := source.Snapshot()
		if e != nil {
			return st, e
		}
		st.WritableGeneration = c.Points[st.LatestPoint].Generation
		st.WritableCompletedAt = c.Points[st.LatestPoint].CompletedAt
	}
	if err = m.saveDevice(job.udid, st); err != nil {
		return st, err
	}
	return st, nil
}
func (app *application) migrateHistorySource(ctx context.Context, job *backupJob, st historyDeviceState, gen backupstore.Generation) (historyDeviceState, error) {
	m := app.backupStorage
	source := filepath.Join(st.Root, job.udid)
	if st.CurrentDeleted {
		st.MigrationDone = true
		st.LegacyState = "deleted"
		return st, m.saveDevice(job.udid, st)
	}
	if st.WritableID != "" {
		s, err := m.store(st.WritableStore)
		if err != nil {
			return st, err
		}
		work, err := s.WorkLocation(st.WritableID)
		if err != nil {
			return st, err
		}
		source = work.Directory
	}
	st.MigrationDone = true
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		st.LegacyState = "missing"
		return st, m.saveDevice(job.udid, st)
	} else if err != nil {
		return st, err
	}
	result := app.checkBackupSourceAt(ctx, job.udid, filepath.Dir(source), "quick")
	st.LegacyBytes, _ = originalBackupSize(ctx, source)
	store, err := m.store(st.StoreID)
	if err != nil {
		return st, err
	}
	work, err := store.NewWork(job.id)
	if err != nil {
		return st, err
	}
	select {
	case m.slot <- struct{}{}:
	case <-ctx.Done():
		_ = store.AbortWork(context.Background(), work.ID)
		return st, ctx.Err()
	}
	_, err = backupstore.CopyTree(ctx, source, work.Directory, backupstore.CopyOptions{PreferClone: true})
	<-m.slot
	if err != nil {
		_ = store.AbortWork(context.Background(), work.ID)
		return st, err
	}
	gen, err = app.importHistoryGeneration(ctx, job.udid, work.Directory, st)
	if err != nil {
		return st, err
	}
	at := app.historyImportCompletion(ctx, work.Directory, st)
	unknownTime := at.IsZero() || at.After(app.nowTime())
	if unknownTime {
		at = app.nowTime()
		result.State = "unavailable"
		result.Reason = "unknown_source_time"
	}
	st.LegacyState = result.State
	point := app.historyPoint(st, gen, at, result)
	point.CompletionUnknown = unknownTime
	if st.LegacyState != "passed" {
		point.Manual = true
	}
	saved, err := store.Commit(ctx, work.ID, point)
	if err != nil {
		return st, err
	}
	if saved.State == "normal" {
		st.LatestStore, st.LatestPoint = saved.StoreID, saved.ID
	}
	return st, m.saveDevice(job.udid, st)
}
func (app *application) historyPoint(st historyDeviceState, gen backupstore.Generation, at time.Time, check backupCheckResult) backupstore.Point {
	state := "pending"
	if check.State == "passed" {
		state = "normal"
	}
	return backupstore.Point{Generation: gen, EpochID: st.EpochID, CompletedAt: at, Day: at.In(app.deploymentLocation()).Format("2006-01-02"), Timezone: app.deploymentLocation().String(), State: state, Check: historyCheckEvidence(check)}
}
func (app *application) generationForHistoryResult(ctx context.Context, udid, dir string, fallback backupstore.Generation) (backupstore.Generation, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fallback, err
	}
	defer root.Close()
	manifest, err := readBackupCheckPlist(ctx, root, "Manifest.plist", nil)
	if err != nil {
		return fallback, err
	}
	encrypted, ok := manifest["IsEncrypted"].(bool)
	if !ok {
		return fallback, backupstore.ErrInvalid
	}
	kind := "unencrypted"
	if encrypted {
		kind = "encrypted"
	}
	return app.generationRegistry().historyGeneration(udid, kind)
}
func (app *application) finishBackupHistory(job *backupJob, run *backupHistoryRun, check *backupCheckResult, at time.Time) {
	if run == nil {
		return
	}
	if !run.active {
		if check.State == "passed" {
			gen, err := app.generationForHistoryResult(job.ctx, job.udid, run.work.Directory, run.state.WritableGeneration)
			if err == nil {
				run.state.WritableGeneration = gen
				run.state.WritableCompletedAt = at
				run.state.CurrentDeleted = false
				run.state.CurrentReset = false
				err = app.backupStorage.saveDevice(job.udid, run.state)
			}
			if err != nil {
				app.setHistoryJobState(job, "uncertain")
				app.addErrorLog(job.udid, fmt.Sprintf("event=backup.current_metadata_failed error=%v", err))
			} else {
				check.Freshness = "current"
				app.bindCurrentHistoryCheck(job.udid, run.work.Parent, check)
				app.mu.Lock()
				app.markCheckSourceUnsafe(job.udid, check)
				app.backupCheckResults[job.udid] = *check
				app.mu.Unlock()
				if app.backupJobRecords != nil {
					app.backupJobRecords.setCheck(job.id, *check)
				}
			}
		}
		return
	}
	ctx, cancel := context.WithTimeout(app.rootCtx, historyIOTimeout)
	defer cancel()
	gen := run.generation
	app.setHistoryJobPhase(job, "commit", "")
	var err error
	if check.State == "passed" {
		gen, err = app.generationForHistoryResult(ctx, job.udid, run.work.Directory, gen)
	}
	var saved backupstore.Point
	if err == nil {
		saved, err = run.store.Commit(ctx, run.work.ID, app.historyPoint(run.state, gen, at, *check))
	}
	if err == nil {
		// 可靠提交后立即记录点 ID，后续引用保存或清理失败不能抹去已保存的事实。
		app.setHistoryJobPhase(job, "reference", saved.ID)
		if saved.State == "normal" {
			run.state.LatestStore, run.state.LatestPoint = saved.StoreID, saved.ID
			run.state.CurrentDeleted = false
			run.state.CurrentReset = false
			err = app.backupStorage.saveDevice(job.udid, run.state)
		}
		if err == nil {
			app.setHistoryJobPhase(job, "cleanup", saved.ID)
			err = app.collectBackupHistory(ctx, job.udid, run.state, gen)
		}
	}
	if err != nil {
		state := "failed"
		if errors.Is(err, backupstore.ErrUncertain) {
			state = "uncertain"
		}
		app.setHistoryJobState(job, state)
		app.addErrorLog(job.udid, fmt.Sprintf("event=backup.history_save_failed job_id=%s error=%v", job.id, err))
		return // Uncertain publication is retained for recovery, never aborted.
	}
	state := "saved"
	if saved.State == "pending" {
		state = "pending"
	}
	app.setHistoryJobPhase(job, "completed", saved.ID)
	app.setHistoryJobState(job, state)
	// The immutable point replaces the work source identity in the check result.
	src, sourceErr := resolveBackupIndexSourceAt(ctx, job.udid, filepath.Join(filepath.Dir(filepath.Dir(run.work.Parent)), "objects", saved.ID))
	if sourceErr == nil {
		check.SourceKey = indexDigest(filepath.Clean(run.state.Root) + "\x00" + job.udid)
		check.SourceIdentity = src.Identity
		check.SourceFingerprint = src.Fingerprint
		check.Freshness = "stale"
		if saved.State == "normal" {
			check.Freshness = "current"
		}
	}
	app.mu.Lock()
	app.markCheckSourceUnsafe(job.udid, check)
	app.backupCheckResults[job.udid] = *check
	app.mu.Unlock()
	if app.backupJobRecords != nil {
		app.backupJobRecords.setCheck(job.id, *check)
	}
}
func (app *application) setHistoryJobState(job *backupJob, state string) {
	job.historyState = state
	if app.backupJobRecords != nil {
		app.backupJobRecords.setHistory(job.id, state)
	}
}
func (s *backupJobStore) setHistory(id, state string) {
	s.mu.Lock()
	if r := s.records[id]; r != nil && !s.closed {
		r.Summary.HistoryState = state
		s.changedUnsafe(r)
	}
	s.mu.Unlock()
	s.signal()
}

// reconcileHistoryLatest repairs the application reference after a store commit
// completed but the registry save did not. It never invents or republishes data.
func (m *backupStorage) reconcileHistoryLatest(udid string) (historyDeviceState, error) {
	st, err := m.deviceState(udid)
	if err != nil || st.EpochID == "" || st.Phase == "disabled" || st.Phase == "disabling" {
		return st, err
	}
	stores, err := m.deviceStores(udid)
	if err != nil {
		return st, err
	}
	var latest backupstore.Point
	for _, s := range stores {
		c, err := s.Snapshot()
		if err != nil {
			return st, err
		}
		for _, p := range c.Points {
			if p.State == "normal" && p.EpochID == st.EpochID && (!st.CurrentDeleted || p.CommittedAt.After(st.DeletedAt)) && (latest.ID == "" || p.CommittedAt.After(latest.CommittedAt) || p.CommittedAt.Equal(latest.CommittedAt) && p.ID > latest.ID) {
				latest = p
			}
		}
	}
	if latest.ID != "" && (st.LatestPoint != latest.ID || st.LatestStore != latest.StoreID) {
		st.LatestPoint, st.LatestStore = latest.ID, latest.StoreID
		st.CurrentDeleted = false
		st.CurrentReset = false
		err = m.saveDevice(udid, st)
	}
	return st, err
}

func (app *application) historyUnpackRoot(udid, source string) (string, error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return source, nil
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return "", err
	}
	if st.EpochID == "" {
		return source, nil
	}
	// Derived files are outside both immutable objects and writable backup sets.
	root := filepath.Join(st.Root, ".iosbackup-unpacked", "v1", udid, indexDigest(source))
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}

// acquireHistoryRead pins an immutable source for a complete external query.
// The configured root is never replaced with an old legacy source on an error.
func (app *application) acquireHistoryRead(udid, configuredRoot string) (string, func(), error) {
	root, release, _, err := app.acquireHistorySource(udid, configuredRoot)
	return root, release, err
}

// 来源身份与读取租约一次解析，避免把旧代基线误标为当前数据。
func (app *application) acquireHistorySource(udid, configuredRoot string) (string, func(), historyCurrentSourceView, error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	noop := func() {}
	source := historyCurrentSourceView{Kind: "current"}
	if m == nil {
		return configuredRoot, noop, source, nil
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return "", nil, source, err
	}
	if st.EpochID == "" || st.Root != filepath.Clean(configuredRoot) {
		return configuredRoot, noop, source, nil
	}
	if st.CurrentDeleted {
		source.Kind = "missing"
		return "", nil, source, os.ErrNotExist
	}
	if st.Phase == "disabled" || (st.Phase == "enabling" && !st.MigrationDone && st.WritableID != "") || (st.Phase == "disabling" && st.TransitionSource == "current") {
		source.StoreID = st.WritableStore
		store, err := m.store(st.WritableStore)
		if err != nil {
			return "", nil, source, err
		}
		work, err := store.WorkLocation(st.WritableID)
		if err != nil {
			return "", nil, source, err
		}
		return work.Parent, noop, source, nil
	}
	if (st.Phase == "enabling" && !st.MigrationDone && st.LatestPoint == "") || (st.Phase == "disabling" && st.TransitionSource == "legacy") {
		return configuredRoot, noop, source, nil
	}
	if st.LatestPoint == "" {
		source.Kind = "missing"
		return "", nil, source, backupstore.ErrBusy
	}
	source.Kind, source.PointID, source.StoreID = "point", st.LatestPoint, st.LatestStore
	s, err := m.store(st.LatestStore)
	if err != nil {
		return "", nil, source, err
	}
	lease, err := s.Acquire(st.LatestPoint)
	if err != nil {
		return "", nil, source, err
	}
	return filepath.Dir(lease.Directory), func() { _ = lease.Close() }, source, nil
}

// Restore may update backup metadata. A managed source is therefore copied to
// a disposable work set, and only that set is ever passed to the device tool.
func (app *application) prepareHistoryRestore(ctx context.Context, udid, configuredRoot string) (string, func(), error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return configuredRoot, func() {}, nil
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return "", nil, err
	}
	if st.EpochID == "" || st.Root != filepath.Clean(configuredRoot) {
		return configuredRoot, func() {}, nil
	}
	sourceRoot, release, err := app.acquireHistoryRead(udid, configuredRoot)
	if err != nil {
		return "", nil, err
	}
	defer release()
	s, err := m.store(st.StoreID)
	if err != nil {
		return "", nil, err
	}
	work, err := s.NewWork(newBackupSessionID())
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), historyIOTimeout)
		defer cancel()
		if err := s.AbortWork(ctx, work.ID); err != nil {
			app.addErrorLog(udid, fmt.Sprintf("event=backup.history_cleanup_failed error=%v", err))
		}
	}
	select {
	case m.slot <- struct{}{}:
	case <-ctx.Done():
		cleanup()
		return "", nil, ctx.Err()
	}
	_, err = backupstore.CopyTree(ctx, filepath.Join(sourceRoot, udid), work.Directory, backupstore.CopyOptions{PreferClone: true})
	<-m.slot
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return work.Parent, cleanup, nil
}

// Translate a captured managed source to the configured current-backup key only
// while it still refers to that exact source; never re-label an old result.
func (app *application) bindCurrentHistoryCheck(udid, actualRoot string, result *backupCheckResult) {
	app.mu.RLock()
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	app.mu.RUnlock()
	actual, err := app.historySourceRoot(udid, root)
	if err == nil && filepath.Clean(actual) == filepath.Clean(actualRoot) {
		absolute, err := filepath.Abs(root)
		if err == nil {
			result.SourceKey = indexDigest(filepath.Clean(absolute) + "\x00" + udid)
		}
	}
}

// Query credentials follow the captured source, not the device's current alias.
func (app *application) backupPasswordEnvAt(udid, sourceRoot string) ([]string, error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return app.backupPasswordEnv(udid), nil
	}
	m.mu.Lock()
	var storeID, pointID string
	for _, location := range m.locations.Stores {
		if location.UDID != udid {
			continue
		}
		if filepath.Dir(sourceRoot) == filepath.Join(location.Path, "objects") {
			storeID, pointID = location.ID, filepath.Base(sourceRoot)
			break
		}
	}
	m.mu.Unlock()
	if storeID != "" {
		store, err := m.store(storeID)
		if err != nil {
			return nil, err
		}
		c, err := store.Snapshot()
		if err != nil {
			return nil, err
		}
		p, ok := c.Points[pointID]
		if !ok {
			return nil, backupstore.ErrConflict
		}
		return app.historyGenerationPasswordEnv(udid, p.Generation)
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return nil, err
	}
	if st.Phase == "disabled" && st.WritableID != "" && !st.CurrentDeleted {
		store, err := m.store(st.WritableStore)
		if err != nil {
			return nil, err
		}
		w, err := store.WorkLocation(st.WritableID)
		if err != nil {
			return nil, err
		}
		if w.Parent == filepath.Clean(sourceRoot) {
			return app.historyGenerationPasswordEnv(udid, st.WritableGeneration)
		}
	}
	return app.backupPasswordEnv(udid), nil
}
func (app *application) historyGenerationPasswordEnv(udid string, g backupstore.Generation) ([]string, error) {
	if g.Kind != "encrypted" || g.CredentialRef == "" {
		return nil, nil
	}
	pw, err := app.generationRegistry().password(udid, g.CredentialRef)
	if err != nil {
		return nil, err
	}
	if pw == "" {
		return nil, nil
	}
	return []string{"BACKUP_PASSWORD=" + pw}, nil
}

// The legacy delete action may delete only the isolated writable current set.
func (app *application) deleteHistoryCurrent(udid, path string) (bool, error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return false, nil
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return false, err
	}
	if st.EpochID == "" {
		return false, nil
	}
	if st.Phase != "disabled" {
		return true, backupstore.ErrConflict
	}
	store, err := m.store(st.WritableStore)
	if err != nil {
		return true, err
	}
	work, err := store.WorkLocation(st.WritableID)
	if err != nil {
		return true, err
	}
	if work.Directory != path {
		return false, nil
	}
	// Save delete intent first. A restart can finish this owned cleanup, but cannot
	// silently rebuild the deleted current set from a retained historical point.
	st.CurrentDeleted = true
	st.CurrentReset = false
	st.DeletedAt = app.nowTime().UTC()
	if err = m.saveDevice(udid, st); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(app.rootCtx, historyIOTimeout)
	defer cancel()
	return true, store.AbortWork(ctx, work.ID)
}

func (app *application) historyImportCompletion(ctx context.Context, dir string, st historyDeviceState) time.Time {
	if st.WritableID != "" {
		return st.WritableCompletedAt
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return time.Time{}
	}
	defer root.Close()
	for _, field := range [][2]string{{"Status.plist", "Date"}, {"Info.plist", "Last Backup Date"}} {
		values, err := readBackupCheckPlist(ctx, root, field[0], nil)
		if err != nil {
			return time.Time{}
		}
		if value, ok := values[field[1]]; ok {
			at, _ := value.(time.Time)
			return at
		}
	}
	return time.Time{}
}
func (app *application) importHistoryGeneration(ctx context.Context, udid, dir string, st historyDeviceState) (backupstore.Generation, error) {
	kind := "unknown"
	root, err := os.OpenRoot(dir)
	if err == nil {
		values, e := readBackupCheckPlist(ctx, root, "Manifest.plist", nil)
		root.Close()
		if e == nil {
			if encrypted, ok := values["IsEncrypted"].(bool); ok {
				kind = "unencrypted"
				if encrypted {
					kind = "encrypted"
				}
			}
		}
	}
	if st.WritableID != "" && st.WritableGeneration.Validate() == nil && st.WritableGeneration.Kind == kind {
		return st.WritableGeneration, nil
	}
	return app.generationRegistry().historyImportGeneration(udid, kind)
}

// In-place writes to the isolated current set invalidate its previous completion
// and credential evidence before the tool can mutate any file.
func (app *application) startHistoryCurrentWrite(job *backupJob, run *backupHistoryRun) error {
	if run == nil || run.active {
		return nil
	}
	if err := job.ctx.Err(); err != nil {
		return err
	}
	run.state.WritableCompletedAt = time.Time{}
	run.state.WritableGeneration = backupstore.Generation{}
	if err := app.backupStorage.saveDevice(job.udid, run.state); err != nil {
		app.setHistoryJobState(job, "uncertain")
		return err
	}
	return nil
}

func historyCheckEvidence(r backupCheckResult) backupstore.Check {
	return backupstore.Check{State: r.State, Scope: r.Kind, CheckedAt: r.CompletedAt, Reason: r.Reason, MetadataComplete: r.MetadataComplete, ManifestComplete: r.ManifestComplete, RecordsChecked: r.RecordsChecked, ObjectsChecked: r.ObjectsChecked, MissingObjects: r.MissingObjects, InvalidRecords: r.InvalidRecords, UnsupportedRecords: r.UnsupportedRecords}
}
