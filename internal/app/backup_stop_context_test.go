package app

import (
	"context"
	"testing"
)

func TestBackupStopStorageModeUsesAcceptedTaskConfig(t *testing.T) {
	for _, history := range []bool{false, true} {
		mode := "in_place"
		if history {
			mode = "history"
		}
		t.Run(mode, func(t *testing.T) {
			a := newBackupControlTestApp(t)
			a.configs["CONTROL"].History.Enabled = history
			job, err := a.beginBackupJob("CONTROL", "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer a.finishBackupJob(job, context.Canceled)
			// 即使当前配置变化，停止提示仍描述任务实际使用的配置快照。
			a.mu.Lock()
			a.configs["CONTROL"].History.Enabled = !history
			a.mu.Unlock()
			status := activeOperationJSON(t, a, "CONTROL")
			if status["backup_storage_mode"] != mode {
				t.Fatalf("task mode lost: got %v, want %s", status["backup_storage_mode"], mode)
			}
		})
	}
}
