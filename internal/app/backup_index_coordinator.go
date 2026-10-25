package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"iosbackup/internal/i18n"
)

type backupIndexBrief struct {
	SourceKey      string `json:"source_key"`
	Revision       string `json:"revision"`
	RefreshState   string `json:"refresh_state"`
	OverviewStatus string `json:"overview_status"`
	ListStatus     string `json:"list_status"`
}
type backupIndexJob struct {
	source  backupIndexSource
	epoch   uint64
	cancel  context.CancelFunc
	done    chan struct{}
	rerun   bool
	next    *backupIndexSource
	refresh backupIndexRefresh
}
type backupIndexWrite struct {
	epoch   uint64
	deleted bool
	source  backupIndexSource
	changed bool
}
type backupIndexFresh struct {
	fingerprint        string
	overviewAt, listAt time.Time
}
type backupIndexCoordinator struct {
	captures map[string]*backupIndexOnlineCapture
	// 锁序为 coordinator.mu -> store.mu / app.mu；持有 app.mu 时不得调用协调器。
	// 扫描不持有这些锁，短暂发布事务与写入准入共享此锁。
	mu        sync.Mutex
	app       *application
	store     *backupIndexStore
	jobs      map[string]*backupIndexJob
	epochs    map[string]uint64
	writes    map[string]*backupIndexWrite
	attempted map[string]string
	fresh     map[string]backupIndexFresh
	states    map[string]backupIndexRefresh
	slot      chan struct{}
	wake      chan struct{}
	workers   sync.WaitGroup
	closed    bool
	read      func(context.Context, backupIndexSource) (backupIndexOverview, backupIndexProducer, error)
}

func (app *application) backupIndexManager() *backupIndexCoordinator {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.backupIndex == nil {
		app.backupIndex = &backupIndexCoordinator{app: app, store: newBackupIndexStore(filepath.Join(app.paths.ConfigsRoot, "backup-index", "v1")), jobs: map[string]*backupIndexJob{}, epochs: map[string]uint64{}, writes: map[string]*backupIndexWrite{}, attempted: map[string]string{}, fresh: map[string]backupIndexFresh{}, states: map[string]backupIndexRefresh{}, slot: make(chan struct{}, 1), wake: make(chan struct{})}
		app.backupIndex.closed = app.backupIndexClosed
		app.backupIndex.read = app.readLocalBackupIndex
	}
	return app.backupIndex
}
func (app *application) readLocalBackupIndex(ctx context.Context, src backupIndexSource) (backupIndexOverview, backupIndexProducer, error) {
	overview, overviewErr := readBackupIndexOverview(ctx, src, nil)
	produce := func(emit func([]string) error) error {
		_, err := streamLocalBackupIndex(ctx, src, overview.Encrypted, app.manifestRows, func(r backupIndexRecord) error {
			return emit([]string{r.FileID, r.Domain, r.RelativePath, strconv.Itoa(r.Flags)})
		})
		return err
	}
	return overview, produce, overviewErr
}
func (m *backupIndexCoordinator) signalUnsafe() { close(m.wake); m.wake = make(chan struct{}) }
func (app *application) wakeBackupIndex() {
	app.mu.RLock()
	m := app.backupIndex
	app.mu.RUnlock()
	if m != nil {
		m.mu.Lock()
		m.signalUnsafe()
		m.mu.Unlock()
	}
}
func (m *backupIndexCoordinator) setBriefUnsafe(src backupIndexSource, revision string, refresh backupIndexRefresh, overview, list string) {
	m.app.mu.Lock()
	if m.app.backupIndexStatuses == nil {
		m.app.backupIndexStatuses = map[string]backupIndexBrief{}
	}
	m.app.backupIndexStatuses[src.UDID] = backupIndexBrief{SourceKey: src.Key, Revision: revision, RefreshState: refresh.State, OverviewStatus: overview, ListStatus: list}
	m.app.mu.Unlock()
}
func (m *backupIndexCoordinator) request(src backupIndexSource, force bool) bool {
	if src.Key == "" {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	if job := m.jobs[src.UDID]; job != nil {
		if job.source.Key != src.Key {
			job.next = &src
			job.cancel()
		} else if force {
			job.rerun = true
		}
		m.mu.Unlock()
		return true
	}
	if !force && m.attempted[src.Key] == backupIndexAttemptToken(src) {
		m.mu.Unlock()
		return false
	}
	m.attempted[src.Key] = backupIndexAttemptToken(src)
	ctx, cancel := context.WithTimeout(m.app.rootCtx, 15*time.Minute)
	revision, _ := newBackupIndexRevision()
	job := &backupIndexJob{source: src, epoch: m.epochs[src.UDID], cancel: cancel, done: make(chan struct{}), refresh: backupIndexRefresh{State: "queued", Revision: revision, LastAttemptAt: m.app.nowTime()}}
	m.jobs[src.UDID] = job
	m.states[src.Key] = job.refresh
	m.setBriefUnsafe(src, revision, job.refresh, "", "")
	m.workers.Add(1)
	m.mu.Unlock()
	m.app.addInfoLog(src.UDID, "event=backup_index.queued")
	m.app.broadcastStatus()
	go m.run(ctx, job)
	return true
}
func (m *backupIndexCoordinator) run(ctx context.Context, j *backupIndexJob) {
	defer m.workers.Done()
	defer j.cancel()
	defer func() {
		m.mu.Lock()
		rerun := j.rerun && !m.closed && m.epochs[j.source.UDID] == j.epoch && m.writes[j.source.UDID] == nil
		if m.jobs[j.source.UDID] == j {
			delete(m.jobs, j.source.UDID)
		}
		close(j.done)
		m.signalUnsafe()
		m.mu.Unlock()
		if j.next != nil {
			m.request(*j.next, true)
		} else if rerun {
			m.request(j.source, true)
		}
	}()
	// 先等待设备写入释放，再竞争实例的单扫描槽；无需连接服务准入。
	for {
		m.mu.Lock()
		m.app.mu.RLock()
		busy := m.app.backupInProgress[j.source.UDID] || m.app.checkInProgress[j.source.UDID]
		m.app.mu.RUnlock()
		wake := m.wake
		m.mu.Unlock()
		if !busy {
			break
		}
		select {
		case <-ctx.Done():
			m.finishFailed(j, ctx.Err())
			return
		case <-wake:
		}
	}
	select {
	case m.slot <- struct{}{}:
		defer func() { <-m.slot }()
	case <-ctx.Done():
		m.finishFailed(j, ctx.Err())
		return
	}
	m.mu.Lock()
	if !m.validUnsafe(j) {
		m.mu.Unlock()
		m.finishFailed(j, context.Canceled)
		return
	}
	j.refresh.State = "running"
	m.states[j.source.Key] = j.refresh
	m.setBriefUnsafe(j.source, j.refresh.Revision, j.refresh, "", "")
	m.mu.Unlock()
	m.app.addInfoLog(j.source.UDID, "event=backup_index.started")
	m.app.broadcastStatus()
	src, sourceErr := m.app.resolveBackupIndexSource(ctx, j.source.UDID)
	if src.Key != j.source.Key {
		m.finishFailed(j, context.Canceled)
		return
	}
	src.Epoch = j.epoch
	overview := backupIndexOverview{Status: "unavailable", Freshness: "unknown", MessageCode: backupIndexErrorCode(sourceErr)}
	var produce backupIndexProducer
	overviewErr := sourceErr
	if sourceErr == nil {
		overview, produce, overviewErr = m.read(ctx, src)
	} else if errors.Is(sourceErr, errBackupIndexMissing) {
		overview.Status = "missing"
	}
	var candidate *backupIndexCandidate
	listErr := sourceErr
	if produce != nil {
		candidate, listErr = m.store.prepare(ctx, src, "manifest-v1", produce)
	} else if listErr == nil {
		listErr = overviewErr
	}
	defer func() {
		if candidate != nil {
			candidate.discard()
		}
	}()
	if ctx.Err() != nil {
		m.finishFailed(j, ctx.Err())
		return
	}
	// 对本地工具读取的来源作结束指纹检查；来源暂失时仅合并历史失败状态。
	if sourceErr == nil {
		fingerprint, err := backupIndexSourceFingerprint(ctx, src)
		if err != nil || fingerprint != src.Fingerprint {
			m.finishFailed(j, errBackupIndexUnavailable)
			return
		}
	}
	m.mu.Lock()
	if !m.validUnsafe(j) {
		m.mu.Unlock()
		m.finishFailed(j, context.Canceled)
		return
	}
	// 当前配置检查在同一准入临界区内；路径切换不能接收迟到结果。
	if !m.configMatchesUnsafe(src) {
		m.mu.Unlock()
		m.finishFailed(j, context.Canceled)
		return
	}
	result, publishErr := m.store.publish(src, overview, candidate, listErr)
	err := errors.Join(overviewErr, listErr, publishErr)
	refresh := j.refresh
	refresh.State = "idle"
	if err != nil {
		refresh.State = "failed"
		refresh.MessageCode = backupIndexErrorCode(err)
	}
	if publishErr == nil {
		fresh := backupIndexFresh{fingerprint: src.Fingerprint}
		if overviewErr == nil && overview.Status == "available" {
			fresh.overviewAt = result.Overview.ReadAt
		}
		if listErr == nil && candidate != nil {
			fresh.listAt = result.FileList.ReadAt
		}
		m.fresh[src.Key] = fresh
	}
	m.states[src.Key] = refresh
	_ = m.store.saveRefresh(src, refresh)
	m.setBriefUnsafe(src, result.Revision, refresh, result.Overview.Status, result.FileList.Status)
	m.mu.Unlock()
	if err != nil {
		m.app.addWarnLog(src.UDID, "event=backup_index.failed reason="+backupIndexErrorCode(err))
	} else {
		m.app.addInfoLog(src.UDID, "event=backup_index.completed")
	}
	m.app.broadcastStatus()
}
func (m *backupIndexCoordinator) validUnsafe(j *backupIndexJob) bool {
	if m.closed || m.jobs[j.source.UDID] != j || m.epochs[j.source.UDID] != j.epoch || m.writes[j.source.UDID] != nil {
		return false
	}
	m.app.mu.RLock()
	defer m.app.mu.RUnlock()
	return !m.app.backupInProgress[j.source.UDID] && !m.app.checkInProgress[j.source.UDID] && m.app.deviceRemovalBlockedUnsafe(j.source.UDID) == nil
}
func (m *backupIndexCoordinator) configMatchesUnsafe(src backupIndexSource) bool {
	m.app.mu.RLock()
	defer m.app.mu.RUnlock()
	if m.app.deviceRemovalBlockedUnsafe(src.UDID) != nil {
		return false
	}
	root := m.app.paths.BackupsRoot
	if cfg := m.app.configs[src.UDID]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	absolute, err := filepath.Abs(root)
	return err == nil && indexDigest(filepath.Clean(absolute)+"\x00"+src.UDID) == src.Key
}
func (m *backupIndexCoordinator) finishFailed(j *backupIndexJob, err error) {
	m.mu.Lock()
	if m.jobs[j.source.UDID] == j && m.epochs[j.source.UDID] == j.epoch {
		refresh := j.refresh
		refresh.State = "failed"
		refresh.MessageCode = backupIndexErrorCode(err)
		if errors.Is(err, context.Canceled) {
			refresh.State = "idle"
		}
		m.states[j.source.Key] = refresh
		_ = m.store.saveRefresh(j.source, refresh)
		m.setBriefUnsafe(j.source, refresh.Revision, refresh, "", "")
	}
	m.mu.Unlock()
	m.app.addWarnLog(j.source.UDID, "event=backup_index.cancelled reason="+backupIndexErrorCode(err))
	m.app.broadcastStatus()
}
func (m *backupIndexCoordinator) snapshot(src backupIndexSource, sourceErr error) (backupIndexSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.store.load(src)
	if err != nil {
		result = emptyBackupIndexSnapshot(src.Key)
		if !errors.Is(err, os.ErrNotExist) {
			result.Overview.Status = "unavailable"
			result.Overview.MessageCode = i18n.BackupIndexCacheInvalid
		}
	}
	// 根目录可访问且备份目录确实不存在时，不再展示遗留缓存；挂载不可用仍保留历史。
	if errors.Is(sourceErr, errBackupIndexMissing) {
		result = emptyBackupIndexSnapshot(src.Key)
		result.Overview.MessageCode = i18n.BackupIndexMissing
		result.FileList.MessageCode = i18n.BackupIndexMissing
	}
	if sourceErr != nil {
		if result.Overview.Status == "available" {
			result.Overview.Freshness = "stale"
		} else if !errors.Is(sourceErr, errBackupIndexMissing) {
			result.Overview.Status = "unavailable"
		}
		result.Overview.MessageCode = backupIndexErrorCode(sourceErr)
		if result.FileList.Complete {
			result.FileList.Freshness = "stale"
		}
	} else if fresh, ok := m.fresh[src.Key]; ok && fresh.fingerprint == src.Fingerprint {
		if !fresh.overviewAt.IsZero() && fresh.overviewAt.Equal(result.Overview.ReadAt) {
			result.Overview.Freshness = "current"
		}
		if !fresh.listAt.IsZero() && fresh.listAt.Equal(result.FileList.ReadAt) {
			result.FileList.Freshness = "current"
		}
	}
	if state, ok := m.states[src.Key]; ok {
		result.Refresh = state
	}
	m.app.mu.RLock()
	if cfg := m.app.configs[src.UDID]; cfg != nil {
		result.LastBackupAt = m.app.formatTimestamp(cfg.LastBackup)
	}
	m.app.mu.RUnlock()
	return result, nil
}

// beginBackupIndexWrite 在调用者已预留 backupInProgress 后执行，等待发生在全局锁外。
func (app *application) beginBackupIndexWrite(udid, root string, changed bool) *backupIndexWrite {
	if root == "" {
		root = app.paths.BackupsRoot
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	src := backupIndexSource{UDID: udid, Directory: filepath.Join(absolute, udid), Key: indexDigest(filepath.Clean(absolute) + "\x00" + udid)}
	m := app.backupIndexManager()
	m.mu.Lock()
	m.epochs[udid]++
	lease := &backupIndexWrite{source: src, changed: changed, epoch: m.epochs[udid]}
	m.writes[udid] = lease
	delete(m.fresh, src.Key)
	m.states[src.Key] = backupIndexRefresh{State: "idle"}
	revision, _ := newBackupIndexRevision()
	m.setBriefUnsafe(src, revision, m.states[src.Key], "", "")
	var onlineDone <-chan struct{}
	if c := m.captures[udid]; c != nil {
		c.cancel()
		onlineDone = c.done
	}
	var done <-chan struct{}
	if j := m.jobs[udid]; j != nil {
		j.rerun = false
		j.cancel()
		done = j.done
	}
	m.signalUnsafe()
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	if onlineDone != nil {
		<-onlineDone
	}
	if changed {
		app.mu.Lock()
		if check, ok := app.backupCheckResults[udid]; ok {
			check.Freshness = "stale"
			app.backupCheckResults[udid] = check
		}
		app.mu.Unlock()
		if app.backupJobRecords != nil {
			app.backupJobRecords.staleChecks(udid)
		}
		if err := m.store.invalidate(src, false); err != nil {
			app.addWarnLog(udid, "event=backup_index.invalidate_failed reason=cache_write_failed")
		}
	}
	return lease
}

// lockBackupIndexFinish 与写入准入保持同一锁序；返回时协调器锁仍由调用方持有。
func (app *application) lockBackupIndexFinish() *backupIndexCoordinator {
	app.mu.RLock()
	m := app.backupIndex
	app.mu.RUnlock()
	if m != nil {
		m.mu.Lock()
	}
	return m
}
func (m *backupIndexCoordinator) releaseWriteUnsafe(udid string) *backupIndexWrite {
	lease := m.writes[udid]
	delete(m.writes, udid)
	m.signalUnsafe()
	return lease
}
func (app *application) afterBackupIndexWrite(m *backupIndexCoordinator, lease *backupIndexWrite, success bool) {
	if m == nil || lease == nil || !success || lease.deleted {
		return
	}
	src, _ := app.resolveBackupIndexSource(app.rootCtx, lease.source.UDID)
	if src.Key != lease.source.Key {
		return
	}
	m.mu.Lock()
	same := m.epochs[src.UDID] == lease.epoch && !m.closed
	m.mu.Unlock()
	if same {
		m.request(src, true)
	}
}

// 供显式本地操作完成路径使用；正常备份在 finishBackupJob 中同步释放占用。
func (app *application) finishBackupIndexWrite(udid string, success, deleted bool) {
	if deleted {
		app.markBackupIndexDeleted(udid)
		return
	}
	m := app.lockBackupIndexFinish()
	if m == nil {
		return
	}
	lease := m.releaseWriteUnsafe(udid)
	m.mu.Unlock()
	app.afterBackupIndexWrite(m, lease, success)
}
func (app *application) markBackupIndexDeleted(udid string) {
	m := app.lockBackupIndexFinish()
	if m == nil {
		return
	}
	defer m.mu.Unlock()
	lease := m.writes[udid]
	if lease == nil {
		return
	}
	lease.deleted = true
	if err := m.store.invalidate(lease.source, true); err != nil {
		app.addWarnLog(udid, "event=backup_index.invalidate_failed reason=delete_marker_write_failed")
	}
	delete(m.fresh, lease.source.Key)
	m.states[lease.source.Key] = backupIndexRefresh{State: "idle"}
	m.setBriefUnsafe(lease.source, "", m.states[lease.source.Key], "missing", "missing")
}
func backupIndexAttemptToken(src backupIndexSource) string {
	return "v1:" + src.Identity + ":" + src.Fingerprint
}
func (app *application) invalidateBackupIndexDevice(udid string) {
	app.mu.Lock()
	if check, ok := app.backupCheckResults[udid]; ok {
		check.Freshness = "stale"
		app.backupCheckResults[udid] = check
	}
	app.mu.Unlock()
	if app.backupJobRecords != nil {
		app.backupJobRecords.staleChecks(udid)
	}
	app.mu.RLock()
	m := app.backupIndex
	app.mu.RUnlock()
	if m == nil {
		return
	}
	m.mu.Lock()
	m.epochs[udid]++
	if c := m.captures[udid]; c != nil {
		c.cancel()
	}
	if j := m.jobs[udid]; j != nil {
		j.rerun = false
		j.cancel()
	}
	m.signalUnsafe()
	m.mu.Unlock()
	app.mu.Lock()
	delete(app.backupIndexStatuses, udid)
	app.mu.Unlock()
}
func (m *backupIndexCoordinator) close() {
	m.mu.Lock()
	m.closed = true
	m.app.mu.RLock()
	for _, check := range m.app.backupChecks {
		check.cancel()
	}
	m.app.mu.RUnlock()
	for _, j := range m.jobs {
		j.cancel()
	}
	for _, c := range m.captures {
		c.cancel()
	}
	m.signalUnsafe()
	m.mu.Unlock()
	m.workers.Wait()
}
func (app *application) closeBackupIndex() {
	app.mu.Lock()
	app.backupIndexClosed = true
	m := app.backupIndex
	app.mu.Unlock()
	if m != nil {
		m.close()
	}
}
