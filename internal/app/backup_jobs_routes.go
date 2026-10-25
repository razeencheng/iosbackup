package app

import (
	"encoding/json"
	"iosbackup/internal/i18n"
	"net/http"
	"strconv"
	"strings"
)

func (app *application) handleBackupJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-jobs/")
	if !udidPattern.MatchString(udid) {
		writeError(w, 400, i18n.InvalidUDID)
		return
	}
	summaries := make([]backupJobSummary, 0)
	status := "unavailable"
	if app.backupJobRecords != nil {
		summaries = app.backupJobRecords.list(udid)
		status = app.backupJobRecords.recordingStatus()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"jobs": summaries, "recording_status": status, "retention_days": 30})
}

func (app *application) handleBackupJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, i18n.MethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/backup-job/"), "/")
	if len(parts) != 2 || !backupJobIDPattern.MatchString(parts[0]) || (parts[1] != "events" && parts[1] != "export") {
		writeError(w, 404, i18n.NotFound)
		return
	}
	if app.backupJobRecords == nil {
		writeError(w, 503, i18n.BackupRecordsUnavailable)
		return
	}
	var after uint64
	var err error
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeError(w, 400, i18n.InvalidCursor)
			return
		}
	}
	limit := 200
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, 400, i18n.InvalidCursor)
			return
		}
	}
	if parts[1] == "export" {
		after = 0
		limit = app.backupJobRecords.limits.Events
	}
	summary, events, ok := app.backupJobRecords.events(parts[0], after, limit)
	if !ok {
		writeError(w, 410, i18n.BackupRecordsExpired)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if parts[1] == "export" {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+parts[0]+`.jsonl"`)
		encoder := json.NewEncoder(w)
		if encoder.Encode(map[string]any{"summary": summary}) != nil {
			return
		}
		for _, event := range events {
			if encoder.Encode(event) != nil {
				return
			}
		}
		return
	}
	next := after
	if len(events) > 0 {
		next = events[len(events)-1].Cursor
	}
	writeJSON(w, 200, map[string]any{"job": summary, "events": events, "next_cursor": next, "has_more": next < summary.LastCursor, "truncated": summary.Truncated || (summary.FirstCursor > 0 && after < summary.FirstCursor-1)})
}
