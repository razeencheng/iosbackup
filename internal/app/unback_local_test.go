package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnbackExportsManifestLocallyWhileDeviceOffline(t *testing.T) {
	app, backupPath := newLocalUnbackTestApp(t, "U-LOCAL")
	fileID := "0123456789abcdef0123456789abcdef01234567"
	source := filepath.Join(backupPath, fileID[:2], fileID)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("photo-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.manifestRows = func(ctx context.Context, dbPath string, onRow func(backupManifestRow) error) error {
		if dbPath != filepath.Join(backupPath, "Manifest.db") {
			t.Fatalf("Manifest 路径错误: %s", dbPath)
		}
		for _, row := range []backupManifestRow{
			{FileID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Domain: "CameraRollDomain", RelativePath: "Media/DCIM", Flags: 2},
			{FileID: fileID, Domain: "CameraRollDomain", RelativePath: "Media/DCIM/IMG_0001.HEIC", Flags: 1},
		} {
			if err := onRow(row); err != nil {
				return err
			}
		}
		return nil
	}

	if err := app.Unback(context.Background(), "U-LOCAL"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(backupPath, "_unback_", "CameraRollDomain", "Media", "DCIM", "IMG_0001.HEIC")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "photo-data" {
		t.Fatalf("解包内容错误: %q", data)
	}
}

func TestUnbackRejectsManifestPathTraversal(t *testing.T) {
	app, backupPath := newLocalUnbackTestApp(t, "U-SAFE")
	app.manifestRows = func(_ context.Context, _ string, onRow func(backupManifestRow) error) error {
		return onRow(backupManifestRow{
			FileID:       "0123456789abcdef0123456789abcdef01234567",
			Domain:       "../../escape",
			RelativePath: "owned",
			Flags:        2,
		})
	}
	if err := app.Unback(context.Background(), "U-SAFE"); err == nil {
		t.Fatal("路径穿越必须失败")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(backupPath), "escape")); !os.IsNotExist(err) {
		t.Fatalf("不得在目标目录外创建文件: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupPath, "_unback_")); !os.IsNotExist(err) {
		t.Fatalf("失败不得发布半成品: %v", err)
	}
}

func TestSafeUnbackTargetAllowsValidUnixControlBytesInFilename(t *testing.T) {
	root := t.TempDir()
	relative := "Documents/request_cache/GetAlbumList_\n\t125599147\x12"
	target, err := safeUnbackTarget(root, "AppDomain-com.tencent.mqq", relative)
	if err != nil {
		t.Fatalf("iOS 备份中的合法 Unix 文件名不应被误判为路径穿越: %v", err)
	}
	want := filepath.Join(root, "AppDomain-com.tencent.mqq", filepath.FromSlash(relative))
	if target != want {
		t.Fatalf("target=%q, want %q", target, want)
	}
}

func TestUnbackFailureRemovesStagingAndPreservesPublishedView(t *testing.T) {
	app, backupPath := newLocalUnbackTestApp(t, "U-ATOMIC")
	app.manifestRows = func(_ context.Context, _ string, onRow func(backupManifestRow) error) error {
		return onRow(backupManifestRow{
			FileID:       "0123456789abcdef0123456789abcdef01234567",
			Domain:       "HomeDomain",
			RelativePath: "missing.db",
			Flags:        1,
		})
	}
	if err := app.Unback(context.Background(), "U-ATOMIC"); err == nil {
		t.Fatal("源对象缺失必须失败")
	}
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".iosbackup-unback-") || entry.Name() == "_unback_" {
			t.Fatalf("失败后残留解包目录 %s", entry.Name())
		}
	}
}

func TestUnbackRejectsEncryptedOrInvalidManifestDatabase(t *testing.T) {
	app, backupPath := newLocalUnbackTestApp(t, "U-ENC")
	if err := os.WriteFile(filepath.Join(backupPath, "Manifest.db"), []byte("encrypted manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error {
		called = true
		return nil
	}
	if err := app.Unback(context.Background(), "U-ENC"); err == nil || !strings.Contains(err.Error(), "未加密") {
		t.Fatalf("应明确拒绝加密/非 SQLite Manifest，得 %v", err)
	}
	if called {
		t.Fatal("非 SQLite Manifest 不应调用 sqlite3")
	}
}

func newLocalUnbackTestApp(t *testing.T, udid string) (*application, string) {
	t.Helper()
	root := t.TempDir()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = filepath.Join(root, "configs")
	cfg.BackupsRoot = filepath.Join(root, "backups")
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices[udid] = &device{UDID: udid, Name: "offline", IsOnline: false}
	app.configs[udid] = &backupConfig{UDID: udid, BackupDirectory: cfg.BackupsRoot}
	backupPath := filepath.Join(cfg.BackupsRoot, udid)
	if err := os.MkdirAll(backupPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "Manifest.db"), append([]byte("SQLite format 3\x00"), make([]byte, 32)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "Info.plist"), []byte("test backup marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	return app, backupPath
}
