package app

import (
	"errors"
	notificationpkg "iosbackup/internal/notification"
)

func (app *application) notifyBackupOutcome(job *backupJob, err error, check *backupCheckResult) {
	if errors.Is(err, errBackupStoppedByUser) {
		return
	}
	outcome := notificationpkg.BackupOutcome{Execution: "succeeded", History: "not_enabled"}
	if job.historyState != "" {
		outcome.History = job.historyState
	}
	if err != nil {
		outcome.Execution = "failed"
		_, outcome.FailureCode = backupFailureState(err)
		outcome.Diagnostics = err.Error()
	}
	if check != nil {
		outcome.Check = check.State
	}
	if manager := app.notificationManagerSnapshot(); manager != nil {
		manager.SendBackupOutcome(outcome, job.device.Name, job.udid)
	}
}
