package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBackupConfigStoreConcurrentUpdates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	store := newBackupConfigStore(path, []string{root})

	const count = 100
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := validBackupConfig(root)
			cfg.UDID = fmt.Sprintf("DEVICE-%03d", i)
			errCh <- store.Put(cfg)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	configs, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != count {
		t.Fatalf("并发更新后应保留 %d 条配置，得到 %d", count, len(configs))
	}

	var envelope backupConfigEnvelope
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("磁盘文件必须始终是有效 JSON: %v", err)
	}
	if envelope.SchemaVersion != backupConfigSchemaVersion || len(envelope.Configs) != count {
		t.Fatalf("无效 envelope: %+v", envelope)
	}
}

func TestBackupConfigStoreMigratesLegacyMap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	cfg := validBackupConfig(root)
	legacy, _ := json.Marshal(map[string]backupConfig{cfg.UDID: cfg})
	if err := os.WriteFile(path, legacy, 0644); err != nil {
		t.Fatal(err)
	}

	store := newBackupConfigStore(path, []string{root})
	configs, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("应迁移一条旧配置，得到 %d", len(configs))
	}
	var envelope backupConfigEnvelope
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.SchemaVersion != backupConfigSchemaVersion {
		t.Fatalf("旧格式应立即迁移成 schema envelope: %v, %+v", err, envelope)
	}
}

func TestBackupConfigStoreLoadsConfigWithoutRemovedAtAsActive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	cfg := validBackupConfig(root)
	data, err := json.Marshal(backupConfigEnvelope{
		SchemaVersion: backupConfigSchemaVersion,
		Configs:       map[string]backupConfig{cfg.UDID: cfg},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	store := newBackupConfigStore(path, []string{root})
	configs, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if configs[cfg.UDID].RemovedAt != nil {
		t.Fatal("不含 removed_at 的旧配置必须保持活动状态")
	}
}

func TestBackupConfigStoreRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	data := []byte(`{"schema_version":999,"configs":{}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := newBackupConfigStore(path, []string{t.TempDir()})
	if _, err := store.Load(); err == nil {
		t.Fatal("必须拒绝未知的更高 schema_version")
	}
}

func TestBackupConfigStoreFallsBackToLastGood(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "backup_configs.json")
	store := newBackupConfigStore(path, []string{root})
	cfg := validBackupConfig(root)
	if err := store.Put(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}

	reloaded := newBackupConfigStore(path, []string{root})
	configs, err := reloaded.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := configs[cfg.UDID]; !ok {
		t.Fatal("主文件损坏时应读取 last-good 配置")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"broken"` {
		t.Fatal("回退读取不得覆盖损坏主文件，便于诊断")
	}
}
