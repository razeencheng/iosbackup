package backupstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStoreCrashHelper(t *testing.T) {
	path := os.Getenv("IOSBK_STORE_CRASH_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	s, err := Open(path, "PHONE", os.Getenv("IOSBK_STORE_CRASH_ID"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	w, p := preparePoint(t, s)
	s.step = func(name string) error {
		if name == os.Getenv("IOSBK_STORE_CRASH_STAGE") {
			os.Exit(77)
		}
		return nil
	}
	_, err = s.Commit(context.Background(), w.ID, p)
	t.Fatalf("crash boundary not reached: %v", err)
}
func TestStoreRecoveryAfterProcessExit(t *testing.T) {
	for _, stage := range []string{"data.after", "ready.after", "rename.after", "catalog.after"} {
		t.Run(stage, func(t *testing.T) {
			s := storeFixture(t)
			w, p := preparePoint(t, s)
			old, err := s.Commit(context.Background(), w.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			path, id := s.path, s.identity.ID
			_ = s.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestStoreCrashHelper$")
			cmd.Env = append(os.Environ(), "IOSBK_STORE_CRASH_PATH="+path, "IOSBK_STORE_CRASH_ID="+id, "IOSBK_STORE_CRASH_STAGE="+stage)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 77 {
				t.Fatalf("helper did not exit at boundary: %v %s", err, output)
			}
			restarted, err := Open(path, "PHONE", id)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if _, err = restarted.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			catalog, err := restarted.Snapshot()
			if err != nil || catalog.Points[old.ID].ID == "" {
				t.Fatal("old point lost", err)
			}
			data, err := os.ReadFile(filepath.Join(path, "objects", old.ID, "PHONE", "Info.plist"))
			if err != nil || string(data) != "original" {
				t.Fatal("old data lost", err)
			}
		})
	}
}
