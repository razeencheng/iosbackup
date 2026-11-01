package app

import "strings"

// 只接受工具的独立规范输出，命令参数和日志中包含的文件名不构成实际模式证据。
func (app *application) observeBackupMode(udid, id, line, stream string) {
	if stream != "STDOUT" {
		return
	}
	mode := ""
	switch strings.TrimSpace(ansiRE.ReplaceAllString(line, "")) {
	case "Full backup mode.":
		mode = "full"
	case "Incremental backup mode.":
		mode = "incremental"
	default:
		return
	}
	app.mu.Lock()
	job := app.backupJobs[udid]
	if job == nil || job.id != id || job.finished || job.observedMode == mode {
		app.mu.Unlock()
		return
	}
	job.observedMode = mode
	if s := app.backupJobRecords; s != nil {
		s.mu.Lock()
		if r := s.records[id]; r != nil && !s.closed && r.Summary.CompletedAt.IsZero() {
			r.Summary.ObservedMode = mode
			s.enqueueUnsafe(r, backupJobEvent{At: app.nowTime(), Code: "mode_observed", Mode: mode})
		}
		s.mu.Unlock()
		s.signal()
	}
	app.mu.Unlock()
	app.broadcastStatus()
}
