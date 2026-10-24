package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"testing"
)

func TestBackupQueryStdoutFailure(t *testing.T) {
	for _, op := range []string{"info", "list"} {
		for _, channel := range []string{"stdout", "stderr"} {
			t.Run(op+"/"+channel, func(t *testing.T) {
				app := newApplication()
				addOnlineDevice(app, "QUERY", connectionTypeDesc(connectTypeUSB))
				exitErr := exec.Command("/bin/sh", "-c", "exit 49").Run()
				app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, stderr io.Writer) error {
					output := stdout
					if channel == "stderr" {
						output = stderr
					}
					io.WriteString(output, "ERROR: Could not perform list: device rejected operation (ErrorCode 207)\n")
					return exitErr
				})
				var logs bytes.Buffer
				old := log.Writer()
				log.SetOutput(&logs)
				defer log.SetOutput(old)
				var err error
				if op == "info" {
					_, err = app.BackupInfo(context.Background(), "QUERY")
				} else {
					_, err = app.BackupList(context.Background(), "QUERY")
				}
				if err == nil || !strings.Contains(err.Error(), "207") {
					t.Fatalf("lost device diagnosis: %v", err)
				}
				var gotExit *exec.ExitError
				if !errors.As(err, &gotExit) || gotExit.ExitCode() != 49 {
					t.Fatalf("lost exit code: %v", err)
				}
				if strings.Count(logs.String(), "event=backup_query.failed") != 1 {
					t.Fatalf("expected one failure log: %s", &logs)
				}
			})
		}
	}
}

func TestBackupQueryOutputFraming(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		want         [][]string
		fail         bool
	}{
		{"multiline", "\"  a\r\nb \"\"quoted\"\"  \",domain,1\r\n", [][]string{{"  a\nb \"quoted\"  ", "domain", "1"}}, false},
		{"diagnostic_comma", "ERROR: Could not perform list: denied, try again (ErrorCode 207)\na,domain,1\n", [][]string{{"a", "domain", "1"}}, false},
		{"private_error_name", "\"ERROR: private, ErrorCode 207\nnext\",domain,1\n", [][]string{{"ERROR: private, ErrorCode 207\nnext", "domain", "1"}}, false},
		{"spaces", "  name  ,domain,1", [][]string{{"  name  ", "domain", "1"}}, false},
		{"lazy_quote_bare_cr", "\"a\"\rb\",c\n", [][]string{{"a\"\rb", "c"}}, false},
		{"unquoted_bare_cr", "a\rb,c\n", [][]string{{"a\rb", "c"}}, false},
		{"quoted_trailing_cr", "\"a\"\r", [][]string{{"a"}}, false},
		{"oversized", strings.Repeat("x", 65537) + ",domain,1\n", nil, true},
		{"unterminated", "\"unclosed\nnext", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [][]string
			err := forEachBackupListRow(strings.NewReader(tc.output), func(row []string) error { got = append(got, append([]string(nil), row...)); return nil })
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v, want failure=%v", err, tc.fail)
			}
			if !tc.fail && fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("rows=%q want=%q", got, tc.want)
			}
		})
	}
}
