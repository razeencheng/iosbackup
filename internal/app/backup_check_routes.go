package app

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

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
	manager := app.backupIndexManager()
	ctx, cancel := context.WithTimeout(app.rootCtx, backupManifestCheckTimeout)
	run := &backupCheckRun{id: newBackupSessionID(), cancel: cancel, done: make(chan struct{})}
	result := backupCheckResult{ID: run.id, Kind: "manifest", State: "running", Freshness: "unknown", StartedAt: app.nowTime()}
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
	name := udid
	if d := app.devices[udid]; d != nil && d.Name != "" {
		name = d.Name
	}
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
			app.backupJobRecords.finish(run.id, backupStateSucceeded, "", app.nowTime())
		}
		app.backupCheckResults[udid] = result
		delete(app.backupChecks, udid)
		app.mu.Unlock()
		release()
	}()
	return run.id, nil
}

func (app *application) checkBackupSourceAt(ctx context.Context, udid, root, kind string) backupCheckResult {
	src, err := resolveBackupIndexSourceAt(ctx, udid, root)
	var result backupCheckResult
	if err != nil {
		state, reason := backupCheckError(err)
		result = backupCheckResult{Kind: kind, State: state, Reason: reason, StartedAt: app.nowTime(), CompletedAt: app.nowTime(), Freshness: "unknown", SourceKey: src.Key}
	} else {
		result = runBackupCheck(ctx, src, kind, nil, app.manifestRows)
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
	if value, ok := app.backupCheckResults[udid]; ok {
		copy := value
		app.markCheckSourceUnsafe(udid, &copy)
		return &copy
	}
	if app.backupJobRecords != nil {
		for _, job := range app.backupJobRecords.list(udid) {
			if job.Check != nil && job.PointID == "" {
				copy := *job.Check
				copy.Freshness = "unknown"
				return &copy
			}
		}
	}
	return nil
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
