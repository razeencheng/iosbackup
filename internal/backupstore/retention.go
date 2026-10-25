package backupstore

import (
	"sort"
	"time"
)

// RetentionRequest applies only to one active epoch and encryption generation.
// The caller must use the persisted policy; a shorter policy requires confirmation.
type RetentionRequest struct {
	Policy                Policy
	EpochID, GenerationID string
	Now                   time.Time
}
type RetentionPlan struct {
	Revision uint64            `json:"revision"`
	Delete   []string          `json:"delete"`
	Retain   map[string]string `json:"retain"`
}

// PlanRetention is pure. Each point keeps its original calendar and timezone.
func PlanRetention(c Catalog, r RetentionRequest, leases map[string]int) (RetentionPlan, error) {
	plan := RetentionPlan{Revision: c.Revision, Delete: []string{}, Retain: map[string]string{}}
	policy, err := r.Policy.Normalize()
	if err != nil || r.Now.IsZero() {
		return plan, ErrInvalid
	}
	if policy.Enabled && (!ValidID(r.EpochID) || !ValidID(r.GenerationID)) {
		return plan, ErrInvalid
	}
	latest := map[string]Point{}
	for _, p := range c.Points {
		if p.Manual || p.EpochID != r.EpochID || p.Generation.ID != r.GenerationID {
			continue
		}
		key := p.Timezone + "/" + p.Day
		if p.State == "pending" {
			key = "pending"
		}
		if old, ok := latest[key]; !ok || pointAfter(p, old) {
			latest[key] = p
		}
	}
	baselines := map[string]bool{}
	for _, id := range c.Baselines {
		baselines[id] = true
	}
	for id, p := range c.Points {
		reason := ""
		switch {
		case !policy.Enabled:
			reason = "disabled"
		case p.Pinned:
			reason = "pinned"
		case p.Manual:
			reason = "manual"
		case p.Generation.ID != r.GenerationID:
			reason = "old_generation"
		case p.EpochID != r.EpochID:
			reason = "previous_epoch"
		case baselines[id]:
			reason = "baseline"
		case leases[id] > 0:
			reason = "in_use"
		case p.State == "pending":
			if latest["pending"].ID == id {
				reason = "latest_pending"
			}
		case p.State == "normal":
			loc, e := time.LoadLocation(p.Timezone)
			if e != nil {
				return plan, ErrInvalid
			}
			if p.Day != p.CompletedAt.In(loc).Format("2006-01-02") {
				return plan, ErrInvalid
			}
			today := r.Now.In(loc)
			cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1-policy.Days).Format("2006-01-02")
			if p.Day >= cutoff && latest[p.Timezone+"/"+p.Day].ID == id {
				reason = "daily"
			}
		default:
			return plan, ErrInvalid
		}
		if reason != "" {
			plan.Retain[id] = reason
		} else {
			plan.Delete = append(plan.Delete, id)
		}
	}
	sort.Slice(plan.Delete, func(i, j int) bool { return pointAfter(c.Points[plan.Delete[j]], c.Points[plan.Delete[i]]) })
	return plan, nil
}
func pointAfter(a, b Point) bool {
	if !a.CompletedAt.Equal(b.CompletedAt) {
		return a.CompletedAt.After(b.CompletedAt)
	}
	if !a.CommittedAt.Equal(b.CommittedAt) {
		return a.CommittedAt.After(b.CommittedAt)
	}
	return a.ID > b.ID
}
