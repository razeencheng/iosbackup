package app

import (
	"context"
	"iosbackup/internal/backupstore"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupHistoryInfoUsesPointPassword(t *testing.T) {
	app := historyTestApp(t)
	secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "review-secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = secrets
	app.backupGenerations.secrets = secrets
	var observed string
	app.cmdRunner = func(_ context.Context, _ string, args, env []string) ([]byte, error) {
		if argsHas(args, "info") {
			observed = envGet(env, "BACKUP_PASSWORD")
		}
		return nil, nil
	}
	if err = app.SetBackupEncryption(context.Background(), "CONTROL", true, "old-test-password"); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "finished", "first", 0, nil)
	command := app.backupCommand
	app.backupCommand = func(ctx context.Context, name string, args ...string) *execCmd {
		cmd := command(ctx, name, args...)
		path := filepath.Join(args[len(args)-1], "CONTROL", "Manifest.plist")
		if err := os.WriteFile(path, []byte(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`), 0600); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	if err = app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err = app.ChangeBackupPassword(context.Background(), "CONTROL", "old-test-password", "new-test-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = app.BackupInfo(context.Background(), "CONTROL"); err != nil {
		t.Fatal(err)
	}
	if observed != "old-test-password" {
		t.Fatalf("history info used current device password %q instead of point password", observed)
	}
}

func TestBackupHistoryLegacyCredentialPreservedWithoutAssumingCompatibility(t *testing.T) {
	app := historyTestApp(t)
	secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "legacy-secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = secrets
	app.backupGenerations.secrets = secrets
	if err = secrets.SetBackupPassword("CONTROL", "legacy-password"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(app.paths.BackupsRoot, "CONTROL")
	writeHistoryBackup(t, dir, "finished", "legacy")
	if err = os.WriteFile(filepath.Join(dir, "Manifest.plist"), []byte(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	app.configs["CONTROL"].LastBackup = time.Now().AddDate(0, 0, -1)
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	var point backupstore.Point
	for _, p := range historyPoints(t, app) {
		point = p
	}
	if point.Generation.CredentialRef == "" {
		t.Fatal("legacy password not versioned")
	}
	if err = secrets.SetBackupPassword("CONTROL", "new-alias"); err != nil {
		t.Fatal(err)
	}
	pw, err := app.generationRegistry().password("CONTROL", point.Generation.CredentialRef)
	if err != nil || pw != "legacy-password" {
		t.Fatal("legacy reference lost", err)
	}
	state, err := app.generationRegistry().load("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	if state.compatibleBaseline(point, true) {
		t.Fatal("unverified legacy password treated as compatible")
	}
}

func TestBackupHistoryImportDoesNotAssumeCurrentPasswordGeneration(t *testing.T) {
	app := historyTestApp(t)
	secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "import-secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = secrets
	app.backupGenerations.secrets = secrets
	app.cmdRunner = (&mockRunner{}).run
	if err = app.SetBackupEncryption(context.Background(), "CONTROL", true, "old-password"); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(app.paths.BackupsRoot, "CONTROL")
	writeHistoryBackup(t, legacy, "finished", "legacy")
	if err = os.WriteFile(filepath.Join(legacy, "Manifest.plist"), []byte(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = app.ChangeBackupPassword(context.Background(), "CONTROL", "old-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	current, err := app.generationRegistry().load("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range historyPoints(t, app) {
		if point.Generation.ID == current.Current || point.Generation.CredentialVerified || current.compatibleBaseline(point, true) {
			t.Fatal("import assigned current password generation without verification")
		}
	}
}
