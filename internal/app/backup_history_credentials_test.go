package app

import (
	"context"
	"encoding/base64"
	"iosbackup/internal/backupstore"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func encryptedHistoryCommand(t *testing.T, app *application, keybag string, full *bool) {
	t.Helper()
	historyCommand(t, app, "finished", "encrypted payload", 0, nil)
	command := app.backupCommand
	app.backupCommand = func(ctx context.Context, name string, args ...string) *execCmd {
		if full != nil {
			*full = argsHas(args, "--full")
		}
		cmd := command(ctx, name, args...)
		manifest := `<plist><dict><key>IsEncrypted</key><true/><key>BackupKeyBag</key><data>` + base64.StdEncoding.EncodeToString([]byte(keybag)) + `</data></dict></plist>`
		if err := os.WriteFile(filepath.Join(args[len(args)-1], "CONTROL", "Manifest.plist"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
}

func TestBackupHistoryEncryptedCandidateDoesNotRequireHostPassword(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-password", true: "saved-password"}[saved], func(t *testing.T) {
			app := historyTestApp(t)
			secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "candidate-secrets.enc"))
			if err != nil {
				t.Fatal(err)
			}
			app.secretStore = secrets
			app.backupGenerations.secrets = secrets
			if saved {
				if err = secrets.SetBackupPassword("CONTROL", "candidate-password"); err != nil {
					t.Fatal(err)
				}
			}
			var full bool
			encryptedHistoryCommand(t, app, "first-keybag", &full)
			if err = app.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			if !full {
				t.Fatal("first backup must request full")
			}
			if err = app.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			if full {
				t.Fatal("successful encrypted source was not offered as incremental candidate")
			}
			for _, p := range historyPoints(t, app) {
				if p.Generation.CredentialVerified {
					t.Fatal("backup success incorrectly verified a password")
				}
			}
			registry := app.backupGenerations
			app.backupGenerations = &generationStore{root: registry.root, secrets: registry.secrets, now: registry.now}
			if err = app.backupGenerations.recoverExisting(); err != nil {
				t.Fatal(err)
			}
			if err = app.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			if full {
				t.Fatal("restart lost the durable incremental candidate")
			}
		})
	}
}

func TestBackupHistoryEncryptedCandidateRejectsChangedMetadata(t *testing.T) {
	app := historyTestApp(t)
	var full bool
	encryptedHistoryCommand(t, app, "saved-keybag", &full)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	st, err := app.backupStorage.deviceState("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	store, err := app.backupStorage.store(st.LatestStore)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.Acquire(st.LatestPoint)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟应用外修改，旧描述记录不能把被替换的密钥包当成已确认来源。
	f, err := lease.Root.OpenFile("Manifest.plist", os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`<plist><dict><key>IsEncrypted</key><true/><key>BackupKeyBag</key><data>Y2hhbmdlZA==</data></dict></plist>`)
	f.Close()
	lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !full {
		t.Fatal("changed encryption metadata reused for incremental")
	}
}

func TestBackupHistoryEncryptionEvidenceRotatesWithoutOverwritingOldPoint(t *testing.T) {
	app := historyTestApp(t)
	encryptedHistoryCommand(t, app, "first-keybag", nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	var old backupstore.Point
	for _, p := range historyPoints(t, app) {
		old = p
	}
	store, _ := app.backupStorage.store(old.StoreID)
	c, _ := store.Snapshot()
	if err := store.SetPinned(context.Background(), old.ID, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	encryptedHistoryCommand(t, app, "changed-keybag", nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	state, err := app.generationRegistry().load("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	if state.Current == old.Generation.ID {
		t.Fatal("different encryption material was grouped with the old source")
	}
	if state.compatibleBaseline(old, true) {
		t.Fatal("old generation is still a current candidate")
	}
	lease, err := store.Acquire(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	manifest, err := readBackupCheckPlist(context.Background(), lease.Root, "Manifest.plist", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest["BackupKeyBag"].([]byte)) != "first-keybag" {
		t.Fatal("old point changed")
	}
}

func TestBackupHistoryImportRechecksWritableKeybag(t *testing.T) {
	app := historyTestApp(t)
	old, err := app.generationRegistry().historyBackupGeneration("CONTROL", "encrypted", historyKeybagDigest(map[string]any{"BackupKeyBag": []byte("old")}))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "CONTROL")
	writeHistoryBackup(t, dir, "finished", "payload")
	if err = os.WriteFile(filepath.Join(dir, "Manifest.plist"), []byte(`<plist><dict><key>IsEncrypted</key><true/><key>BackupKeyBag</key><data>bmV3</data></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := app.importHistoryGeneration(context.Background(), "CONTROL", dir, historyDeviceState{WritableID: "existing", WritableGeneration: old})
	if err != nil {
		t.Fatal(err)
	}
	if imported.ID == old.ID {
		t.Fatal("changed writable source inherited old encryption evidence")
	}
}

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
