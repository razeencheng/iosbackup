package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
)

type backupCheckRun struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

// 手动检查占用当前备份，避免备份、删除、解包或配置变更与读取并发。
func (app *application) startBackupCheck(udid string) (string, error) {
	app.mu.RLock()
	known := app.configs[udid] != nil || app.devices[udid] != nil
	app.mu.RUnlock()
	if !known {
		return "", errDeviceNotFound
	}
	backupPath, release, err := app.acquireLocalBackupOp(udid)
	if err != nil {
		return "", err
	}
	// 已知不支持的加密清单在建立任务前拒绝，避免重复留下失败任务。
	// 持有来源占用，只读所选备份的元数据，不使用手机当前的加密设置。
	probeCtx, probeCancel := context.WithTimeout(app.rootCtx, backupQuickCheckTimeout)
	encrypted := app.backupSourceIsEncrypted(probeCtx, udid, filepath.Dir(backupPath))
	probeCancel()
	if encrypted {
		release()
		return "", &i18n.Error{Code: i18n.HistoryCheckEncrypted, Cause: errBackupIndexEncrypted}
	}
	manager := app.backupIndexManager()
	ctx, cancel := context.WithTimeout(app.rootCtx, backupManifestCheckTimeout)
	run := &backupCheckRun{id: newBackupSessionID(), cancel: cancel, done: make(chan struct{})}
	result := backupCheckResult{ID: run.id, Kind: "manifest", State: "running", Freshness: "unknown", StartedAt: app.nowTime()}
	app.bindCurrentHistoryCheck(udid, filepath.Dir(backupPath), &result)
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		cancel()
		release()
		return "", context.Canceled
	}
	manager.workers.Add(1)
	app.mu.Lock()
	if app.backupChecks == nil {
		app.backupChecks = make(map[string]*backupCheckRun)
	}
	app.backupChecks[udid] = run
	if app.backupCheckResults == nil {
		app.backupCheckResults = make(map[string]backupCheckResult)
	}
	app.backupCheckResults[udid] = result
	name := app.backupTaskDeviceNameUnsafe(udid)
	if app.backupJobRecords != nil {
		app.backupJobRecords.start(&backupJobSeed{ID: run.id, UDID: udid, DeviceName: name, Source: "manual", RequestedMode: "auto", Operation: "check", StartedAt: result.StartedAt})
		app.backupJobRecords.setCheck(run.id, result)
		app.backupJobRecords.transition(run.id, backupStateRunning)
	}
	app.mu.Unlock()
	manager.mu.Unlock()
	app.broadcastStatus()
	go func() {
		defer manager.workers.Done()
		defer cancel()
		defer close(run.done)
		select {
		case manager.slot <- struct{}{}:
			result = app.checkBackupSourceAt(ctx, udid, filepath.Dir(backupPath), "manifest")
			<-manager.slot
		case <-ctx.Done():
			result.State, result.Reason = backupCheckError(ctx.Err())
			result.CompletedAt = app.nowTime()
		}
		result.ID = run.id
		app.bindCurrentHistoryCheck(udid, filepath.Dir(backupPath), &result)
		app.mu.Lock()
		app.markCheckSourceUnsafe(udid, &result)
		if app.backupJobRecords != nil {
			app.backupJobRecords.setCheck(run.id, result)
			state, code := backupCheckJobOutcome(result)
			app.backupJobRecords.finish(run.id, state, code, app.nowTime())
		}
		app.backupCheckResults[udid] = result
		delete(app.backupChecks, udid)
		app.mu.Unlock()
		release()
	}()
	return run.id, nil
}

// 无法确定加密状态时仍走原检查流程，保留元数据缺失、损坏等真实诊断。
func (app *application) backupSourceIsEncrypted(ctx context.Context, udid, root string) bool {
	src, err := locateBackupIndexSourceAt(ctx, udid, root)
	if err != nil {
		return false
	}
	dir, err := openBackupIndexRoot(src)
	if err != nil {
		return false
	}
	defer dir.Close()
	manifest, err := app.plistLimit().readPlist(ctx, dir, "Manifest.plist", nil)
	return err == nil && manifest["IsEncrypted"] == true
}

func (app *application) checkBackupSourceAt(ctx context.Context, udid, root, kind string) backupCheckResult {
	src, err := locateBackupIndexSourceAt(ctx, udid, root)
	var result backupCheckResult
	if err != nil {
		state, reason := backupCheckError(err)
		result = backupCheckResult{Kind: kind, State: state, Reason: reason, StartedAt: app.nowTime(), CompletedAt: app.nowTime(), Freshness: "unknown", SourceKey: src.Key}
	} else {
		result = app.plistLimit().runBackupCheck(ctx, src, kind, nil, app.manifestRows)
	}
	result.StartedAt = app.inDeploymentTime(result.StartedAt)
	result.CompletedAt = app.inDeploymentTime(result.CompletedAt)
	return result
}

func (app *application) handleBackupCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, 403, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-check/")
	if !udidPattern.MatchString(udid) {
		writeError(w, 400, i18n.InvalidUDID)
		return
	}
	var request struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := decodeRequestJSON(r, &request); err != nil {
		writeError(w, 400, i18n.InvalidJSON)
		return
	}
	if request.Action == "cancel" {
		app.mu.RLock()
		run := app.backupChecks[udid]
		if run != nil && run.id == request.ID {
			run.cancel()
			app.mu.RUnlock()
			writeJSON(w, 202, map[string]string{"id": request.ID})
			return
		}
		app.mu.RUnlock()
		writeError(w, 409, i18n.BackupJobStale)
		return
	}
	if request.Action != "start" {
		writeError(w, 400, i18n.InvalidJSON)
		return
	}
	id, err := app.startBackupCheck(udid)
	if err != nil {
		if errors.Is(err, errBackupIndexEncrypted) {
			writeOperationError(w, http.StatusUnprocessableEntity, err, i18n.HistoryCheckEncrypted)
			return
		}
		writeOperationError(w, 409, err, i18n.OperationFailed)
		return
	}
	writeJSON(w, 202, map[string]string{"id": id})
}

// 自动轻量检查在工具和输出读取器退出后运行，仍持有本次备份的写入占用。
func (app *application) quickCheckBackup(job *backupJob) backupCheckResult {
	ctx, cancel := context.WithTimeout(app.rootCtx, backupQuickCheckTimeout)
	defer cancel()
	pending := backupCheckResult{ID: job.id, Kind: "quick", State: "running", Freshness: "unknown", StartedAt: app.nowTime()}
	app.mu.Lock()
	if app.backupCheckResults == nil {
		app.backupCheckResults = make(map[string]backupCheckResult)
	}
	app.backupCheckResults[job.udid] = pending
	app.mu.Unlock()
	if app.backupJobRecords != nil {
		app.backupJobRecords.setCheck(job.id, pending)
	}
	app.broadcastStatus()
	root := job.backupRoot
	if root == "" {
		root = job.config.BackupDirectory
	}
	if root == "" {
		root = app.paths.BackupsRoot
	}
	result := app.checkBackupSourceAt(ctx, job.udid, root, "quick")
	result.ID = job.id
	app.bindCurrentHistoryCheck(job.udid, root, &result)
	app.mu.Lock()
	if app.backupCheckResults == nil {
		app.backupCheckResults = make(map[string]backupCheckResult)
	}
	app.markCheckSourceUnsafe(job.udid, &result)
	app.backupCheckResults[job.udid] = result
	if app.backupJobRecords != nil {
		app.backupJobRecords.setCheck(job.id, result)
	}
	app.mu.Unlock()
	return result
}

// 检查结束时刻与执行结束时刻独立保留。
func (s *backupJobStore) setCheck(id string, result backupCheckResult) {
	s.mu.Lock()
	if r := s.records[id]; r != nil && !s.closed {
		copy := result
		r.Summary.Check = &copy
		s.changedUnsafe(r)
	}
	s.mu.Unlock()
	s.signal()
}
func (s *backupJobStore) executionFinished(id, state string, at time.Time) {
	s.mu.Lock()
	if r := s.records[id]; r != nil && !s.closed && r.Summary.ExecutionCompletedAt.IsZero() {
		r.Summary.ExecutionCompletedAt = at
		r.Summary.ExecutionState = state
		s.changedUnsafe(r)
	}
	s.mu.Unlock()
	s.signal()
}
func (s *backupJobStore) staleChecks(udid string) {
	s.mu.Lock()
	for _, r := range s.records {
		if r.Summary.UDID == udid && r.Summary.PointID == "" && r.Summary.Check != nil {
			copy := *r.Summary.Check
			copy.Freshness = "stale"
			r.Summary.Check = &copy
			s.changedUnsafe(r)
		}
	}
	s.mu.Unlock()
	s.signal()
}

func (app *application) backupCheckSnapshotUnsafe(udid string) *backupCheckResult {
	source := app.currentCheckSourceUnsafe(udid)
	var jobs []backupJobSummary
	if app.backupJobRecords != nil {
		jobs = app.backupJobRecords.list(udid)
	}
	source.bindLegacyEvidence(jobs)
	if value, ok := app.backupCheckResults[udid]; ok {
		active := app.backupChecks[udid] != nil && app.backupChecks[udid].id == value.ID
		ref := source.checkRef(value.SourceRef, &value)
		if active || source.matches(ref) {
			copy := value
			if copy.SourceRef == "" && ref != "" {
				copy.Freshness = "unknown"
			}
			copy.SourceRef = ref
			app.markCheckSourceUnsafe(udid, &copy)
			return app.attachCompletedCheckUnsafe(udid, &copy)
		}
	}
	for _, job := range jobs {
		if job.Check != nil && !(job.Operation == "check" && job.PointID != "") && source.matches(source.checkRef(backupJobCheckSourceRef(job), job.Check)) {
			copy := *job.Check
			copy.SourceRef = source.checkRef(backupJobCheckSourceRef(job), job.Check)
			copy.Freshness = "unknown"
			app.markCheckSourceUnsafe(udid, &copy)
			return app.attachCompletedCheckUnsafe(udid, &copy)
		}
	}
	return source.pointResult()
}

type currentBackupCheckSource struct {
	managed        bool
	ref            string
	point          backupstore.Point
	legacyEvidence *backupCheckResult
}

// 旧当前点手动检查没有 SourceRef，需要生成任务提供来源身份。
// 内容变化不能抹去同一来源的异常；通过结果仍要求完整指纹一致。
func (s *currentBackupCheckSource) bindLegacyEvidence(jobs []backupJobSummary) {
	if s.point.ID == "" {
		return
	}
	for _, job := range jobs {
		if job.ID == s.point.JobID && job.PointID == s.point.ID && job.Operation != "check" && job.Check != nil {
			s.legacyEvidence = job.Check
			return
		}
	}
}

func (s currentBackupCheckSource) checkRef(ref string, check *backupCheckResult) string {
	if ref != "" || check == nil || s.legacyEvidence == nil {
		return ref
	}
	old := s.legacyEvidence
	if old.SourceKey == "" || old.SourceIdentity == "" || old.SourceKey != check.SourceKey || old.SourceIdentity != check.SourceIdentity {
		return ""
	}
	if old.SourceFingerprint != "" && old.SourceFingerprint == check.SourceFingerprint || check.State == "issues" || check.State == "unavailable" {
		return s.ref
	}
	return ""
}

func (s currentBackupCheckSource) matches(ref string) bool {
	if s.managed {
		return s.ref != "" && ref == s.ref
	}
	return ref == ""
}

func backupJobCheckSourceRef(job backupJobSummary) string {
	if job.Check.SourceRef != "" {
		return job.Check.SourceRef
	}
	// 兼容已有记录：备份任务已持久化点 ID，独立历史检查仍由调用方排除。
	if job.PointID != "" {
		return "point:" + job.PointID
	}
	return ""
}

// 只读内存中的当前引用与目录快照，不在状态广播锁内访问磁盘。
// 缓存不能证明存储在线，因此重启后仍显示 unknown 新鲜度。
func (app *application) currentCheckSourceUnsafe(udid string) currentBackupCheckSource {
	m := app.backupStorage
	if m == nil {
		return currentBackupCheckSource{}
	}
	st, err := m.deviceState(udid)
	if err != nil {
		return currentBackupCheckSource{managed: true}
	}
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	if st.EpochID == "" || filepath.Clean(root) != st.Root {
		return currentBackupCheckSource{}
	}
	source := currentBackupCheckSource{managed: true}
	if st.CurrentDeleted {
		return source
	}
	if st.Phase == "disabled" && st.WritableID != "" {
		source.ref = "work:" + st.WritableID
		return source
	}
	if st.Phase != "active" || st.LatestPoint == "" {
		return source
	}
	store, err := m.store(st.LatestStore)
	if err != nil {
		return source
	}
	p, ok := store.CachedPoint(st.LatestPoint)
	if !ok || p.State != "normal" {
		return source
	}
	source.ref, source.point = "point:"+p.ID, p
	return source
}

func (s currentBackupCheckSource) pointResult() *backupCheckResult {
	if s.point.ID == "" {
		return nil
	}
	p, c := s.point, s.point.Check
	id := ""
	if backupJobIDPattern.MatchString(p.JobID) {
		id = p.JobID
	}
	return &backupCheckResult{ID: id, Kind: c.Scope, State: c.State, Reason: c.Reason, PlistFile: c.PlistFile, PlistLimitMiB: c.PlistLimitMiB, StartedAt: c.CheckedAt, CompletedAt: c.CheckedAt, SourceRef: s.ref, Freshness: "unknown", MetadataComplete: c.MetadataComplete, ManifestComplete: c.ManifestComplete, RecordsChecked: c.RecordsChecked, ObjectsChecked: c.ObjectsChecked, MissingObjects: c.MissingObjects, InvalidRecords: c.InvalidRecords, UnsupportedRecords: c.UnsupportedRecords}
}

func (app *application) backupTaskDeviceNameUnsafe(udid string) string {
	if device := app.devices[udid]; device != nil && device.knownName() != "" {
		return device.knownName()
	}
	if cfg := app.configs[udid]; cfg != nil && cfg.Name != "" {
		return cfg.Name
	}
	return udid
}

func backupCheckJobOutcome(result backupCheckResult) (string, string) {
	if result.State == "passed" || result.State == "issues" {
		return backupStateSucceeded, ""
	}
	switch result.Reason {
	case "cancelled":
		return backupStateStopped, "check_cancelled"
	case "timeout":
		return backupStateInterrupted, "check_timeout"
	case "process_restarted":
		return backupStateInterrupted, "process_restarted"
	default:
		return backupStateFailed, "check_unavailable"
	}
}

// 返回值只供页面展示；最近尝试始终保留，旧完成结果另行显示时间与来源新鲜度。
func (app *application) attachCompletedCheckUnsafe(udid string, result *backupCheckResult) *backupCheckResult {
	result.LastCompleted = nil
	if result.State == "passed" || result.State == "issues" || app.backupJobRecords == nil {
		return result
	}
	jobs := app.backupJobRecords.list(udid)
	source := app.currentCheckSourceUnsafe(udid)
	source.bindLegacyEvidence(jobs)
	for _, job := range jobs {
		previous := job.Check
		if previous == nil || previous.ID == result.ID || previous.State != "passed" && previous.State != "issues" || previous.SourceKey != result.SourceKey {
			continue
		}
		// 历史点的独立检查不能冒充当前来源检查。
		if job.Operation == "check" && job.PointID != "" {
			continue
		}
		ref := source.checkRef(backupJobCheckSourceRef(job), previous)
		if ref != result.SourceRef {
			continue
		}
		copy := *previous
		copy.SourceRef = ref
		copy.LastCompleted = nil
		if result.Freshness == "unknown" && copy.Freshness == "current" {
			copy.Freshness = "unknown"
		}
		if result.SourceIdentity == "" || result.SourceFingerprint == "" {
			copy.Freshness = "unknown"
		} else if copy.SourceIdentity != result.SourceIdentity || copy.SourceFingerprint != result.SourceFingerprint {
			copy.Freshness = "stale"
		}
		app.markCheckSourceUnsafe(udid, &copy)
		result.LastCompleted = &copy
		break
	}
	if result.LastCompleted == nil {
		previous := app.currentCheckSourceUnsafe(udid).pointResult()
		if previous != nil && previous.SourceRef == result.SourceRef && previous.ID != result.ID {
			result.LastCompleted = previous
		}
	}
	return result
}

func (app *application) markCheckSourceUnsafe(udid string, result *backupCheckResult) {
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	absolute, err := filepath.Abs(root)
	if err != nil || result.SourceKey != "" && result.SourceKey != indexDigest(filepath.Clean(absolute)+"\x00"+udid) {
		result.Freshness = "stale"
	}
}
