package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBackupQueryDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, output, reason string
		code                 int64
		exit                 int
		cause                error
	}{
		{"explicit", "ERROR: Could not perform list (ErrorCode 207)", "device_error", 207, 49, nil},
		{"unknown49", "", "command_failed", 0, 49, nil},
		{"unknown255", "", "command_failed", 0, 255, nil},
		{"encrypted_notice", "This is an encrypted backup", "command_failed", 0, 49, nil},
		{"encrypted_failure", "ERROR: Invalid password when restoring encrypted backup (MBErrorDomain/207)", "encrypted_query_failed", 207, 49, nil},
		{"timeout", "ERROR: Could not perform list (ErrorCode 207)", "timeout", 207, 49, context.DeadlineExceeded},
		{"cancelled", "", "cancelled", 0, 49, context.Canceled},
		{"reported_error_after_success", "ERROR: Could not perform list (ErrorCode 207)", "device_error", 207, 0, nil},
		{"zero_does_not_erase_error", "ERROR: Could not read backup\nErrorCode 0", "command_failed", 0, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApplication()
			q := newQueryDiagnostics("DEVICE", "/backup", nil)
			io.WriteString(q, tc.output)
			runErr := exec.Command("/bin/sh", "-c", fmt.Sprintf("exit %d", tc.exit)).Run()
			if tc.cause != nil {
				runErr = annotateCommandError(runErr, tc.cause)
			}
			err := app.finishBackupQuery("list", "DEVICE", "USB", time.Now(), runErr, nil, q)
			var queryErr *backupQueryError
			if !errors.As(err, &queryErr) {
				t.Fatalf("missing query failure: %v", err)
			}
			d := queryErr.diagnostic
			if d.Reason != tc.reason {
				t.Fatalf("reason=%s want=%s", d.Reason, tc.reason)
			}
			if tc.code == 0 && d.DeviceErrorCode != nil {
				t.Fatalf("invented device code: %v", *d.DeviceErrorCode)
			}
			if tc.code != 0 && (d.DeviceErrorCode == nil || *d.DeviceErrorCode != tc.code) {
				t.Fatalf("missing device code: %+v", d)
			}
		})
	}
}

func TestBackupQueryDiagnosticRedaction(t *testing.T) {
	secret := "a-secret-password"
	q := newQueryDiagnostics("PRIVATE-UDID", "/备份/有 空格", []string{"BACKUP_PASSWORD=" + secret})
	for _, p := range []string{"ERROR: Could not read a-sec", "ret-password for PRIVATE-UDID /备份/有 空格/私人文档 (ErrorCode 207)\n"} {
		io.WriteString(q, p)
	}
	io.WriteString(q, "ERROR: Could not read file: relative/private-notes.txt\n")
	q.observe("ERROR: Could not read "+strings.Repeat("x", 1020)+"a-secret", true)
	io.WriteString(q, "ERROR: Could not read 工具原始原因\n")
	err := newApplication().finishBackupQuery("list", "PRIVATE-UDID", "USB", time.Now(), errors.New("unknown private argv"), nil, q)
	for _, private := range []string{secret, "a-secret", "PRIVATE-UDID", "有 空格", "私人文档", "private-notes", "private argv"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("private text leaked: %s", private)
		}
	}
	if !strings.Contains(err.Error(), "207") || !strings.Contains(err.Error(), "工具原始原因") {
		t.Fatalf("lost safe evidence: %v", err)
	}
}

func TestBackupQueryConsumerFailureStopsWriter(t *testing.T) {
	app := newApplication()
	addOnlineDevice(app, "CANCEL", connectionTypeDesc(connectTypeUSB))
	done := make(chan struct{})
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		defer close(done)
		for {
			if _, err := io.WriteString(out, "a,domain,1\n"); err != nil {
				return err
			}
		}
	})
	cause := errors.New("consumer rejected record")
	_, err := app.streamBackupList(context.Background(), "CANCEL", func([]string) error { return cause })
	if !errors.Is(err, cause) {
		t.Fatalf("lost consumer cause: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("writer not joined")
	}
}

func TestBackupQueryDiagnosticPrivateFragments(t *testing.T) {
	for _, tc := range []struct{ name, secret, line, private string }{
		{"relative_path", "pw", "ERROR: Could not open Private Folder/file.db (ErrorCode 207)", "Private Folder"},
		{"overlap_secret", "DEVICE-private-suffix", "ERROR: Could not unlock with DEVICE-private-suffix (ErrorCode 207)", "private-suffix"},
		{"bare_filename", "pw", "ERROR: Could not open personal-medical.pdf (ErrorCode 207)", "personal-medical"},
		{"multiline_secret", "first-line\nsecond-line", "ERROR: Could not unlock with first-line\nsecond-line (ErrorCode 207)", "first-line"},
		{"ansi_secret", "private\x1b[31msecret", "ERROR: Could not unlock with private\x1b[31msecret (ErrorCode 207)", "privatesecret"},
		{"whitespace_secret", " private-password ", "ERROR: Invalid password  private-password ", "private-password"},
		{"public_name_suffix", "pw", "ERROR: Could not read personal-medical-Manifest.db", "personal-medical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newQueryDiagnostics("DEVICE", "/backups", []string{"BACKUP_PASSWORD=" + tc.secret})
			io.WriteString(q, tc.line)
			err := newApplication().finishBackupQuery("list", "DEVICE", "USB", time.Now(), errors.New("failed"), nil, q)
			if strings.Contains(err.Error(), tc.private) {
				t.Fatalf("private fragment leaked: %v", err)
			}
		})
	}
}

func TestBackupQueryInterruptedPartialRecord(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		app := newApplication()
		addOnlineDevice(app, "PARTIAL", connectionTypeDesc(connectTypeUSB))
		app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
			io.WriteString(out, "\"partial path")
			return cause
		})
		_, err := app.BackupList(context.Background(), "PARTIAL")
		var queryErr *backupQueryError
		if !errors.As(err, &queryErr) || !errors.Is(err, cause) || queryErr.diagnostic.Reason == "parse_failed" {
			t.Fatalf("interruption masked by partial record: %v", err)
		}
	}
}

func TestBackupQueryUpstreamDiagnostics(t *testing.T) {
	for _, line := range []string{"Error requesting backup info from device, error code -6", "Error requesting backup list from device, error code -6", "Could not perform backup protocol version exchange, error code -1", "Could not read Info.plist", "ERROR: Could not start service com.apple.mobilebackup2: Service prohibited"} {
		q := newQueryDiagnostics("DEVICE", "/backups", nil)
		io.WriteString(q, line)
		err := newApplication().finishBackupQuery("list", "DEVICE", "USB", time.Now(), errors.New("failed"), nil, q)
		if !strings.Contains(err.Error(), line) {
			t.Fatalf("recognized upstream output lost: %v", err)
		}
	}
}
