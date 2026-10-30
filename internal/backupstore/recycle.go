package backupstore

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
)

// Recycle 是带版本和占用保护的底层回收原语。保留策略在上层生成候选。
func (s *Store) Recycle(ctx context.Context, id string, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recycleLocked(ctx, id, revision)
}

// Delete requires explicit user confirmation in the application layer. Unlike
// automatic retention it may remove a pin or the final baseline, but never a reader.
func (s *Store) Delete(ctx context.Context, id string, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(ctx, id, revision, true, nil)
}
func (s *Store) recycleLocked(ctx context.Context, id string, revision uint64) error {
	return s.deleteLocked(ctx, id, revision, false, nil)
}
func (s *Store) deleteLocked(ctx context.Context, id string, revision uint64, explicit bool, beforeIntent func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := s.checkReady(); err != nil {
		return err
	}
	if revision != s.catalog.Revision {
		return ErrConflict
	}
	point, ok := s.catalog.Points[id]
	if !ok {
		return os.ErrNotExist
	}
	if s.leases[id] > 0 {
		return ErrBusy
	}
	if !explicit && (point.Pinned || point.Manual) {
		return ErrConflict
	}
	for _, baseline := range s.catalog.Baselines {
		if baseline == id && !explicit {
			return ErrConflict
		}
	}
	if err := s.verifyRecycleSource(id, point, true); err != nil {
		return s.uncertainError(err)
	}
	if beforeIntent != nil {
		if err := beforeIntent(); err != nil {
			return err
		}
	}
	tx := transaction{Schema: 1, ID: id, StoreID: s.identity.ID, JobID: point.JobID, Phase: "deleting", Point: &point, Revision: revision, ExplicitDelete: explicit}
	if err := s.stage("recycle-intent", func() error { return s.saveTransaction(tx) }); err != nil {
		return s.uncertainError(err)
	}
	if err := s.finishRecycle(ctx, tx); err != nil {
		return s.uncertainError(err)
	}
	return nil
}
func (s *Store) finishRecycle(ctx context.Context, tx transaction) error {
	id := tx.ID
	_, published := s.catalog.Points[id]
	if err := s.verifyRecycleSource(id, *tx.Point, published); err != nil {
		return err
	}
	if point, ok := s.catalog.Points[id]; ok {
		if point != *tx.Point || s.catalog.Revision != tx.Revision || (!tx.ExplicitDelete && (point.Pinned || point.Manual)) {
			return ErrConflict
		}
		for _, baseline := range s.catalog.Baselines {
			if baseline == id && !tx.ExplicitDelete {
				return ErrConflict
			}
		}
		if s.catalog.Revision == math.MaxUint64 {
			return ErrCapacity
		}
		next := s.catalog
		next.Points = clonePoints(next.Points)
		delete(next.Points, id)
		next.Baselines = cloneBaselines(next.Baselines)
		for gen, baseline := range next.Baselines {
			if baseline != id {
				continue
			}
			delete(next.Baselines, gen)
			var candidate Point
			for _, p := range next.Points {
				if p.Generation.ID == gen && p.State == "normal" && (candidate.ID == "" || p.CommittedAt.After(candidate.CommittedAt)) {
					candidate = p
				}
			}
			if candidate.ID != "" {
				next.Baselines[gen] = candidate.ID
			}
		}
		next.Revision++
		if err := s.stage("recycle-catalog", func() error { return s.writeJSON("catalog.json", next) }); err != nil {
			return err
		}
		s.catalog = next
	}
	if err := s.stage("recycle-data", func() error { return removeTree(ctx, s.root, filepath.Join("objects", id)) }); err != nil {
		return err
	}
	if err := s.stage("recycle-descriptor", func() error {
		err := s.root.Remove(filepath.Join("descriptors", id+".json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}); err != nil {
		return err
	}
	if err := s.syncDirs("objects", "descriptors"); err != nil {
		return err
	}
	if err := s.stage("recycle-cleanup", func() error { return s.root.Remove(filepath.Join("transactions", id+".json")) }); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.syncDirs("transactions")
}

// 描述缺失只可出现在目录和数据均已移除后的回收尾部；否则保留现场。
func (s *Store) verifyRecycleSource(id string, expected Point, published bool) error {
	var descriptor Point
	err := s.readJSON(filepath.Join("descriptors", id+".json"), &descriptor)
	if errors.Is(err, os.ErrNotExist) {
		_, objectErr := s.root.Lstat(filepath.Join("objects", id))
		if !published && errors.Is(objectErr, os.ErrNotExist) {
			return nil
		}
		return ErrUncertain
	}
	if err != nil || descriptor != expected {
		return ErrUncertain
	}
	if published {
		return s.checkObjectShape(filepath.Join("objects", id))
	}
	info, err := s.root.Lstat(filepath.Join("objects", id))
	if err == nil && !info.IsDir() {
		return ErrUncertain
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteWithIntent holds the same lock as reader admission while the application
// durably records its current-source intent. The callback must not call Store.
func (s *Store) DeleteWithIntent(ctx context.Context, id string, revision uint64, beforeIntent func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(ctx, id, revision, true, beforeIntent)
}
