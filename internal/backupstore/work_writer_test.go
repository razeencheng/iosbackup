package backupstore

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkAbortDoesNotDeleteLiveWriter(t *testing.T) {
	id, _ := NewID()
	s, err := Create(filepath.Join(t.TempDir(), "store"), "PHONE", id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.NewWork("writer")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(w.Parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = s.AbortWork(context.Background(), w.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("live writer did not block abort", err)
	}
	if _, err = os.Stat(w.Parent); err != nil {
		t.Fatal("live work deleted", err)
	}
	f.Close()
	if err = s.AbortWork(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestWorkWriterSurvivesParentStoreClose(t *testing.T) {
	id, _ := NewID()
	path := filepath.Join(t.TempDir(), "store")
	s, err := Create(path, "PHONE", id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.NewWork("writer")
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.HoldWorkWriter(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf ready; read answer")
	cmd.ExtraFiles = []*os.File{f}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); cmd.Wait() }()
	if _, err = io.ReadFull(out, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, "PHONE", id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.AbortWork(ctx, w.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("orphan child writer not protected", err)
	}
	if _, err = io.WriteString(in, "done\n"); err != nil {
		t.Fatal(err)
	}
	in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = s.AbortWork(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AbortWork(ctx, w.ID); err != nil {
		t.Fatal("completed cleanup not idempotent", err)
	}
}
