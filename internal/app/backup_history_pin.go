package app

import (
	"iosbackup/internal/backupstore"
	"slices"
	"time"
)

type historyPinPreview struct {
	UDID, PointID, EpochID, GenerationID string
	Policy                               backupstore.Policy
	Plan                                 backupstore.RetentionPlan
	Expires                              time.Time
}

// 取消固定只改变保留方式；预览列出按当前策略后续可清理的真实对象。
func (app *application) previewHistoryUnpin(s *backupstore.Store, p backupstore.Point, revision uint64) (historyPinPreview, error) {
	app.mu.RLock()
	cfg := cloneBackupConfig(app.configs[p.UDID])
	app.mu.RUnlock()
	st, err := app.backupStorage.deviceState(p.UDID)
	if err != nil {
		return historyPinPreview{}, err
	}
	policy := backupstore.Policy{Count: 2}
	if cfg != nil {
		policy = cfg.History
	}
	policy.Enabled = policy.Enabled && st.Phase == "active"
	generation, err := app.generationRegistry().load(p.UDID)
	if err != nil {
		return historyPinPreview{}, err
	}
	req := backupstore.RetentionRequest{Policy: policy, EpochID: st.EpochID, GenerationID: generation.Current, Now: app.nowTime()}
	plan, err := s.PlanPinChange(req, p.ID, false, revision)
	if err != nil {
		return historyPinPreview{}, err
	}
	return historyPinPreview{UDID: p.UDID, PointID: p.ID, EpochID: st.EpochID, GenerationID: generation.Current, Policy: policy, Plan: plan}, nil
}
func sameHistoryPinPreview(a, b historyPinPreview) bool {
	return a.UDID == b.UDID && a.PointID == b.PointID && a.EpochID == b.EpochID && a.GenerationID == b.GenerationID && a.Policy == b.Policy && a.Plan.Revision == b.Plan.Revision && slices.Equal(a.Plan.Delete, b.Plan.Delete)
}
