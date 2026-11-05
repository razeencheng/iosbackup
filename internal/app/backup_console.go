package app

import (
	"context"
	"errors"
	"os"
	"time"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/i18n"
)

// 控制台只公开来源身份，不公开磁盘目录或凭据引用。
type historyCurrentSourceView struct {
	Kind      string `json:"kind"`
	PointID   string `json:"point_id,omitempty"`
	StoreID   string `json:"store_id,omitempty"`
	Available bool   `json:"available"`
	ErrorCode string `json:"error_code,omitempty"`
}

type historyStorageView struct {
	State          string    `json:"state"`
	AvailableBytes *uint64   `json:"available_bytes,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	CopyStrategy   string    `json:"copy_strategy"`
	ErrorCode      string    `json:"error_code,omitempty"`
}

func (app *application) historyCurrentSource(ctx context.Context, udid string, cfg *backupConfig) historyCurrentSourceView {
	root := app.paths.BackupsRoot
	if cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	app.mu.RLock()
	blocked := app.deviceRemovalBlockedUnsafe(udid)
	app.mu.RUnlock()
	if blocked != nil {
		return historyCurrentSourceView{Kind: "missing", ErrorCode: i18n.DeviceRemovedOperation}
	}
	actualRoot, release, source, err := app.acquireHistorySource(udid, root)
	if err == nil {
		defer release()
		_, err = resolveBackupIndexSourceAt(ctx, udid, actualRoot)
	}
	switch {
	case err == nil:
		source.Available = true
	case errors.Is(err, errBackupIndexMissing), err == os.ErrNotExist:
		// 引用还在但文件不可读取时保留点身份；普通目录不存在才表示没有当前集。
		if source.Kind != "point" {
			source.Kind = "missing"
		}
		source.ErrorCode = i18n.BackupIndexMissing
	case errors.Is(err, backupstore.ErrBusy):
		source.ErrorCode = i18n.Busy
	default:
		source.ErrorCode = i18n.HistoryUnavailable
	}
	return source
}

func (app *application) historyStorageStatus(cfg *backupConfig) historyStorageView {
	root := app.paths.BackupsRoot
	if cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	info, err := os.Stat(root)
	if err == nil && !info.IsDir() {
		err = backupstore.ErrUnsafe
	}
	var capacity backupstore.Capacity
	if err == nil {
		capacity, err = backupstore.StorageCapacity(root)
	}
	return historyCapacityView(capacity, err, app.nowTime())
}

func historyCapacityView(capacity backupstore.Capacity, err error, at time.Time) historyStorageView {
	// 只调用文件系统容量查询，不遍历内容，也不在读取请求中创建克隆探测文件。
	view := historyStorageView{State: "unavailable", CheckedAt: at, CopyStrategy: "unknown", ErrorCode: i18n.HistoryUnavailable}
	if err == nil && capacity.BytesKnown {
		view.State, view.AvailableBytes, view.ErrorCode = "available", &capacity.Bytes, ""
	}
	return view
}

// 仅兼容没有关联协议版本的旧摘要；新任务的空关联不能被迁移导入点覆盖。
// 旧关联以 JobID 为依据，并排除导入时间未知或早于任务开始的数据。查询不修改旧记录。
func (app *application) linkHistoricalJobs(udid string, summaries []backupJobSummary) {
	wanted := map[string]int{}
	for index, summary := range summaries {
		succeeded := summary.ExecutionState == "succeeded" || summary.ExecutionState == "" && summary.State == backupStateSucceeded
		if summary.PointLinkVersion == 0 && summary.PointID == "" && summary.Operation != "check" && succeeded {
			wanted[summary.ID] = index
		}
	}
	if len(wanted) == 0 {
		return
	}
	links := map[string]string{}
	ambiguous := map[string]bool{}
	for _, location := range app.historyLocations(udid) {
		store, err := app.backupStorage.store(location.ID)
		if err != nil {
			// 部分存储不可读时无法排除一次任务产生多个点，旧关联保持未知。
			return
		}
		catalog, err := store.ReadSnapshot()
		if err != nil {
			// 部分存储不可读时无法排除一次任务产生多个点，旧关联保持未知。
			return
		}
		for _, point := range catalog.Points {
			index, ok := wanted[point.JobID]
			if !ok || point.CompletionUnknown || point.CompletedAt.Before(summaries[index].StartedAt) {
				continue
			}
			if id, exists := links[point.JobID]; exists && id != point.ID {
				ambiguous[point.JobID] = true
			}
			links[point.JobID] = point.ID
		}
	}
	for jobID, pointID := range links {
		if ambiguous[jobID] {
			continue
		}
		index := wanted[jobID]
		summaries[index].PointID = pointID
		summaries[index].HistoryCommitted = true
	}
}

func (app *application) setHistoryJobPhase(job *backupJob, phase, pointID string) {
	if app.backupJobRecords == nil {
		return
	}
	store := app.backupJobRecords
	store.mu.Lock()
	if record := store.records[job.id]; record != nil && !store.closed {
		record.Summary.HistoryPhase = phase
		if pointID != "" {
			record.Summary.PointID = pointID
			record.Summary.HistoryCommitted = true
		}
		store.changedUnsafe(record)
	}
	store.mu.Unlock()
	store.signal()
}
