//go:build linux

package backupstore

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

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
