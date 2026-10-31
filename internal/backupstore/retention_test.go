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
func TestRetentionNaturalDays(t *testing.T) {
	for _, days := range []int{0, 1, 3, 7, 30} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			c, r := retentionFixture()
			r.Policy.Days = days
			for i := 0; i < 35; i++ {
				retentionPoint(&c, r, i, i, "UTC")
			}
			p, err := PlanRetention(c, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := days
			if want == 0 {
				want = 3
			}
			if len(p.Delete) != 35-want {
				t.Fatalf("deleted %d want %d", len(p.Delete), 35-want)
			}
		})
	}
}

func TestRetentionMidnightDSTUsesCalendarDates(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	c, r := retentionFixture()
	r.Now = time.Date(2018, 11, 6, 12, 0, 0, 0, loc)
	r.Policy.Days = 3
	for i := 0; i < 5; i++ {
		retentionPoint(&c, r, i, i, loc.String())
	}
	plan, err := PlanRetention(c, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Retain) != 3 || len(plan.Delete) != 2 {
		t.Fatalf("midnight DST changed natural-day retention: %+v", plan)
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
func TestRetentionSameDayAndCalendarBoundaries(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Asia/Shanghai", "Pacific/Kiritimati"} {
		t.Run(zone, func(t *testing.T) {
			c, r := retentionFixture()
			r.Now = time.Date(2026, 3, 9, 4, 30, 0, 0, time.UTC)
			id := retentionPoint(&c, r, 1, 2, zone)
			// Calendar subtraction, not 72 elapsed hours, across DST and midnight.
			plan, err := PlanRetention(c, r, nil)
			if err != nil || len(plan.Delete) != 0 {
				t.Fatalf("calendar point lost %s %v %+v", id, err, plan)
			}
			newer := retentionPoint(&c, r, 2, 2, zone)
			p := c.Points[newer]
			p.CompletedAt = p.CompletedAt.Add(time.Minute)
			c.Points[newer] = p
			plan, _ = PlanRetention(c, r, nil)
			if len(plan.Delete) != 1 || plan.Delete[0] != id {
				t.Fatalf("same day %+v", plan)
			}
			p = c.Points[id]
			p.Pinned = true
			c.Points[id] = p
			plan, _ = PlanRetention(c, r, nil)
			if len(plan.Delete) != 0 {
				t.Fatal("pinned daily point lost")
			}
		})
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
	req := RetentionRequest{Policy: Policy{Enabled: true}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now()}
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
	req := RetentionRequest{Policy: Policy{Enabled: true}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now()}
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
