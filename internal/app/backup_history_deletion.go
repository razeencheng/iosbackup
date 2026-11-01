package app

import (
	"context"
	"iosbackup/internal/backupstore"
)

type historyPointDeletion struct {
	StoreID       string                 `json:"store_id"`
	PointID       string                 `json:"point_id"`
	Revision      uint64                 `json:"revision"`
	Generation    backupstore.Generation `json:"generation"`
	AllowFallback bool                   `json:"allow_fallback"`
}

// 应用意图先于存储删除持久化。重启会完成同一删除，再选择仍可读取的同代来源。
// 只在启动或设备写入互斥下调用；普通当前备份的删除从不经过此路径。
func (m *backupStorage) finishHistoryPointDeletion(ctx context.Context, udid string, st historyDeviceState) (historyDeviceState, error) {
	intent := st.PointDeletion
	store, err := m.store(intent.StoreID)
	if err != nil {
		return st, err
	}
	c, err := store.Snapshot()
	if err != nil {
		return st, err
	}
	if _, ok := c.Points[intent.PointID]; ok {
		if err = store.Delete(ctx, intent.PointID, intent.Revision); err != nil {
			return st, err
		}
	}
	stores, err := m.deviceStores(udid)
	if err != nil {
		return st, err
	}
	var selected backupstore.Point
	if intent.AllowFallback {
		for _, s := range stores {
			c, err := s.Snapshot()
			if err != nil {
				return st, err
			}
			for _, p := range c.Points {
				if p.State != "normal" || p.EpochID != st.EpochID || p.Generation != intent.Generation {
					continue
				}
				if selected.ID != "" && (p.CommittedAt.Before(selected.CommittedAt) || p.CommittedAt.Equal(selected.CommittedAt) && p.ID <= selected.ID) {
					continue
				}
				if historyBaselineMetadata(ctx, s, p, intent.Generation) {
					selected = p
				}
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return st, err
	}
	st.LatestStore, st.LatestPoint = selected.StoreID, selected.ID
	st.CurrentDeleted, st.CurrentReset = selected.ID == "", false
	st.PointDeletion = nil
	return st, m.saveDevice(udid, st)
}
