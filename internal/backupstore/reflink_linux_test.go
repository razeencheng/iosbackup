//go:build linux

package backupstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// 仅在显式提供的小型独立测试文件系统运行，不能对实际备份盘制造低空间。
func TestRealReflinkCapacityPreservesSavedPoint(t *testing.T) {
	if os.Getenv("IOSBK_TEST_CAPACITY") != "1" || os.Getenv("IOSBK_REQUIRE_CLONE") != "1" {
		t.Skip("requires an isolated small reflink filesystem and explicit capacity-test opt-in")
	}
	base := t.TempDir()
	var stat syscall.Statfs_t
	if err := syscall.Statfs(base, &stat); err != nil {
		t.Fatal(err)
	}
	unit := stat.Frsize
	if unit <= 0 {
		unit = stat.Bsize
	}
	if unit <= 0 || stat.Blocks == 0 || saturatedProduct(stat.Blocks, uint64(unit)) > 2<<30 || stat.Type != 0x58465342 && stat.Type != 0x9123683e {
		t.Fatal("capacity test requires a dedicated XFS/Btrfs filesystem no larger than 2 GiB")
	}
	s := storeFixture(t)
	w, point := preparePoint(t, s)
	point.Pinned = true
	payload := filepath.Join(w.Directory, "payload")
	file, err := os.OpenFile(payload, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	const payloadBytes = 128 << 20
	chunk := bytes.Repeat([]byte("original-backup-data\n"), 4096)
	for written := 0; written < payloadBytes; {
		n := min(len(chunk), payloadBytes-written)
		if _, err = file.Write(chunk[:n]); err != nil {
			file.Close()
			t.Fatal(err)
		}
		written += n
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	point, err = s.Commit(context.Background(), w.ID, point)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Acquire(point.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	digest := func() [32]byte {
		f, err := lease.Root.Open("payload")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			t.Fatal(err)
		}
		return [32]byte(h.Sum(nil))
	}
	before := digest()
	capacity, err := StorageCapacity(base)
	if err != nil || !capacity.BytesKnown || capacity.Bytes <= payloadBytes {
		t.Fatal("insufficient room to establish isolated test", capacity, err)
	}
	filler, err := os.OpenFile(filepath.Join(base, "capacity-reservation"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer filler.Close()
	if err = syscall.Fallocate(int(filler.Fd()), 0, 0, int64(capacity.Bytes-(64<<20))); err != nil {
		t.Fatal("reserve space on isolated filesystem", err)
	}
	if err = filler.Sync(); err != nil {
		t.Fatal("sync capacity reservation", err)
	}
	capacity, err = StorageCapacity(base)
	if err != nil || !capacity.BytesKnown || capacity.Bytes >= payloadBytes || capacity.Bytes <= 20<<20 {
		t.Fatal("test did not establish the clone-only capacity window", capacity, err)
	}
	t.Logf("filesystem=0x%x source_bytes=%d free_bytes=%d", stat.Type, payloadBytes, capacity.Bytes)
	for _, mode := range []string{"clone", "copy", "single-file-fallback"} {
		work, err := s.NewWork("capacity-" + mode)
		if err != nil {
			t.Fatal(err)
		}
		clone := cloneFile
		if mode == "single-file-fallback" {
			clone = func(dst, src *os.File) error {
				if filepath.Base(src.Name()) == "payload" {
					return syscall.ENOTSUP
				}
				return cloneFile(dst, src)
			}
		}
		report, copyErr := copyTreeWith(context.Background(), lease.Directory, work.Directory, CopyOptions{PreferClone: mode != "copy"}, StorageCapacity, clone)
		if mode == "clone" {
			if copyErr != nil || report.Mode != "clone" || report.ClonedFiles != report.Files || report.Files != 2 {
				t.Fatal("real clone rejected despite sufficient metadata space", report, copyErr)
			}
			if err = os.WriteFile(filepath.Join(work.Directory, "payload"), []byte("changed work copy"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(copyErr, ErrCapacity) {
			t.Fatalf("%s ignored the full-copy budget: report=%+v error=%v", mode, report, copyErr)
		}
		if err = s.AbortWork(context.Background(), work.ID); err != nil {
			t.Fatal(err)
		}
		catalog, err := s.ReadSnapshot()
		if err != nil || len(catalog.Points) != 1 || catalog.Points[point.ID] != point || digest() != before {
			t.Fatal("low-space work changed the saved point", mode, err)
		}
		t.Logf("mode=%s cloned=%d capacity_rejected=%t saved_point_preserved=true", mode, report.ClonedFiles, errors.Is(copyErr, ErrCapacity))
	}
}
