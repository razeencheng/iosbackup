package app

import (
	"context"
	"strings"
	"testing"
)

func TestBackupNativeDeviceLockedDiagnosis(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"native stdout error", `printf 'IOSBK_ACTIVITY 0 waiting_authorization\n' >&2
printf 'ErrorCode 208: Device locked (MBErrorDomain/208)\n'
exit 48`, "device_locked"},
		{"stderr error", `printf 'ErrorCode 208: Device locked (MBErrorDomain/208)\n' >&2
exit 48`, "device_locked"},
		{"authorization wait is not failure", `printf '*** Waiting for passcode to be entered on the device ***\n'
exit 1`, "backup_failed"},
		{"file text is not a diagnostic", `printf 'Receiving file ErrorCode 208: Device locked (MBErrorDomain/208)\n'
exit 1`, "backup_failed"},
		{"successful completion wins", `printf 'ErrorCode 208: Device locked (MBErrorDomain/208)\nBackup Successful.\n'`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newBackupControlTestApp(t)
			a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
			a.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
				return newExecCmd(ctx, "/bin/sh", "-c", tc.script)
			}
			err := a.PerformBackup("CONTROL")
			d := a.buildStatusSnapshot().Devices[0]
			if d.LastBackupErrorCode != tc.want {
				t.Fatalf("got %q, want %q (err=%v)", d.LastBackupErrorCode, tc.want, err)
			}
			if (err == nil) != (tc.want == "") {
				t.Fatalf("unexpected result: %v", err)
			}
			records := a.backupJobRecords.list("CONTROL")
			if len(records) != 1 || records[0].ErrorCode != tc.want {
				t.Fatalf("task did not retain diagnostic: %+v", records)
			}
			if tc.want == "device_locked" && strings.Contains(err.Error(), "MBErrorDomain") {
				t.Fatal("raw tool diagnostic escaped into public error")
			}
		})
	}
}
