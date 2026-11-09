package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type streamRunnerFunc func(context.Context, string, []string, []string, io.Writer, io.Writer) error

func (f streamRunnerFunc) Run(ctx context.Context, name string, args, env []string, stdout, stderr io.Writer) error {
	return f(ctx, name, args, env, stdout, stderr)
}

func TestCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := defaultStreamCmdRunner{}
	err := runner.Run(ctx, "/bin/sh", []string{"-c", "sleep 30"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		t.Fatalf("cancelled command returned %v", err)
	}
}

func TestBackupListJSONStopsMaterializingAfterLimit(t *testing.T) {
	app := newApplication()
	addOnlineDevice(app, "LIMIT", connectionTypeDesc(connectTypeUSB))
	row := []byte("file.db,AppDomain,1024\n")
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, _ io.Writer) error {
		for i := 0; i < 100000; i++ {
			if _, err := stdout.Write(row); err != nil {
				return err
			}
		}
		return nil
	})

	entries, total, truncated, err := app.BackupListLimited(context.Background(), "LIMIT", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5000 || total != 100000 || !truncated {
		t.Fatalf("len=%d total=%d truncated=%v", len(entries), total, truncated)
	}
}

func TestLongCommandKeepsOnlyErrorTail(t *testing.T) {
	app := newApplication()
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, stderr io.Writer) error {
		_, _ = io.CopyN(stdout, bytes.NewReader(bytes.Repeat([]byte("o"), 1<<20)), 1<<20)
		_, _ = io.CopyN(stderr, bytes.NewReader(bytes.Repeat([]byte("e"), 1<<20)), 1<<20)
		return errors.New("boom")
	})
	out, err := app.runIdeviceCmdBoundedEnv(context.Background(), cmdKindLong, nil, nil, "/bin/false")
	if err == nil {
		t.Fatal("expected command error")
	}
	if len(out) != maxCommandErrorBytes {
		t.Fatalf("tail length=%d", len(out))
	}
}

func TestBackupListLongCSVRecords(t *testing.T) {
	long := strings.Repeat("x", 70<<10)
	multiline := "first\nsecond,\"third\""
	input := "before,1\r\n" + long + ",\"first\nsecond,\"\"third\"\"\"\r\nafter,3"
	var rows [][]string
	err := forEachBackupListRow(strings.NewReader(input), func(fields []string) error {
		rows = append(rows, append([]string(nil), fields...))
		return nil
	})
	if err != nil || len(rows) != 3 || rows[1][0] != long || rows[1][1] != multiline || rows[2][0] != "after" {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestBackupListHTTPRejectsOversizedRecords(t *testing.T) {
	app := newBackupControlTestApp(t)
	for _, tc := range []struct {
		name, format, prefix string
		abort                bool
	}{
		{"JSON", "", "ok,1\n", false}, {"CSV-first", "?format=csv", "", false}, {"CSV-started", "?format=csv", "ok,1\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 每个物理行都在上限内，整条引号记录超限。
			output := tc.prefix + "\"" + strings.Repeat("x\n", backupIndexMaxRecordBytes/2) + "\""
			app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, _ io.Writer) error {
				_, err := io.WriteString(stdout, output)
				return err
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/backup-list/CONTROL"+tc.format, nil)
			if tc.abort {
				defer func() {
					if recover() != http.ErrAbortHandler || w.Body.String() != "ok,1\n" {
						t.Fatal("partial CSV response was not aborted")
					}
				}()
			}
			app.handleBackupList(w, r)
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "api.backup_list_record_limit") || w.Header().Get("Content-Disposition") != "" {
				t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
			}
		})
	}
}
