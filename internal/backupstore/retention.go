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

// PlanRetention 按完成时间选取最近 N 份普通正常备份，不受日历日期影响。
func PlanRetention(c Catalog, r RetentionRequest, leases map[string]int) (RetentionPlan, error) {
	plan := RetentionPlan{Revision: c.Revision, Delete: []string{}, Retain: map[string]string{}}
	policy, err := r.Policy.Normalize()
	if err != nil || r.Now.IsZero() {
		return plan, ErrInvalid
	}
	if policy.Enabled && (!ValidID(r.EpochID) || !ValidID(r.GenerationID)) {
		return plan, ErrInvalid
	}
	var normal []Point
	var pending Point
	for _, p := range c.Points {
		if p.Manual || p.EpochID != r.EpochID || p.Generation.ID != r.GenerationID {
			continue
		}
		switch p.State {
		case "normal":
			if !p.Pinned {
				normal = append(normal, p)
			}
		case "pending":
			if pending.ID == "" || pointAfter(p, pending) {
				pending = p
			}
		default:
			return plan, ErrInvalid
		}
	}
	sort.Slice(normal, func(i, j int) bool { return pointAfter(normal[i], normal[j]) })
	newest := map[string]bool{}
	for i, p := range normal {
		if i >= policy.Count {
			break
		}
		newest[p.ID] = true
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
			if pending.ID == id {
				reason = "latest_pending"
			}
		case p.State == "normal":
			if newest[id] {
				reason = "recent"
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
