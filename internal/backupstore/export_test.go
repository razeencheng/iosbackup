package backupstore

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func exportFixture(t *testing.T) (*Store, Point, map[string]string) {
	t.Helper()
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	files := map[string]string{"Info.plist": "source device", "Status.plist": "finished", "Manifest.plist": "encrypted unchanged", "Manifest.db": "manifest bytes", ".legitimate": "hidden backup data", "ab/object": "payload", "secrets.json": "legitimate user backup content"}
	for name, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(w.Directory, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.Directory, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(w.Directory, "_unback_"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Directory, "_unback_", "unpacked"), []byte("exclude"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, files
}
func TestBackupExportComplete(t *testing.T) {
	s, p, files := exportFixture(t)
	lease, err := s.Acquire(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	var out bytes.Buffer
	if err = lease.Export(context.Background(), &out, p.UDID); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&out)
	seen := map[string]string{}
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		b, e := io.ReadAll(reader)
		if e != nil {
			t.Fatal(e)
		}
		seen[h.Name] = string(b)
	}
	if len(seen) != len(files) {
		t.Fatalf("unexpected archive entries: %v", seen)
	}
	for name, data := range files {
		if seen["PHONE/"+name] != data {
			t.Fatalf("missing/changed %s", name)
		}
	}
	c, _ := s.Snapshot()
	if err = s.Delete(context.Background(), p.ID, c.Revision); !errors.Is(err, ErrBusy) {
		t.Fatalf("export lease not protected: %v", err)
	}
}
func TestBackupExportRejectsUnsafeAndMissing(t *testing.T) {
	for _, mode := range []string{"link", "missing", "cancel", "udid", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			s, p, _ := exportFixture(t)
			lease, err := s.Acquire(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			udid := p.UDID
			switch mode {
			case "link":
				err = os.Symlink("/etc/passwd", filepath.Join(lease.Directory, "bad"))
			case "missing":
				err = os.Remove(filepath.Join(lease.Directory, "Manifest.db"))
			case "cancel":
				cancel()
			case "udid":
				udid = "../escape"
			case "foreign":
				udid = "OTHER"
			}
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err = lease.Export(ctx, &out, udid); err == nil {
				t.Fatal("invalid export succeeded")
			}
		})
	}
}

type exportBrokenWriter struct{ left int }

func (w *exportBrokenWriter) Write(p []byte) (int, error) {
	if w.left <= 0 {
		return 0, io.ErrClosedPipe
	}
	if len(p) > w.left {
		p = p[:w.left]
	}
	w.left -= len(p)
	return len(p), nil
}
func TestBackupExportWriteFailure(t *testing.T) {
	s, p, _ := exportFixture(t)
	l, e := s.Acquire(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = l.Export(context.Background(), &exportBrokenWriter{left: 600}, p.UDID); e == nil {
		t.Fatal("broken download accepted")
	}
}
func TestStoreExplicitDeleteAndReview(t *testing.T) {
	s, p, _ := exportFixture(t)
	c, _ := s.Snapshot()
	if e := s.SetPinned(context.Background(), p.ID, true, c.Revision); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Snapshot()
	if e := s.Recycle(context.Background(), p.ID, c.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := s.RecordReview(context.Background(), p.ID, Check{State: "passed", Scope: "manifest", CheckedAt: p.CompletedAt}, c.Revision); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Snapshot()
	if c.Points[p.ID].Review.Scope != "manifest" {
		t.Fatal("review lost")
	}
	if e := s.Delete(context.Background(), p.ID, c.Revision-1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := s.Delete(context.Background(), p.ID, c.Revision); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Snapshot()
	if len(c.Points) != 0 || len(c.Baselines) != 0 {
		t.Fatal("stale baseline after deletion")
	}
}
func TestStoreExplicitDeleteRecovery(t *testing.T) {
	for _, stage := range []string{"recycle-intent.after", "recycle-catalog.after", "recycle-data.after", "recycle-descriptor.after", "recycle-cleanup.after"} {
		t.Run(stage, func(t *testing.T) {
			s, p, _ := exportFixture(t)
			c, _ := s.Snapshot()
			if e := s.SetPinned(context.Background(), p.ID, true, c.Revision); e != nil {
				t.Fatal(e)
			}
			c, _ = s.Snapshot()
			s.step = func(name string) error {
				if name == stage {
					return errors.New("injected")
				}
				return nil
			}
			if e := s.Delete(context.Background(), p.ID, c.Revision); !errors.Is(e, ErrUncertain) {
				t.Fatal(e)
			}
			s.step = nil
			path, id := s.path, s.identity.ID
			s.Close()
			next, e := Open(path, p.UDID, id)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if _, e = next.Recover(context.Background()); e != nil {
				t.Fatal(e)
			}
			after, e := next.Snapshot()
			if e != nil || len(after.Points) != 0 || len(after.Baselines) != 0 {
				t.Fatal("delete recovery", e, after)
			}
		})
	}
}

type exportCountingWriter struct {
	max, bytes int
	cancel     context.CancelFunc
}

func (w *exportCountingWriter) Write(data []byte) (int, error) {
	w.max = max(w.max, len(data))
	w.bytes += len(data)
	if w.cancel != nil && w.bytes > 512<<10 {
		w.cancel()
	}
	return len(data), nil
}
func TestBackupExportBoundedAndCancelled(t *testing.T) {
	s, p, _ := exportFixture(t)
	l, e := s.Acquire(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	f, e := os.Create(filepath.Join(l.Directory, "large"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(16 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	writer := &exportCountingWriter{}
	if e = l.Export(context.Background(), writer, p.UDID); e != nil {
		t.Fatal(e)
	}
	if writer.max > 128<<10 || writer.bytes < 16<<20 {
		t.Fatal("unbounded or incomplete stream", writer)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer = &exportCountingWriter{cancel: cancel}
	if e = l.Export(ctx, writer, p.UDID); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if writer.bytes >= 1<<20 {
		t.Fatal("read continued after cancellation")
	}
}
