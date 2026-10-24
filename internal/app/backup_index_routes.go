package app

import (
	"errors"
	"fmt"
	"net/http"

	"iosbackup/internal/i18n"
)

// handleBackupIndex 本地索引读取不使用连接任务；下载仅交付已完整发布的固定版本。
func (app *application) handleBackupIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost && !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-index/")
	if !udidPattern.MatchString(udid) {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	src, sourceErr := app.resolveBackupIndexSource(r.Context(), udid)
	if errors.Is(sourceErr, errDeviceNotFound) {
		writeError(w, http.StatusNotFound, i18n.DeviceNotFound)
		return
	}
	if errors.Is(sourceErr, errDeviceRemoved) || errors.Is(sourceErr, errDeviceRemovalPending) {
		writeError(w, http.StatusConflict, i18n.DeviceRemovedOperation)
		return
	}
	if src.Key == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	manager := app.backupIndexManager()
	if r.URL.Query().Get("format") == "csv" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
			return
		}
		if errors.Is(sourceErr, errBackupIndexMissing) {
			writeError(w, http.StatusConflict, i18n.BackupIndexCsvUnavailable)
			return
		}
		file, err := manager.store.openCSV(src, r.URL.Query().Get("revision"))
		if err != nil {
			writeError(w, http.StatusConflict, i18n.BackupIndexCsvUnavailable)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			writeError(w, http.StatusConflict, i18n.BackupIndexCsvUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"backup-list-%s.csv\"", udid))
		// ServeContent 先计算 Content-Length，并支持客户端断开与范围请求。
		http.ServeContent(w, r, "backup-list.csv", info.ModTime(), file)
		return
	}
	manager.request(src, r.Method == http.MethodPost)
	snapshot, _ := manager.snapshot(src, sourceErr)
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusAccepted
	}
	writeJSON(w, status, snapshot)
}
