package app

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
)

type backupIndexOnlineCapture struct {
	source   backupIndexSource
	epoch    uint64
	rows     chan []string
	done     chan struct{}
	cancel   context.CancelFunc
	queryErr error // close(rows) 将最终查询结果同步给 worker。
}

// captureOnlineBackupIndex 仅捕获用户已经发起的查询。共享索引槽忙时直接沿用原查询。
// 捕获 worker 可独立取消；写入抢占或缓存失败不会中止原查询或把成功查询变成失败。
func (app *application) captureOnlineBackupIndex(ctx context.Context, udid, root string, query func(func([]string) error) (int, error), onRow func([]string) error, cacheValid ...func() bool) (int, error) {
	src, sourceErr := app.resolveBackupIndexSource(ctx, udid)
	// Key 始终来自逻辑配置；root 是调用方已占用的实际来源，二者在历史模式下不同。
	actual, actualErr := resolveBackupIndexSourceAt(ctx, udid, root)
	if sourceErr != nil || actualErr != nil || src.Identity != actual.Identity || src.Fingerprint != actual.Fingerprint {
		return query(onRow)
	}
	manager := app.backupIndexManager()
	manager.mu.Lock()
	if manager.closed || manager.writes[udid] != nil {
		manager.mu.Unlock()
		return query(onRow)
	}
	select {
	case manager.slot <- struct{}{}:
	default:
		manager.mu.Unlock()
		return query(onRow)
	}
	captureCtx, cancel := context.WithCancel(app.rootCtx)
	stopRequestCancel := context.AfterFunc(ctx, cancel)
	capture := &backupIndexOnlineCapture{source: src, epoch: manager.epochs[udid], rows: make(chan []string, 1), done: make(chan struct{}), cancel: cancel}
	if manager.captures == nil {
		manager.captures = map[string]*backupIndexOnlineCapture{}
	}
	manager.captures[udid] = capture
	manager.workers.Add(1)
	manager.mu.Unlock()
	go func() { defer stopRequestCancel(); manager.runOnlineCapture(captureCtx, capture) }()
	total, queryErr := query(func(fields []string) error {
		if len(fields) > 1024 || backupIndexEncodedRecordBytes(fields) > manager.store.limits.RecordBytes {
			cancel()
		} else {
			select {
			case capture.rows <- append([]string(nil), fields...):
			case <-capture.done:
			case <-captureCtx.Done():
			}
		}
		return onRow(fields)
	})
	capture.queryErr = queryErr
	if len(cacheValid) > 0 && !cacheValid[0]() {
		capture.queryErr = errBackupIndexCorrupt
	}
	close(capture.rows)
	<-capture.done
	return total, queryErr
}
func (m *backupIndexCoordinator) runOnlineCapture(ctx context.Context, c *backupIndexOnlineCapture) {
	defer m.workers.Done()
	defer c.cancel()
	defer func() { <-m.slot }()
	defer func() {
		m.mu.Lock()
		if m.captures[c.source.UDID] == c {
			delete(m.captures, c.source.UDID)
		}
		close(c.done)
		m.signalUnsafe()
		m.mu.Unlock()
	}()
	candidate, cacheErr := m.store.prepare(ctx, c.source, "online-v1", func(emit func([]string) error) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case fields, ok := <-c.rows:
				if !ok {
					return c.queryErr
				}
				if err := emit(fields); err != nil {
					return err
				}
			}
		}
	})
	if candidate != nil {
		defer candidate.discard()
	}
	if cacheErr != nil {
		m.app.addWarnLog(c.source.UDID, "event=backup_index.capture_failed reason="+backupIndexErrorCode(cacheErr))
		return
	}
	overview, overviewErr := readBackupIndexOverview(ctx, c.source, nil)
	fingerprint, err := backupIndexSourceFingerprint(ctx, c.source)
	if err != nil || fingerprint != c.source.Fingerprint || ctx.Err() != nil {
		return
	}
	if overviewErr != nil {
		overview.Status = "unavailable"
		overview.Freshness = "unknown"
		overview.MessageCode = backupIndexErrorCode(overviewErr)
	}
	m.mu.Lock()
	m.app.mu.RLock()
	writing := m.app.backupInProgress[c.source.UDID] || m.app.checkInProgress[c.source.UDID]
	m.app.mu.RUnlock()
	if m.closed || m.epochs[c.source.UDID] != c.epoch || m.writes[c.source.UDID] != nil || writing || !m.configMatchesUnsafe(c.source) {
		m.mu.Unlock()
		return
	}
	result, publishErr := m.store.publish(c.source, overview, candidate, nil)
	if publishErr == nil {
		fresh := backupIndexFresh{fingerprint: c.source.Fingerprint, listAt: result.FileList.ReadAt}
		if overviewErr == nil {
			fresh.overviewAt = result.Overview.ReadAt
		}
		m.fresh[c.source.Key] = fresh
		m.states[c.source.Key] = result.Refresh
		m.setBriefUnsafe(c.source, result.Revision, result.Refresh, result.Overview.Status, result.FileList.Status)
	}
	m.mu.Unlock()
	if publishErr != nil {
		m.app.addWarnLog(c.source.UDID, "event=backup_index.capture_failed reason="+backupIndexErrorCode(errors.Join(publishErr, overviewErr)))
	} else {
		m.app.addInfoLog(c.source.UDID, "event=backup_index.capture_completed")
		m.app.broadcastStatus()
	}
}

// 原在线过滤器会裁剪、截断和去除进度。逐行审计原始字节；不能证明无损时只放弃缓存。
// 不改变旧查询响应，也不将多行/宽松引号解析的结果称为完整离线清单。
type backupListCacheAudit struct {
	safe bool
	line []byte
}

func (a *backupListCacheAudit) Write(data []byte) (int, error) {
	for _, b := range data {
		if !a.safe {
			break
		}
		if b == '\n' || b == '\r' {
			a.checkLine()
			continue
		}
		if len(a.line) >= maxBackupLogLineBytes {
			a.safe = false
			a.line = nil
			break
		}
		a.line = append(a.line, b)
	}
	return len(data), nil
}
func (a *backupListCacheAudit) checkLine() {
	raw := string(a.line)
	a.line = a.line[:0]
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return
	}
	plain := ansiRE.ReplaceAllString(clean, "")
	if !strings.Contains(plain, ",") && isProgressLine(plain) {
		return
	}
	if strings.HasSuffix(clean, backupLogTruncatedMarker) || clean != raw || clean != plain || strings.ContainsRune(raw, '\b') {
		a.safe = false
		return
	}
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	fields, err := reader.Read()
	if err != nil || len(fields) < 2 {
		a.safe = false
		return
	}
	if _, err = reader.Read(); err != io.EOF {
		a.safe = false
	}
}
func (a *backupListCacheAudit) complete() bool {
	if a.safe && len(a.line) > 0 {
		a.checkLine()
	}
	return a.safe
}
