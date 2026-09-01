package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// checkBackupDir：空/相对路径失败；可写目录可用；被文件占用失败。
func TestCheckBackupDir(t *testing.T) {
	if ok, _ := checkBackupDir(""); ok {
		t.Error("空路径应失败")
	}
	if ok, _ := checkBackupDir("relative/path"); ok {
		t.Error("相对路径应失败")
	}
	dir := t.TempDir()
	if ok, reason := checkBackupDir(filepath.Join(dir, "sub")); !ok {
		t.Errorf("可创建/可写目录应可用，得 %q", reason)
	}
	f := filepath.Join(dir, "afile")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := checkBackupDir(f); ok {
		t.Error("被文件占用的路径应失败")
	}
}

// DeleteDeviceBackup：备份中拒绝；正常删除并重置 LastBackup；非法 UDID 拒绝。
func TestDeleteDeviceBackup(t *testing.T) {
	app := newApplication()
	udid := "DEL-UDID"
	base := t.TempDir()
	app.configs[udid] = &backupConfig{UDID: udid, BackupDirectory: base, LastBackup: time.Now()}
	app.devices[udid] = &device{UDID: udid, Name: "GG", LastBackup: time.Now()}
	bdir := filepath.Join(base, udid)
	if err := os.MkdirAll(filepath.Join(bdir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(bdir, "f"), []byte("x"), 0644)

	app.backupInProgress[udid] = true
	if err := app.DeleteDeviceBackup(udid); err == nil {
		t.Error("备份进行中应拒绝删除")
	}
	delete(app.backupInProgress, udid)

	if err := app.DeleteDeviceBackup(udid); err != nil {
		t.Fatalf("删除应成功: %v", err)
	}
	if _, err := os.Stat(bdir); !os.IsNotExist(err) {
		t.Error("备份目录应已被删除")
	}
	if !app.configs[udid].LastBackup.IsZero() {
		t.Error("删除后 LastBackup 应重置为零")
	}

	if err := app.DeleteDeviceBackup("../etc"); err == nil {
		t.Error("含路径穿越的 UDID 应拒绝")
	}
}
