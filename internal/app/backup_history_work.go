package app

import (
	"context"
	"iosbackup/internal/backupstore"
	"os"
	"path/filepath"
	"time"
)

// 只在设备写入互斥内，或启动时执行。事务恢复必须先完成。
func (m *backupStorage) reclaimHistoryWork(ctx context.Context, udid string) error {
	st, err := m.deviceState(udid)
	if err != nil || st.EpochID == "" {
		return err
	}
	stores, err := m.deviceStores(udid)
	if err != nil {
		return err
	}
	if st.MigrationDone && (st.Phase == "active" || st.Phase == "enabling") && st.WritableID != "" {
		if st.RetiredWork != "" {
			return backupstore.ErrUncertain
		}
		st.RetiredStore, st.RetiredWork = st.WritableStore, st.WritableID
		st.WritableStore, st.WritableID = "", ""
		st.WritableGeneration, st.WritableCompletedAt = backupstore.Generation{}, time.Time{}
		// 已提交导入点的登记与释放旧引用先落盘，之后才允许删除源。
		if err = m.saveDevice(udid, st); err != nil {
			return err
		}
	}
	if st.RetiredWork != "" {
		if st.RetiredStore == st.WritableStore && st.RetiredWork == st.WritableID {
			return backupstore.ErrUncertain
		}
		s, err := m.store(st.RetiredStore)
		if err != nil {
			return err
		}
		if err = s.AbortWork(ctx, st.RetiredWork); err != nil {
			return err
		}
		st.RetiredStore, st.RetiredWork = "", ""
		if err = m.saveDevice(udid, st); err != nil {
			return err
		}
	}
	for _, s := range stores {
		catalog, err := s.ReadSnapshot()
		if err != nil {
			return err
		}
		ids, err := s.UncommittedWork()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if catalog.StoreID == st.WritableStore && id == st.WritableID {
				continue
			}
			if err = s.AbortWork(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

type historyWorkWriterKey struct{}

// 把工作目录锁交给外部写工具。父进程异常退出也不能让恢复清理越过子进程。
func (app *application) historyWorkWriterContext(ctx context.Context, udid, directory string) (context.Context, func(), error) {
	app.mu.RLock()
	m := app.backupStorage
	app.mu.RUnlock()
	if m == nil {
		return ctx, func() {}, nil
	}
	m.mu.Lock()
	locations := append([]historyLocation(nil), m.locations.Stores...)
	m.mu.Unlock()
	for _, loc := range locations {
		if loc.UDID != udid || filepath.Dir(directory) != filepath.Join(loc.Path, "work") {
			continue
		}
		s, err := m.store(loc.ID)
		if err != nil {
			return ctx, nil, err
		}
		writer, err := s.HoldWorkWriter(filepath.Base(directory))
		if err != nil {
			return ctx, nil, err
		}
		return context.WithValue(ctx, historyWorkWriterKey{}, writer), func() { _ = writer.Close() }, nil
	}
	return ctx, func() {}, nil
}

func historyWorkWriter(ctx context.Context) *os.File {
	writer, _ := ctx.Value(historyWorkWriterKey{}).(*os.File)
	return writer
}
