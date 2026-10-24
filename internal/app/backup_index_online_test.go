package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupIndexOnlineCapture(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	query := func(emit func([]string) error) (int, error) {
		if err := emit([]string{"raw", "field,\nvalue", "7"}); err != nil {
			return 0, err
		}
		return 1, nil
	}
	calls := 0
	n, err := app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, query, func(row []string) error { calls++; return nil })
	if err != nil || n != 1 || calls != 1 {
		t.Fatal(n, err, calls)
	}
	got, err := m.store.load(src)
	if err != nil || got.FileList.Format != "online-v1" || got.FileList.Entries[0].Fields[1] != "field,\nvalue" {
		t.Fatalf("capture %+v %v", got, err)
	}
	revision := got.Revision
	_, err = app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, func(emit func([]string) error) (int, error) {
		_ = emit([]string{"partial"})
		return 1, errors.New("tool failed")
	}, func([]string) error { return nil })
	if err == nil {
		t.Fatal("tool error lost")
	}
	got, _ = m.store.load(src)
	if got.Revision != revision {
		t.Fatal("partial command replaced complete capture")
	}
	m.store.limits.CSVBytes = 1
	calls = 0
	n, err = app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, query, func([]string) error { calls++; return nil })
	if err != nil || n != 1 || calls != 1 {
		t.Fatal("cache failure changed query result", n, err, calls)
	}
}
func TestBackupIndexOnlineCaptureRejectsChangedSource(t *testing.T) {
	app, m, src := indexCoordinatorTestApp(t)
	_, err := app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, func(emit func([]string) error) (int, error) {
		if err := os.WriteFile(filepath.Join(src.Directory, "Manifest.plist"), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		return 1, emit([]string{"row"})
	}, func([]string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := m.store.load(src)
	if got.FileList.Complete {
		t.Fatal("mismatched source published")
	}
}

func TestBackupIndexOnlineCaptureCancellation(t *testing.T) {
	for _, mode := range []string{"writer", "shutdown", "root"} {
		t.Run(mode, func(t *testing.T) {
			app, m, src := indexCoordinatorTestApp(t)
			root, cancel := context.WithCancel(context.Background())
			defer cancel()
			app.rootCtx = root
			entered := make(chan struct{})
			release := make(chan struct{})
			queryDone := make(chan error, 1)
			go func() {
				_, err := app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, func(emit func([]string) error) (int, error) {
					if err := emit([]string{"before"}); err != nil {
						return 0, err
					}
					close(entered)
					<-release
					return 2, emit([]string{"after"})
				}, func([]string) error { return nil })
				queryDone <- err
			}()
			<-entered
			m.mu.Lock()
			capture := m.captures[src.UDID]
			m.mu.Unlock()
			if capture == nil {
				t.Fatal("capture not admitted")
			}
			stopped := make(chan struct{})
			go func() {
				switch mode {
				case "writer":
					app.mu.Lock()
					app.backupInProgress[src.UDID] = true
					app.mu.Unlock()
					app.beginBackupIndexWrite(src.UDID, app.paths.BackupsRoot, true)
				case "shutdown":
					m.close()
				case "root":
					cancel()
					<-capture.done
				}
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("cache cancellation waited for original query")
			}
			got, _ := m.store.load(src)
			if got.FileList.Complete {
				t.Fatal("cancelled capture published")
			}
			close(release)
			select {
			case err := <-queryDone:
				if err != nil {
					t.Fatal("cache cancellation changed query", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("query stuck")
			}
			if mode == "writer" {
				finishIndexTestJob(app, src.UDID, errors.New("cleanup"))
			}
		})
	}
}

func TestBackupIndexOnlineCaptureRejectsLossyLegacyOutput(t *testing.T) {
	for _, raw := range []string{"id,domain," + strings.Repeat("x", maxBackupLogLineBytes+64) + ",1\n", "id,domain,\"first\n  second\",1\n", " id,domain,path,1\n", "id,domain,file [line truncated]\n"} {
		app, m, src := indexCoordinatorTestApp(t)
		candidate, err := m.store.prepare(context.Background(), src, "manifest-v1", indexTestRows(1))
		if err != nil {
			t.Fatal(err)
		}
		before, err := m.store.publish(src, indexTestOverview(), candidate, nil)
		if err != nil {
			t.Fatal(err)
		}
		audit := &backupListCacheAudit{safe: true}
		_, err = app.captureOnlineBackupIndex(context.Background(), src.UDID, app.paths.BackupsRoot, func(emit func([]string) error) (int, error) {
			n := 0
			err := forEachBackupListRow(io.TeeReader(strings.NewReader(raw), audit), func(fields []string) error { n++; return emit(fields) })
			return n, err
		}, func([]string) error { return nil }, audit.complete)
		if err != nil {
			t.Fatal("legacy query behavior changed", err)
		}
		got, err := m.store.load(src)
		if err != nil || got.Revision != before.Revision {
			t.Fatal("lossy legacy output replaced complete cache")
		}
	}
	audit := &backupListCacheAudit{safe: true}
	_, _ = audit.Write([]byte("id,domain,\"file,quote\",1\n"))
	if !audit.complete() {
		t.Fatal("valid single-line CSV rejected")
	}
}
