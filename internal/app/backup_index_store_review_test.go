package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupIndexReviewUnknownSourceMustRetainIdentity(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	c, e := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(1))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.publish(src, indexTestOverview(), c, nil); e != nil {
		t.Fatal(e)
	}
	absent := src
	absent.Identity = ""
	absent.Fingerprint = ""
	if _, e = s.publish(absent, backupIndexOverview{Status: "unavailable", Freshness: "unknown"}, nil, errBackupIndexUnavailable); e != nil {
		t.Fatal(e)
	}
	replaced := src
	replaced.Identity = "different-directory"
	replaced.Fingerprint = "different-manifest"
	got, e := s.load(replaced)
	if e != nil {
		t.Fatal(e)
	}
	if got.FileList.Complete || got.FileList.CSVAvailable {
		t.Fatal("old list accepted for a different directory after unavailable-source refresh")
	}
}
func TestBackupIndexReviewQuotedRecordRoundTrip(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	value := strings.Repeat("\"", 600<<10)
	c, e := s.prepare(context.Background(), src, "online-v1", func(emit func([]string) error) error { return emit([]string{value}) })
	if e != nil {
		if errors.Is(e, errBackupIndexLimit) {
			return
		}
		t.Fatal(e)
	}
	if _, e = s.publish(src, indexTestOverview(), c, nil); e != nil {
		t.Fatal(e)
	}
	got, e := s.load(src)
	if e != nil {
		t.Fatal(e)
	}
	if !got.FileList.CSVAvailable || len(got.FileList.Entries) != 1 || got.FileList.Entries[0].Fields[0] != value {
		t.Fatalf("successful published record cannot reload: status=%s message=%s", got.FileList.Status, got.FileList.MessageCode)
	}
}
func TestBackupIndexReviewFullCacheBudgetIncludesDescriptor(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	s.limits.CacheBytes = 64
	c, e := s.prepare(context.Background(), src, "online-v1", func(emit func([]string) error) error { return emit([]string{"a"}) })
	if errors.Is(e, errBackupIndexLimit) {
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.publish(src, indexTestOverview(), c, nil); e != nil {
		return
	}
	if _, e = s.diskUsage(); errors.Is(e, errBackupIndexLimit) {
		t.Fatal("successful publish exceeded total cache budget with descriptor/state")
	}
}

func TestBackupIndexDescriptorLimitKeepsPrevious(t *testing.T) {
	s := newBackupIndexStore(t.TempDir())
	src := indexTestSource()
	c, _ := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(1))
	old, e := s.publish(src, indexTestOverview(), c, nil)
	if e != nil {
		t.Fatal(e)
	}
	overview := indexTestOverview()
	overview.DeviceName = strings.Repeat("x", 129<<10)
	if _, e = s.publish(src, overview, nil, errors.New("list failed")); !errors.Is(e, errBackupIndexLimit) {
		t.Fatalf("oversize descriptor accepted: %v", e)
	}
	got, e := s.load(src)
	if e != nil || got.Revision != old.Revision {
		t.Fatal("oversize descriptor damaged previous result", e)
	}
}

func TestBackupIndexRejectsEqualLengthTailCorruption(t *testing.T) {
	root := t.TempDir()
	s := newBackupIndexStore(root)
	src := indexTestSource()
	c, e := s.prepare(context.Background(), src, "manifest-v1", indexTestRows(5100))
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.publish(src, indexTestOverview(), c, nil)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, src.Key, "objects", snapshot.FileList.Object)
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data[len(data)-4] = 'X'
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := newBackupIndexStore(root).load(src)
	if e != nil {
		t.Fatal(e)
	}
	if got.FileList.CSVAvailable {
		t.Fatal("corrupt tail accepted")
	}
	if _, e = s.openCSV(src, snapshot.Revision); e == nil {
		t.Fatal("corrupt complete export accepted")
	}
}
