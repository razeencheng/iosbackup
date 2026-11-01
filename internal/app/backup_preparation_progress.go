package app

import (
	"iosbackup/internal/backupstore"
	"time"
)

// 本地阶段独立计量，不写手机工具的百分比或 watchdog 活动时钟。
type backupLocalProgress struct {
	Phase        string    `json:"phase"`
	Mode         string    `json:"mode,omitempty"`
	Bytes        int64     `json:"bytes"`
	Files        int64     `json:"files"`
	ScannedFiles int64     `json:"scanned_files"`
	TotalBytes   int64     `json:"total_bytes"`
	TotalFiles   int64     `json:"total_files"`
	TotalsKnown  bool      `json:"totals_known"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (app *application) historyCopyOptions(job *backupJob) backupstore.CopyOptions {
	app.reportHistoryPreparation(job, backupstore.CopyReport{Phase: "waiting"})
	return backupstore.CopyOptions{PreferClone: true, Progress: func(report backupstore.CopyReport) { app.reportHistoryPreparation(job, report) }}
}

func (app *application) reportHistoryPreparation(job *backupJob, r backupstore.CopyReport) {
	at := app.nowTime()
	local := &backupLocalProgress{Phase: r.Phase, Mode: r.Mode, Bytes: r.Bytes, Files: r.Files, ScannedFiles: r.ScannedFiles, TotalBytes: r.TotalBytes, TotalFiles: r.TotalFiles, TotalsKnown: r.TotalsKnown, UpdatedAt: at}
	if job.historyPolicyOnly {
		service := app.historyService()
		service.mu.Lock()
		if run := service.policies[job.udid]; run != nil && run.ID == job.id && run.State == "running" {
			run.Progress = local
		}
		service.mu.Unlock()
	} else {
		app.mu.Lock()
		if app.backupJobs[job.udid] != job || job.finished {
			app.mu.Unlock()
			return
		}
		p := app.backupProgress[job.udid]
		p.State, p.Phase, p.Local, p.UpdatedAt = backupProgressPreparing, "local_"+r.Phase, local, at
		app.backupProgress[job.udid] = p
		app.mu.Unlock()
		update := backupProgressUpdate{Phase: "local_" + r.Phase}
		if r.Phase == "copying" {
			bytes := r.Bytes
			update.CurrentBytes = &bytes
		}
		if r.Bytes > 0 || r.ScannedFiles > 0 {
			update.LastActivityAt = at
		}
		app.recordBackupActivity(job.id, update)
	}
	app.broadcastStatus()
}

func (app *application) connectBackupProgress(job *backupJob) {
	app.mu.Lock()
	if app.backupJobs[job.udid] == job && !job.finished {
		app.backupProgress[job.udid] = backupProgress{State: backupProgressPreparing, Phase: "preparing", UpdatedAt: app.nowTime()}
	}
	app.mu.Unlock()
	app.recordBackupActivity(job.id, backupProgressUpdate{Phase: "preparing"})
	app.broadcastStatus()
}
