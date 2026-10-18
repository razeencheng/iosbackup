package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"iosbackup/internal/persistence"

	"iosbackup/internal/i18n"
)

type backupIndexLimits struct {
	PreviewEntries                                  int
	PreviewBytes, RecordBytes, CSVBytes, CacheBytes int64
}

func defaultBackupIndexLimits() backupIndexLimits {
	return backupIndexLimits{5000, 16 << 20, 1 << 20, 512 << 20, 2 << 30}
}

type backupIndexProducer func(func([]string) error) error

type backupIndexRefresh struct {
	State         string    `json:"state"`
	Revision      string    `json:"revision,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	MessageCode   string    `json:"message_code,omitempty"`
}
type backupIndexFileList struct {
	Digest       string              `json:"sha256,omitempty"`
	Bytes        int64               `json:"csv_bytes,omitempty"`
	Status       string              `json:"status"`
	Format       string              `json:"format,omitempty"`
	ReadAt       time.Time           `json:"read_at,omitempty"`
	EntryCount   *int64              `json:"entry_count"`
	PreviewCount int                 `json:"preview_count"`
	Truncated    bool                `json:"truncated"`
	Complete     bool                `json:"complete"`
	Freshness    string              `json:"freshness"`
	MessageCode  string              `json:"message_code,omitempty"`
	Entries      []backupIndexRecord `json:"entries"`
	CSVAvailable bool                `json:"csv_available"`
	Object       string              `json:"-"`
	Identity     string              `json:"-"`
	Fingerprint  string              `json:"-"`
}
type backupIndexSnapshot struct {
	SourceIdentity    string              `json:"-"`
	SourceFingerprint string              `json:"-"`
	SchemaVersion     int                 `json:"schema_version"`
	SourceKey         string              `json:"source_key"`
	Revision          string              `json:"revision"`
	LastBackupAt      string              `json:"last_backup_at,omitempty"`
	Refresh           backupIndexRefresh  `json:"refresh"`
	Overview          backupIndexOverview `json:"overview"`
	FileList          backupIndexFileList `json:"file_list"`
}
type backupIndexDocument struct {
	SchemaVersion   int                 `json:"schema_version"`
	SourceKey       string              `json:"source_key"`
	Revision        string              `json:"revision"`
	Identity        string              `json:"identity"`
	Fingerprint     string              `json:"fingerprint"`
	Overview        backupIndexOverview `json:"overview"`
	FileList        backupIndexFileList `json:"file_list"`
	ListObject      string              `json:"list_object,omitempty"`
	ListIdentity    string              `json:"list_identity,omitempty"`
	ListFingerprint string              `json:"list_fingerprint,omitempty"`
}
type backupIndexStoredState struct {
	Deleted bool               `json:"deleted"`
	Stale   bool               `json:"stale"`
	Refresh backupIndexRefresh `json:"refresh"`
}
type backupIndexCandidate struct {
	list  backupIndexFileList
	path  string
	store *backupIndexStore
}

func (c *backupIndexCandidate) discard() {
	if c == nil {
		return
	}
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if c.store.candidates[c.path] {
		delete(c.store.candidates, c.path)
		_ = os.Remove(c.path)
	}
}

type backupIndexStore struct {
	root            string
	mu              sync.Mutex // 仅保护缓存事务；不能持有 app.mu 调用。
	limits          backupIndexLimits
	reserved        int64
	candidates      map[string]bool
	readers         map[string]int
	verified        map[string]string
	writeDescriptor func(string, []byte) error
}

func newBackupIndexStore(root string) *backupIndexStore {
	return &backupIndexStore{root: root, limits: defaultBackupIndexLimits(), candidates: map[string]bool{}, readers: map[string]int{}, verified: map[string]string{}, writeDescriptor: func(p string, b []byte) error { return writeFileAtomic(p, b, 0600) }}
}

var backupIndexKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var backupIndexRevisionPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func newBackupIndexRevision() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func emptyBackupIndexSnapshot(key string) backupIndexSnapshot {
	return backupIndexSnapshot{SchemaVersion: 1, SourceKey: key, Refresh: backupIndexRefresh{State: "idle"}, Overview: backupIndexOverview{Status: "missing", Freshness: "unknown"}, FileList: backupIndexFileList{Status: "missing", Freshness: "unknown", Entries: []backupIndexRecord{}}}
}
func backupIndexErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var readErr *backupIndexReadError
	if errors.As(err, &readErr) {
		return readErr.Code
	}
	switch {
	case errors.Is(err, errBackupIndexLimit):
		return i18n.BackupIndexLimit
	case errors.Is(err, errBackupIndexEncrypted):
		return i18n.BackupIndexEncrypted
	case errors.Is(err, errBackupIndexMissing):
		return i18n.BackupIndexMissing
	case errors.Is(err, errBackupIndexUnavailable):
		return i18n.BackupIndexUnavailable
	case errors.Is(err, context.Canceled):
		return i18n.BackupIndexCancelled
	default:
		return i18n.BackupIndexRefreshFailed
	}
}
func (s *backupIndexStore) directory(src backupIndexSource, create bool) (string, error) {
	if !backupIndexKeyPattern.MatchString(src.Key) {
		return "", errors.New("invalid index source key")
	}
	dirs := []string{s.root, filepath.Join(s.root, src.Key), filepath.Join(s.root, src.Key, "objects")}
	for _, p := range dirs {
		if create {
			if err := persistence.EnsurePrivateDirectory(p); err != nil {
				return "", err
			}
		}
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe index directory")
		}
	}
	return dirs[1], nil
}
func (s *backupIndexStore) diskUsage() (int64, error) {
	var size int64
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == s.root {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe cache object")
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return errors.New("unsafe cache object")
		}
		if st.Size() > s.limits.CacheBytes-size {
			return errBackupIndexLimit
		}
		size += st.Size()
		return nil
	})
	return size, err
}

type backupIndexBudgetWriter struct {
	w         io.Writer
	remaining int64
}

func (w *backupIndexBudgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errBackupIndexLimit
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// prepare 流式生成独立对象。完整成功之前不改变 current.json，读取旧快照无需等待扫描。
func (s *backupIndexStore) prepare(ctx context.Context, src backupIndexSource, format string, produce backupIndexProducer) (candidate *backupIndexCandidate, retErr error) {
	if format != "manifest-v1" && format != "online-v1" {
		return nil, errors.New("invalid index format")
	}
	s.mu.Lock()
	dir, err := s.directory(src, true)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.cleanupAllUnsafe()
	used, err := s.diskUsage()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	const metadataReserve int64 = 384 << 10
	allowance := min(s.limits.CSVBytes, s.limits.CacheBytes-used-s.reserved-metadataReserve)
	if allowance <= 0 {
		s.mu.Unlock()
		return nil, errBackupIndexLimit
	}
	s.reserved += allowance + metadataReserve
	revision, err := newBackupIndexRevision()
	if err != nil {
		s.reserved -= allowance + metadataReserve
		s.mu.Unlock()
		return nil, err
	}
	path := filepath.Join(dir, "objects", revision+".csv")
	s.candidates[path] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.reserved -= allowance + metadataReserve
		if retErr != nil {
			delete(s.candidates, path)
			_ = os.Remove(path)
		}
		s.mu.Unlock()
	}()
	f, err := os.CreateTemp(dir, ".staging-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return nil, err
	}
	digest := sha256.New()
	budget := &backupIndexBudgetWriter{io.MultiWriter(f, digest), allowance}
	cw := csv.NewWriter(budget)
	if format == "manifest-v1" {
		if err = cw.Write([]string{"file_id", "domain", "relative_path", "flags"}); err != nil {
			return nil, err
		}
	}
	count := int64(0)
	preview := []backupIndexRecord{}
	previewBytes := int64(0)
	previewFull := false
	err = produce(func(fields []string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var n int64
		for _, field := range fields {
			n += int64(len(field))
		}
		if n > s.limits.RecordBytes || len(fields) > 1024 || backupIndexEncodedRecordBytes(fields) > s.limits.RecordBytes {
			return errBackupIndexLimit
		}
		record, err := backupIndexCSVRecord(format, fields)
		if err != nil {
			return err
		}
		if err = cw.Write(fields); err != nil {
			return err
		}
		count++
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if !previewFull && len(preview) < s.limits.PreviewEntries && previewBytes+int64(len(encoded)) <= s.limits.PreviewBytes {
			preview = append(preview, record)
			previewBytes += int64(len(encoded))
		} else {
			previewFull = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	cw.Flush()
	if err = cw.Error(); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return nil, err
	}
	if err = syncBackupIndexDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fmt.Fprintf(digest, "\x00%s:%d", format, count)
	list := backupIndexFileList{Digest: hex.EncodeToString(digest.Sum(nil)), Bytes: allowance - budget.remaining, Status: "available", Format: format, ReadAt: nowBeijing(), EntryCount: &count, PreviewCount: len(preview), Truncated: int64(len(preview)) < count, Complete: true, Freshness: "current", Entries: preview, CSVAvailable: true, Object: revision + ".csv", Identity: src.Identity, Fingerprint: src.Fingerprint}
	return &backupIndexCandidate{list: list, path: path, store: s}, nil
}
func backupIndexCSVRecord(format string, fields []string) (backupIndexRecord, error) {
	if format == "online-v1" {
		return backupIndexRecord{Fields: append([]string(nil), fields...)}, nil
	}
	if len(fields) != 4 {
		return backupIndexRecord{}, errBackupIndexCorrupt
	}
	flags, err := strconv.Atoi(fields[3])
	if err != nil {
		return backupIndexRecord{}, errBackupIndexCorrupt
	}
	return backupIndexRecord{FileID: fields[0], Domain: fields[1], RelativePath: fields[2], Flags: flags}, nil
}
func syncBackupIndexDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *backupIndexStore) readState(dir string) (backupIndexStoredState, error) {
	b, err := persistence.ReadSecretFile(filepath.Join(dir, "state.json"), 64<<10, true)
	if errors.Is(err, os.ErrNotExist) {
		return backupIndexStoredState{}, nil
	}
	if err != nil {
		return backupIndexStoredState{}, err
	}
	var state backupIndexStoredState
	err = json.Unmarshal(b, &state)
	return state, err
}
func (s *backupIndexStore) load(src backupIndexSource) (backupIndexSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnsafe(src)
}
func (s *backupIndexStore) loadUnsafe(src backupIndexSource) (backupIndexSnapshot, error) {
	result := emptyBackupIndexSnapshot(src.Key)
	dir, err := s.directory(src, false)
	if err != nil {
		return result, err
	}
	state, err := s.readState(dir)
	if err != nil {
		return result, err
	}
	if state.Deleted {
		result.Refresh = state.Refresh
		return result, nil
	}
	b, err := persistence.ReadSecretFile(filepath.Join(dir, "current.json"), 128<<10, true)
	if err != nil {
		return result, err
	}
	var doc backupIndexDocument
	if err = json.Unmarshal(b, &doc); err != nil {
		return result, err
	}
	if doc.SchemaVersion != 1 || doc.SourceKey != src.Key || !backupIndexRevisionPattern.MatchString(doc.Revision) {
		return result, errBackupIndexCorrupt
	}
	result.SourceIdentity = doc.Identity
	result.SourceFingerprint = doc.Fingerprint
	result.Revision = doc.Revision
	result.Overview = doc.Overview
	result.FileList = doc.FileList
	result.Refresh = state.Refresh
	if result.Refresh.State == "" || result.Refresh.State == "running" || result.Refresh.State == "queued" {
		result.Refresh.State = "idle"
	}
	result.Overview.Freshness = "unknown"
	result.FileList.Freshness = "unknown"
	if state.Stale {
		result.Overview.Freshness = "stale"
		result.FileList.Freshness = "stale"
	}
	if src.Identity != "" && doc.Identity != "" && src.Identity != doc.Identity {
		return emptyBackupIndexSnapshot(src.Key), nil
	}
	result.FileList.Object = doc.ListObject
	result.FileList.Identity = doc.ListIdentity
	result.FileList.Fingerprint = doc.ListFingerprint
	result.FileList.Entries = []backupIndexRecord{}
	if src.Fingerprint != "" && doc.Fingerprint != src.Fingerprint {
		result.Overview.Freshness = "stale"
		result.FileList.Freshness = "stale"
	}
	if result.FileList.Complete {
		if src.Identity != "" && doc.ListIdentity != src.Identity {
			result.FileList = backupIndexFileList{Status: "unavailable", Freshness: "unknown", MessageCode: i18n.BackupIndexCacheInvalid, Entries: []backupIndexRecord{}}
			return result, nil
		}
		if !validBackupIndexObject(doc.ListObject) || result.FileList.EntryCount == nil || *result.FileList.EntryCount < 0 {
			return emptyBackupIndexSnapshot(src.Key), errBackupIndexCorrupt
		}
		f, err := openBackupIndexFile(filepath.Join(dir, "objects", doc.ListObject), s.limits.CSVBytes)
		if err != nil {
			result.FileList = backupIndexFileList{Status: "unavailable", Freshness: "unknown", MessageCode: i18n.BackupIndexCacheInvalid, Entries: []backupIndexRecord{}}
			return result, nil
		}
		stat, statErr := f.Stat()
		if statErr != nil || stat.Size() != result.FileList.Bytes || s.verifyCSVUnsafe(f, result.FileList) != nil {
			f.Close()
			result.FileList = backupIndexFileList{Status: "unavailable", Freshness: "unknown", MessageCode: i18n.BackupIndexCacheInvalid, Entries: []backupIndexRecord{}}
			return result, nil
		}
		entries, err := s.readPreview(f, result.FileList.Format, *result.FileList.EntryCount)
		f.Close()
		if err != nil {
			result.FileList = backupIndexFileList{Status: "unavailable", Freshness: "unknown", MessageCode: i18n.BackupIndexCacheInvalid, Entries: []backupIndexRecord{}}
			return result, nil
		}
		result.FileList.Entries = entries
		result.FileList.PreviewCount = len(entries)
		result.FileList.Truncated = int64(len(entries)) < *result.FileList.EntryCount
		result.FileList.CSVAvailable = true
	}
	return result, nil
}
func validBackupIndexObject(name string) bool {
	return strings.HasSuffix(name, ".csv") && backupIndexRevisionPattern.MatchString(strings.TrimSuffix(name, ".csv"))
}
func openBackupIndexFile(path string, limit int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		f.Close()
		return nil, errBackupIndexCorrupt
	}
	return f, nil
}
func (s *backupIndexStore) readPreview(f io.Reader, format string, count int64) ([]backupIndexRecord, error) {
	if format == "manifest-v1" {
		reader := bufio.NewReaderSize(f, 32<<10)
		header, err := reader.ReadString('\n')
		if err != nil || header != "file_id,domain,relative_path,flags\n" {
			return nil, errBackupIndexCorrupt
		}
		entries := []backupIndexRecord{}
		used := int64(0)
		stop := errors.New("preview complete")
		err = readBackupManifestCSV(context.Background(), reader, func(row backupManifestRow) error {
			if len(entries) >= s.limits.PreviewEntries || int64(len(entries)) >= count {
				return stop
			}
			record := backupIndexRecord{FileID: row.FileID, Domain: row.Domain, RelativePath: row.RelativePath, Flags: row.Flags}
			b, _ := json.Marshal(record)
			if used+int64(len(b)) > s.limits.PreviewBytes {
				return stop
			}
			used += int64(len(b))
			entries = append(entries, record)
			return nil
		})
		if errors.Is(err, stop) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		if int64(len(entries)) < count && len(entries) < s.limits.PreviewEntries {
			return nil, errBackupIndexCorrupt
		}
		return entries, nil
	}
	if format != "online-v1" {
		return nil, errBackupIndexCorrupt
	}

	// 每次 Read 外加限长 Reader，csv.Reader 内部预读量固定；不以整份 CSV 常驻内存。
	br := &backupIndexRecordReader{reader: f, limit: s.limits.RecordBytes + 4096}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	if format == "manifest-v1" {
		header, err := cr.Read()
		if err != nil || strings.Join(header, ",") != "file_id,domain,relative_path,flags" {
			return nil, errBackupIndexCorrupt
		}
		br.reset()
	}
	entries := []backupIndexRecord{}
	bytes := int64(0)
	for int64(len(entries)) < count && len(entries) < s.limits.PreviewEntries {
		fields, err := cr.Read()
		br.reset()
		if err != nil {
			return nil, err
		}
		r, err := backupIndexCSVRecord(format, fields)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(r)
		if bytes+int64(len(b)) > s.limits.PreviewBytes {
			break
		}
		bytes += int64(len(b))
		entries = append(entries, r)
	}
	return entries, nil
}

// backupIndexRecordReader 限制 CSV 单次解析的输入（含标准库至多一次缓冲预读）。
type backupIndexRecordReader struct {
	reader      io.Reader
	limit, used int64
}

func (r *backupIndexRecordReader) Read(p []byte) (int, error) {
	remaining := r.limit - r.used
	if remaining <= 0 {
		return 0, errBackupIndexLimit
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, e := r.reader.Read(p)
	r.used += int64(n)
	return n, e
}
func (r *backupIndexRecordReader) reset() { r.used = 0 }

// publish 只在调用者确认来源与写入 epoch 仍一致时调用；本方法不读取 app.mu。
func (s *backupIndexStore) publish(src backupIndexSource, overview backupIndexOverview, c *backupIndexCandidate, listErr error) (backupIndexSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.directory(src, true)
	if err != nil {
		return emptyBackupIndexSnapshot(src.Key), err
	}
	old, loadErr := s.loadUnsafe(src)
	if loadErr != nil {
		old = emptyBackupIndexSnapshot(src.Key)
	}
	result := old
	if overview.Status == "available" {
		result.Overview = overview
	} else if old.Overview.Status == "available" {
		result.Overview.Freshness = "stale"
		result.Overview.MessageCode = overview.MessageCode
	} else {
		result.Overview = overview
	}
	if c != nil {
		result.FileList = c.list
	} else {
		code := backupIndexErrorCode(listErr)
		// 仅同一个 Manifest 的加密历史清单可复用，绝不把旧明文清单关联到新加密备份。
		encrypted := overview.Encrypted != nil && *overview.Encrypted
		compatible := old.FileList.Complete && (src.Identity == "" || old.FileList.Identity == src.Identity) && (!encrypted || old.FileList.Fingerprint == src.Fingerprint)
		if compatible {
			result.FileList.Freshness = "stale"
			result.FileList.MessageCode = code
		} else {
			status := "unavailable"
			if errors.Is(listErr, errBackupIndexEncrypted) {
				status = "unsupported"
			}
			if errors.Is(listErr, errBackupIndexMissing) {
				status = "missing"
			}
			result.FileList = backupIndexFileList{Status: status, Freshness: "unknown", MessageCode: code, Entries: []backupIndexRecord{}}
		}
	}
	revision, err := newBackupIndexRevision()
	if err != nil {
		return old, err
	}
	result.Revision = revision
	result.SourceKey = src.Key
	result.SchemaVersion = 1
	doc := backupIndexDocument{SchemaVersion: 1, SourceKey: src.Key, Revision: revision, Identity: src.Identity, Fingerprint: src.Fingerprint, Overview: result.Overview, FileList: result.FileList, ListObject: result.FileList.Object, ListIdentity: result.FileList.Identity, ListFingerprint: result.FileList.Fingerprint}
	if doc.Identity == "" {
		doc.Identity = old.SourceIdentity
	}
	if doc.Fingerprint == "" {
		doc.Fingerprint = old.SourceFingerprint
	}
	doc.FileList.Entries = nil
	doc.FileList.PreviewCount = 0
	b, err := json.Marshal(doc)
	if err != nil {
		return old, err
	}
	if int64(len(b)) > 128<<10 {
		return old, errBackupIndexLimit
	}
	used, usageErr := s.diskUsage()
	if usageErr != nil {
		return old, usageErr
	}
	if used+int64(len(b))*2+(128<<10) > s.limits.CacheBytes {
		return old, errBackupIndexLimit
	}
	if err = s.writeDescriptor(filepath.Join(dir, "current.json"), b); err != nil {
		// rename 后的目录 sync 失败可能已经切换指针，重新核对后决定对象归属。
		actual, readErr := persistence.ReadSecretFile(filepath.Join(dir, "current.json"), 128<<10, true)
		if readErr != nil || string(actual) != string(b) {
			return old, err
		}
	}
	if c != nil {
		delete(s.candidates, c.path)
	}
	state := backupIndexStoredState{Refresh: backupIndexRefresh{State: "idle"}}
	if listErr != nil || overview.Status != "available" {
		state.Refresh.State = "failed"
		state.Refresh.MessageCode = backupIndexErrorCode(listErr)
		if state.Refresh.MessageCode == "" {
			state.Refresh.MessageCode = overview.MessageCode
		}
	}
	state.Refresh.LastAttemptAt = nowBeijing()
	result.Refresh = state.Refresh
	if err = s.writeStateUnsafe(dir, state); err != nil {
		return result, err
	}
	s.cleanupUnsafe(dir, result.FileList.Object)
	return result, nil
}
func (s *backupIndexStore) writeStateUnsafe(dir string, state backupIndexStoredState) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "state.json"), b, 0600)
}
func (s *backupIndexStore) saveRefresh(src backupIndexSource, refresh backupIndexRefresh) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.directory(src, true)
	if err != nil {
		return err
	}
	state, err := s.readState(dir)
	if err != nil {
		return err
	}
	state.Refresh = refresh
	return s.writeStateUnsafe(dir, state)
}
func (s *backupIndexStore) invalidate(src backupIndexSource, deleted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.directory(src, true)
	if err != nil {
		return err
	}
	return s.writeStateUnsafe(dir, backupIndexStoredState{Deleted: deleted, Stale: true, Refresh: backupIndexRefresh{State: "idle"}})
}
func (s *backupIndexStore) cleanupUnsafe(dir, keep string) {
	entries, err := os.ReadDir(filepath.Join(dir, "objects"))
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, "objects", e.Name())
		if e.Name() != keep && validBackupIndexObject(e.Name()) && !s.candidates[p] && s.readers[p] == 0 {
			_ = os.Remove(p)
		}
	}
	// 进程退出遗留的 staging 在下一次完整发布时清理；活动准备项在 objects 中登记。
	entries, _ = os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			st, err := e.Info()
			if err == nil && time.Since(st.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

type backupIndexDownload struct {
	*os.File
	store *backupIndexStore
	path  string
	once  sync.Once
}

func (d *backupIndexDownload) Close() error {
	var err error
	d.once.Do(func() {
		err = d.File.Close()
		d.store.mu.Lock()
		d.store.readers[d.path]--
		if d.store.readers[d.path] == 0 {
			delete(d.store.readers, d.path)
		}
		d.store.mu.Unlock()
	})
	return err
}
func (s *backupIndexStore) openCSV(src backupIndexSource, revision string) (*backupIndexDownload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !backupIndexRevisionPattern.MatchString(revision) {
		return nil, errBackupIndexCorrupt
	}
	snapshot, err := s.loadUnsafe(src)
	if err != nil {
		return nil, err
	}
	if snapshot.Revision != revision || !snapshot.FileList.CSVAvailable || !snapshot.FileList.Complete {
		return nil, fmt.Errorf("index revision unavailable")
	}
	path := filepath.Join(s.root, src.Key, "objects", snapshot.FileList.Object)
	f, err := openBackupIndexFile(path, s.limits.CSVBytes)
	if err != nil {
		return nil, err
	}
	s.readers[path]++
	return &backupIndexDownload{File: f, store: s, path: path}, nil
}

// cleanupAllUnsafe 回收已无描述引用、下载引用或生成任务引用的旧对象。
func (s *backupIndexStore) cleanupAllUnsafe() {
	dirs, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, entry := range dirs {
		if !entry.IsDir() || !backupIndexKeyPattern.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.root, entry.Name())
		raw, err := persistence.ReadSecretFile(filepath.Join(dir, "current.json"), 128<<10, true)
		var doc backupIndexDocument
		if errors.Is(err, os.ErrNotExist) {
			s.cleanupUnsafe(dir, "")
			continue
		}
		if err != nil || json.Unmarshal(raw, &doc) != nil {
			continue
		}
		s.cleanupUnsafe(dir, doc.ListObject)
	}
}

// backupIndexEncodedRecordBytes 与 encoding/csv 的引号规则一致；按磁盘记录而非解码字段限长。
func backupIndexEncodedRecordBytes(fields []string) int64 {
	total := int64(len(fields)) // 逗号及行尾
	for _, field := range fields {
		total += int64(len(field))
		first, _ := utf8.DecodeRuneInString(field)
		if field == `\.` || strings.ContainsAny(field, ",\r\n\"") || unicode.IsSpace(first) {
			total += 2 + int64(strings.Count(field, `"`))
		}
	}
	return total
}

// verifyCSVUnsafe 首次加载及文件身份/mtime 变化后验证完整内容；摘要同时绑定格式与计数。
// 私有目录内的已验证不可变对象可复用验证结果，避免每次轮询重读完整 CSV。
func (s *backupIndexStore) verifyCSVUnsafe(f *os.File, list backupIndexFileList) error {
	if list.EntryCount == nil || !backupIndexKeyPattern.MatchString(list.Digest) {
		return errBackupIndexCorrupt
	}
	before, err := f.Stat()
	if err != nil {
		return err
	}
	stamp := fmt.Sprintf("%s:%d:%d:%s:%s:%d", indexFileIdentity(before), before.Size(), before.ModTime().UnixNano(), list.Digest, list.Format, *list.EntryCount)
	if s.verified[f.Name()] == stamp {
		return nil
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(f, s.limits.CSVBytes+1)); err != nil {
		return err
	}
	fmt.Fprintf(hash, "\x00%s:%d", list.Format, *list.EntryCount)
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(hash.Sum(nil)) != list.Digest {
		return errBackupIndexCorrupt
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	s.verified[f.Name()] = stamp
	return nil
}
