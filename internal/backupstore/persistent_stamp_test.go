package backupstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type remountedInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i remountedInfo) Sys() any { return &i.stat }

func TestPersistentIdentityHandlesRemountButRejectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := remountedInfo{FileInfo: info, stat: *info.Sys().(*syscall.Stat_t)}
	changed.stat.Dev++
	if persistentFileIdentity(info) != persistentFileIdentity(changed) {
		t.Fatal("persistent fingerprint depends on mount device allocation")
	}
	if fileIdentity(info) == fileIdentity(changed) {
		t.Fatal("live identity no longer detects device changes")
	}
	changed.stat.Ino++
	if persistentFileIdentity(info) == persistentFileIdentity(changed) {
		t.Fatal("replacement inode lost from persistent evidence")
	}
}

func TestReadyRecoverySupportsVersionedAndLegacyStamps(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "legacy"}[legacy], func(t *testing.T) {
			s := storeFixture(t)
			work, point := preparePoint(t, s)
			s.step = func(stage string) error {
				if stage == "ready.after" {
					return errors.New("simulated stop")
				}
				return nil
			}
			if _, err := s.Commit(context.Background(), work.ID, point); !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
			s.step = nil
			tx, err := s.readTransaction(work.ID)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				tx.SourceStamp, err = s.objectStamp(context.Background(), filepath.Join("work", work.ID))
				if err != nil {
					t.Fatal(err)
				}
				if err = s.saveTransaction(tx); err != nil {
					t.Fatal(err)
				}
			} else if !strings.HasPrefix(tx.SourceStamp, "v2:") {
				t.Fatal("new ready transaction has no remount-stable stamp version")
			}
			if _, err = s.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			lease, err := s.Acquire(work.ID)
			if err != nil {
				t.Fatal(err)
			}
			lease.Close()
		})
	}
}
