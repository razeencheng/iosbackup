package app

import (
	"context"
	"encoding/json"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generationFixture(t *testing.T) *generationStore {
	t.Helper()
	dir := t.TempDir()
	sec, err := newAESSecretStore(testKey32(), filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	return &generationStore{root: filepath.Join(dir, "generations"), secrets: sec, now: time.Now}
}
func TestBackupGenerationsLegacyIsReadOnly(t *testing.T) {
	s := generationFixture(t)
	if err := s.secrets.SetBackupPassword("PHONE", "old-test-password"); err != nil {
		t.Fatal(err)
	}
	state, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "" || len(state.Generations) != 0 || state.Pending != nil {
		t.Fatalf("legacy inferred: %+v", state)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatal("read created registry")
	}
}
func TestBackupGenerationsPreserveCredentials(t *testing.T) {
	s := generationFixture(t)
	if err := s.secrets.SetBackupPassword("PHONE", "old-test-password"); err != nil {
		t.Fatal(err)
	}
	change, err := s.begin("PHONE", "change", "old-test-password", "new-test-password")
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	old := state.Generations[state.Current]
	if old.Kind != "encrypted" || old.CredentialRef == "" {
		t.Fatalf("old generation missing: %+v", old)
	}
	if err := s.finish("PHONE", change); err != nil {
		t.Fatal(err)
	}
	state, err = s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if state.Current == old.ID || state.Pending != nil {
		t.Fatal("new generation not selected")
	}
	if got, err := s.password("PHONE", old.CredentialRef); err != nil || got != "old-test-password" {
		t.Fatalf("old password lost: %v", err)
	}
	next, err := s.begin("PHONE", "disable", "new-test-password", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.finish("PHONE", next); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.GetBackupPassword("PHONE"); !errors.Is(err, errPasswordNotFound) {
		t.Fatalf("legacy alias not removed: %v", err)
	}
	if got, err := s.password("PHONE", state.Generations[state.Current].CredentialRef); err != nil || got != "new-test-password" {
		t.Fatal("disable lost historical password")
	}
	data, err := os.ReadFile(filepath.Join(s.root, "PHONE.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "test-password") {
		t.Fatal("plaintext in metadata")
	}
}
func TestBackupGenerationsFailureAndRestart(t *testing.T) {
	s := generationFixture(t)
	id, err := s.begin("PHONE", "enable", "", "new-test-password")
	if err != nil {
		t.Fatal(err)
	}
	restart := &generationStore{root: s.root, secrets: s.secrets, now: time.Now}
	state, err := restart.load("PHONE")
	if err != nil || state.Pending == nil {
		t.Fatal("lost uncertain intent", err)
	}

	s.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if err := s.finish("PHONE", id); err == nil {
		t.Fatal("accepted metadata failure")
	}
	state, err = restart.load("PHONE")
	if err != nil || state.Pending == nil {
		t.Fatal("lost recoverable intent", err)
	}
	if err := restart.finish("PHONE", id); err != nil {
		t.Fatal(err)
	}
}
func TestBackupGenerationsPreflightFailure(t *testing.T) {
	s := generationFixture(t)
	s.secrets = newUnavailableSecretStore(filepath.Join(t.TempDir(), "missing"))
	if _, err := s.begin("PHONE", "change", "old", "new"); !errors.Is(err, errEncryptionUnavailable) {
		t.Fatal(err)
	}
	s = generationFixture(t)
	s.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := s.begin("PHONE", "enable", "", "new"); err == nil {
		t.Fatal("accepted unsaved intent")
	}
}
func TestBackupGenerationsRejectMalformedState(t *testing.T) {
	s := generationFixture(t)
	if err := os.MkdirAll(s.root, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"schema": 999, "udid": "PHONE"})
	if err := os.WriteFile(filepath.Join(s.root, "PHONE.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load("PHONE"); err == nil {
		t.Fatal("accepted future metadata")
	}
	if _, err := s.load("../PHONE"); err == nil {
		t.Fatal("accepted path traversal")
	}
}

func TestBackupGenerationsNewGenerationRequiresFull(t *testing.T) {
	s := generationFixture(t)
	id, err := s.begin("PHONE", "enable", "", "old")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finish("PHONE", id); err != nil {
		t.Fatal(err)
	}
	old, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	p := backupstore.Point{State: "normal", Generation: old.Generations[old.Current]}
	if !old.compatibleBaseline(p, true) || old.compatibleBaseline(p, false) {
		t.Fatal("known metadata boundary not enforced")
	}
	id, err = s.begin("PHONE", "change", "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finish("PHONE", id); err != nil {
		t.Fatal(err)
	}
	next, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if next.compatibleBaseline(p, true) {
		t.Fatal("reused previous generation baseline")
	}
}

func TestBackupGenerationsConfigDefaults(t *testing.T) {
	root := t.TempDir()
	store := newBackupConfigStore(filepath.Join(root, "config.json"), []string{root})
	for _, days := range []int{0, 7} {
		cfg := backupConfig{UDID: "PHONE", StartTime: "22:00", EndTime: "06:00", BackupInterval: 24, BackupDirectory: root}
		cfg.History.Days = days
		if err := store.Put(cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		want := days
		if want == 0 {
			want = 3
		}
		if loaded["PHONE"].History.Enabled || loaded["PHONE"].History.Days != want {
			t.Fatalf("defaults overwrote policy: %+v", loaded["PHONE"].History)
		}
	}
}

func TestBackupGenerationsEncryptionPreflightStopsPhone(t *testing.T) {
	app, runner := newEncTestApp(t, "PHONE")
	registry := app.generationRegistry()
	registry.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if err := app.ChangeBackupPassword(context.Background(), "PHONE", "old", "new"); err == nil {
		t.Fatal("unsaved credentials accepted")
	}
	if _, ok := runner.lastCall(); ok {
		t.Fatal("phone changed before credentials saved")
	}
}

func TestBackupGenerationsRetryKeepsUnresolvedAndSubmittedPassword(t *testing.T) {
	s := generationFixture(t)
	if err := s.secrets.SetBackupPassword("PHONE", "stale-alias"); err != nil {
		t.Fatal(err)
	}
	first, err := s.begin("PHONE", "change", "actual-old", "new")
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.password("PHONE", state.Pending.OldCredentialRef); err != nil || got != "actual-old" {
		t.Fatal("submitted password not preserved", err)
	}
	second, err := s.begin("PHONE", "change", "actual-old", "new")
	if err != nil {
		t.Fatal("retry blocked", err)
	}
	state, err = s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Unresolved) != 1 || state.Unresolved[0].ID != first || state.Pending.ID != second {
		t.Fatal("lost unresolved intent")
	}
	if err = s.finish("PHONE", second); err != nil {
		t.Fatal(err)
	}
	third, err := s.begin("PHONE", "disable", "externally-changed", "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	ref := state.Pending.OldCredentialRef
	if err = s.finish("PHONE", third); err != nil {
		t.Fatal(err)
	}
	if got, err := s.password("PHONE", ref); err != nil || got != "externally-changed" {
		t.Fatal("external password lost", err)
	}
}

func TestBackupGenerationsRenameThenSyncFailure(t *testing.T) {
	s := generationFixture(t)
	id, err := s.begin("PHONE", "enable", "", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s.write = func(path string, data []byte, mode os.FileMode) error {
		if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
			return err
		}
		return errors.New("directory sync failed")
	}
	if err = s.finish("PHONE", id); err == nil {
		t.Fatal("uncertain write accepted")
	}
	state, err := s.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	p := backupstore.Point{State: "normal", Generation: state.Generations[state.Current]}
	if state.compatibleBaseline(p, true) {
		t.Fatal("uncertain metadata reused")
	}
	restart := &generationStore{root: s.root, secrets: s.secrets, now: time.Now}
	state, err = restart.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	if state.compatibleBaseline(p, true) {
		t.Fatal("restart skipped durability recovery")
	}
	if err = restart.recover("PHONE"); err != nil {
		t.Fatal(err)
	}
	state, err = restart.load("PHONE")
	if err != nil || !state.compatibleBaseline(p, true) {
		t.Fatal("recovered visible confirmation not usable", err)
	}
}

func TestBackupGenerationsRemovalRetainsReferences(t *testing.T) {
	app, _ := newDeviceRemovalTestApp(t, "PHONE")
	secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = secrets
	registry := app.generationRegistry()
	id, err := registry.begin("PHONE", "enable", "", "kept-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.finish("PHONE", id); err != nil {
		t.Fatal(err)
	}
	before, err := registry.load("PHONE")
	if err != nil {
		t.Fatal(err)
	}
	ref := before.Generations[before.Current].CredentialRef
	if _, err = app.removeDevice("PHONE"); err != nil {
		t.Fatal(err)
	}
	if value, err := registry.password("PHONE", ref); err != nil || value != "kept-password" {
		t.Fatal("removed historical credential", err)
	}
	after, err := registry.load("PHONE")
	if err != nil || before.Current != after.Current {
		t.Fatal("changed generation during removal", err)
	}
}
