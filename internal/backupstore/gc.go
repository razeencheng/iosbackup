package backupstore

import "context"

func (s *Store) PlanGC(r RetentionRequest) (RetentionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return RetentionPlan{}, err
	}
	return PlanRetention(s.catalog, r, s.leases)
}

// PlanPinChange 在同一目录版本及读租约下预览固定状态变更，不写入目录。
func (s *Store) PlanPinChange(r RetentionRequest, id string, pinned bool, revision uint64) (RetentionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return RetentionPlan{}, err
	}
	if s.catalog.Revision != revision {
		return RetentionPlan{}, ErrConflict
	}
	p, ok := s.catalog.Points[id]
	if !ok {
		return RetentionPlan{}, ErrConflict
	}
	c := s.snapshotUnsafe()
	p.Pinned = pinned
	c.Points[id] = p
	return PlanRetention(c, r, s.leases)
}

// GC rechecks the revision and all read leases while holding the catalog lock.
// Completed IDs are returned even on an error; errors never imply freed space.
func (s *Store) GC(ctx context.Context, r RetentionRequest, revision uint64) ([]string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var done []string
	if err := s.checkReady(); err != nil {
		return done, err
	}
	if s.catalog.Revision != revision {
		return done, ErrConflict
	}
	plan, err := PlanRetention(s.catalog, r, s.leases)
	if err != nil {
		return done, err
	}
	for _, id := range plan.Delete {
		if s.leases[id] > 0 {
			return done, ErrBusy
		}
	}
	for _, id := range plan.Delete {
		// 前一个点的慢删除期间，新的读者可能已取得后续点的租约。
		// 保留该点，由调用者根据实际删除数量报告延后清理。
		if s.leases[id] > 0 {
			continue
		}
		if err = ctx.Err(); err != nil {
			return done, err
		}
		if err = s.recycleLocked(ctx, id, s.catalog.Revision); err != nil {
			return done, err
		}
		done = append(done, id)
	}
	return done, nil
}
