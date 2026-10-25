// Package backupstore 管理独立的历史备份对象，不依赖设备或 HTTP 状态。
package backupstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
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
	ID            string    `json:"id"`
	Kind          string    `json:"kind"` // unknown | encrypted | unencrypted
	CredentialRef string    `json:"credential_ref,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Identity struct {
	Schema    int       `json:"schema"`
	ID        string    `json:"id"`
	UDID      string    `json:"udid"`
	CreatedAt time.Time `json:"created_at"`
}

type Check struct {
	State     string    `json:"state"`
	Scope     string    `json:"scope"`
	CheckedAt time.Time `json:"checked_at"`
}

type Point struct {
	ID          string     `json:"id"`
	StoreID     string     `json:"store_id"`
	UDID        string     `json:"udid"`
	Generation  Generation `json:"generation"`
	EpochID     string     `json:"epoch_id"`
	JobID       string     `json:"job_id"`
	CompletedAt time.Time  `json:"completed_at"`
	CommittedAt time.Time  `json:"committed_at"`
	Day         string     `json:"day"`
	Timezone    string     `json:"timezone"`
	State       string     `json:"state"` // normal | pending
	Check       Check      `json:"check"`
	Pinned      bool       `json:"pinned"`
	Manual      bool       `json:"manual"`
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
		if g.CredentialRef != "" {
			return ErrInvalid
		}
	case "encrypted":
		if g.CredentialRef != "" && !ValidID(g.CredentialRef) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (p Point) Validate(identity Identity) error {
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
		if p.Check.State != "passed" {
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
