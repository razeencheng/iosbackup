package app

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 以下集成测试使用真实工具及合成 fixture，不代表真实 iOS 备份样本验证。
// 默认测试不依赖工具；显式提供 IOSBK_TEST_SQLITE3/IOSBK_TEST_PLISTUTIL 后，工具缺失必须失败。
func TestBackupIndexSQLiteToolIntegration(t *testing.T) {
	binary := os.Getenv("IOSBK_TEST_SQLITE3")
	if binary == "" {
		t.Skip("set IOSBK_TEST_SQLITE3 to explicitly validate the installed sqlite3")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("IOSBK_TEST_SQLITE3 must be an absolute executable path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := filepath.Join(t.TempDir(), "Manifest.db")
	query := `CREATE TABLE Files(fileID TEXT,domain TEXT,relativePath TEXT,flags INTEGER); INSERT INTO Files VALUES('id','HomeDomain','a,"b"'||char(13)||char(10)||'end',99);`
	cmd := newExecCmd(ctx, binary, db, query)
	if err := cmd.Run(); err != nil {
		t.Fatalf("explicit sqlite3 fixture generation failed: %v", err)
	}
	called := 0
	err := streamBackupManifestRowsCommand(ctx, binary, db, func(row backupManifestRow) error {
		called++
		if row.RelativePath != "a,\"b\"\r\nend" || row.Flags != 99 {
			t.Fatalf("real sqlite CSV must preserve filename bytes: %+v", row)
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("real sqlite stream count=%d: %v", called, err)
	}
	// 已打开描述符必须继续读取原文件，即使来源文件名随后被替换。
	file, err := os.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(db, db+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), db); err != nil {
		t.Fatal(err)
	}
	called = 0
	err = streamBackupManifestFileRows(ctx, binary, file, func(row backupManifestRow) error { called++; return nil })
	if err != nil || called != 1 {
		t.Fatalf("descriptor reopened replaced path: %d %v", called, err)
	}
	badDB := filepath.Join(t.TempDir(), "Corrupt.db")
	if err := os.WriteFile(badDB, []byte("SQLite format 3\x00synthetic corrupt body"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := streamBackupManifestRowsCommand(ctx, binary, badDB, func(backupManifestRow) error { return nil }); err == nil {
		t.Fatal("actual sqlite must reject corrupt database")
	}
}

func TestBackupIndexPlistToolIntegration(t *testing.T) {
	binary := os.Getenv("IOSBK_TEST_PLISTUTIL")
	if binary == "" {
		t.Skip("set IOSBK_TEST_PLISTUTIL to explicitly validate the installed plistutil")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("IOSBK_TEST_PLISTUTIL must be an absolute executable path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fixture := `<?xml version="1.0"?><plist version="1.0"><dict><key>IsEncrypted</key><false/><key>Device Name</key><string>合成 fixture</string></dict></plist>`
	cmd := newExecCmd(ctx, binary, "-f", "bin", "-i", "-", "-o", "-")
	cmd.cmd.Stdin = strings.NewReader(fixture)
	binaryData, err := cmd.Output()
	if err != nil {
		t.Fatalf("explicit plistutil binary fixture generation failed: %v", err)
	}
	if !bytes.HasPrefix(binaryData, []byte("bplist00")) {
		t.Fatal("plistutil did not produce a binary plist")
	}
	values, err := parseBackupPlist(ctx, binaryData, func(ctx context.Context, data []byte) ([]byte, error) {
		return convertBackupBinaryPlistCommand(ctx, binary, data)
	})
	if err != nil || values["IsEncrypted"] != false || values["Device Name"] != "合成 fixture" {
		t.Fatalf("actual binary plist conversion: %v %v", values, err)
	}
	if _, err := convertBackupBinaryPlistCommand(ctx, binary, []byte("bplist00invalid")); err == nil {
		t.Fatal("actual plistutil must reject corrupt binary")
	}
}

func TestBackupIndexSQLiteRejectsCommittedWAL(t *testing.T) {
	binary := os.Getenv("IOSBK_TEST_SQLITE3")
	if binary == "" {
		t.Skip("set IOSBK_TEST_SQLITE3 for actual WAL fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := filepath.Join(t.TempDir(), "Manifest.db")
	cmd := newExecCmd(ctx, binary, db)
	input, err := cmd.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); _ = cmd.Wait() }()
	_, err = input.Write([]byte("CREATE TABLE Files(fileID TEXT,domain TEXT,relativePath TEXT,flags INTEGER);\nPRAGMA journal_mode=WAL;\nINSERT INTO Files VALUES('id','domain','wal-only-row',1);\nSELECT 'READY';\n"))
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(output)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(line) == "READY" {
			break
		}
	}
	check := newExecCmd(ctx, binary, "-readonly", db, "SELECT count(*) FROM Files;")
	data, err := check.Output()
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("WAL fixture not committed: %q %v", data, err)
	}
	called := false
	err = streamBackupManifestRowsCommand(ctx, binary, db, func(backupManifestRow) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("WAL must be rejected before emitting a misleading full list: called=%v err=%v", called, err)
	}
}
