package app

import (
	"context"
	"testing"
)

func TestBackupControlNotificationDistinguishesStopFromFailure(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, stop := range []bool{false, true} {
			name := map[bool]string{false: "manual", true: "automatic"}[automatic] + map[bool]string{false: "/fault", true: "/stop"}[stop]
			t.Run(name, func(t *testing.T) {
				app := newBackupControlTestApp(t)
				app.configs["CONTROL"].AutoBackupEnabled = true
				sink, flush := capturePolicyNotifications(t, app)
				entered, proceed := make(chan struct{}), make(chan struct{})
				app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
					close(entered)
					<-proceed
					return newExecCmd(ctx, "/bin/sh", "-c", "exit 2")
				}
				job, err := app.beginBackupJob("CONTROL", "", "", automatic)
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- app.runBackupJob(job) }()
				<-entered
				if stop {
					if err := app.stopBackup("CONTROL", job.id); err != nil {
						close(proceed)
						<-result
						t.Fatal(err)
					}
				}
				close(proceed)
				if err := <-result; err == nil {
					t.Fatal("expected stopped or failed task")
				}
				flush()
				want := 1
				if stop {
					want = 0
				}
				if sink.Count() != want {
					t.Fatalf("failure notification count=%d want=%d", sink.Count(), want)
				}
			})
		}
	}
}
