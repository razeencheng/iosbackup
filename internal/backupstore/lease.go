package backupstore

import (
	"os"
	"path/filepath"
	"sync"
)

type Lease struct {
	Root      *os.Root
	Directory string
	once      sync.Once
	release   func()
	err       error
}

func (s *Store) Acquire(id string) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	point, ok := s.catalog.Points[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	var descriptor Point
	if err := s.readJSON(filepath.Join("descriptors", id+".json"), &descriptor); err != nil {
		return nil, err
	}
	if descriptor != point {
		return nil, ErrUncertain
	}
	if err := s.checkObjectShape(filepath.Join("objects", id)); err != nil {
		return nil, err
	}
	path := filepath.Join("objects", id, s.identity.UDID)
	for _, name := range []string{filepath.Join("objects", id), path} {
		info, err := s.root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, ErrUnsafe
		}
	}
	root, err := s.root.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	s.leases[id]++
	return &Lease{Root: root, Directory: filepath.Join(s.path, path), release: func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.leases[id]--
		if s.leases[id] == 0 {
			delete(s.leases, id)
		}
	}}, nil
}
func (l *Lease) Close() error {
	l.once.Do(func() { l.err = l.Root.Close(); l.release() })
	return l.err
}
