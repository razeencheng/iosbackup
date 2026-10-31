package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type historyPreviewToken struct {
	UDID     string
	Previous backupstore.Policy
	Preview  historyPolicyPreview
	Expires  time.Time
}
type historyReviewRun struct {
	ID     string            `json:"id"`
	Result backupCheckResult `json:"result"`
	Saved  bool              `json:"saved"`
	cancel context.CancelFunc
	done   chan struct{}
}
type historyService struct {
	closed     bool
	mu         sync.Mutex
	previews   map[string]historyPreviewToken
	exports    map[string]*backupExport
	reviews    map[string]*historyReviewRun
	policies   map[string]*historyPolicyRun
	exportSlot chan struct{}
}

func (app *application) historyService() *historyService {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.historyHTTP == nil {
		app.historyHTTP = &historyService{previews: map[string]historyPreviewToken{}, exports: map[string]*backupExport{}, reviews: map[string]*historyReviewRun{}, exportSlot: make(chan struct{}, 1)}
		app.historyHTTP.policies = make(map[string]*historyPolicyRun)
	}
	return app.historyHTTP
}

type historyPointView struct {
	JobID             string            `json:"job_id"`
	ID                string            `json:"id"`
	StoreID           string            `json:"store_id"`
	UDID              string            `json:"udid"`
	Revision          uint64            `json:"revision"`
	CompletedAt       time.Time         `json:"completed_at"`
	CommittedAt       time.Time         `json:"committed_at"`
	CompletionUnknown bool              `json:"completion_unknown"`
	Day               string            `json:"day"`
	Timezone          string            `json:"timezone"`
	State             string            `json:"state"`
	Encryption        string            `json:"encryption"`
	GenerationID      string            `json:"generation_id"`
	Check             backupstore.Check `json:"check"`
	Review            backupstore.Check `json:"review"`
	Pinned            bool              `json:"pinned"`
	Manual            bool              `json:"manual"`
	Baseline          bool              `json:"baseline"`
	Reason            string            `json:"reason"`
	InUse             bool              `json:"in_use"`
	Bytes             int64             `json:"bytes"`
	BytesKnown        bool              `json:"bytes_known"`
	CanDelete         bool              `json:"can_delete"`
	CanPin            bool              `json:"can_pin"`
	CanCheck          bool              `json:"can_check"`
	CanExport         bool              `json:"can_export"`
}

func pointView(p backupstore.Point, c backupstore.Catalog, busy bool, reason string) historyPointView {
	return historyPointView{JobID: p.JobID, ID: p.ID, StoreID: p.StoreID, UDID: p.UDID, Revision: c.Revision, CompletedAt: p.CompletedAt, CommittedAt: p.CommittedAt, CompletionUnknown: p.CompletionUnknown, Day: p.Day, Timezone: p.Timezone, State: p.State, Encryption: p.Generation.Kind, GenerationID: p.Generation.ID, Check: p.Check, Review: p.Review, Pinned: p.Pinned, Manual: p.Manual, Baseline: c.Baselines[p.Generation.ID] == p.ID, Reason: reason, InUse: busy, Bytes: p.Bytes, BytesKnown: p.BytesKnown, CanDelete: !busy, CanPin: true, CanCheck: !busy, CanExport: true}
}
func (app *application) historyLocations(udid string) []historyLocation {
	if app.backupStorage == nil {
		return nil
	}
	m := app.backupStorage
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []historyLocation
	for _, loc := range m.locations.Stores {
		if udid == "" || loc.UDID == udid {
			out = append(out, loc)
		}
	}
	return out
}
func (app *application) findHistoryPoint(id, udid string) (*backupstore.Store, backupstore.Catalog, backupstore.Point, error) {
	if !backupstore.ValidID(id) {
		return nil, backupstore.Catalog{}, backupstore.Point{}, os.ErrNotExist
	}
	unavailable := false
	for _, loc := range app.historyLocations(udid) {
		s, e := app.backupStorage.store(loc.ID)
		if e != nil {
			unavailable = true
			continue
		}
		c, e := s.Snapshot()
		if e != nil {
			unavailable = true
			continue
		}
		if p, ok := c.Points[id]; ok {
			_, e = s.InUse(id)
			return s, c, p, e
		}
	}
	if unavailable {
		return nil, backupstore.Catalog{}, backupstore.Point{}, backupstore.ErrUncertain
	}
	return nil, backupstore.Catalog{}, backupstore.Point{}, os.ErrNotExist
}
func historyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, errDeviceNotFound):
		writeError(w, 404, i18n.NotFound)
	case errors.Is(err, backupstore.ErrInvalid):
		writeError(w, 400, i18n.InvalidJSON)
	case errors.Is(err, backupstore.ErrConflict):
		writeError(w, 409, i18n.HistoryConflict)
	case errors.Is(err, backupstore.ErrBusy), errors.Is(err, errDeviceBusy):
		writeError(w, 409, i18n.Busy)
	case errors.Is(err, backupstore.ErrCapacity):
		writeError(w, 507, i18n.HistoryCapacity)
	default:
		writeError(w, 503, i18n.HistoryUnavailable)
	}
}
func historyBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if !csrfOK(r) {
		writeError(w, 403, i18n.CSRFRequired)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decodeRequestJSON(r, target) != nil {
		writeError(w, 400, i18n.InvalidJSON)
		return false
	}
	return true
}
func (app *application) handleBackupHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/backup-history/"), "/")
	if len(parts) > 2 || !udidPattern.MatchString(parts[0]) {
		writeError(w, 404, i18n.NotFound)
		return
	}
	udid := parts[0]
	if len(parts) == 2 {
		app.handleHistoryPolicy(w, r, udid, parts[1])
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	app.mu.RLock()
	cfg := cloneBackupConfig(app.configs[udid])
	busy := app.backupInProgress[udid] || app.checkInProgress[udid]
	removed := app.isDeviceRemovedUnsafe(udid)
	app.mu.RUnlock()
	locations := app.historyLocations(udid)
	if cfg == nil && len(locations) == 0 {
		writeError(w, 404, i18n.NotFound)
		return
	}
	policy := backupstore.Policy{Days: 3}
	if cfg != nil {
		policy, _ = cfg.History.Normalize()
	}
	st := historyDeviceState{}
	if app.backupStorage != nil {
		var e error
		st, e = app.backupStorage.deviceState(udid)
		if e != nil {
			historyError(w, e)
			return
		}
	}
	generation := ""
	if reg, e := app.generationRegistry().load(udid); e == nil {
		generation = reg.Current
	}
	points := []historyPointView{}
	unavailable := []string{}
	var bytes int64
	allKnown := true
	for _, loc := range locations {
		s, e := app.backupStorage.store(loc.ID)
		if e != nil {
			unavailable = append(unavailable, loc.ID)
			allKnown = false
			continue
		}
		plan, e := s.PlanGC(backupstore.RetentionRequest{Policy: backupstore.Policy{Enabled: policy.Enabled && st.Phase == "active", Days: policy.Days}, EpochID: st.EpochID, GenerationID: generation, Now: app.nowTime()})
		if e != nil {
			unavailable = append(unavailable, loc.ID)
			allKnown = false
			continue
		}
		c, e := s.Snapshot()
		if e != nil {
			unavailable = append(unavailable, loc.ID)
			continue
		}
		for _, p := range c.Points {
			used, e := s.InUse(p.ID)
			if e != nil {
				continue
			}
			v := pointView(p, c, used, plan.Retain[p.ID])
			v.CanDelete = v.CanDelete && !busy
			v.CanPin = !busy
			v.CanCheck = v.CanCheck && !busy
			points = append(points, v)
			bytes += p.Bytes
			allKnown = allKnown && p.BytesKnown
		}
	}
	sort.Slice(points, func(i, j int) bool {
		left, right := points[i].CompletedAt, points[j].CompletedAt
		if points[i].CompletionUnknown {
			left = points[i].CommittedAt
		}
		if points[j].CompletionUnknown {
			right = points[j].CommittedAt
		}
		if left.Equal(right) {
			return points[i].ID > points[j].ID
		}
		return left.After(right)
	})
	offset := 0
	if value := r.URL.Query().Get("offset"); value != "" {
		var e error
		offset, e = strconv.Atoi(value)
		if e != nil || offset < 0 || offset > len(points) {
			writeError(w, 400, i18n.InvalidCursor)
			return
		}
	}
	total := len(points)
	end := min(offset+100, total)
	next := 0
	if end < total {
		next = end
	}
	pending := cfg != nil && cfg.HistoryPolicyPending
	writeJSON(w, 200, map[string]any{"udid": udid, "policy": policy, "policy_operation": app.historyPolicyStatus(udid, cfg), "phase": st.Phase, "points": points[offset:end], "total": total, "next_offset": next, "unavailable_stores": unavailable, "busy": busy, "can_retry_policy": pending && !busy && !removed, "can_edit_policy": !pending && !busy && !removed && len(unavailable) == 0, "logical_bytes": bytes, "bytes_known": allKnown, "legacy_bytes": st.LegacyBytes, "current_source": app.historyCurrentSource(r.Context(), udid, cfg), "storage": app.historyStorageStatus(cfg)})
}
func (app *application) handleHistoryPolicy(w http.ResponseWriter, r *http.Request, udid, action string) {
	if action != "policy-preview" && action != "policy" && action != "policy-retry" {
		writeError(w, 404, i18n.NotFound)
		return
	}
	expected := http.MethodPost
	if action == "policy" {
		expected = http.MethodPut
	}
	if r.Method != expected {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	if action == "policy-retry" {
		var request struct{}
		if !historyBody(w, r, &request) {
			return
		}
		app.mu.RLock()
		cfg := cloneBackupConfig(app.configs[udid])
		app.mu.RUnlock()
		if cfg == nil {
			historyError(w, errDeviceNotFound)
			return
		}
		if !cfg.HistoryPolicyPending {
			historyError(w, backupstore.ErrConflict)
			return
		}
		operation, err := app.startHistoryPolicy(r.Context(), udid, cfg.History, nil)
		if err != nil {
			historyError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"policy": cfg.History, "operation": operation})
		return
	}
	var request struct {
		Policy backupstore.Policy `json:"policy"`
		Token  string             `json:"token"`
	}
	if !historyBody(w, r, &request) {
		return
	}
	policy, e := request.Policy.Normalize()
	if e != nil {
		historyError(w, e)
		return
	}
	service := app.historyService()
	app.mu.RLock()
	cfg := cloneBackupConfig(app.configs[udid])
	app.mu.RUnlock()
	if cfg == nil {
		historyError(w, errDeviceNotFound)
		return
	}
	previous, _ := cfg.History.Normalize()
	if action == "policy-preview" {
		preview, e := app.previewHistoryPolicy(r.Context(), udid, policy)
		if e != nil {
			historyError(w, e)
			return
		}
		token, e := backupstore.NewID()
		if e != nil {
			historyError(w, e)
			return
		}
		now := time.Now()
		service.mu.Lock()
		for k, p := range service.previews {
			if !p.Expires.After(now) {
				delete(service.previews, k)
			}
		}
		if len(service.previews) >= 64 {
			service.mu.Unlock()
			historyError(w, backupstore.ErrBusy)
			return
		}
		service.previews[token] = historyPreviewToken{udid, previous, preview, now.Add(10 * time.Minute)}
		service.mu.Unlock()
		count := 0
		for _, p := range preview.Stores {
			count += len(p.Delete)
		}
		writeJSON(w, 200, map[string]any{"token": token, "preview": preview, "affected_count": count})
		return
	}
	service.mu.Lock()
	token, ok := service.previews[request.Token]
	delete(service.previews, request.Token)
	service.mu.Unlock()
	if !ok || token.UDID != udid || !token.Expires.After(time.Now()) || token.Previous != previous || token.Preview.Policy != policy || token.Preview.Root != cfg.BackupDirectory {
		historyError(w, backupstore.ErrConflict)
		return
	}
	operation, e := app.startHistoryPolicy(r.Context(), udid, policy, &token.Preview)
	if e != nil {
		historyError(w, e)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"policy": policy, "operation": operation})
}
func (app *application) acquireHistoryMutation(udid string) (func(), error) {
	app.mu.Lock()
	if app.backupInProgress[udid] || app.checkInProgress[udid] || app.deviceRemovalPending[udid] {
		app.mu.Unlock()
		return nil, errDeviceBusy
	}
	app.backupInProgress[udid] = true
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	app.mu.Unlock()
	app.beginBackupIndexWrite(udid, root, true)
	return func() {
		manager := app.lockBackupIndexFinish()
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		delete(app.historyReviewInProgress, udid)
		app.mu.Unlock()
		var lease *backupIndexWrite
		if manager != nil {
			lease = manager.releaseWriteUnsafe(udid)
			manager.mu.Unlock()
		}
		app.afterBackupIndexWrite(manager, lease, false)
		app.broadcastStatus()
	}, nil
}
func (app *application) handleBackupPoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/backup-point/"), "/")
	if len(parts) > 2 || !backupstore.ValidID(parts[0]) {
		writeError(w, 404, i18n.NotFound)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if action != "" && action != "pin" && action != "check" && action != "exports" {
		writeError(w, 404, i18n.NotFound)
		return
	}
	if (action == "" && r.Method != http.MethodGet && r.Method != http.MethodDelete) || (action != "" && r.Method != http.MethodPost && !(action == "check" && r.Method == http.MethodGet)) {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	s, c, p, e := app.findHistoryPoint(parts[0], r.URL.Query().Get("udid"))
	if e != nil {
		historyError(w, e)
		return
	}
	if r.Method == http.MethodGet {
		if action == "check" {
			app.historyReviewStatus(w, p.ID)
			return
		}
		used, e := s.InUse(p.ID)
		if e != nil {
			historyError(w, e)
			return
		}
		v := pointView(p, c, used, "")
		app.mu.RLock()
		busy := app.backupInProgress[p.UDID] || app.checkInProgress[p.UDID] || app.deviceRemovalPending[p.UDID]
		app.mu.RUnlock()
		v.CanPin = !busy
		v.CanDelete = v.CanDelete && !busy
		v.CanCheck = v.CanCheck && !busy
		writeJSON(w, 200, v)
		return
	}
	if action == "exports" {
		var request struct{}
		if !historyBody(w, r, &request) {
			return
		}
		app.createBackupExport(w, r, s, p)
		return
	}
	var request struct {
		Revision uint64 `json:"revision"`
		Pinned   *bool  `json:"pinned"`
		Confirm  string `json:"confirm"`
		Action   string `json:"action"`
		ID       string `json:"id"`
	}
	if !historyBody(w, r, &request) {
		return
	}
	if action == "check" && request.Action == "cancel" {
		app.cancelHistoryReview(w, p.ID, request.ID)
		return
	}
	if action == "pin" && request.Pinned == nil || action == "" && request.Confirm != p.ID {
		writeError(w, 400, i18n.InvalidJSON)
		return
	}
	release, e := app.acquireHistoryMutation(p.UDID)
	if e != nil {
		historyError(w, e)
		return
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	c, e = s.Snapshot()
	if e != nil {
		historyError(w, e)
		return
	}
	if c.Revision != request.Revision {
		historyError(w, backupstore.ErrConflict)
		return
	}
	switch action {
	case "pin":
		e = s.SetPinned(r.Context(), p.ID, *request.Pinned, c.Revision)
	case "check":
		if request.Action != "" && request.Action != "start" {
			writeError(w, 400, i18n.InvalidJSON)
			return
		}
		var id string
		id, e = app.startHistoryReview(s, p, release)
		if e == nil {
			release = nil
			writeJSON(w, 202, map[string]string{"id": id})
			return
		}
	case "":
		e = s.DeleteWithIntent(r.Context(), p.ID, c.Revision, func() error {
			st, err := app.backupStorage.deviceState(p.UDID)
			if err != nil {
				return err
			}
			if st.LatestPoint == p.ID && st.Phase == "active" {
				st.CurrentDeleted, st.CurrentReset, st.DeletedAt = true, false, app.nowTime()
				return app.backupStorage.saveDevice(p.UDID, st)
			}
			return nil
		})
	}
	if e != nil {
		historyError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (app *application) startHistoryReview(s *backupstore.Store, p backupstore.Point, release func()) (string, error) {
	lease, e := s.Acquire(p.ID)
	if e != nil {
		return "", e
	}
	manager := app.backupIndexManager()
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		lease.Close()
		return "", context.Canceled
	}
	manager.workers.Add(1)
	manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(app.rootCtx, backupManifestCheckTimeout)
	run := &historyReviewRun{ID: newBackupSessionID(), Result: backupCheckResult{Kind: "manifest", State: "running", StartedAt: app.nowTime(), Freshness: "current"}, cancel: cancel, done: make(chan struct{})}
	service := app.historyService()
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		manager.workers.Done()
		cancel()
		lease.Close()
		return "", context.Canceled
	}
	if len(service.reviews) >= 256 {
		for id, old := range service.reviews {
			if old.Result.State != "running" {
				delete(service.reviews, id)
				break
			}
		}
	}
	if len(service.reviews) >= 256 {
		service.mu.Unlock()
		manager.workers.Done()
		cancel()
		lease.Close()
		return "", backupstore.ErrBusy
	}
	service.reviews[p.ID] = run
	service.mu.Unlock()
	app.mu.Lock()
	if app.historyReviewInProgress == nil {
		app.historyReviewInProgress = make(map[string]bool)
	}
	app.historyReviewInProgress[p.UDID] = true
	app.mu.Unlock()
	if app.backupJobRecords != nil {
		app.backupJobRecords.start(&backupJobSeed{ID: run.ID, UDID: p.UDID, DeviceName: p.UDID, PointID: p.ID, Source: "manual", RequestedMode: "auto", Operation: "check", StartedAt: run.Result.StartedAt})
		app.backupJobRecords.setCheck(run.ID, run.Result)
		app.backupJobRecords.transition(run.ID, backupStateRunning)
	}
	app.broadcastStatus()
	go func() {
		defer manager.workers.Done()
		defer cancel()
		defer release()
		defer lease.Close()
		defer close(run.done)
		var result backupCheckResult
		select {
		case manager.slot <- struct{}{}:
			result = app.checkBackupSourceAt(ctx, p.UDID, filepath.Dir(lease.Directory), "manifest")
			<-manager.slot
		case <-ctx.Done():
			result = backupCheckResult{Kind: "manifest", State: "unavailable", Reason: "cancelled", StartedAt: run.Result.StartedAt, CompletedAt: app.nowTime()}
		}
		result.ID = run.ID
		result.Freshness = "current"
		c, e := s.Snapshot()
		if e == nil {
			e = s.RecordReview(context.Background(), p.ID, historyCheckEvidence(result), c.Revision)
		}
		if app.backupJobRecords != nil {
			app.backupJobRecords.setCheck(run.ID, result)
			app.backupJobRecords.finish(run.ID, backupStateSucceeded, "", app.nowTime())
		}
		service.mu.Lock()
		run.Result = result
		run.Saved = e == nil
		service.mu.Unlock()
	}()
	return run.ID, nil
}
func (app *application) historyReviewStatus(w http.ResponseWriter, id string) {
	s := app.historyService()
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.reviews[id]
	if run == nil {
		writeError(w, 410, i18n.HistorySessionExpired)
		return
	}
	writeJSON(w, 200, run)
}
func (app *application) cancelHistoryReview(w http.ResponseWriter, id, runID string) {
	s := app.historyService()
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.reviews[id]
	if run == nil || run.ID != runID || run.Result.State != "running" {
		writeError(w, 409, i18n.HistoryConflict)
		return
	}
	run.cancel()
	writeJSON(w, 202, map[string]string{"id": run.ID})
}
