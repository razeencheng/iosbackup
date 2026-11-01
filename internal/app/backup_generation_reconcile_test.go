package app

import (
	"context"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGenerationPendingCanReconcileSuccessfulBackup(t *testing.T) {
	for _, kind := range []string{"encrypted", "unencrypted"} {
		t.Run(kind, func(t *testing.T) {
			s := generationFixture(t)
			first, err := s.begin("PHONE", "enable", "", "old-password")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.finish("PHONE", first); err != nil {
				t.Fatal(err)
			}
			old, err := s.historyBackupGeneration("PHONE", "encrypted", strings.Repeat("a", 64))
			if err != nil {
				t.Fatal(err)
			}
			change, err := s.begin("PHONE", "change", "old-password", "new-password")
			if err != nil {
				t.Fatal(err)
			}
			before, _ := s.load("PHONE")
			candidate := before.Generations[before.Pending.Target]
			// 模拟操作结果未知后重启；只确认元数据耐久性，不推断手机状态。
			s = &generationStore{root: s.root, secrets: s.secrets, now: time.Now}
			if err = s.recover("PHONE"); err != nil {
				t.Fatal(err)
			}
			keybag := ""
			if kind == "encrypted" {
				keybag = strings.Repeat("b", 64)
			}
			g, err := s.historyBackupGeneration("PHONE", kind, keybag)
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.load("PHONE")
			if err != nil {
				t.Fatal(err)
			}
			if state.Pending != nil || g.Kind != kind || !state.compatibleBaseline(backupstore.Point{State: "normal", Generation: g}, true) {
				t.Fatal("successful backup did not establish independent source evidence", state.Pending, g)
			}
			if len(state.Unresolved) != 1 || state.Unresolved[0].ID != change {
				t.Fatal("uncertain phone operation was discarded")
			}
			if g.CredentialVerified || g.CredentialRef != "" {
				t.Fatal("uncertain password bound to new source")
			}
			for ref, want := range map[string]string{old.CredentialRef: "old-password", candidate.CredentialRef: "new-password"} {
				if got, e := s.password("PHONE", ref); e != nil || got != want {
					t.Fatal("historical credential lost", e)
				}
			}
			again, err := s.historyBackupGeneration("PHONE", kind, keybag)
			if err != nil || again.ID != g.ID {
				t.Fatal("stable evidence created another generation", err)
			}
		})
	}
}

func TestGenerationFailureDoesNotInventPasswordEvidence(t *testing.T) {
	s := generationFixture(t)
	change, err := s.begin("PHONE", "enable", "", "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.historyBackupGeneration("PHONE", "encrypted", ""); err != nil {
		t.Fatal(err)
	}
	state, _ := s.load("PHONE")
	if state.Pending == nil || state.Pending.ID != change {
		t.Fatal("missing keybag resolved unknown operation")
	}
	if err = s.finish("PHONE", change); err != nil {
		t.Fatal(err)
	}
	before, _ := s.load("PHONE")
	verified := before.Generations[before.Current]
	g, err := s.historyBackupGeneration("PHONE", "encrypted", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	state, _ = s.load("PHONE")
	if g.CredentialVerified || !state.Generations[verified.ID].CredentialVerified {
		t.Fatal("inferred password verification or erased old evidence")
	}
}

func TestGenerationCommandFailureBeforeStartEndsPending(t *testing.T) {
	for _, action := range []string{"enable", "change"} {
		t.Run(action, func(t *testing.T) {
			a, _ := newEncTestApp(t, "PHONE")
			s := a.generationRegistry()
			original, err := s.historyBackupGeneration("PHONE", "unencrypted", "")
			if err != nil {
				t.Fatal(err)
			}
			a.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
				return nil, &os.PathError{Op: "fork/exec", Path: cmdIdevicebackup2, Err: syscall.ENOENT}
			}
			if action == "enable" {
				err = a.SetBackupEncryption(context.Background(), "PHONE", true, "new")
			} else {
				err = a.ChangeBackupPassword(context.Background(), "PHONE", "old", "new")
			}
			if err == nil {
				t.Fatal("command failure hidden")
			}
			state, err := s.load("PHONE")
			if err != nil || state.Pending != nil || state.Current != original.ID {
				t.Fatal("unstarted command left pending change", err)
			}
		})
	}
}

func TestGenerationUncertainCommandFailureKeepsPending(t *testing.T) {
	a, _ := newEncTestApp(t, "PHONE")
	a.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("connection lost after request")
	}
	if err := a.ChangeBackupPassword(context.Background(), "PHONE", "old", "new"); err == nil {
		t.Fatal("command failure hidden")
	}
	state, err := a.generationRegistry().load("PHONE")
	if err != nil || state.Pending == nil {
		t.Fatal("uncertain phone change discarded", err)
	}
}

func TestGenerationReconcileRetriesAfterSaveFailure(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_write", true: "after_write"}[afterWrite], func(t *testing.T) {
			s := generationFixture(t)
			id, err := s.begin("PHONE", "change", "old", "new")
			if err != nil {
				t.Fatal(err)
			}
			s.write = func(path string, data []byte, mode os.FileMode) error {
				if afterWrite {
					if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
						return err
					}
				}
				return errors.New("injected registry failure")
			}
			if _, err = s.historyBackupGeneration("PHONE", "encrypted", strings.Repeat("d", 64)); err == nil {
				t.Fatal("unsaved evidence accepted")
			}
			state, _ := s.load("PHONE")
			if state.compatibleBaseline(backupstore.Point{State: "normal", Generation: state.Generations[state.Current]}, true) {
				t.Fatal("uncertain write reused")
			}
			s = &generationStore{root: s.root, secrets: s.secrets, now: time.Now}
			if err = s.recover("PHONE"); err != nil {
				t.Fatal(err)
			}
			g, err := s.historyBackupGeneration("PHONE", "encrypted", strings.Repeat("d", 64))
			if err != nil {
				t.Fatal(err)
			}
			state, err = s.load("PHONE")
			if err != nil {
				t.Fatal(err)
			}
			if state.Pending != nil || len(state.Unresolved) != 1 || state.Unresolved[0].ID != id || g.CredentialVerified || g.CredentialRef != "" {
				t.Fatal("retry lost unresolved operation or inferred password")
			}
		})
	}
}

func TestGenerationReconciledHistoryCanIncrementAfterRestart(t *testing.T) {
	a := historyTestApp(t)
	s := generationFixture(t)
	a.backupGenerations = s
	a.secretStore = s.secrets
	if _, err := s.begin("CONTROL", "change", "old", "new"); err != nil {
		t.Fatal(err)
	}
	var full bool
	encryptedHistoryCommand(t, a, "observed-keybag", &full)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !full {
		t.Fatal("unknown source did not request full")
	}
	a.backupGenerations = &generationStore{root: s.root, secrets: s.secrets, now: time.Now}
	if err := a.backupGenerations.recoverExisting(); err != nil {
		t.Fatal(err)
	}
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if full {
		t.Fatal("reconciled source still requests full after restart")
	}
}
