package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const backupExportReadyTTL = 5 * time.Minute
const backupExportIdleTimeout = 60 * time.Second

type backupExport struct {
	done          chan struct{}
	ID            string    `json:"id"`
	PointID       string    `json:"point_id"`
	UDID          string    `json:"udid"`
	State         string    `json:"state"`
	Expires       time.Time `json:"expires_at"`
	Finished      time.Time `json:"finished_at,omitempty"`
	DownloadError string    `json:"download_error,omitempty"`
	Filename      string    `json:"filename"`
	LogicalBytes  int64     `json:"logical_bytes"`
	BytesKnown    bool      `json:"bytes_known"`
	bytes         atomic.Int64
	lease         *backupstore.Lease
	ctx           context.Context
	cancel        context.CancelFunc
	timer         *time.Timer
	stopRoot      func() bool
	once          sync.Once
}

func (e *backupExport) release() {
	e.once.Do(func() {
		defer close(e.done)
		e.lease.Close()
		if e.stopRoot != nil {
			e.stopRoot()
		}
		e.cancel()
	})
}
func (app *application) createBackupExport(w http.ResponseWriter, r *http.Request, store *backupstore.Store, p backupstore.Point) {
	app.mu.RLock()
	filename := backupExportFilename(app.backupTaskDeviceNameUnsafe(p.UDID), p)
	app.mu.RUnlock()
	service := app.historyService()
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		historyError(w, context.Canceled)
		return
	}
	now := time.Now()
	live := 0
	for id, e := range service.exports {
		if e.State == "ready" || e.State == "streaming" {
			live++
			if e.PointID == p.ID {
				historyError(w, backupstore.ErrBusy)
				return
			}
		} else if exportReleased(e) && now.Sub(e.Finished) > 30*time.Minute {
			delete(service.exports, id)
		}
	}
	// 达到记录上限时只淘汰已释放来源的最旧终态，不阻塞新导出或动仍在传输的会话。
	for len(service.exports) >= 64 {
		var oldest *backupExport
		for _, e := range service.exports {
			if e.State != "ready" && e.State != "streaming" && exportReleased(e) && (oldest == nil || e.Finished.Before(oldest.Finished) || e.Finished.Equal(oldest.Finished) && e.ID < oldest.ID) {
				oldest = e
			}
		}
		if oldest == nil {
			break
		}
		delete(service.exports, oldest.ID)
	}
	if live >= 4 || len(service.exports) >= 64 {
		historyError(w, backupstore.ErrBusy)
		return
	}
	lease, err := store.Acquire(p.ID)
	if err != nil {
		historyError(w, err)
		return
	}
	id, err := backupstore.NewID()
	if err != nil {
		lease.Close()
		historyError(w, err)
		return
	}
	ctx, cancel := context.WithCancel(app.rootCtx)
	e := &backupExport{done: make(chan struct{}), ID: id, PointID: p.ID, UDID: p.UDID, State: "ready", Expires: now.Add(backupExportReadyTTL), lease: lease, ctx: ctx, cancel: cancel, Filename: filename, LogicalBytes: p.Bytes, BytesKnown: p.BytesKnown}
	service.exports[id] = e
	expire := func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		if e.State == "ready" {
			e.State = "expired"
			e.Finished = time.Now()
			e.release()
		}
	}
	e.timer = time.AfterFunc(backupExportReadyTTL, expire)
	e.stopRoot = context.AfterFunc(app.rootCtx, expire)
	writeJSON(w, 201, exportView(e, len(service.exportSlot) > 0))
}
func exportReleased(e *backupExport) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
func exportView(e *backupExport, transferBusy bool) map[string]any {
	return map[string]any{"id": e.ID, "point_id": e.PointID, "udid": e.UDID, "state": e.State, "expires_at": e.Expires, "finished_at": e.Finished, "bytes_sent": e.bytes.Load(), "download_error": e.DownloadError, "can_download": e.State == "ready" && e.Expires.After(time.Now()) && !transferBusy, "filename": e.Filename, "logical_bytes": e.LogicalBytes, "bytes_known": e.BytesKnown, "supports_resume": false}
}

func backupExportFilename(name string, p backupstore.Point) string {
	runes := []rune(name)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, string(runes))
	// 按 UTF-8 字节限制名称，为日期和后缀预留空间。
	for len(name) > 160 {
		runes = []rune(name)
		name = string(runes[:len(runes)-1])
	}
	name = strings.Trim(name, " .-_")
	if name == "" {
		name = "device"
	}
	at := p.CompletedAt
	prefix := ""
	if p.CompletionUnknown {
		at = p.CommittedAt
		prefix = "saved-"
	}
	if loc, err := time.LoadLocation(p.Timezone); err == nil {
		at = at.In(loc)
	}
	id := p.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return "iosbackup-" + name + "-" + prefix + at.Format("20060102-150405-0700") + "-" + id + ".tar"
}
func (app *application) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/backup-export/"), "/")
	if len(parts) > 2 || !backupstore.ValidID(parts[0]) || (len(parts) == 2 && parts[1] != "download") {
		writeError(w, 404, i18n.NotFound)
		return
	}
	if r.Method != http.MethodGet && !(len(parts) == 1 && r.Method == http.MethodDelete) {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	if r.Method == http.MethodDelete {
		var body struct{}
		if !historyBody(w, r, &body) {
			return
		}
	}
	service := app.historyService()
	service.mu.Lock()
	e := service.exports[parts[0]]
	if e == nil {
		service.mu.Unlock()
		writeError(w, 410, i18n.HistorySessionExpired)
		return
	}
	if r.Method == http.MethodDelete {
		if e.State == "ready" {
			e.State = "cancelled"
			e.Finished = time.Now()
			e.timer.Stop()
			e.release()
		} else if e.State == "streaming" {
			e.cancel()
		}
		view := exportView(e, len(service.exportSlot) > 0)
		service.mu.Unlock()
		writeJSON(w, 202, view)
		return
	}
	if len(parts) == 1 {
		view := exportView(e, len(service.exportSlot) > 0)
		service.mu.Unlock()
		writeJSON(w, 200, view)
		return
	}
	if e.State != "ready" || !e.Expires.After(time.Now()) {
		service.mu.Unlock()
		writeError(w, 410, i18n.HistorySessionExpired)
		return
	}
	w.Header().Set("Accept-Ranges", "none")
	if r.Header.Get("Range") != "" {
		service.mu.Unlock()
		writeError(w, http.StatusRequestedRangeNotSatisfiable, i18n.HistoryExportNoResume)
		return
	}
	select {
	case service.exportSlot <- struct{}{}:
	default:
		e.DownloadError = "busy"
		service.mu.Unlock()
		historyError(w, backupstore.ErrBusy)
		return
	}
	e.State = "streaming"
	e.DownloadError = ""
	e.timer.Stop()
	service.mu.Unlock()
	ctx, cancel := context.WithCancel(e.ctx)
	stopRequest := context.AfterFunc(r.Context(), cancel)
	controller := http.NewResponseController(w)
	var deadlineMu sync.Mutex
	deadlineDone := make(chan struct{})
	stopDeadline := context.AfterFunc(ctx, func() {
		defer close(deadlineDone)
		deadlineMu.Lock()
		defer deadlineMu.Unlock()
		_ = controller.SetWriteDeadline(time.Now())
	})
	failed := true
	defer func() {
		stopRequest()
		if !stopDeadline() {
			<-deadlineDone
		}
		cancel()
		_ = controller.SetWriteDeadline(time.Time{})
		service.mu.Lock()
		if failed {
			if e.ctx.Err() != nil || r.Context().Err() != nil {
				e.State = "cancelled"
			} else {
				e.State = "failed"
			}
		} else {
			e.State = "transferred"
		}
		e.Finished = time.Now()
		e.release()
		service.mu.Unlock()
		<-service.exportSlot
	}()
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": e.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writer := &backupExportWriter{ctx: ctx, w: w, controller: controller, bytes: &e.bytes, deadlineMu: &deadlineMu}
	if err := e.lease.Export(ctx, writer, e.UDID); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		panic(http.ErrAbortHandler)
	}
	if ctx.Err() != nil {
		panic(http.ErrAbortHandler)
	}
	failed = false
}

type backupExportWriter struct {
	deadlineMu *sync.Mutex
	ctx        context.Context
	w          http.ResponseWriter
	controller *http.ResponseController
	bytes      *atomic.Int64
}

func (w *backupExportWriter) Write(data []byte) (int, error) {
	w.deadlineMu.Lock()
	if err := w.ctx.Err(); err != nil {
		w.deadlineMu.Unlock()
		return 0, err
	}
	if err := w.controller.SetWriteDeadline(time.Now().Add(backupExportIdleTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.deadlineMu.Unlock()
		return 0, err
	}
	w.deadlineMu.Unlock()
	n, err := w.w.Write(data)
	w.bytes.Add(int64(n))
	return n, err
}

func (app *application) closeHistoryHTTP() {
	app.mu.RLock()
	service := app.historyHTTP
	app.mu.RUnlock()
	if service == nil {
		return
	}
	service.mu.Lock()
	service.closed = true
	var waiting []<-chan struct{}
	for _, e := range service.exports {
		if e.State == "ready" {
			e.State = "cancelled"
			e.Finished = time.Now()
			e.timer.Stop()
			e.release()
		} else if e.State == "streaming" {
			e.cancel()
			waiting = append(waiting, e.done)
		}
	}
	for _, r := range service.reviews {
		r.cancel()
		waiting = append(waiting, r.done)
	}
	for _, run := range service.policies {
		run.cancel()
		waiting = append(waiting, run.done)
	}
	service.mu.Unlock()
	for _, done := range waiting {
		<-done
	}
}
