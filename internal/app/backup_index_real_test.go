package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 显式指定只读样本才运行；不输出设备名、UDID、路径或清单内容。
func TestBackupIndexRealReadOnlySample(t *testing.T) {
	root, id := os.Getenv("IOSBK_TEST_BACKUP_ROOT"), os.Getenv("IOSBK_TEST_BACKUP_UDID")
	if root == "" || id == "" {
		t.Skip("set IOSBK_TEST_BACKUP_ROOT and IOSBK_TEST_BACKUP_UDID for read-only real sample")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	app := newApplication()
	app.paths.BackupsRoot = root
	app.configs[id] = &backupConfig{UDID: id}
	src, err := app.resolveBackupIndexSource(ctx, id)
	if err != nil {
		t.Fatal(backupIndexErrorCode(err))
	}
	converter := backupPlistConverter(nil)
	if tool := os.Getenv("IOSBK_TEST_PLISTUTIL"); tool != "" {
		converter = func(ctx context.Context, data []byte) ([]byte, error) {
			return convertBackupBinaryPlistCommand(ctx, tool, data)
		}
	}
	overview, err := readBackupIndexOverview(ctx, src, converter)
	if err != nil {
		t.Fatal(backupIndexErrorCode(err))
	}
	if overview.SizeBytes == nil || overview.Encrypted == nil {
		t.Fatal("overview incomplete")
	}
	if *overview.Encrypted {
		t.Skip("encrypted real sample: overview verified; local list intentionally unsupported")
	}
	tool := os.Getenv("IOSBK_TEST_SQLITE3")
	if tool == "" {
		tool = "/usr/local/bin/sqlite3"
	}
	streamer := func(ctx context.Context, path string, emit func(backupManifestRow) error) error {
		return streamBackupManifestRowsCommand(ctx, tool, path, emit)
	}
	expected, err := newExecCmd(ctx, tool, "file:"+filepath.Join(src.Directory, "Manifest.db")+"?mode=ro&immutable=1", "SELECT count(*) FROM Files;").Output()
	if err != nil {
		t.Fatal("independent count failed")
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(expected)))
	if err != nil {
		t.Fatal("invalid independent count")
	}
	store := newBackupIndexStore(filepath.Join(t.TempDir(), "index"))
	candidate, err := store.prepare(ctx, src, "manifest-v1", func(emit func([]string) error) error {
		_, err := streamLocalBackupIndex(ctx, src, overview.Encrypted, streamer, func(r backupIndexRecord) error {
			return emit([]string{r.FileID, r.Domain, r.RelativePath, strconv.Itoa(r.Flags)})
		})
		return err
	})
	if err != nil {
		t.Fatal(backupIndexErrorCode(err))
	}
	defer candidate.discard()
	snapshot, err := store.publish(src, overview, candidate, nil)
	if err != nil {
		t.Fatal(backupIndexErrorCode(err))
	}
	if snapshot.FileList.EntryCount == nil || *snapshot.FileList.EntryCount != int64(count) {
		t.Fatal("count mismatch")
	}
	reloaded := newBackupIndexStore(store.root)
	saved, err := reloaded.load(src)
	if err != nil || !saved.FileList.Complete || saved.FileList.EntryCount == nil || *saved.FileList.EntryCount != int64(count) {
		t.Fatal("persisted list mismatch")
	}
	download, err := reloaded.openCSV(src, saved.Revision)
	if err != nil {
		t.Fatal("download failed")
	}
	defer download.Close()
	bytes, err := io.Copy(io.Discard, download)
	if err != nil || bytes != saved.FileList.Bytes {
		t.Fatal("download length mismatch")
	}
	fingerprint, err := backupIndexSourceFingerprint(ctx, src)
	if err != nil || fingerprint != src.Fingerprint {
		t.Fatal("sample changed during verification")
	}
	t.Logf("read-only sample verified: original_bytes=%d entries=%d preview=%d csv_bytes=%d encrypted=false", *overview.SizeBytes, count, saved.FileList.PreviewCount, bytes)
}
