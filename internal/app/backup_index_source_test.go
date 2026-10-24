package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func indexSourceTestApp(t *testing.T) (*application, string) {
	t.Helper()
	app := newApplication()
	root := t.TempDir()
	app.paths.BackupsRoot = root
	app.configs["OFFLINE"] = &backupConfig{UDID: "OFFLINE"}
	dir := filepath.Join(root, "OFFLINE")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return app, dir
}

func TestBackupIndexSourceOfflineAndStableMissingKey(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	canonical, _ := filepath.EvalSymlinks(dir)
	if err != nil || src.Directory != canonical || src.Key == "" || src.Identity == "" {
		t.Fatalf("offline source: %+v %v", src, err)
	}
	if err := os.Rename(app.paths.BackupsRoot, app.paths.BackupsRoot+"-away"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(app.paths.BackupsRoot+"-away", app.paths.BackupsRoot)
	absent, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if !errors.Is(err, errBackupIndexUnavailable) || absent.Key != src.Key {
		t.Fatalf("mount absence must retain cache key: %+v %v", absent, err)
	}
}

func TestBackupIndexSourceRejectsInvalidUnknownRemoved(t *testing.T) {
	app, _ := indexSourceTestApp(t)
	for _, id := range []string{"../OFFLINE", "UNKNOWN"} {
		if _, err := app.resolveBackupIndexSource(context.Background(), id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	now := nowBeijing()
	app.configs["OFFLINE"].RemovedAt = &now
	if _, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE"); !errors.Is(err, errDeviceRemoved) {
		t.Fatalf("removed: %v", err)
	}
}

func TestBackupIndexSourceChangesAndMissingBackup(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	before, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	missing, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if !errors.Is(err, errBackupIndexMissing) || missing.Key != before.Key {
		t.Fatalf("missing: %+v %v", missing, err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	after, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	if after.Identity == before.Identity || after.Key != before.Key {
		t.Fatal("replacement must change identity but not cache key")
	}
	app.configs["OFFLINE"].BackupDirectory = t.TempDir()
	changed, _ := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if changed.Key == before.Key {
		t.Fatal("changed configured source must change key")
	}
}

func TestOriginalBackupSizeStrictExclusions(t *testing.T) {
	_, dir := indexSourceTestApp(t)
	for name, data := range map[string]string{"normal": "abc", ".hidden": "1234", "ordinary.tmp": "12", "_unback_/copy": "excluded", ".iosbackup-unback-123/tmp": "excluded", "nested/_unback_/real": "12345"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	size, err := originalBackupSize(context.Background(), dir)
	if err != nil || size != 14 {
		t.Fatalf("size=%d err=%v", size, err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := originalBackupSize(context.Background(), dir); !errors.Is(err, errBackupIndexUnsafeSource) {
		t.Fatalf("symlink: %v", err)
	}
}

func TestOriginalBackupSizeErrorsAndCancellation(t *testing.T) {
	_, dir := indexSourceTestApp(t)
	if _, err := originalBackupSize(context.Background(), filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing source must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := originalBackupSize(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestBackupIndexSourceRejectsSymlinkAndFingerprintsMetadata(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := backupIndexSourceFingerprint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := backupIndexSourceFingerprint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if fp == next {
		t.Fatal("metadata change must invalidate fingerprint")
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"-old", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE"); !errors.Is(err, errBackupIndexUnsafeSource) {
		t.Fatalf("symlink: %v", err)
	}
}

func TestBackupIndexSourceNilEntriesAreUnknown(t *testing.T) {
	app, _ := indexSourceTestApp(t)
	app.configs["NIL"] = nil
	app.devices["NIL"] = nil
	if _, err := app.resolveBackupIndexSource(context.Background(), "NIL"); !errors.Is(err, errDeviceNotFound) {
		t.Fatalf("nil map entries are not a known device: %v", err)
	}
}

func TestBackupIndexSourceRejectsMetadataSymlinkAndUnreadableWalk(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(dir, "Manifest.plist")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE"); !errors.Is(err, errBackupIndexUnsafeSource) {
		t.Fatalf("metadata symlink: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "Manifest.plist")); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(dir, "unreadable")
	if err := os.Mkdir(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadable, 0700)
	if os.Geteuid() != 0 {
		if _, err := originalBackupSize(context.Background(), dir); err == nil {
			t.Fatal("inaccessible subtree must fail, not publish partial size")
		}
	}
}

func TestOriginalBackupSizeOverflow(t *testing.T) {
	const maxInt64 = int64(1<<63 - 1)
	if _, err := checkedBackupIndexSize(maxInt64, 1); !errors.Is(err, errBackupIndexLimit) {
		t.Fatalf("size overflow must fail: %v", err)
	}
	if _, err := checkedBackupIndexSize(0, -1); !errors.Is(err, errBackupIndexLimit) {
		t.Fatalf("negative size must fail: %v", err)
	}
	if n, err := checkedBackupIndexSize(maxInt64-1, 1); err != nil || n != maxInt64 {
		t.Fatalf("valid boundary: %d %v", n, err)
	}
}

type backupIndexFaultFS struct {
	fs.FS
	failStat bool
	failure  error
}

func (f backupIndexFaultFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !f.failStat {
		return nil, f.failure
	}
	return fs.ReadDir(f.FS, name)
}

func (f backupIndexFaultFS) Stat(name string) (fs.FileInfo, error) {
	if name != "." && f.failStat {
		return nil, f.failure
	}
	return fs.Stat(f.FS, name)
}

func TestOriginalBackupSizePropagatesTraversalAndStatFailures(t *testing.T) {
	failure := errors.New("injected filesystem error")
	for _, statFailure := range []bool{false, true} {
		source := backupIndexFaultFS{FS: fstest.MapFS{"data": &fstest.MapFile{Data: []byte("original")}}, failStat: statFailure, failure: failure}
		if _, err := originalBackupSizeFS(context.Background(), source); !errors.Is(err, failure) {
			t.Fatalf("partial size published after stat=%v error: %v", statFailure, err)
		}
	}
}
