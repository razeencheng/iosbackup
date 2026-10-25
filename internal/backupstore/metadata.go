package backupstore

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// SetPinned is a durable compare-and-swap; pinning does not change check state.
func (s *Store) SetPinned(ctx context.Context, id string, pinned bool, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return err
	}
	if revision != s.catalog.Revision {
		return ErrConflict
	}
	p, ok := s.catalog.Points[id]
	if !ok {
		return os.ErrNotExist
	}
	next := p
	next.Pinned = pinned
	return s.updatePoint(ctx, p, next)
}

// Freeze is restartable. A caller must durably enter a disabling state before
// calling it, and may enable a fresh epoch only after every store is frozen.
func (s *Store) Freeze(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return err
	}
	ids := make([]string, 0, len(s.catalog.Points))
	for id := range s.catalog.Points {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := s.catalog.Points[id]
		next := p
		next.Manual = true
		if err := s.updatePoint(ctx, p, next); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) updatePoint(ctx context.Context, old, next Point) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if old == next {
		return nil
	}
	if s.catalog.Revision == math.MaxUint64 {
		return ErrCapacity
	}
	if err := s.verifyRecycleSource(old.ID, old, true); err != nil {
		return s.uncertainError(err)
	}
	tx := transaction{Schema: 1, ID: old.ID, StoreID: s.identity.ID, JobID: old.JobID, Phase: "metadata", Point: &next, Previous: &old, Revision: s.catalog.Revision}
	if err := s.stage("metadata-intent", func() error { return s.saveTransaction(tx) }); err != nil {
		return s.uncertainError(err)
	}
	if err := s.finishMetadata(tx); err != nil {
		return s.uncertainError(err)
	}
	return nil
}
func (s *Store) finishMetadata(tx transaction) error {
	old, next := *tx.Previous, *tx.Point
	p, ok := s.catalog.Points[tx.ID]
	if !ok {
		return ErrUncertain
	}
	before := p == old && s.catalog.Revision == tx.Revision
	after := p == next && s.catalog.Revision == tx.Revision+1
	if !before && !after {
		return ErrUncertain
	}
	var desc Point
	if err := s.readJSON(filepath.Join("descriptors", tx.ID+".json"), &desc); err != nil {
		return err
	}
	if desc != old && desc != next {
		return ErrUncertain
	}
	if err := s.checkObjectShape(filepath.Join("objects", tx.ID)); err != nil {
		return err
	}
	if err := s.stage("metadata-descriptor", func() error { return s.writeJSON(filepath.Join("descriptors", tx.ID+".json"), next) }); err != nil {
		return err
	}
	if before {
		c := s.catalog
		c.Points = clonePoints(c.Points)
		c.Points[tx.ID] = next
		c.Revision++
		if err := s.stage("metadata-catalog", func() error { return s.writeJSON("catalog.json", c) }); err != nil {
			return err
		}
		s.catalog = c
	}
	if err := s.stage("metadata-cleanup", func() error { return s.root.Remove(filepath.Join("transactions", tx.ID+".json")) }); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.syncDirs("transactions")
}
