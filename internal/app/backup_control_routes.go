package app

import (
	"errors"
	"iosbackup/internal/i18n"
	"net/http"
)

func (app *application) handleBackupStop(w http.ResponseWriter, r *http.Request) {
	app.handleBackupControl(w, r, "stop")
}
func (app *application) handleBackupResume(w http.ResponseWriter, r *http.Request) {
	app.handleBackupControl(w, r, "resume")
}
func (app *application) handleBackupHoldRetry(w http.ResponseWriter, r *http.Request) {
	app.handleBackupControl(w, r, "hold-retry")
}

func (app *application) handleBackupControl(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-"+action+"/")
	if !udidPattern.MatchString(udid) {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	var request struct {
		JobID       string `json:"job_id"`
		HoldVersion string `json:"hold_version"`
	}
	if err := decodeRequestJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	if (action == "stop" && request.JobID == "") || (action != "stop" && request.HoldVersion == "") {
		writeError(w, http.StatusBadRequest, i18n.BackupControlIdentityRequired)
		return
	}
	var target *backupJob
	if action == "stop" {
		app.mu.RLock()
		if job := app.backupJobs[udid]; job != nil && job.id == request.JobID {
			target = job
		}
		app.mu.RUnlock()
	}
	var err error
	if action == "stop" {
		err = app.stopBackup(udid, request.JobID)
	} else {
		err = app.persistBackupHold(udid, request.HoldVersion, action == "resume")
	}
	code, status := i18n.BackupControlUpdated, http.StatusOK
	if err != nil {
		status = http.StatusConflict
		code = i18n.OperationFailed
		var localized *i18n.Error
		if errors.As(err, &localized) {
			code = localized.Code
		}
		if errors.Is(err, errDeviceNotFound) {
			code, status = i18n.DeviceNotFound, http.StatusNotFound
		}
		if code == i18n.BackupHoldSaveFailed {
			status = http.StatusInternalServerError
		}
	}
	payload := apiMessage(w, code, nil, err != nil)
	app.mu.RLock()
	h := app.backupHoldUnsafe(udid)
	payload["auto_backup_hold"], payload["auto_backup_hold_version"], payload["auto_backup_hold_persistence"] = h.Held, h.Version, h.Persistence
	payload["backup_state"] = app.deviceOperationStates[udid].BackupState
	payload["backing_up"] = app.backupInProgress[udid]
	if c := app.configs[udid]; c != nil {
		payload["auto_backup_enabled"] = c.AutoBackupEnabled
	}
	if action == "stop" {
		// 停止响应始终绑定请求的任务，持久化等待期间下一任务可能已入场。
		payload["backup_job_id"] = request.JobID
		payload["backing_up"] = target != nil && !target.finished
		payload["backup_state"] = ""
		if target != nil {
			state := backupStateStopping
			if target.resultSet {
				state = backupStateSucceeded
				if target.result != nil {
					state, _ = backupFailureState(target.result)
				}
			}
			payload["backup_state"] = state
			if err == nil && !target.finished {
				status = http.StatusAccepted
			}
		}
	} else if job := app.backupJobs[udid]; job != nil {
		payload["backup_job_id"] = job.id
	}

	app.mu.RUnlock()
	if err != nil {
		payload["error_code"] = code
	}
	writeJSON(w, status, payload)
}
