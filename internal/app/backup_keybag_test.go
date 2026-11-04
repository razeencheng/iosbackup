package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
)

func keybagTLV(tag string, value []byte) []byte {
	b := append([]byte(tag), make([]byte, 4)...)
	binary.BigEndian.PutUint32(b[4:], uint32(len(value)))
	return append(b, value...)
}
func keybagInt(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

// 合成密钥包只用于回归，不包含真实设备材料。包装器与生产校验分开实现。
func testWrappedKey(key, plain []byte) []byte {
	cipher, _ := aes.NewCipher(key)
	r := append(bytes.Repeat([]byte{0xa6}, 8), plain...)
	n := len(plain) / 8
	for j := 0; j < 6; j++ {
		for i := 1; i <= n; i++ {
			block := append(append([]byte{}, r[:8]...), r[i*8:(i+1)*8]...)
			cipher.Encrypt(block, block)
			binary.BigEndian.PutUint64(r[:8], binary.BigEndian.Uint64(block[:8])^uint64(n*j+i))
			copy(r[i*8:], block[8:])
		}
	}
	return r
}
func testBackupKeybag(password string, seed byte, reverse bool) []byte {
	salt, aux := bytes.Repeat([]byte{seed}, 20), bytes.Repeat([]byte{seed + 1}, 20)
	key, _ := pbkdf2.Key(sha256.New, password, aux, 2, 32)
	key, _ = pbkdf2.Key(sha1.New, string(key), salt, 2, 32)
	var bag []byte
	for _, field := range []struct {
		tag   string
		value []byte
	}{
		{"VERS", keybagInt(5)}, {"TYPE", keybagInt(1)}, {"UUID", bytes.Repeat([]byte{seed}, 16)},
		{"HMCK", bytes.Repeat([]byte{seed + 2}, 40)}, {"WRAP", keybagInt(0)}, {"SALT", salt},
		{"ITER", keybagInt(2)}, {"DPWT", keybagInt(1)}, {"DPIC", keybagInt(2)}, {"DPSL", aux},
	} {
		bag = append(bag, keybagTLV(field.tag, field.value)...)
	}
	classes := []uint32{1, 19}
	if reverse {
		classes[0], classes[1] = classes[1], classes[0]
	}
	for _, class := range classes {
		wrap := uint32(2)
		if class == 19 {
			wrap = 3 // 外层仍由密码包装，内部设备密钥层不参与本功能。
		}
		for _, field := range []struct {
			tag   string
			value []byte
		}{
			{"UUID", bytes.Repeat([]byte{seed + byte(class)}, 16)}, {"CLAS", keybagInt(class)},
			{"WRAP", keybagInt(wrap)}, {"KTYP", keybagInt(0)},
			{"WPKY", testWrappedKey(key, bytes.Repeat([]byte{seed + byte(class)}, 32))},
		} {
			bag = append(bag, keybagTLV(field.tag, field.value)...)
		}
	}
	return bag
}

func TestEncryptedRetentionIgnoresKeybagEntryOrder(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-password", true: "with-password"}[saved], func(t *testing.T) {
			a := historyTestApp(t)
			a.configs["CONTROL"].History.Count = 1
			if saved {
				installKeybagTestPassword(t, a, "password")
			}
			var full bool
			for i := 0; i < 3; i++ {
				encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, i%2 != 0)), &full)
				if err := a.PerformBackup("CONTROL"); err != nil {
					t.Fatal(err)
				}
				if full != (i == 0) {
					t.Fatalf("round %d unexpected full=%t", i, full)
				}
				if got := len(historyPoints(t, a)); got != 1 {
					t.Fatalf("entry reordering bypassed retention: round=%d points=%d", i, got)
				}
				for _, p := range historyPoints(t, a) {
					if p.Generation.CredentialVerified {
						t.Fatal("backup elevated password evidence")
					}
				}
			}
		})
	}
}

func installKeybagTestPassword(t *testing.T, a *application, pw string) {
	t.Helper()
	s, err := newAESSecretStore(testKey32(), filepath.Join(a.paths.ConfigsRoot, "keybag-secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetBackupPassword("CONTROL", pw); err != nil {
		t.Fatal(err)
	}
	a.secretStore, a.backupGenerations.secrets = s, s
}

func TestEncryptedRetentionAcrossFullWithSamePassword(t *testing.T) {
	a := historyTestApp(t)
	a.configs["CONTROL"].History.Count = 1
	installKeybagTestPassword(t, a, "password")
	for i := 0; i < 3; i++ {
		encryptedHistoryCommand(t, a, string(testBackupKeybag("password", byte(10+i), false)), nil)
		job, err := a.beginBackupJob("CONTROL", "", "", false, backupRequest{Mode: "full"})
		if err != nil {
			t.Fatal(err)
		}
		if err = a.runBackupJob(job); err != nil {
			t.Fatal(err)
		}
		if got := len(historyPoints(t, a)); got != 1 {
			t.Fatalf("same-password full backup bypassed retention: round=%d points=%d", i, got)
		}
	}
}

// RFC 3394 §4.6 是独立于本文件包装器的已知向量。
func TestBackupKeyWrapIntegrityRFC3394(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090A0B0C0D0E0F101112131415161718191A1B1C1D1E1F")
	wrapped, _ := hex.DecodeString("28C9F404C4B810F4CBCCB35CFB87F8263F5786E2D80ED326CBC7F0E71A99F43BFB988B9B7A02DD21")
	if !backupWrappedKeyMatches(key, wrapped) {
		t.Fatal("independent RFC vector rejected")
	}
	wrapped[9] ^= 1
	if backupWrappedKeyMatches(key, wrapped) {
		t.Fatal("tampered wrapped key accepted")
	}
}

func TestBackupKeybagPasswordProof(t *testing.T) {
	bag := testBackupKeybag("password", 10, true)
	if !backupKeybagPasswordMatches(context.Background(), bag, "password") {
		t.Fatal("valid password rejected")
	}
	if backupKeybagPasswordMatches(context.Background(), bag, "wrong") {
		t.Fatal("wrong password accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if backupKeybagPasswordMatches(ctx, bag, "password") {
		t.Fatal("cancelled proof accepted")
	}
	for _, bad := range [][]byte{nil, bag[:len(bag)-1], append(append([]byte{}, bag...), keybagTLV("UNKN", []byte{1})...)} {
		if backupKeybagPasswordMatches(context.Background(), bad, "password") {
			t.Fatal("unsupported keybag accepted")
		}
	}
}

func TestEncryptedRetentionProtectsUnprovenPasswordGeneration(t *testing.T) {
	for _, reason := range []string{"missing-password", "wrong-password", "changed-password", "pending-change", "changed-source"} {
		t.Run(reason, func(t *testing.T) {
			a := historyTestApp(t)
			a.configs["CONTROL"].History.Count = 1
			if reason != "missing-password" {
				pw := "password"
				if reason == "wrong-password" {
					pw = "wrong"
				}
				installKeybagTestPassword(t, a, pw)
			}
			encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, false)), nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			var first backupstore.Point
			for _, p := range historyPoints(t, a) {
				first = p
			}
			if reason == "pending-change" {
				if _, err := a.generationRegistry().begin("CONTROL", "change", "password", "candidate"); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "changed-source" {
				s, _ := a.backupStorage.store(first.StoreID)
				lease, err := s.Acquire(first.ID)
				if err != nil {
					t.Fatal(err)
				}
				f, err := lease.Root.OpenFile("Manifest.plist", os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`)
				f.Close()
				lease.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			pw := "password"
			if reason == "changed-password" {
				pw = "other-password"
			}
			encryptedHistoryCommand(t, a, string(testBackupKeybag(pw, 20, false)), nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			points := historyPoints(t, a)
			if len(points) != 2 || points[first.ID].ID == "" {
				t.Fatal("unproven old generation was collected")
			}
			state, err := a.generationRegistry().load("CONTROL")
			if err != nil {
				t.Fatal(err)
			}
			if state.Current == first.Generation.ID {
				t.Fatal("unproven keybags merged")
			}
		})
	}
}

func TestEncryptedRetentionRestartAndFallbackKeepPointEvidence(t *testing.T) {
	a := historyTestApp(t)
	installKeybagTestPassword(t, a, "password")
	encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, false)), nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	st, _ := a.backupStorage.deviceState("CONTROL")
	first := st.LatestPoint
	encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 20, true)), nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	old := a.generationRegistry()
	a.backupGenerations = &generationStore{root: old.root, secrets: old.secrets, now: old.now}
	if err := a.backupGenerations.recoverExisting(); err != nil {
		t.Fatal(err)
	}
	st, _ = a.backupStorage.deviceState("CONTROL")
	release, err := a.acquireHistoryMutation("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := a.backupStorage.store(st.LatestStore)
	c, _ := s.Snapshot()
	state, _ := a.generationRegistry().load("CONTROL")
	st.PointDeletion = &historyPointDeletion{StoreID: st.LatestStore, PointID: st.LatestPoint, Revision: c.Revision, Generation: state.Generations[state.Current], AllowFallback: true}
	st, err = a.backupStorage.finishHistoryPointDeletion(context.Background(), "CONTROL", st)
	release()
	if err != nil || st.LatestPoint != first {
		t.Fatal("same-password older point not selected after deletion", err)
	}
	var full bool
	encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, true)), &full)
	if err = a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if full {
		t.Fatal("older point lost its own compatible source evidence")
	}
	for _, p := range historyPoints(t, a) {
		if p.Generation.ID != state.Current {
			t.Fatal("fallback incorrectly started a new generation")
		}
	}
}

func TestContinuedGenerationSaveFailurePreservesOldPoint(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-write", true: "after-write"}[afterWrite], func(t *testing.T) {
			a := historyTestApp(t)
			a.configs["CONTROL"].History.Count = 1
			encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, false)), nil)
			if err := a.PerformBackup("CONTROL"); err != nil {
				t.Fatal(err)
			}
			var first backupstore.Point
			for _, p := range historyPoints(t, a) {
				first = p
			}
			a.generationRegistry().write = func(path string, b []byte, mode os.FileMode) error {
				var state backupGenerations
				if err := json.Unmarshal(b, &state); err != nil {
					return err
				}
				if state.Generations[state.Current].KeybagDigest != first.Generation.KeybagDigest {
					if afterWrite {
						if err := persistence.WriteFileAtomic(path, b, mode); err != nil {
							return err
						}
					}
					return errors.New("injected generation save failure")
				}
				return persistence.WriteFileAtomic(path, b, mode)
			}
			encryptedHistoryCommand(t, a, string(testBackupKeybag("password", 10, true)), nil)
			_ = a.PerformBackup("CONTROL")
			if p := historyPoints(t, a)[first.ID]; p.ID == "" || p.Generation != first.Generation {
				t.Fatal("failed persistence changed old point")
			}
		})
	}
}

func TestKeybagParserRejectsAmbiguousOrUnboundedEvidence(t *testing.T) {
	bag := testBackupKeybag("password", 10, false)
	for _, data := range [][]byte{nil, bag[:7], bag[:len(bag)-1], bytes.Repeat([]byte{0}, 65537), append(bytes.Clone(bag), keybagTLV("CLAS", keybagInt(2))...), append(bytes.Clone(bag), keybagTLV("UNKN", []byte{0})...)} {
		if _, ok := parseBackupKeybag(data); ok {
			t.Fatal("ambiguous structure accepted")
		}
	}
	a, ok := parseBackupKeybag(bag)
	if !ok {
		t.Fatal("fixture invalid")
	}
	b, _ := parseBackupKeybag(testBackupKeybag("password", 10, true))
	if a.identity() != b.identity() {
		t.Fatal("reordering changed structural identity")
	}
	b.keys[0]["WPKY"], b.keys[1]["WPKY"] = b.keys[1]["WPKY"], b.keys[0]["WPKY"]
	if a.identity() == b.identity() {
		t.Fatal("class/key association ignored")
	}
	for _, tag := range []string{"ITER", "DPIC"} {
		bad := bytes.Clone(bag)
		offset := bytes.Index(bad, []byte(tag))
		binary.BigEndian.PutUint32(bad[offset+8:], 0xffffffff)
		start := time.Now()
		if backupKeybagPasswordMatches(context.Background(), bad, "password") {
			t.Fatal("excessive work accepted")
		}
		if time.Since(start) > time.Second {
			t.Fatal("unbounded derivation")
		}
	}
}

func TestKeybagPasswordProofRejectsPartiallySupportedClasses(t *testing.T) {
	for _, tag := range []string{"KTYP", "WRAP"} {
		bag := testBackupKeybag("password", 10, false)
		start := bytes.Index(bag, []byte("CLAS"))
		offset := start + bytes.Index(bag[start:], []byte(tag))
		binary.BigEndian.PutUint32(bag[offset+8:], 0xffffffff)
		wrapped := start + bytes.Index(bag[start:], []byte("WPKY"))
		bag[wrapped+8] ^= 1
		if backupKeybagPasswordMatches(context.Background(), bag, "password") {
			t.Fatal("partially verifiable bag accepted", tag)
		}
	}
}

func FuzzBackupKeybag(f *testing.F) {
	f.Add(testBackupKeybag("password", 10, false))
	f.Add([]byte("UUID\x00\x00\x00\xff"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if bag, ok := parseBackupKeybag(data); ok {
			_ = bag.identity()
		}
	})
}
