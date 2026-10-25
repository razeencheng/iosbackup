package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupStorageRegistrationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "configs", "history-stores.json")
	backups := filepath.Join(dir, "backups")
	if err := os.Mkdir(backups, 0700); err != nil {
		t.Fatal(err)
	}
	m := newBackupStorage(registry)
	id, err := m.create(context.Background(), backups, "PHONE")
	if err != nil {
		t.Fatal(err)
	}
	work, _, err := m.prepare(context.Background(), id, "job-1", "", backupstore.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work.Directory, "Info.plist"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	gen, _ := backupstore.NewID()
	epoch, _ := backupstore.NewID()
	now := time.Now().UTC()
	point, err := m.stores[id].Commit(context.Background(), work.ID, backupstore.Point{Generation: backupstore.Generation{ID: gen, Kind: "unencrypted", CreatedAt: now}, EpochID: epoch, CompletedAt: now, Day: now.Format("2006-01-02"), Timezone: "UTC", State: "normal", Check: backupstore.Check{State: "passed", Scope: "quick", CheckedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	next, report, err := m.prepare(context.Background(), id, "job-2", point.ID, backupstore.CopyOptions{})
	if err != nil || report.Files != 1 {
		t.Fatal(report, err)
	}
	if err = os.WriteFile(filepath.Join(next.Directory, "Info.plist"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := m.stores[id].Acquire(point.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := lease.Root.Open("Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	_, err = f.Read(buf)
	f.Close()
	lease.Close()
	if err != nil || string(buf) != "source" {
		t.Fatal("copy changed history")
	}
	if err = m.close(); err != nil {
		t.Fatal(err)
	}
	restarted := newBackupStorage(registry)
	defer restarted.close()
	if err = restarted.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarted.stores[id] == nil || restarted.unavailable[id] != nil {
		t.Fatal("registered store not recovered")
	}
	// 模拟挂载消失；不能在同名新位置重建空历史。
	if err = restarted.close(); err != nil {
		t.Fatal(err)
	}
	path := m.locations.Stores[0].Path
	if err = os.Rename(path, path+"-offline"); err != nil {
		t.Fatal(err)
	}
	missing := newBackupStorage(registry)
	defer missing.close()
	if err = missing.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if missing.unavailable[id] == nil {
		t.Fatal("missing storage treated as empty")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing store recreated")
	}
}
func TestBackupStorageRegisterBeforeCreate(t *testing.T) {
	dir := t.TempDir()
	m := newBackupStorage(filepath.Join(dir, "registry.json"))
	m.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := m.create(context.Background(), dir, "PHONE"); err == nil {
		t.Fatal("registration failure ignored")
	}
	if _, err := os.Stat(filepath.Join(dir, ".iosbackup-history")); !os.IsNotExist(err) {
		t.Fatal("created storage before registration")
	}
}
func TestBackupStorageQueueCanCancel(t *testing.T) {
	m := newBackupStorage(filepath.Join(t.TempDir(), "registry.json"))
	m.slot <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := m.prepare(ctx, "store", "job", "", backupstore.CopyOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-m.slot
}
