package backupstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"iosbackup/internal/persistence"
)

const metadataLimit = 16 << 20

type Store struct {
	mu        sync.Mutex
	path      string
	root      *os.Root
	lock      *os.File
	identity  Identity
	catalog   Catalog
	uncertain bool
	closed    bool
	leases    map[string]int
	step      func(string) error
}
type Work struct {
	ID        string
	Parent    string
	Directory string
}
type RecoveryReport struct {
	Recovered   []string
	Uncommitted []string
	Unclaimed   []string
}

// Create 只接纳新建目录；调用方须先可靠登记 ID 与位置，不能用于重建缺失挂载。
func Create(path, udid, id string, now time.Time) (*Store, error) {
	identity := Identity{Schema: 1, ID: id, UDID: udid, CreatedAt: now.UTC()}
	catalog := Catalog{Schema: 1, StoreID: id, Revision: 1, Points: map[string]Point{}, Baselines: map[string]string{}}
	if catalog.Validate(identity) != nil {
		return nil, ErrInvalid
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	root, err := openSafeRoot(path)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, root: root, identity: identity, catalog: catalog, leases: map[string]int{}}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	for _, dir := range []string{"work", "objects", "descriptors", "transactions"} {
		if err = root.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	s.lock, err = root.OpenFile("lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = s.lockStore(); err != nil {
		return nil, err
	}
	if err = s.writeJSON("catalog.json", catalog); err != nil {
		return nil, err
	}
	if err = s.writeJSON("store.json", identity); err != nil {
		return nil, err
	}
	if err = s.syncDirs("."); err != nil {
		return nil, err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	err = parent.Sync()
	closeErr := parent.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	ok = true
	return s, nil
}
func Open(path, udid, id string) (*Store, error) {
	if !ValidID(id) || !udidPattern.MatchString(udid) {
		return nil, ErrInvalid
	}
	root, err := openSafeRoot(path)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, root: root, uncertain: true, leases: map[string]int{}}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	s.lock, err = root.OpenFile("lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := s.lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrUnsafe
	}
	if err = s.lockStore(); err != nil {
		return nil, err
	}
	if err = s.readJSON("store.json", &s.identity); err != nil {
		return nil, err
	}
	if s.identity.ID != id || s.identity.UDID != udid {
		return nil, ErrInvalid
	}
	if err = s.loadCatalog(); err != nil {
		return nil, err
	}
	for _, dir := range []string{"work", "objects", "descriptors", "transactions"} {
		info, e := root.Lstat(dir)
		if e != nil || !info.IsDir() {
			return nil, ErrUnsafe
		}
	}
	ok = true
	return s, nil
}
func (s *Store) lockStore() error {
	if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Join(ErrBusy, err)
	}
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if len(s.leases) > 0 {
		return ErrBusy
	}
	s.closed = true
	var err error
	if s.lock != nil {
		err = errors.Join(syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN), s.lock.Close())
	}
	if s.root != nil {
		err = errors.Join(err, s.root.Close())
	}
	return err
}
func (s *Store) checkReady() error {
	if s.closed {
		return os.ErrClosed
	}
	if s.uncertain {
		return ErrUncertain
	}
	return s.checkIdentity()
}
func (s *Store) checkIdentity() error {
	info, err := os.Lstat(s.path)
	if err != nil {
		return err
	}
	held, err := s.root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(info, held) {
		return ErrUncertain
	}
	var identity Identity
	if err = s.readJSON("store.json", &identity); err != nil {
		return err
	}
	if identity != s.identity {
		return ErrUncertain
	}
	return nil
}
func (s *Store) Snapshot() (Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Catalog{}, os.ErrClosed
	}
	c := s.catalog
	c.Points = maps.Clone(c.Points)
	c.Baselines = maps.Clone(c.Baselines)
	return c, nil
}
func (s *Store) loadCatalog() error {
	var c Catalog
	if err := s.readJSON("catalog.json", &c); err != nil {
		return err
	}
	if err := c.Validate(s.identity); err != nil {
		return err
	}
	s.catalog = c
	return nil
}
func (s *Store) readJSON(name string, value any) error {
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > metadataLimit {
		return ErrInvalid
	}
	dec := json.NewDecoder(io.LimitReader(f, metadataLimit+1))
	dec.DisallowUnknownFields()
	if err = dec.Decode(value); err != nil {
		return err
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (s *Store) writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > metadataLimit {
		return ErrCapacity
	}
	return persistence.WriteFileAtomicRoot(s.root, path, data, 0600)
}
func (s *Store) syncDirs(names ...string) error {
	for _, name := range names {
		f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		err = f.Sync()
		ce := f.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
	}
	return nil
}
func (s *Store) stage(name string, fn func() error) error {
	if s.step != nil {
		if err := s.step(name + ".before"); err != nil {
			return err
		}
	}
	if err := fn(); err != nil {
		return err
	}
	if s.step != nil {
		return s.step(name + ".after")
	}
	return nil
}
func (s *Store) uncertainError(err error) error {
	s.uncertain = true
	return errors.Join(ErrUncertain, err)
}

// removeTree 不跟随链接，且仅在已确认归属的子树上使用。
func removeTree(ctx context.Context, root *os.Root, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		dir, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		for {
			entries, e := dir.ReadDir(256)
			for _, entry := range entries {
				if err = removeTree(ctx, root, filepath.Join(path, entry.Name())); err != nil {
					dir.Close()
					return err
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				dir.Close()
				return e
			}
		}
		if err = dir.Close(); err != nil {
			return err
		}
	}
	return root.Remove(path)
}
