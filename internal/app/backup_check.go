package app

import (
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var errBackupCheckMetadataMissing = errors.New("required backup metadata missing")

var backupCheckDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	backupQuickCheckTimeout    = 30 * time.Second
	backupManifestCheckTimeout = 15 * time.Minute
)

// 检查结果只保存覆盖范围与计数；不保存备份内部路径，也不承诺内容哈希或恢复兼容性。
type backupCheckResult struct {
	LastCompleted      *backupCheckResult `json:"last_completed,omitempty"`
	ID                 string             `json:"id,omitempty"`
	Kind               string             `json:"kind"`
	State              string             `json:"state"`
	Reason             string             `json:"reason,omitempty"`
	StartedAt          time.Time          `json:"started_at"`
	CompletedAt        time.Time          `json:"completed_at,omitempty"`
	SourceKey          string             `json:"source_key,omitempty"`
	SourceIdentity     string             `json:"source_identity,omitempty"`
	SourceFingerprint  string             `json:"source_fingerprint,omitempty"`
	Freshness          string             `json:"freshness"`
	MetadataComplete   bool               `json:"metadata_complete"`
	ManifestComplete   bool               `json:"manifest_complete"`
	RecordsChecked     int64              `json:"records_checked"`
	ObjectsChecked     int64              `json:"objects_checked"`
	MissingObjects     int64              `json:"missing_objects"`
	InvalidRecords     int64              `json:"invalid_records"`
	UnsupportedRecords int64              `json:"unsupported_records"`
}

func backupCheckError(err error) (string, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "unavailable", "timeout"
	case errors.Is(err, context.Canceled):
		return "unavailable", "cancelled"
	case errors.Is(err, errBackupCheckMetadataMissing), errors.Is(err, errBackupIndexMissing):
		return "issues", "metadata_missing"
	case errors.Is(err, errBackupIndexCorrupt):
		return "issues", "metadata_invalid"
	case errors.Is(err, errBackupIndexLimit):
		return "unavailable", "resource_limit"
	case errors.Is(err, errBackupIndexUnsafeSource):
		return "unavailable", "unsafe_source"
	default:
		return "unavailable", "read_failed"
	}
}

func readBackupCheckPlist(ctx context.Context, root *os.Root, name string, converter backupPlistConverter) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openBackupIndexRegular(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(errBackupCheckMetadataMissing, err)
	}
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, backupIndexMaxPlistBytes+1))
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return parseBackupPlist(ctx, data, converter)
}

// 调用者持有来源占用；独立读取器不访问设备、不修改清单或备份对象。
func runBackupCheck(ctx context.Context, src backupIndexSource, kind string, converter backupPlistConverter, streamer backupManifestRowStreamer) (result backupCheckResult) {
	result = backupCheckResult{Kind: kind, State: "passed", Freshness: "current", StartedAt: time.Now().UTC(), SourceKey: src.Key, SourceIdentity: src.Identity, SourceFingerprint: src.Fingerprint}
	defer func() { result.CompletedAt = time.Now().UTC() }()
	fail := func(err error) {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		result.State, result.Reason = backupCheckError(err)
	}
	if err := ctx.Err(); err != nil {
		fail(err)
		return
	}
	if kind != "quick" && kind != "manifest" {
		result.State, result.Reason = "unavailable", "unsupported_check"
		return
	}
	root, err := openBackupIndexRoot(src)
	if err != nil {
		fail(err)
		return
	}
	defer root.Close()
	fingerprint, err := backupIndexSourceFingerprint(ctx, src)
	if err != nil {
		fail(err)
		return
	}
	result.SourceFingerprint = fingerprint
	defer func() {
		after, err := backupIndexSourceFingerprint(ctx, src)
		if err != nil {
			if result.State == "passed" {
				fail(err)
			}
			result.Freshness = "unknown"
			return
		}
		if fingerprint != after {
			result.State, result.Reason, result.Freshness = "unavailable", "source_changed", "stale"
			result.ManifestComplete = false
		}
	}()
	manifest, err := readBackupCheckPlist(ctx, root, "Manifest.plist", converter)
	if err != nil {
		fail(err)
		return
	}
	encrypted, ok := manifest["IsEncrypted"].(bool)
	if !ok {
		result.State, result.Reason = "issues", "metadata_invalid"
		return
	}
	if _, err = readBackupCheckPlist(ctx, root, "Info.plist", converter); err != nil {
		fail(err)
		return
	}
	status, err := readBackupCheckPlist(ctx, root, "Status.plist", converter)
	if err != nil {
		fail(err)
		return
	}
	state, ok := status["SnapshotState"].(string)
	if !ok {
		result.State, result.Reason = "issues", "metadata_invalid"
		return
	}
	if state != "finished" {
		result.State, result.Reason = "issues", "not_finished"
		return
	}
	file, err := openBackupIndexRegular(root, "Manifest.db")
	if errors.Is(err, os.ErrNotExist) {
		err = errors.Join(errBackupCheckMetadataMissing, err)
	}
	if err != nil {
		fail(err)
		return
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		fail(statErr)
		return
	}
	if closeErr != nil {
		fail(closeErr)
		return
	}
	if info.Size() == 0 {
		result.State, result.Reason = "issues", "metadata_invalid"
		return
	}
	if err = ctx.Err(); err != nil {
		fail(err)
		return
	}
	result.MetadataComplete = true
	if kind == "quick" {
		return
	}
	if encrypted {
		result.State, result.Reason = "unavailable", "encrypted_manifest"
		return
	}
	_, err = streamLocalBackupIndex(ctx, src, &encrypted, streamer, func(row backupIndexRecord) error {
		result.RecordsChecked++
		if !backupFileIDPattern.MatchString(row.FileID) {
			result.InvalidRecords++
			return nil
		}
		switch row.Flags {
		case 1:
			err := checkBackupObject(root, strings.ToLower(row.FileID))
			if errors.Is(err, os.ErrNotExist) {
				result.MissingObjects++
				return nil
			}
			if err != nil {
				return err
			}
			result.ObjectsChecked++
		case 2, 4: // 目录与符号链接的恢复信息在清单中，不要求普通文件对象。
		default:
			result.UnsupportedRecords++
		}
		return nil
	})
	if err != nil {
		fail(err)
		return
	}
	if result.UnsupportedRecords > 0 {
		result.State, result.Reason = "unavailable", "unsupported_records"
	} else {
		result.ManifestComplete = true
	}
	if result.InvalidRecords > 0 {
		result.State, result.Reason = "issues", "invalid_records"
	} else if result.MissingObjects > 0 {
		result.State, result.Reason = "issues", "missing_objects"
	}
	return
}

func checkBackupObject(root *os.Root, id string) error {
	// 同时兼容分桶及旧版平铺对象；不跟随对象目录或文件的链接。
	bucket, err := root.Lstat(id[:2])
	if err == nil {
		if !bucket.IsDir() || bucket.Mode()&os.ModeSymlink != 0 {
			return errBackupIndexUnsafeSource
		}
		file, openErr := openBackupIndexRegular(root, id[:2]+"/"+id)
		if openErr == nil {
			return file.Close()
		}
		if !errors.Is(openErr, os.ErrNotExist) {
			return openErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := openBackupIndexRegular(root, id)
	if err != nil {
		return err
	}
	return file.Close()
}

func validBackupCheckResult(result backupCheckResult) bool {
	if previous := result.LastCompleted; previous != nil {
		if previous.LastCompleted != nil || previous.State != "passed" && previous.State != "issues" || !validBackupCheckResult(*previous) {
			return false
		}
	}
	if result.ID != "" && !backupJobIDPattern.MatchString(result.ID) {
		return false
	}
	if result.Kind != "quick" && result.Kind != "manifest" {
		return false
	}
	switch result.State {
	case "running", "passed", "issues", "unavailable":
	default:
		return false
	}
	switch result.Freshness {
	case "current", "stale", "unknown":
	default:
		return false
	}
	switch result.Reason {
	case "", "timeout", "cancelled", "metadata_missing", "metadata_invalid", "resource_limit", "unsafe_source", "read_failed", "unsupported_check", "not_finished", "encrypted_manifest", "unsupported_records", "invalid_records", "missing_objects", "source_changed", "process_restarted", "unknown_source_time":
	default:
		return false
	}
	for _, value := range []string{result.SourceKey, result.SourceIdentity, result.SourceFingerprint} {
		if value != "" && !backupCheckDigestPattern.MatchString(value) {
			return false
		}
	}
	return result.RecordsChecked >= 0 && result.ObjectsChecked >= 0 && result.MissingObjects >= 0 && result.InvalidRecords >= 0 && result.UnsupportedRecords >= 0
}
