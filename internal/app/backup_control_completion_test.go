package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type backupCompletionBarrier struct {
	entered, release chan struct{}
	once             sync.Once
}

func (w *backupCompletionBarrier) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "[STDOUT] FINAL_OUTPUT") {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return len(p), nil
}

func TestBackupControlCompletedCommandSurvivesStopDuringOutputDrain(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "success"}[success], func(t *testing.T) {
			app := newBackupControlTestApp(t)
			sink, flush := capturePolicyNotifications(t, app)
			job, err := app.beginBackupJob("CONTROL", "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			barrier := &backupCompletionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
			previous := log.Writer()
			log.SetOutput(barrier)
			defer log.SetOutput(previous)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
			defer release()
			var command *execCmd
			app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
				script := "printf 'FINAL_OUTPUT'; exit 2"
				if success {
					script = "printf 'Backup Successful.\\nFINAL_OUTPUT'"
				}
				command = newExecCmd(ctx, "/bin/sh", "-c", script)
				return command
			}
			result := make(chan error, 1)
			go func() { result <- app.runBackupJob(job) }()
			select {
			case <-barrier.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("output did not reach barrier")
			}
			<-command.done
			if (command.cmd.ProcessState.ExitCode() == 0) != success {
				t.Fatal("unexpected exit status")
			}
			stopped := make(chan error, 1)
			go func() { stopped <- app.stopBackup("CONTROL", job.id) }()
			// 日志输出暂时阻塞停止请求；用状态广播前的内存屏障确认请求已受理。
			deadline := time.After(5 * time.Second)
			for {
				app.mu.RLock()
				accepted := job.stopAccepted
				app.mu.RUnlock()
				if accepted {
					break
				}
				select {
				case <-deadline:
					t.Fatal("stop was not accepted")
				case <-time.After(time.Millisecond):
				}
			}
			if d := app.buildStatusSnapshot().Devices[0]; !d.BackingUp || app.backupSem.inUse() != 1 {
				t.Fatal("resources released before output drained")
			}
			release()
			runErr := <-result
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
			if success && runErr != nil {
				t.Fatalf("success overwritten: %v", runErr)
			}
			if !success && (runErr == nil || errors.Is(runErr, errBackupStoppedByUser)) {
				t.Fatalf("real failure overwritten: %v", runErr)
			}
			flush()
			want := 1
			if success {
				want = 0
			}
			if sink.Count() != want {
				t.Fatalf("failure notifications=%d want=%d", sink.Count(), want)
			}
			d := app.buildStatusSnapshot().Devices[0]
			if !d.AutoBackupHold || (d.LastBackupAt != "") != success {
				t.Fatalf("wrong completed result: %+v", d)
			}
		})
	}
}

func TestBackupControlPreparationFailureNotifies(t *testing.T) {
	app := newBackupControlTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	blocker := filepath.Join(app.paths.BackupsRoot, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	app.configs["CONTROL"].BackupDirectory = blocker
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	if err := app.PerformBackup("CONTROL"); err == nil {
		t.Fatal("expected directory error")
	}
	flush()
	if sink.Count() != 1 || !strings.Contains(logs.String(), "event=backup.failed") {
		t.Fatalf("silent preparation failure: notifications=%d logs=%q", sink.Count(), logs.String())
	}
}

func TestBackupControlStopBeforeExecutionDoesNotNotifyFailure(t *testing.T) {
	app := newBackupControlTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.stopBackup("CONTROL", job.id); err != nil {
		t.Fatal(err)
	}
	if err := app.runBackupJob(job); !errors.Is(err, errBackupStoppedByUser) {
		t.Fatalf("wrong result: %v", err)
	}
	flush()
	if sink.Count() != 0 {
		t.Fatal("user stop notified as failure")
	}
}
