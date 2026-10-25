package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type historyLocation struct {
	ID   string `json:"id"`
	UDID string `json:"udid"`
	Path string `json:"path"`
}
type historyLocations struct {
	Schema int               `json:"schema"`
	Stores []historyLocation `json:"stores"`
}
type backupStorage struct {
	mu          sync.Mutex
	registry    string
	locations   historyLocations
	stores      map[string]*backupstore.Store
	unavailable map[string]error
	slot        chan struct{}
	write       func(string, []byte, os.FileMode) error
}

func newBackupStorage(registry string) *backupStorage {
	return &backupStorage{registry: registry, slot: make(chan struct{}, 1), stores: map[string]*backupstore.Store{}, unavailable: map[string]error{}, locations: historyLocations{Schema: 1}}
}
func (m *backupStorage) load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := persistence.ReadSecretFile(m.registry, 8<<20, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var locations historyLocations
	if err = decodeStrictJSON(data, &locations); err != nil {
		return err
	}
	if locations.Schema != 1 {
		return backupstore.ErrInvalid
	}
	ids := map[string]bool{}
	paths := map[string]bool{}
	for _, location := range locations.Stores {
		if !backupstore.ValidID(location.ID) || !udidPattern.MatchString(location.UDID) || !filepath.IsAbs(location.Path) || filepath.Clean(location.Path) != location.Path || filepath.Base(location.Path) != location.UDID || filepath.Base(filepath.Dir(location.Path)) != "v1" || filepath.Base(filepath.Dir(filepath.Dir(location.Path))) != ".iosbackup-history" || ids[location.ID] || paths[location.Path] {
			return backupstore.ErrInvalid
		}
		ids[location.ID] = true
		paths[location.Path] = true
	}
	m.locations = locations
	for _, location := range locations.Stores {
		if err = ctx.Err(); err != nil {
			return err
		}
		store, err := backupstore.Open(location.Path, location.UDID, location.ID)
		if err == nil {
			_, err = store.Recover(ctx)
		}
		if err != nil {
			if store != nil {
				_ = store.Close()
			}
			m.unavailable[location.ID] = err
			continue
		}
		m.stores[location.ID] = store
	}
	return nil
}
func (m *backupStorage) save() error {
	data, err := json.Marshal(m.locations)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return backupstore.ErrCapacity
	}
	write := m.write
	if write == nil {
		write = persistence.WriteFileAtomic
	}
	return write(m.registry, data, 0600)
}
func (m *backupStorage) create(ctx context.Context, backupRoot, udid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !udidPattern.MatchString(udid) || !filepath.IsAbs(backupRoot) {
		return "", backupstore.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(backupRoot)
	if err != nil {
		return "", err
	}
	path := filepath.Join(canonical, ".iosbackup-history", "v1", udid)
	for _, location := range m.locations.Stores {
		if location.Path == path {
			if m.unavailable[location.ID] != nil {
				return "", m.unavailable[location.ID]
			}
			if m.stores[location.ID] == nil {
				return "", backupstore.ErrUncertain
			}
			return location.ID, nil
		}
	}
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return "", backupstore.ErrUncertain
	}
	id, err := backupstore.NewID()
	if err != nil {
		return "", err
	}
	// 先登记身份。即使 rename 后返回错误，也不创建或自动重新认领存储。
	m.locations.Stores = append(m.locations.Stores, historyLocation{ID: id, UDID: udid, Path: path})
	if err = m.save(); err != nil {
		m.unavailable[id] = err
		return "", err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		m.unavailable[id] = err
		return "", err
	}
	defer root.Close()
	for _, dir := range []string{".iosbackup-history", filepath.Join(".iosbackup-history", "v1")} {
		if err = root.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			m.unavailable[id] = err
			return "", err
		}
		info, e := root.Lstat(dir)
		if e != nil || !info.IsDir() {
			m.unavailable[id] = backupstore.ErrUnsafe
			return "", backupstore.ErrUnsafe
		}
		f, e := root.Open(dir)
		if e != nil {
			m.unavailable[id] = e
			return "", e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			m.unavailable[id] = e
			return "", e
		}
	}
	// 刷盘新建管理目录的父入口，避免重启只留下配置登记而目录未持久化。
	for _, dir := range []string{".", ".iosbackup-history"} {
		f, e := root.Open(dir)
		if e != nil {
			return "", e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return "", e
		}
	}
	store, err := backupstore.Create(path, udid, id, time.Now().UTC())
	if err != nil {
		m.unavailable[id] = err
		return "", err
	}
	m.stores[id] = store
	return id, nil
}
func (m *backupStorage) prepare(ctx context.Context, id, job, baseline string, options backupstore.CopyOptions) (backupstore.Work, backupstore.CopyReport, error) {
	var report backupstore.CopyReport
	// 实例共用一个大 I/O 槽；队列等待期间仍可取消。
	select {
	case <-ctx.Done():
		return backupstore.Work{}, report, ctx.Err()
	case m.slot <- struct{}{}:
	}
	defer func() { <-m.slot }()
	if err := ctx.Err(); err != nil {
		return backupstore.Work{}, report, err
	}
	m.mu.Lock()
	store, err := m.stores[id], m.unavailable[id]
	m.mu.Unlock()
	if err != nil {
		return backupstore.Work{}, report, err
	}
	if store == nil {
		return backupstore.Work{}, report, backupstore.ErrInvalid
	}
	var source *backupstore.Lease
	if baseline != "" {
		catalog, err := store.Snapshot()
		if err != nil {
			return backupstore.Work{}, report, err
		}
		if catalog.Points[baseline].State != "normal" {
			return backupstore.Work{}, report, backupstore.ErrInvalid
		}
		source, err = store.Acquire(baseline)
		if err != nil {
			return backupstore.Work{}, report, err
		}
		defer source.Close()
	}
	work, err := store.NewWork(job)
	if err != nil {
		return work, report, err
	}
	if source == nil {
		err = os.Mkdir(work.Directory, 0700)
	} else {
		report, err = backupstore.CopyTree(ctx, source.Directory, work.Directory, options)
	}
	if err != nil {
		cleanupErr := store.AbortWork(context.Background(), work.ID)
		return work, report, errors.Join(err, cleanupErr)
	}
	return work, report, nil
}
func (m *backupStorage) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result error
	for _, store := range m.stores {
		result = errors.Join(result, store.Close())
	}
	return result
}

func (app *application) recoverBackupStorage(ctx context.Context) error {
	storage := newBackupStorage(filepath.Join(app.paths.ConfigsRoot, "history-stores.json"))
	if err := storage.load(ctx); err != nil {
		_ = storage.close()
		return err
	}
	generations := app.generationRegistry()
	if err := generations.recoverExisting(); err != nil {
		_ = storage.close()
		return err
	}
	app.mu.Lock()
	app.backupStorage = storage
	app.mu.Unlock()
	for id, err := range storage.unavailable {
		app.addErrorLog("SYSTEM", fmt.Sprintf("event=backup.history_store_unavailable store_id=%s error=%v", id, err))
	}
	return nil
}
