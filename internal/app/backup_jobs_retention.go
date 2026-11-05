package app

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// 损坏或孤立记录也有保留上限。最多使用总预算的四分之一，
// 并遵循相同的保留天数；不能因反复崩溃而无限积累诊断文件。
func (s *backupJobStore) pruneQuarantine() error {
	var failure error
	type item struct {
		id       string
		bytes    int
		modified time.Time
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.quarantine))
	for id := range s.quarantine {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	items := make([]item, 0, len(ids))
	total := 0
	for _, id := range ids {
		record := item{id: id}
		for _, suffix := range []string{".summary.json", ".events.jsonl"} {
			info, err := os.Lstat(filepath.Join(s.root, id+suffix))
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					s.mu.Lock()
					s.recordingIssue = true
					s.mu.Unlock()
					failure = errors.Join(failure, err)
				}
				continue
			}
			record.bytes += int(info.Size())
			if info.ModTime().After(record.modified) {
				record.modified = info.ModTime()
			}
		}
		items = append(items, record)
		total += record.bytes
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].modified.Equal(items[j].modified) {
			return items[i].id < items[j].id
		}
		return items[i].modified.Before(items[j].modified)
	})
	count := len(items)
	for _, record := range items {
		if record.bytes == 0 || count > s.limits.Jobs || total > s.limits.TotalBytes/4 || s.now().Sub(record.modified) > time.Duration(s.limits.Days)*24*time.Hour {
			removed := true
			for _, suffix := range []string{".summary.json", ".events.jsonl"} {
				if err := s.removeFile(filepath.Join(s.root, record.id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
					removed = false
					failure = errors.Join(failure, err)
				}
			}
			s.mu.Lock()
			if removed {
				delete(s.quarantine, record.id)
				total -= record.bytes
				count--
			} else {
				s.recordingIssue = true
			}
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	s.quarantineBytes = total
	s.mu.Unlock()
	return failure
}

func (s *backupJobStore) trimActiveEventsUnsafe(total int) {
	if total <= s.limits.TotalBytes {
		return
	}
	active := make([]*backupJobRecord, 0)
	for _, r := range s.records {
		if r.Summary.CompletedAt.IsZero() {
			active = append(active, r)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Summary.StartedAt.Before(active[j].Summary.StartedAt) })
	for _, r := range active {
		trimmed := false
		for total > s.limits.TotalBytes && len(r.Events) > 0 {
			size := backupEventSize(r.Events[0])
			r.Events = r.Events[1:]
			r.Bytes -= size
			total -= size
			r.Summary.Dropped++
			r.Summary.Truncated = true
			trimmed = true
		}
		if trimmed {
			s.updateEventCursorsUnsafe(r)
			r.EventRevision++
			s.changedUnsafe(r)
		}
	}
}
