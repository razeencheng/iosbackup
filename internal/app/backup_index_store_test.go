package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func indexTestSource() backupIndexSource {
	return backupIndexSource{UDID: "INDEX-TEST", Key: strings.Repeat("a", 64), Identity: "identity", Fingerprint: "fingerprint"}
}
func indexTestOverview() backupIndexOverview {
	n := int64(128)
	encrypted := false
	return backupIndexOverview{Status: "available", Freshness: "current", ReadAt: nowBeijing(), SizeBytes: &n, Encrypted: &encrypted}
}
func indexTestRows(n int) backupIndexProducer {
	return func(emit func([]string) error) error {
		for i := 0; i < n; i++ {
			if err := emit([]string{"id", "HomeDomain", "Library/a,\"b\"\n.txt", "1"}); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestBackupIndexStorePublish(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	c, err := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.load(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate visible before publish: %v", err)
	}
	first, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.FileList.EntryCount == nil || *first.FileList.EntryCount != 3 || !first.FileList.CSVAvailable {
		t.Fatalf("bad snapshot: %+v", first)
	}
	_, err = s.prepare(context.Background(), src, "manifest-v1", func(emit func([]string) error) error { _ = emit([]string{"partial"}); return io.ErrUnexpectedEOF })
	if err == nil {
		t.Fatal("partial accepted")
	}
	second, err := s.load(src)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision {
		t.Fatal("failed preparation replaced old version")
	}
	f, err := s.openCSV(src, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Library/a,\"\"b\"\"\n.txt") {
		t.Fatalf("CSV quoting lost: %s", data)
	}
	c, err = s.prepare(context.Background(), src, "manifest-v1", indexTestRows(0))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if *empty.FileList.EntryCount != 0 || !empty.FileList.Complete {
		t.Fatal("empty valid list was unavailable")
	}
	if _, err = s.openCSV(src, first.Revision); err == nil {
		t.Fatal("obsolete revision accepted")
	}
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal("open prior download lost", err)
	}
}
func TestBackupIndexStoreBounded(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	c, err := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(5001))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FileList.Entries) != 5000 || *got.FileList.EntryCount != 5001 || !got.FileList.Truncated {
		t.Fatalf("preview incorrectly bounded: %d", len(got.FileList.Entries))
	}
	f, err := s.openCSV(src, got.Revision)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	f.Close()
	if st.Mode().Perm() != 0600 {
		t.Fatal("CSV is not private")
	}
	s.limits.CSVBytes = 32
	if _, err = s.prepare(context.Background(), src, "manifest-v1", indexTestRows(2)); err == nil {
		t.Fatal("CSV budget ignored")
	}
	current, _ := s.load(src)
	if current.Revision != got.Revision {
		t.Fatal("over-budget candidate replaced data")
	}
	s.limits = defaultBackupIndexLimits()
	s.limits.PreviewBytes = 12
	c, err = s.prepare(context.Background(), src, "manifest-v1", indexTestRows(2))
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FileList.Entries) != 0 || !got.FileList.Truncated {
		t.Fatal("preview byte budget ignored")
	}
}
func TestBackupIndexReloadAndPartialRefresh(t *testing.T) {
	root := t.TempDir()
	s := newBackupIndexStore(root)
	src := indexTestSource()
	c, _ := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(2))
	first, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newBackupIndexStore(root).load(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.FileList.Entries) != 2 || reloaded.FileList.Freshness != "unknown" {
		t.Fatalf("restart lost history: %+v", reloaded.FileList)
	}
	next, err := s.publish(src, indexTestOverview(), nil, io.ErrUnexpectedEOF)
	if err != nil {
		t.Fatal(err)
	}
	if !next.FileList.ReadAt.Equal(first.FileList.ReadAt) || next.FileList.Freshness != "stale" || len(next.FileList.Entries) != 2 {
		t.Fatal("failed refresh lost previous list or read time")
	}
	if err = s.invalidate(src, true); err != nil {
		t.Fatal(err)
	}
	deleted, err := newBackupIndexStore(root).load(src)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.FileList.CSVAvailable || deleted.Overview.Status != "missing" {
		t.Fatal("deleted backup still visible")
	}
}
func TestBackupIndexStorePointerFailure(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	c, _ := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(1))
	first, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.writeDescriptor = func(path string, data []byte) error { return io.ErrClosedPipe }
	c, _ = s.prepare(context.Background(), src, "manifest-v1", indexTestRows(2))
	if _, err = s.publish(src, indexTestOverview(), c, nil); err == nil {
		t.Fatal("write error ignored")
	}
	current, err := s.load(src)
	if err != nil || current.Revision != first.Revision {
		t.Fatal("failed pointer damaged current")
	}
	s.writeDescriptor = func(path string, data []byte) error {
		if err := writeFileAtomic(path, data, 0600); err != nil {
			return err
		}
		return io.ErrClosedPipe
	}
	c, _ = s.prepare(context.Background(), src, "manifest-v1", indexTestRows(4))
	published, err := s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal("post-rename ambiguity not resolved", err)
	}
	f, err := s.openCSV(src, published.Revision)
	if err != nil {
		t.Fatal("live object removed after ambiguous write", err)
	}
	f.Close()
}
func TestBackupIndexStoreRejectsUntrustedCache(t *testing.T) {
	root := t.TempDir()
	s := newBackupIndexStore(root)
	src := indexTestSource()
	if err := os.MkdirAll(filepath.Join(root, src.Key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, src.Key, "objects")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(1)); err == nil {
		t.Fatal("symlink cache accepted")
	}
}

func TestBackupIndexReloadPreservesManifestFilenameBytes(t *testing.T) {
	root := t.TempDir()
	s := newBackupIndexStore(root)
	src := indexTestSource()
	path := "comma,quote\"and\r\nline"
	c, err := s.prepare(context.Background(), src, "manifest-v1", func(emit func([]string) error) error { return emit([]string{"id", "HomeDomain", path, "1"}) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.publish(src, indexTestOverview(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := newBackupIndexStore(root).load(src)
	if err != nil {
		t.Fatal(err)
	}
	if got.FileList.Entries[0].RelativePath != path {
		t.Fatalf("reload changed filename bytes: %q", got.FileList.Entries[0].RelativePath)
	}
}
