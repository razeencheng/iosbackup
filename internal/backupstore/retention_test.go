package backupstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func retentionFixture() (Catalog, RetentionRequest) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	c := Catalog{Revision: 1, Points: map[string]Point{}, Baselines: map[string]string{}}
	r := RetentionRequest{Policy: Policy{Enabled: true}, EpochID: fmt.Sprintf("%032x", 1), GenerationID: fmt.Sprintf("%032x", 2), Now: now}
	return c, r
}
func retentionPoint(c *Catalog, r RetentionRequest, n, ago int, zone string) string {
	id := fmt.Sprintf("%032x", n+10)
	loc, _ := time.LoadLocation(zone)
	at := r.Now.In(loc).AddDate(0, 0, -ago)
	c.Points[id] = Point{ID: id, EpochID: r.EpochID, Generation: Generation{ID: r.GenerationID}, CompletedAt: at, CommittedAt: at, Day: at.In(loc).Format("2006-01-02"), Timezone: zone, State: "normal"}
	return id
}
func TestRetentionLatestCount(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3, 7, 30, 365} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			c, r := retentionFixture()
			r.Policy.Count = count
			for i := 0; i < 35; i++ {
				retentionPoint(&c, r, i, i*30, "UTC")
			}
			plan, err := PlanRetention(c, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := count
			if want == 0 {
				want = 2
			}
			if want > 35 {
				want = 35
			}
			if len(plan.Retain) != want {
				t.Fatalf("retained %d, want %d: %+v", len(plan.Retain), want, plan)
			}
			r.Now = r.Now.AddDate(1, 0, 0)
			later, err := PlanRetention(c, r, nil)
			if err != nil || len(later.Retain) != want {
				t.Fatalf("elapsed time lost history: %+v %v", later, err)
			}
		})
	}
}
func TestRetentionCountsSameDayBackups(t *testing.T) {
	c, r := retentionFixture()
	r.Policy.Count = 3
	ids := []string{}
	for i := 0; i < 4; i++ {
		id := retentionPoint(&c, r, i, 0, "UTC")
		p := c.Points[id]
		p.CompletedAt = p.CompletedAt.Add(time.Duration(i) * time.Minute)
		p.CommittedAt = p.CompletedAt
		c.Points[id] = p
		ids = append(ids, id)
	}
	c.Baselines[r.GenerationID] = ids[3]
	plan, err := PlanRetention(c, r, nil)
	if err != nil || len(plan.Retain) != 3 || len(plan.Delete) != 1 || plan.Delete[0] != ids[0] {
		t.Fatalf("same-day backups must occupy separate slots including current: %+v %v", plan, err)
	}
	// 固定点退出普通名额；取消固定后重新参与排序。
	pinned := c.Points[ids[3]]
	pinned.Pinned = true
	c.Points[pinned.ID] = pinned
	plan, err = PlanRetention(c, r, nil)
	if err != nil || len(plan.Retain) != 4 {
		t.Fatalf("pin consumed ordinary slot: %+v %v", plan, err)
	}
	pinned.Pinned = false
	c.Points[pinned.ID] = pinned
	plan, err = PlanRetention(c, r, nil)
	if err != nil || len(plan.Delete) != 1 || plan.Delete[0] != ids[0] {
		t.Fatalf("unpin plan: %+v %v", plan, err)
	}
}
func TestRetentionPendingAndProtectedExtrasDoNotDisplaceNormal(t *testing.T) {
	c, r := retentionFixture()
	r.Policy.Count = 2
	normal := []string{retentionPoint(&c, r, 1, 10, "UTC"), retentionPoint(&c, r, 2, 20, "UTC")}
	for i := 3; i <= 6; i++ {
		id := retentionPoint(&c, r, i, 0, "UTC")
		p := c.Points[id]
		switch i {
		case 3:
			p.State = "pending"
		case 4:
			p.Pinned = true
		case 5:
			p.Manual = true
		case 6:
			p.Generation.ID = "old"
		}
		c.Points[id] = p
	}
	plan, err := PlanRetention(c, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range normal {
		if plan.Retain[id] == "" {
			t.Fatalf("extra result displaced healthy point: %+v", plan)
		}
	}
}
func TestRetentionStableTieBreak(t *testing.T) {
	c, r := retentionFixture()
	r.Policy.Count = 1
	older := retentionPoint(&c, r, 1, 0, "UTC")
	newer := retentionPoint(&c, r, 2, 0, "UTC")
	plan, err := PlanRetention(c, r, nil)
	if err != nil || len(plan.Delete) != 1 || plan.Delete[0] != older {
		t.Fatalf("ID tie-break: %+v %v", plan, err)
	}
	p := c.Points[older]
	p.CommittedAt = p.CommittedAt.Add(time.Second)
	c.Points[older] = p
	plan, err = PlanRetention(c, r, nil)
	if err != nil || len(plan.Delete) != 1 || plan.Delete[0] != newer {
		t.Fatalf("commit tie-break: %+v %v", plan, err)
	}
}
func TestRetentionProtectionAndPending(t *testing.T) {
	c, r := retentionFixture()
	keep := map[string]string{}
	pin := retentionPoint(&c, r, 1, 20, "UTC")
	p := c.Points[pin]
	p.Pinned = true
	c.Points[pin] = p
	keep[pin] = "pinned"
	manual := retentionPoint(&c, r, 2, 20, "UTC")
	p = c.Points[manual]
	p.Manual = true
	c.Points[manual] = p
	keep[manual] = "manual"
	old := retentionPoint(&c, r, 3, 20, "UTC")
	p = c.Points[old]
	p.Generation.ID = "old"
	c.Points[old] = p
	keep[old] = "old_generation"
	epoch := retentionPoint(&c, r, 4, 20, "UTC")
	p = c.Points[epoch]
	p.EpochID = "old"
	c.Points[epoch] = p
	keep[epoch] = "previous_epoch"
	base := retentionPoint(&c, r, 5, 20, "UTC")
	c.Baselines[r.GenerationID] = base
	keep[base] = "baseline"
	read := retentionPoint(&c, r, 6, 20, "UTC")
	keep[read] = "in_use"
	older := retentionPoint(&c, r, 7, 0, "UTC")
	p = c.Points[older]
	p.State = "pending"
	p.CommittedAt = p.CommittedAt.Add(-time.Minute)
	c.Points[older] = p
	newer := retentionPoint(&c, r, 8, 0, "UTC")
	p = c.Points[newer]
	p.State = "pending"
	c.Points[newer] = p
	keep[newer] = "latest_pending"
	plan, err := PlanRetention(c, r, map[string]int{read: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0] != older {
		t.Fatalf("unexpected deletes %+v", plan)
	}
	for id, reason := range keep {
		if plan.Retain[id] != reason {
			t.Errorf("%s reason %q want %q", id, plan.Retain[id], reason)
		}
	}
	r.Policy.Enabled = false
	plan, _ = PlanRetention(c, r, nil)
	if len(plan.Delete) != 0 {
		t.Fatal("disabled cleanup")
	}
}
func TestStoreGCRevisionLeaseAndPin(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	p.CompletedAt = p.CompletedAt.AddDate(0, 0, -10)
	p.Day = p.CompletedAt.Format("2006-01-02")
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, next := preparePoint(t, s)
	next.Generation = p.Generation
	next.EpochID = p.EpochID
	if _, err = s.Commit(context.Background(), w.ID, next); err != nil {
		t.Fatal(err)
	}
	req := RetentionRequest{Policy: Policy{Enabled: true, Count: 1}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now()}
	plan, err := s.PlanGC(req)
	if err != nil || len(plan.Delete) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}
	lease, err := s.Acquire(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.GC(context.Background(), req, plan.Revision); err != nil || len(done) != 0 {
		t.Fatalf("leased point was not skipped: %v %v", done, err)
	}
	lease.Close()
	if err = s.SetPinned(context.Background(), old.ID, true, plan.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GC(context.Background(), req, plan.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale plan accepted", err)
	}
	c, _ := s.Snapshot()
	if err = s.SetPinned(context.Background(), old.ID, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	same, _ := s.Snapshot()
	if same.Revision != c.Revision {
		t.Fatal("idempotent pin changed revision")
	}
	if err = s.SetPinned(context.Background(), old.ID, false, c.Revision); err != nil {
		t.Fatal(err)
	}
	plan, _ = s.PlanGC(req)
	done, err := s.GC(context.Background(), req, plan.Revision)
	if err != nil || len(done) != 1 {
		t.Fatalf("gc %v %v", done, err)
	}
}
func TestStoreGCMetadataRecovery(t *testing.T) {
	for _, stage := range []string{"metadata-intent.after", "metadata-descriptor.after", "metadata-catalog.after", "metadata-cleanup.before"} {
		t.Run(stage, func(t *testing.T) {
			s := storeFixture(t)
			w, p := preparePoint(t, s)
			point, err := s.Commit(context.Background(), w.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := s.Snapshot()
			s.step = func(at string) error {
				if at == stage {
					return errors.New("fault")
				}
				return nil
			}
			if err = s.SetPinned(context.Background(), point.ID, true, c.Revision); !errors.Is(err, ErrUncertain) {
				t.Fatalf("fault %v", err)
			}
			s.step = nil
			if _, err = s.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			c, _ = s.Snapshot()
			if !c.Points[point.ID].Pinned {
				t.Fatal("pin lost")
			}
			if err = s.Freeze(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			c, _ = s.Snapshot()
			if !c.Points[point.ID].Manual {
				t.Fatal("manual retention lost")
			}
		})
	}
}

func TestStoreGCPinCannotRacePastCommittedDeletion(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	p.CompletedAt = p.CompletedAt.AddDate(0, 0, -10)
	p.Day = p.CompletedAt.Format("2006-01-02")
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, next := preparePoint(t, s)
	next.Generation = p.Generation
	next.EpochID = p.EpochID
	if _, err = s.Commit(context.Background(), w.ID, next); err != nil {
		t.Fatal(err)
	}
	req := RetentionRequest{Policy: Policy{Enabled: true, Count: 1}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now()}
	plan, _ := s.PlanGC(req)
	entered, proceed := make(chan struct{}), make(chan struct{})
	s.step = func(stage string) error {
		if stage == "recycle-intent.before" {
			close(entered)
			<-proceed
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.GC(context.Background(), req, plan.Revision); done <- err }()
	<-entered
	pinStarted, pinDone := make(chan struct{}), make(chan error, 1)
	go func() { close(pinStarted); pinDone <- s.SetPinned(context.Background(), old.ID, true, plan.Revision) }()
	<-pinStarted
	close(proceed)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = <-pinDone; !errors.Is(err, ErrConflict) {
		t.Fatal("late pin falsely reported success", err)
	}
}
func TestStoreGCPendingPinDoesNotChangeCheck(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	p.State = "pending"
	p.Check.State = "unavailable"
	saved, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Snapshot()
	if err = s.SetPinned(context.Background(), saved.ID, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Snapshot()
	point := c.Points[saved.ID]
	if point.State != "pending" || point.Check.State != "unavailable" || len(c.Baselines) != 0 {
		t.Fatal("pin promoted pending result")
	}
}
