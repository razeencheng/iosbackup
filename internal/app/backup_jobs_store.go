package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"iosbackup/internal/persistence"
)

type backupJobLimits struct{ Days, Jobs, Events, EventBytes, TotalBytes, Queue int }

func defaultBackupJobLimits() backupJobLimits {
	return backupJobLimits{30, 1000, 10000, 1 << 20, 256 << 20, 256}
}

var backupJobIDPattern = regexp.MustCompile(`^bkp-[0-9a-f]{12,32}$`)
var backupJobTempPattern = regexp.MustCompile(`^\.bkp-[0-9a-f]{12,32}\.(summary\.json|events\.jsonl)-[0-9]+$`)

type backupJobRecord struct {
	Summary                backupJobSummary
	Events                 []backupJobEvent
	Bytes                  int
	NextCursor             uint64
	EventRevision          uint64
	PersistedEventRevision uint64
	Dirty                  bool
	LastProgressAt         time.Time
	LastPhase              string
}
type queuedBackupEvent struct {
	ID    string
	Event backupJobEvent
}
type backupJobStore struct {
	quarantine      map[string]bool
	quarantineBytes int
	recordingIssue  bool
	cleanupError    bool
	mu              sync.Mutex
	root            string
	now             func() time.Time
	limits          backupJobLimits
	records         map[string]*backupJobRecord
	queue           chan queuedBackupEvent
	wake            chan struct{}
	flush           chan chan struct{}
	stop, done      chan struct{}
	closeOnce       sync.Once
	closed          bool
	revision        uint64
	writeFile       func(string, []byte, os.FileMode) error
	removeFile      func(string) error
	onChange        func()
}

func openBackupJobStore(root string, now func() time.Time, limits backupJobLimits, callbacks ...func()) (*backupJobStore, error) {
	if limits.Days < 1 || limits.Jobs < 1 || limits.Events < 1 || limits.EventBytes < 128 || limits.TotalBytes < 4096 || limits.Queue < 1 {
		return nil, errors.New("invalid backup job limits")
	}
	if err := persistence.EnsurePrivateDirectory(root); err != nil {
		return nil, err
	}
	s := &backupJobStore{root: root, now: now, limits: limits, records: make(map[string]*backupJobRecord), quarantine: make(map[string]bool), queue: make(chan queuedBackupEvent, limits.Queue), wake: make(chan struct{}, 1), flush: make(chan chan struct{}), stop: make(chan struct{}), done: make(chan struct{}), writeFile: writeFileAtomic, removeFile: os.Remove}
	if len(callbacks) > 0 {
		s.onChange = callbacks[0]
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go s.worker()
	return s, nil
}
func (s *backupJobStore) changedUnsafe(r *backupJobRecord) {
	s.revision++
	r.Summary.Revision++
	if r.Summary.Persistence != "error" {
		r.Summary.Persistence = "pending"
	}
	r.Dirty = true
}
func (s *backupJobStore) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *backupJobStore) start(job *backupJobSeed) {
	s.mu.Lock()
	if s.closed || !backupJobIDPattern.MatchString(job.ID) || !udidPattern.MatchString(job.UDID) || s.records[job.ID] != nil {
		s.mu.Unlock()
		return
	}
	name := job.DeviceName
	if len(name) > 256 {
		name = name[:256]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	r := &backupJobRecord{Summary: backupJobSummary{ID: job.ID, UDID: job.UDID, DeviceName: name, Source: job.Source, RequestedMode: job.RequestedMode, ObservedMode: "unknown", State: backupStateStarting, StartedAt: job.StartedAt}}
	s.records[job.ID] = r
	s.changedUnsafe(r)
	s.enqueueUnsafe(r, backupJobEvent{At: job.StartedAt, Code: "started"})
	s.pruneUnsafe(s.now())
	s.mu.Unlock()
	s.signal()
}

// 状态同步仅更新内存，磁盘写入交由记录线程，不阻塞任务控制。
func (s *backupJobStore) transition(id, state string) {
	s.mu.Lock()
	r := s.records[id]
	if r != nil && !s.closed && r.Summary.CompletedAt.IsZero() && r.Summary.State != state {
		if (state == backupStateRunning && r.Summary.State == backupStateStarting) || state == backupStateStopping {
			r.Summary.State = state
			s.changedUnsafe(r)
		}
	}
	s.mu.Unlock()
	s.signal()
}

func (s *backupJobStore) enqueueUnsafe(r *backupJobRecord, e backupJobEvent) {
	r.NextCursor++
	e.Cursor = r.NextCursor
	if !validJobEvent(e) {
		r.Summary.Dropped++
		r.Summary.Truncated = true
		s.changedUnsafe(r)
		return
	}
	e.Percent = cloneOptionalFloat64(e.Percent)
	e.Bytes = cloneOptionalInt64(e.Bytes)
	select {
	case s.queue <- queuedBackupEvent{r.Summary.ID, e}:
	default:
		r.Summary.Dropped++
		r.Summary.Truncated = true
		s.changedUnsafe(r)
	}
}
func (s *backupJobStore) emit(id string, e backupJobEvent) {
	s.mu.Lock()
	r := s.records[id]
	if r != nil && !s.closed && r.Summary.CompletedAt.IsZero() {
		s.enqueueUnsafe(r, e)
	}
	s.mu.Unlock()
	s.signal()
}
func (s *backupJobStore) activity(id string, u backupProgressUpdate, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	if r == nil || s.closed || !r.Summary.CompletedAt.IsZero() {
		return
	}
	if u.LastActivityAt.After(r.Summary.LastActivityAt) {
		r.Summary.LastActivityAt = u.LastActivityAt
		s.changedUnsafe(r)
	}
	phaseChanged := u.Phase != "" && u.Phase != r.LastPhase
	if !phaseChanged && at.Sub(r.LastProgressAt) < 5*time.Second {
		return
	}
	if !phaseChanged && u.OverallPercent == nil && u.CurrentBytes == nil {
		return
	}
	code := "progress"
	if phaseChanged {
		code = "phase_changed"
		r.LastPhase = u.Phase
	}
	r.LastProgressAt = at
	s.enqueueUnsafe(r, backupJobEvent{At: at, Code: code, Phase: u.Phase, Percent: u.OverallPercent, Bytes: u.CurrentBytes})
}
func (s *backupJobStore) finish(id, state, code string, at time.Time) {
	s.mu.Lock()
	r := s.records[id]
	if r != nil && r.Summary.CompletedAt.IsZero() {
		s.enqueueUnsafe(r, backupJobEvent{At: at, Code: state, ErrorCode: code})
		r.Summary.State, r.Summary.ErrorCode, r.Summary.CompletedAt = state, code, at
		s.changedUnsafe(r)
		s.pruneUnsafe(at)
	}
	s.mu.Unlock()
	s.signal()
}
func (s *backupJobStore) revisionNumber() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.revision }
func (s *backupJobStore) list(udid string) []backupJobSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]backupJobSummary, 0)
	for _, r := range s.records {
		if r.Summary.UDID == udid {
			out = append(out, r.Summary)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}
func (s *backupJobStore) events(id string, after uint64, limit int) (backupJobSummary, []backupJobEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	if r == nil {
		return backupJobSummary{}, nil, false
	}
	out := make([]backupJobEvent, 0)
	for _, e := range r.Events {
		if e.Cursor > after {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return r.Summary, out, true
}
func (s *backupJobStore) drain() {
	for i := 0; i < s.limits.Queue; i++ {
		select {
		case item := <-s.queue:
			s.mu.Lock()
			r := s.records[item.ID]
			if r != nil {
				data, _ := json.Marshal(item.Event)
				if len(data) > 4096 {
					r.Summary.Dropped++
					r.Summary.Truncated = true
				} else {
					r.Events = append(r.Events, item.Event)
					r.Bytes += len(data) + 1
					for len(r.Events) > s.limits.Events || r.Bytes > s.limits.EventBytes {
						old, _ := json.Marshal(r.Events[0])
						r.Bytes -= len(old) + 1
						r.Events = r.Events[1:]
						r.Summary.Dropped++
						r.Summary.Truncated = true
					}
					if len(r.Events) > 0 {
						r.Summary.FirstCursor = r.Events[0].Cursor
						r.Summary.LastCursor = r.Events[len(r.Events)-1].Cursor
					}
					r.EventRevision++
				}
				s.changedUnsafe(r)
			}
			s.mu.Unlock()
		default:
			return
		}
	}
}

func (s *backupJobStore) pruneUnsafe(at time.Time) {
	ended := make([]*backupJobRecord, 0)
	total := s.quarantineBytes
	for _, r := range s.records {
		total += r.Bytes + 2048
		if !r.Summary.CompletedAt.IsZero() {
			ended = append(ended, r)
		}
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].Summary.CompletedAt.Before(ended[j].Summary.CompletedAt) })
	count := len(ended)
	for _, r := range ended {
		if count > s.limits.Jobs || total > s.limits.TotalBytes || at.Sub(r.Summary.CompletedAt) > time.Duration(s.limits.Days)*24*time.Hour {
			delete(s.records, r.Summary.ID)
			count--
			total -= r.Bytes + 2048
			s.revision++
		}
	}
	s.trimActiveEventsUnsafe(total)
}
func (s *backupJobStore) worker() {
	defer close(s.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.wake:
			s.drain()
			s.persist()
		case <-ticker.C:
			s.drain()
			s.persist()
		case done := <-s.flush:
			s.drain()
			s.persist()
			close(done)
		case <-s.stop:
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			s.drain()
			s.persist()
			return
		}
	}
}
func (s *backupJobStore) sync(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case s.flush <- done:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *backupJobStore) close(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.stop) })
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *backupJobStore) persist() {
	quarantineErr := s.pruneQuarantine()
	type snapshot struct {
		Summary       backupJobSummary
		Events        []backupJobEvent
		EventRevision uint64
		WriteEvents   bool
	}
	s.mu.Lock()
	s.pruneUnsafe(s.now())
	pending := make([]snapshot, 0)
	for _, r := range s.records {
		if r.Dirty {
			summary := r.Summary
			summary.Persistence = "saved"
			pending = append(pending, snapshot{summary, append([]backupJobEvent(nil), r.Events...), r.EventRevision, r.EventRevision != r.PersistedEventRevision})
		}
	}
	s.mu.Unlock()
	// 先回收已过期文件；清理失败时停止新增磁盘写入，避免突破预算。
	cleanupErr := errors.Join(quarantineErr, s.sweep())
	s.mu.Lock()
	changed := s.cleanupError != (cleanupErr != nil)
	s.cleanupError = cleanupErr != nil
	s.mu.Unlock()
	for _, p := range pending {
		err := cleanupErr
		if err == nil && p.WriteEvents {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			for _, e := range p.Events {
				if err = enc.Encode(e); err != nil {
					break
				}
			}
			if err == nil {
				err = s.writeFile(filepath.Join(s.root, p.Summary.ID+".events.jsonl"), buf.Bytes(), 0600)
			}
		}
		if err == nil {
			data, encodeErr := json.Marshal(p.Summary)
			err = encodeErr
			if err == nil {
				err = s.writeFile(filepath.Join(s.root, p.Summary.ID+".summary.json"), data, 0600)
			}
		}
		s.mu.Lock()
		r := s.records[p.Summary.ID]
		if r != nil {
			if err == nil {
				r.PersistedEventRevision = p.EventRevision
				if r.Summary.Revision == p.Summary.Revision {
					r.Dirty = false
					r.Summary.Persistence = "saved"
					s.revision++
					changed = true
				}
			} else if r.Summary.Persistence != "error" {
				r.Summary.Persistence = "error"
				s.revision++
				changed = true
			}
		}
		s.mu.Unlock()
	}
	if changed && s.onChange != nil {
		s.onChange()
	}
}

func (s *backupJobStore) sweep() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if backupJobTempPattern.MatchString(name) {
			s.mu.Lock()
			s.recordingIssue = true
			s.mu.Unlock()
			if removeErr := s.removeFile(filepath.Join(s.root, name)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
			continue
		}
		id := ""
		for _, suffix := range []string{".summary.json", ".events.jsonl"} {
			if strings.HasSuffix(name, suffix) {
				id = strings.TrimSuffix(name, suffix)
				break
			}
		}
		if !backupJobIDPattern.MatchString(id) {
			continue
		}
		s.mu.Lock()
		_, keep := s.records[id]
		keep = keep || s.quarantine[id]
		s.mu.Unlock()
		if !keep {
			if removeErr := s.removeFile(filepath.Join(s.root, name)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}
	return err
}

func readBoundedJobFile(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, errors.New("invalid task record file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if len(data) > int(max) {
		return nil, errors.New("task record exceeds limit")
	}
	return data, err
}
func (s *backupJobStore) load() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	summaryFiles := make(map[string]bool)
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".summary.json")
		if id == entry.Name() || !backupJobIDPattern.MatchString(id) {
			continue
		}
		summaryFiles[id] = true
		data, err := readBoundedJobFile(filepath.Join(s.root, entry.Name()), 8192)
		if err != nil {
			s.quarantine[id] = true
			continue
		}
		var summary backupJobSummary
		if json.Unmarshal(data, &summary) != nil || summary.ID != id || !validBackupJobSummary(summary) {
			s.quarantine[id] = true
			continue
		}
		r := &backupJobRecord{Summary: summary, NextCursor: summary.LastCursor, Dirty: true}
		if summary.CompletedAt.IsZero() {
			r.Summary.State = backupStateInterrupted
			r.Summary.ErrorCode = "process_restarted"
			r.Summary.CompletedAt = s.now()
		}
		events, readErr := readBoundedJobFile(filepath.Join(s.root, id+".events.jsonl"), int64(s.limits.EventBytes+4096))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			r.Summary.Truncated = true
			r.Summary.Persistence = "error"
		}
		scanner := bufio.NewScanner(bytes.NewReader(events))
		scanner.Buffer(make([]byte, 4096), 4097)
		for scanner.Scan() {
			var e backupJobEvent
			if json.Unmarshal(scanner.Bytes(), &e) != nil || !validJobEvent(e) || e.Cursor == 0 || (len(r.Events) > 0 && e.Cursor <= r.Events[len(r.Events)-1].Cursor) {
				r.Summary.Truncated = true
				continue
			}
			r.Events = append(r.Events, e)
			canonical, _ := json.Marshal(e)
			r.Bytes += len(canonical) + 1
			if e.Cursor > r.NextCursor {
				r.NextCursor = e.Cursor
			}
		}
		if scanner.Err() != nil {
			r.Summary.Truncated = true
		}
		for len(r.Events) > s.limits.Events || r.Bytes > s.limits.EventBytes {
			data, _ := json.Marshal(r.Events[0])
			r.Bytes -= len(data) + 1
			r.Events = r.Events[1:]
			r.Summary.Truncated = true
			r.Summary.Dropped++
		}
		if len(r.Events) > 0 {
			r.Summary.FirstCursor = r.Events[0].Cursor
			r.Summary.LastCursor = r.Events[len(r.Events)-1].Cursor
		} else {
			r.Summary.FirstCursor = 0
			r.Summary.LastCursor = 0
		}
		if r.Summary.LastCursor < summary.LastCursor {
			r.Summary.Truncated = true
		}
		r.EventRevision = 1
		r.Summary.Revision++
		s.records[id] = r
		s.pruneUnsafe(s.now())
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".events.jsonl")
		if id != entry.Name() && backupJobIDPattern.MatchString(id) && !summaryFiles[id] {
			s.quarantine[id] = true
		}
	}
	if len(s.quarantine) > 0 {
		s.recordingIssue = true
	}
	if err := s.pruneQuarantine(); err != nil {
		s.cleanupError = true
	}
	s.pruneUnsafe(s.now())
	return nil
}

func (s *backupJobStore) recordingStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.quarantine) > 0 || s.recordingIssue || s.cleanupError {
		return "partial"
	}
	for _, r := range s.records {
		if r.Summary.Persistence == "error" {
			return "partial"
		}
	}
	return "available"
}

func backupEventSize(e backupJobEvent) int { data, _ := json.Marshal(e); return len(data) + 1 }
func (s *backupJobStore) updateEventCursorsUnsafe(r *backupJobRecord) {
	r.Summary.FirstCursor, r.Summary.LastCursor = 0, 0
	if len(r.Events) > 0 {
		r.Summary.FirstCursor = r.Events[0].Cursor
		r.Summary.LastCursor = r.Events[len(r.Events)-1].Cursor
	}
}
