package app

import (
	"context"
	"testing"
)

func TestObservedModeFromToolOutput(t *testing.T) {
	for _, tc := range []struct{ name, output, want string }{
		{"full", "Full backup mode.\n", "full"},
		{"incremental", "Incremental backup mode.\nIncremental backup mode.\n", "incremental"},
		{"ansi", "  \033[32mIncremental backup mode.\033[0m  \n", "incremental"},
		{"unknown", "File Full backup mode. does not imply mode\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := backupControlFixture(t)
			dir := t.TempDir()
			a.backupJobRecords = openJobTestStore(t, dir, defaultBackupJobLimits())
			a.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
				return newExecCmd(ctx, "/bin/sh", "-c", "printf 'IOSBK_ACTIVITY 1 receiving\\n' >&2; printf '%b' \"$1\"; printf 'Backup Successful.\\n'", "fixture", tc.output)
			}
			job, err := a.beginBackupJob("PHONE", "", "", false, backupRequest{Mode: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			if err = a.runBackupJob(job); err != nil {
				t.Fatal(err)
			}
			syncJobs(t, a.backupJobRecords)
			r := a.backupJobRecords.list("PHONE")[0]
			if r.ObservedMode != tc.want || r.RequestedMode != "auto" {
				t.Fatal("observed mode/request mismatch", r.ObservedMode, r.RequestedMode)
			}
			_, events, _ := a.backupJobRecords.events(job.id, 0, 100)
			count := 0
			for _, e := range events {
				if e.Code == "mode_observed" {
					count++
				}
			}
			want := 1
			if tc.want == "unknown" {
				want = 0
			}
			if count != want {
				t.Fatal("mode event not deduplicated", count, want)
			}
			for _, d := range a.buildStatusSnapshot().Devices {
				if d.UDID == "PHONE" && d.ObservedMode != tc.want {
					t.Fatal("live snapshot differs", d.ObservedMode)
				}
			}
			if err = a.backupJobRecords.close(context.Background()); err != nil {
				t.Fatal(err)
			}
			restored := openJobTestStore(t, dir, defaultBackupJobLimits())
			if restored.list("PHONE")[0].ObservedMode != tc.want {
				t.Fatal("mode lost after restart")
			}
		})
	}
}

func TestObservedModeRejectsLateAndWrongStream(t *testing.T) {
	a, _ := backupControlFixture(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	old, err := a.beginBackupJob("PHONE", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	a.finishBackupJob(old, context.Canceled)
	job, err := a.beginBackupJob("PHONE", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	a.observeBackupMode("PHONE", old.id, "Full backup mode.", "STDOUT")
	a.observeBackupMode("PHONE", job.id, "Full backup mode.", "STDERR")
	if a.backupJobRecords.list("PHONE")[0].ObservedMode != "unknown" {
		t.Fatal("stale task or stderr changed mode")
	}
	a.observeBackupMode("PHONE", job.id, "Incremental backup mode.", "STDOUT")
	a.finishBackupJob(job, context.Canceled)
	if a.backupJobRecords.list("PHONE")[0].ObservedMode != "incremental" {
		t.Fatal("later failure erased observation")
	}
}

func TestObservedModeDoesNotGuessFromForcedRequest(t *testing.T) {
	a, _ := backupControlFixture(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	a.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
		return newExecCmd(ctx, "/bin/sh", "-c", "exit 1")
	}
	job, err := a.beginBackupJob("PHONE", "", "", false, backupRequest{Mode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runBackupJob(job); err == nil {
		t.Fatal("failure hidden")
	}
	if a.backupJobRecords.list("PHONE")[0].ObservedMode != "unknown" {
		t.Fatal("request fabricated observed mode")
	}
}
