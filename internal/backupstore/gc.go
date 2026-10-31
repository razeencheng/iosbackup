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

// GC rechecks the revision and all read leases while holding the catalog lock.
// Completed IDs are returned even on an error; errors never imply freed space.
func (s *Store) GC(ctx context.Context, r RetentionRequest, revision uint64) ([]string, error) {
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
