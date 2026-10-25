package backupstore

import (
	"context"
	"path/filepath"
)

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
