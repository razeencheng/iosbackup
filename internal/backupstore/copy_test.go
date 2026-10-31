package backupstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func copyFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "source")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"Info.plist": "metadata", "Status.plist": "finished", ".hidden": "keep", "a.tmp": "keep-temp", "aa/file": "original-object"} {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return src, filepath.Join(base, "copy")
}
func TestCopyIsolation(t *testing.T) {
	for _, clone := range []bool{false, true} {
		t.Run(map[bool]string{false: "copy", true: "auto"}[clone], func(t *testing.T) {
			src, dst := copyFixture(t)
			if err := os.Mkdir(filepath.Join(src, "_unback_"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "_unback_", "derived"), []byte("exclude"), 0600); err != nil {
				t.Fatal(err)
			}
			report, err := CopyTree(context.Background(), src, dst, CopyOptions{PreferClone: clone})
			if err != nil {
				t.Fatal(err)
			}
			if report.Files != 5 || report.Bytes == 0 {
				t.Fatalf("report %+v", report)
			}
			for _, name := range []string{".hidden", "a.tmp"} {
				if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(filepath.Join(dst, "_unback_")); !os.IsNotExist(err) {
				t.Fatal("copied derived content")
			}
			// 原地改写、截断、删除、重命名均不能影响源文件。
			if err := os.WriteFile(filepath.Join(dst, "aa/file"), []byte("rewrite"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(filepath.Join(dst, "Info.plist"), 0); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dst, "Status.plist")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dst, "a.tmp"), filepath.Join(dst, "renamed")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, "new"), []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"aa/file": "original-object", "Info.plist": "metadata", "Status.plist": "finished", "a.tmp": "keep-temp"} {
				got, err := os.ReadFile(filepath.Join(src, name))
				if err != nil || string(got) != want {
					t.Fatalf("source changed: %s %v", name, err)
				}
			}
		})
	}
}
func TestCopyIsolationRejectsUnsafeTrees(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "nested", "same", "existing"} {
		t.Run(kind, func(t *testing.T) {
			src, dst := copyFixture(t)
			switch kind {
			case "symlink":
				if err := os.Symlink("/etc", filepath.Join(src, "escape")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0600); err != nil {
					t.Fatal(err)
				}
			case "nested":
				dst = filepath.Join(src, "nested")
			case "same":
				dst = src
			case "existing":
				if err := os.Mkdir(dst, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CopyTree(context.Background(), src, dst, CopyOptions{}); err == nil {
				t.Fatal("unsafe copy accepted")
			}
		})
	}
}
func TestCopyIsolationCancellationAndSourceChange(t *testing.T) {
	src, dst := copyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CopyTree(ctx, src, dst, CopyOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	changed := false
	_, err := CopyTree(context.Background(), src, dst, CopyOptions{Progress: func(CopyReport) {
		if !changed {
			changed = true
			_ = os.WriteFile(filepath.Join(src, "Info.plist"), []byte(strings.Repeat("x", 100)), 0600)
		}
	}})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("source change accepted: %v", err)
	}
}
func TestCopyIsolationCapacity(t *testing.T) {
	src, dst := copyFixture(t)
	capacity, err := StorageCapacity(src)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.BytesKnown {
		if _, err := CopyTree(context.Background(), src, dst, CopyOptions{ReserveBytes: ^uint64(0)}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("capacity not enforced: %v", err)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestCopyIsolationShortWriteAndInodeLimit(t *testing.T) {
	if err := copyBytes(context.Background(), shortWriter{}, strings.NewReader("data"), make([]byte, 128)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if err := checkCapacity(Capacity{InodesKnown: true, Inodes: 1}, 1, 2, 0); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if err := checkCapacity(Capacity{}, 1, 2, 0); err != nil {
		t.Fatal("unknown capacity must not become zero", err)
	}
}

func TestCopyIsolationProbeCleanupFailure(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cleanupErr := errors.New("probe removal failed")
	supported, err := probeCloneWith(root, func(*os.File, *os.File) error { return syscall.ENOTSUP }, func(string) error { return cleanupErr })
	if supported || !errors.Is(err, cleanupErr) {
		t.Fatal("cleanup failure treated as unsupported clone", err)
	}
}

func TestCopyIsolationMetadataDigestIgnoresCoarseTimestamp(t *testing.T) {
	src, _ := copyFixture(t)
	root, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := stampTree(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(src, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(src, "Info.plist"), []byte("metadatu"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(filepath.Join(src, "Info.plist"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := stampTree(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("metadata mutation not detected")
	}
}

func simulatedClone(dst, src *os.File) error {
	_, err := io.Copy(dst, io.NewSectionReader(src, 0, 1<<30))
	return err
}

func TestCloneProbeClosesBeforeRemoval(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var cloned *os.File
	ok, err := probeCloneWith(root, func(dst, src *os.File) error { cloned = dst; return simulatedClone(dst, src) }, func(path string) error {
		if strings.HasPrefix(path, ".clone-copy-") && cloned != nil {
			if _, e := cloned.Stat(); !errors.Is(e, os.ErrClosed) {
				return errors.New("target still open during removal")
			}
		}
		return root.Remove(path)
	})
	if err != nil || !ok {
		t.Fatal("probe rejected a close-before-delete filesystem", err)
	}
}

func TestCloneProbePermissionFallbackOnlyAppliesToClone(t *testing.T) {
	for _, denied := range []error{syscall.EPERM, syscall.EACCES} {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ok, err := probeCloneWith(root, func(*os.File, *os.File) error { return denied }, root.Remove)
		root.Close()
		if ok || err != nil {
			t.Fatal("clone-only permission restriction blocked ordinary copy", err)
		}
	}
}

func TestCopyCloneCapacityBudgetAndFallback(t *testing.T) {
	for _, mode := range []string{"clone", "unsupported", "file-fallback"} {
		t.Run(mode, func(t *testing.T) {
			src, dst := copyFixture(t)
			if err := os.WriteFile(filepath.Join(src, "large"), make([]byte, 2<<20), 0600); err != nil {
				t.Fatal(err)
			}
			capacity := func(string) (Capacity, error) {
				return Capacity{BytesKnown: true, Bytes: 1 << 20, InodesKnown: true, Inodes: 1024}, nil
			}
			clone := func(out, in *os.File) error {
				if mode == "unsupported" || mode == "file-fallback" && filepath.Base(in.Name()) == "large" {
					return syscall.ENOTSUP
				}
				return simulatedClone(out, in)
			}
			report, err := copyTreeWith(context.Background(), src, dst, CopyOptions{PreferClone: true, ReserveBytes: 64 << 10}, capacity, clone)
			if mode == "clone" {
				if err != nil || report.Mode != "clone" {
					t.Fatal("clone was charged the full logical size", report, err)
				}
			} else if !errors.Is(err, ErrCapacity) {
				t.Fatal("full-copy fallback ignored space budget", err)
			}
		})
	}
}
