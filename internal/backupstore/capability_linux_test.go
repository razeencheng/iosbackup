//go:build linux

package backupstore

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLinuxCapacityUsesFragmentUnitsAndUnknownCounters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stat  syscall.Statfs_t
		bytes uint64
		known bool
	}{
		{"fragment", syscall.Statfs_t{Bsize: 1 << 20, Frsize: 4096, Bavail: 100}, 409600, true},
		{"fallback", syscall.Statfs_t{Bsize: 4096, Bavail: 100}, 409600, true},
		{"unknown", syscall.Statfs_t{Bsize: 4096, Frsize: 4096, Bavail: math.MaxUint64}, 0, false},
		{"zero", syscall.Statfs_t{Bsize: 4096, Frsize: 4096, Bavail: 0}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := capacityFromStatfs(tc.stat)
			if c.Bytes != tc.bytes || c.BytesKnown != tc.known {
				t.Fatalf("capacity %+v", c)
			}
		})
	}
	c := capacityFromStatfs(syscall.Statfs_t{Bsize: 4096, Files: 100, Ffree: math.MaxUint64})
	if c.InodesKnown {
		t.Fatal("unknown inode count treated as unlimited")
	}
}

func TestLinuxSyncRejectsBindMount(t *testing.T) {
	if os.Getenv("IOSBK_TEST_MOUNTS") != "1" {
		t.Skip("requires an isolated mount namespace")
	}
	base := t.TempDir()
	container := filepath.Join(base, "object")
	inside := filepath.Join(container, "PHONE", "nested")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{inside, outside} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount(outside, inside, "", syscall.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer syscall.Unmount(inside, 0)
	root, err := os.OpenRoot(container)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = syncTree(context.Background(), root); !errors.Is(err, ErrUnsafe) {
		t.Fatal("accepted nested bind mount on the same filesystem", err)
	}
}

func TestCopyFilesystemCapability(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	content := make([]byte, 4<<20)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(source, "object"), content, 0600); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	report, err := CopyTree(context.Background(), source, filepath.Join(root, "copy"), CopyOptions{PreferClone: true})
	if err != nil {
		t.Fatal(err)
	}
	var stat syscall.Statfs_t
	if err = syscall.Statfs(root, &stat); err != nil {
		t.Fatal(err)
	}
	t.Logf("filesystem=0x%x mode=%s files=%d logical=%d allocated=%d cloned=%d duration=%s", stat.Type, report.Mode, report.Files, report.Bytes, report.AllocatedBytes, report.ClonedFiles, time.Since(begin))
	if os.Getenv("IOSBK_REQUIRE_CLONE") == "1" && report.ClonedFiles != 1 {
		t.Fatal("target does not support tested clone")
	}
}

func TestLinuxCloneProbe(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	supported, err := probeClone(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual target clone isolation supported=%t", supported)
}
