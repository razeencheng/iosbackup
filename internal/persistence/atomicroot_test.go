//go:build go1.25

package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicRootStaysWithOpenedDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "root")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := path + "-moved"
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = WriteFileAtomicRoot(root, "catalog.json", []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(moved, "catalog.json")); err != nil || string(data) != "safe" {
		t.Fatal("did not use opened root", err)
	}
	if _, err = os.Stat(filepath.Join(path, "catalog.json")); !os.IsNotExist(err) {
		t.Fatal("wrote replaced path")
	}
	if err = WriteFileAtomicRoot(root, "../escape", []byte("bad"), 0600); err == nil {
		t.Fatal("escaped root")
	}
}
