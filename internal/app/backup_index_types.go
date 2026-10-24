package app

import (
	"errors"
	"time"
)

const (
	backupIndexMaxPlistBytes  = 8 << 20
	backupIndexMaxRecordBytes = 1 << 20
)

var (
	errBackupIndexUnavailable  = errors.New("backup source unavailable")
	errBackupIndexMissing      = errors.New("backup missing")
	errBackupIndexEncrypted    = errors.New("encrypted backup manifest")
	errBackupIndexCorrupt      = errors.New("invalid backup metadata")
	errBackupIndexUnsafeSource = errors.New("unsafe backup source")
	errBackupIndexLimit        = errors.New("backup index resource limit")
)

type backupIndexReadError struct {
	Code string
	Err  error
}

func (e *backupIndexReadError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *backupIndexReadError) Unwrap() error { return e.Err }

type backupIndexSource struct {
	UDID        string `json:"udid"`
	Directory   string `json:"-"`
	Key         string `json:"key"`
	Identity    string `json:"identity"`
	Fingerprint string `json:"fingerprint"`
	Epoch       uint64 `json:"epoch"`
}

type backupIndexOverview struct {
	Status      string     `json:"status"`
	Freshness   string     `json:"freshness"`
	MessageCode string     `json:"message_code,omitempty"`
	ReadAt      time.Time  `json:"read_at,omitempty"`
	SizeBytes   *int64     `json:"size_bytes"`
	Encrypted   *bool      `json:"encrypted"`
	BackupDate  *time.Time `json:"backup_date,omitempty"`
	DeviceName  string     `json:"device_name,omitempty"`
}

type backupIndexRecord struct {
	FileID       string   `json:"file_id"`
	Domain       string   `json:"domain"`
	RelativePath string   `json:"relative_path"`
	Flags        int      `json:"flags"`
	Fields       []string `json:"fields,omitempty"`
}
