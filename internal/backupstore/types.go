// Package backupstore 管理独立的历史备份对象，不依赖设备或 HTTP 状态。
package backupstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
	_ "time/tzdata"
)

var (
	ErrInvalid   = errors.New("invalid backup store metadata")
	ErrUncertain = errors.New("backup store needs recovery")
	ErrConflict  = errors.New("backup store revision conflict")
	ErrBusy      = errors.New("backup object is in use")
	ErrUnsafe    = errors.New("unsafe backup source")
	ErrChanged   = errors.New("backup source changed")
	ErrCapacity  = errors.New("insufficient backup storage capacity")
)

type Policy struct {
	Enabled bool `json:"enabled"`
	Days    int  `json:"days"`
}

func (p Policy) Normalize() (Policy, error) {
	if p.Days == 0 {
		p.Days = 3
	}
	if p.Days < 1 || p.Days > 365 {
		return p, ErrInvalid
	}
	return p, nil
}
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func ValidID(id string) bool { return idPattern.MatchString(id) }

type Generation struct {
	// 仅在本应用成功备份后记录来源密钥包摘要，不代表密码已经验证。
	KeybagDigest       string    `json:"keybag_digest,omitempty"`
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"` // unknown | encrypted | unencrypted
	CredentialRef      string    `json:"credential_ref,omitempty"`
	CredentialVerified bool      `json:"credential_verified,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type Identity struct {
	Schema    int       `json:"schema"`
	ID        string    `json:"id"`
	UDID      string    `json:"udid"`
	CreatedAt time.Time `json:"created_at"`
}

type Check struct {
	Reason             string    `json:"reason,omitempty"`
	MetadataComplete   bool      `json:"metadata_complete"`
	ManifestComplete   bool      `json:"manifest_complete"`
	RecordsChecked     int64     `json:"records_checked"`
	ObjectsChecked     int64     `json:"objects_checked"`
	MissingObjects     int64     `json:"missing_objects"`
	InvalidRecords     int64     `json:"invalid_records"`
	UnsupportedRecords int64     `json:"unsupported_records"`
	State              string    `json:"state"`
	Scope              string    `json:"scope"`
	CheckedAt          time.Time `json:"checked_at"`
}

type Point struct {
	Bytes             int64      `json:"bytes,omitempty"`
	BytesKnown        bool       `json:"bytes_known,omitempty"`
	Review            Check      `json:"review,omitempty"`
	ReviewAttempt     Check      `json:"review_attempt,omitempty"`
	CompletionUnknown bool       `json:"completion_unknown,omitempty"`
	ID                string     `json:"id"`
	StoreID           string     `json:"store_id"`
	UDID              string     `json:"udid"`
	Generation        Generation `json:"generation"`
	EpochID           string     `json:"epoch_id"`
	JobID             string     `json:"job_id"`
	CompletedAt       time.Time  `json:"completed_at"`
	CommittedAt       time.Time  `json:"committed_at"`
	Day               string     `json:"day"`
	Timezone          string     `json:"timezone"`
	State             string     `json:"state"` // normal | pending
	Check             Check      `json:"check"`
	Pinned            bool       `json:"pinned"`
	Manual            bool       `json:"manual"`
}

type Catalog struct {
	Schema    int               `json:"schema"`
	StoreID   string            `json:"store_id"`
	Revision  uint64            `json:"revision"`
	Points    map[string]Point  `json:"points"`
	Baselines map[string]string `json:"baselines"`
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)
var jobPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (g Generation) Validate() error {
	if !ValidID(g.ID) || g.CreatedAt.IsZero() {
		return ErrInvalid
	}
	switch g.Kind {
	case "unknown", "unencrypted":
		if g.CredentialRef != "" || g.CredentialVerified || g.KeybagDigest != "" {
			return ErrInvalid
		}
	case "encrypted":
		if (g.CredentialRef != "" && !ValidID(g.CredentialRef)) || (g.CredentialVerified && g.CredentialRef == "") {
			return ErrInvalid
		}
		if g.KeybagDigest != "" {
			if len(g.KeybagDigest) != 64 {
				return ErrInvalid
			}
			if _, err := hex.DecodeString(g.KeybagDigest); err != nil {
				return ErrInvalid
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (p Point) Validate(identity Identity) error {
	if p.Bytes < 0 || (!p.BytesKnown && p.Bytes != 0) || p.Review != (Check{}) && !validReview(p.Review) || p.ReviewAttempt != (Check{}) && !validReview(p.ReviewAttempt) {
		return ErrInvalid
	}
	if !ValidID(p.ID) || p.StoreID != identity.ID || p.UDID != identity.UDID || !ValidID(p.EpochID) || !jobPattern.MatchString(p.JobID) || p.Generation.Validate() != nil || p.CompletedAt.IsZero() || p.CommittedAt.Before(p.CompletedAt) || p.Check.CheckedAt.IsZero() {
		return ErrInvalid
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil || p.Day != p.CompletedAt.In(loc).Format("2006-01-02") {
		return ErrInvalid
	}
	if p.Check.Scope != "quick" && p.Check.Scope != "manifest" {
		return ErrInvalid
	}
	switch p.State {
	case "normal":
		if p.Check.State != "passed" || p.CompletionUnknown {
			return ErrInvalid
		}
	case "pending":
		if p.Check.State != "issues" && p.Check.State != "unavailable" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (c Catalog) Validate(identity Identity) error {
	if identity.Schema != 1 || !ValidID(identity.ID) || !udidPattern.MatchString(identity.UDID) || identity.CreatedAt.IsZero() || c.Schema != 1 || c.StoreID != identity.ID || c.Revision == 0 || c.Points == nil || c.Baselines == nil {
		return ErrInvalid
	}
	for id, p := range c.Points {
		if id != p.ID || p.Validate(identity) != nil {
			return ErrInvalid
		}
	}
	for gen, id := range c.Baselines {
		p, ok := c.Points[id]
		if !ok || p.Generation.ID != gen || p.State != "normal" {
			return ErrInvalid
		}
	}
	return nil
}

func validReview(c Check) bool {
	return (c.State == "passed" || c.State == "issues" || c.State == "unavailable") && (c.Scope == "quick" || c.Scope == "manifest") && !c.CheckedAt.IsZero()
}
