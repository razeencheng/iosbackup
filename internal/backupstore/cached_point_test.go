package backupstore

import (
	"context"
	"testing"
	"time"
)

func TestCachedPointDoesNotWaitForSealing(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	first, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, p = preparePoint(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	s.step = func(stage string) error {
		if stage == "data.before" {
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Commit(context.Background(), w.ID, p); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("commit did not reach data stage: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not reach data stage")
	}
	read := make(chan Point, 1)
	go func() { p, _ := s.CachedPoint(first.ID); read <- p }()
	blocked := false
	select {
	case got := <-read:
		if got != first {
			t.Error("lost published evidence")
		}
	case <-time.After(time.Second):
		blocked = true
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if blocked {
		<-read
		t.Fatal("display-only lookup waited for sealing I/O")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CachedPoint(first.ID); ok {
		t.Fatal("closed store published evidence")
	}
}
