package backupstore

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"time"

	"iosbackup/internal/persistence"
)

type transaction struct {
	ExplicitDelete bool   `json:"explicit_delete,omitempty"`
	Previous       *Point `json:"previous,omitempty"`
	SourceStamp    string `json:"source_stamp,omitempty"`
	Schema         int    `json:"schema"`
	ID             string `json:"id"`
	StoreID        string `json:"store_id"`
	JobID          string `json:"job_id"`
	Phase          string `json:"phase"` // work | sealing | ready | deleting
	Point          *Point `json:"point,omitempty"`
	Revision       uint64 `json:"revision,omitempty"`
}

func (s *Store) readTransaction(id string) (transaction, error) {
	var tx transaction
	if !ValidID(id) {
		return tx, ErrInvalid
	}
	if err := s.readJSON(filepath.Join("transactions", id+".json"), &tx); err != nil {
		return tx, err
	}
	if tx.Schema != 1 || tx.ID != id || tx.StoreID != s.identity.ID || !jobPattern.MatchString(tx.JobID) {
		return tx, ErrInvalid
	}
	switch tx.Phase {
	case "work":
		if tx.Point != nil {
			return tx, ErrInvalid
		}
	case "metadata":
		if tx.Previous == nil || tx.Point == nil || tx.Revision == 0 || tx.Revision == math.MaxUint64 || tx.Previous.Validate(s.identity) != nil || tx.Point.Validate(s.identity) != nil || tx.Point.ID != id {
			return tx, ErrInvalid
		}
		a, b := *tx.Previous, *tx.Point
		a.Pinned, a.Manual, a.Review = b.Pinned, b.Manual, b.Review
		if a != b {
			return tx, ErrInvalid
		}
	case "sealing", "ready", "deleting":
		if tx.Point == nil || tx.Point.ID != id || tx.Point.Validate(s.identity) != nil {
			return tx, ErrInvalid
		}
	default:
		return tx, ErrInvalid
	}
	return tx, nil
}
func (s *Store) saveTransaction(tx transaction) error {
	return s.writeJSON(filepath.Join("transactions", tx.ID+".json"), tx)
}
func (s *Store) NewWork(jobID string) (Work, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return Work{}, err
	}
	if !jobPattern.MatchString(jobID) {
		return Work{}, ErrInvalid
	}
	id, err := NewID()
	if err != nil {
		return Work{}, err
	}
	tx := transaction{Schema: 1, ID: id, StoreID: s.identity.ID, JobID: jobID, Phase: "work"}
	if err = s.saveTransaction(tx); err != nil {
		return Work{}, s.uncertainError(err)
	}
	if err = s.root.Mkdir(filepath.Join("work", id), 0700); err != nil {
		return Work{}, s.uncertainError(err)
	}
	if err = s.syncDirs("work"); err != nil {
		return Work{}, s.uncertainError(err)
	}
	parent := filepath.Join(s.path, "work", id)
	return Work{ID: id, Parent: parent, Directory: filepath.Join(parent, s.identity.UDID)}, nil
}
func (s *Store) Commit(ctx context.Context, workID string, point Point) (Point, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return Point{}, err
	}
	if err := ctx.Err(); err != nil {
		return Point{}, err
	}
	tx, err := s.readTransaction(workID)
	if err != nil {
		return Point{}, err
	}
	if tx.Phase != "work" {
		return Point{}, ErrConflict
	}
	point.ID, point.StoreID, point.UDID, point.JobID = workID, s.identity.ID, s.identity.UDID, tx.JobID
	point.CompletedAt = point.CompletedAt.UTC()
	point.Generation.CreatedAt = point.Generation.CreatedAt.UTC()
	point.Check.CheckedAt = point.Check.CheckedAt.UTC()
	point.CommittedAt = time.Now().UTC()
	if err = point.Validate(s.identity); err != nil {
		return Point{}, err
	}
	root, statErr := s.root.OpenRoot(filepath.Join("work", workID, s.identity.UDID))
	if statErr != nil {
		return Point{}, statErr
	}
	stats, statErr := stampTree(ctx, root)
	root.Close()
	if statErr != nil {
		return Point{}, statErr
	}
	point.Bytes, point.BytesKnown = stats.Bytes, true
	tx.Point = &point
	tx.Phase = "sealing"
	if err = s.stage("intent", func() error { return s.saveTransaction(tx) }); err != nil {
		return Point{}, s.uncertainError(err)
	}
	if err = s.stage("data", func() error { return s.syncObject(ctx, filepath.Join("work", workID)) }); err != nil {
		return Point{}, s.uncertainError(err)
	}
	if err = s.stage("descriptor", func() error { return s.writeJSON(filepath.Join("descriptors", point.ID+".json"), point) }); err != nil {
		return Point{}, s.uncertainError(err)
	}
	stamp, stampErr := s.objectStamp(ctx, filepath.Join("work", workID))
	if stampErr != nil {
		return Point{}, s.uncertainError(stampErr)
	}
	tx.SourceStamp = stamp
	tx.Phase = "ready"
	if err = s.stage("ready", func() error { return s.saveTransaction(tx) }); err != nil {
		return Point{}, s.uncertainError(err)
	}
	// ready 后不响应普通取消：必须完成或保留可恢复的提交，不先清理数据。
	if err = s.publish(tx); err != nil {
		return Point{}, s.uncertainError(err)
	}
	return point, nil
}
func (s *Store) checkObjectShape(path string) error {
	info, err := s.root.Lstat(path)
	if err != nil || !info.IsDir() {
		return ErrUncertain
	}
	object, err := s.root.OpenRoot(path)
	if err != nil {
		return err
	}
	defer object.Close()
	entries, err := object.Open(".")
	if err != nil {
		return err
	}
	names, err := entries.Readdirnames(2)
	entries.Close()
	if err != nil && len(names) == 0 {
		return ErrUncertain
	}
	if len(names) != 1 || names[0] != s.identity.UDID {
		return ErrUncertain
	}
	info, err = object.Lstat(s.identity.UDID)
	if err != nil || !info.IsDir() {
		return ErrUncertain
	}
	return nil
}
func (s *Store) syncObject(ctx context.Context, path string) error {
	if err := s.checkObjectShape(path); err != nil {
		return err
	}
	object, err := s.root.OpenRoot(path)
	if err != nil {
		return err
	}
	defer object.Close()
	return syncTree(ctx, object)
}
func (s *Store) publish(tx transaction) error {
	point := *tx.Point
	path := filepath.Join("work", point.ID)
	if _, err := s.root.Lstat(path); errors.Is(err, os.ErrNotExist) {
		path = filepath.Join("objects", point.ID)
	} else if err != nil {
		return err
	}
	stamp, err := s.objectStamp(context.Background(), path)
	if err != nil {
		return err
	}
	if tx.SourceStamp == "" || stamp != tx.SourceStamp {
		return ErrChanged
	}
	var descriptor Point
	if err := s.readJSON(filepath.Join("descriptors", point.ID+".json"), &descriptor); err != nil {
		return err
	}
	if descriptor != point {
		return ErrInvalid
	}
	work, object := filepath.Join("work", point.ID), filepath.Join("objects", point.ID)
	_, destErr := s.root.Lstat(object)
	if errors.Is(destErr, os.ErrNotExist) {
		if err := s.stage("rename", func() error { return persistence.RenameWithinRoot(s.root, work, object) }); err != nil {
			return err
		}
	} else if destErr != nil {
		return destErr
	} else {
		if _, err := s.root.Lstat(work); !errors.Is(err, os.ErrNotExist) {
			return ErrUncertain
		}
	}
	if err := s.stage("parents", func() error { return s.syncDirs("work", "objects") }); err != nil {
		return err
	}
	next := s.catalog
	next.Points = clonePoints(s.catalog.Points)
	next.Baselines = cloneBaselines(s.catalog.Baselines)
	if existing, ok := next.Points[point.ID]; ok {
		if existing != point {
			return ErrInvalid
		}
	} else {
		if next.Revision == math.MaxUint64 {
			return ErrCapacity
		}
		next.Revision++
		next.Points[point.ID] = point
		if point.State == "normal" {
			next.Baselines[point.Generation.ID] = point.ID
		}
	}
	if err := s.stage("catalog", func() error { return s.writeJSON("catalog.json", next) }); err != nil {
		return err
	}
	s.catalog = next
	return s.stage("cleanup", func() error {
		if err := s.root.Remove(filepath.Join("transactions", tx.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return s.syncDirs("transactions")
	})
}
func clonePoints(source map[string]Point) map[string]Point {
	target := make(map[string]Point, len(source))
	for k, v := range source {
		target[k] = v
	}
	return target
}
func cloneBaselines(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for k, v := range source {
		target[k] = v
	}
	return target
}
func (s *Store) AbortWork(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkReady(); err != nil {
		return err
	}
	tx, err := s.readTransaction(id)
	if err != nil {
		return err
	}
	if tx.Phase != "work" && tx.Phase != "sealing" {
		return ErrConflict
	}
	if _, ok := s.catalog.Points[id]; ok {
		return ErrConflict
	}
	if _, err = s.root.Lstat(filepath.Join("objects", id)); !errors.Is(err, os.ErrNotExist) {
		return ErrUncertain
	}
	if err = removeTree(ctx, s.root, filepath.Join("work", id)); err != nil {
		return s.uncertainError(err)
	}
	if err = s.root.Remove(filepath.Join("descriptors", id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return s.uncertainError(err)
	}
	if err = s.syncDirs("work", "descriptors"); err != nil {
		return s.uncertainError(err)
	}
	if err = s.root.Remove(filepath.Join("transactions", id+".json")); err != nil {
		return s.uncertainError(err)
	}
	return s.syncDirs("transactions")
}

func (s *Store) objectStamp(ctx context.Context, path string) (string, error) {
	directory := filepath.Join(path, s.identity.UDID)
	info, err := s.root.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrUnsafe
	}
	root, err := s.root.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	stamp, err := stampTree(ctx, root)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(stamp.Digest[:]), nil
}
