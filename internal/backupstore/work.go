package backupstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// HoldWorkWriter 的目录锁可经 ExtraFiles 传给写入工具；应用退出后仍由
// 存活的子进程持有。只关闭文件，不显式解锁共享的 open file description。
func (s *Store) HoldWorkWriter(id string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	tx, err := s.readTransaction(id)
	if err != nil {
		return nil, err
	}
	if tx.Phase != "work" {
		return nil, ErrConflict
	}
	return s.lockWork(id, syscall.LOCK_SH)
}

func (s *Store) lockWork(id string, mode int) (*os.File, error) {
	f, err := s.root.OpenFile(filepath.Join("work", id), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		f.Close()
		return nil, ErrUnsafe
	}
	if err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.Join(ErrBusy, err)
	}
	return f, nil
}

// UncommittedWork 仅列出可识别的未发布事务。由应用层核对持久引用后回收。
func (s *Store) UncommittedWork() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	names, err := s.names("transactions")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, name := range names {
		if strings.HasPrefix(name, ".atomic-") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if name != id+".json" {
			return nil, ErrUncertain
		}
		tx, err := s.readTransaction(id)
		if err != nil {
			return nil, err
		}
		if tx.Phase == "work" || tx.Phase == "sealing" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// WorkLocation only returns an existing, owned, unpublished writable transaction.
func (s *Store) WorkLocation(id string) (Work, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return Work{}, err
	}
	tx, err := s.readTransaction(id)
	if err != nil {
		return Work{}, err
	}
	if tx.Phase != "work" {
		return Work{}, ErrConflict
	}
	if _, ok := s.catalog.Points[id]; ok {
		return Work{}, ErrConflict
	}
	parent := filepath.Join(s.path, "work", id)
	if err = s.checkObjectShape(filepath.Join("work", id)); err != nil {
		return Work{}, err
	}
	return Work{ID: id, Parent: parent, Directory: filepath.Join(parent, s.identity.UDID)}, nil
}
func (s *Store) SyncWork(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return err
	}
	tx, err := s.readTransaction(id)
	if err != nil {
		return err
	}
	if tx.Phase != "work" {
		return ErrConflict
	}
	return s.syncObject(ctx, filepath.Join("work", id))
}
