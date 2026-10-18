package app

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 合成 fixture 仅验证格式和边界，不代表真实设备备份已验证。
func writeIndexFixture(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestBackupIndexPlistTypedValuesAndBinaryAdapter(t *testing.T) {
	xml := `<?xml version="1.0"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>IsEncrypted</key><false/><key>Device Name</key><string>A &amp; B</string><key>Nested</key><array><integer>2</integer><data>YQ==</data></array></dict></plist>`
	for _, binary := range []bool{false, true} {
		input := []byte(xml)
		calls := 0
		converter := func(ctx context.Context, b []byte) ([]byte, error) {
			calls++
			if string(b) != "bplist00synthetic" {
				t.Fatal("binary adapter input")
			}
			return []byte(xml), nil
		}
		if binary {
			input = []byte("bplist00synthetic")
		}
		values, err := parseBackupPlist(context.Background(), input, converter)
		if err != nil || values["IsEncrypted"] != false || values["Device Name"] != "A & B" {
			t.Fatalf("values=%v err=%v", values, err)
		}
		if calls != map[bool]int{false: 0, true: 1}[binary] {
			t.Fatal("unexpected converter call")
		}
	}
}
func TestBackupIndexPlistRejectsMalformedLimitsAndCancellation(t *testing.T) {
	for _, input := range []string{`<plist><dict><key>x</key></dict></plist>`, `<plist><dict><key>x</key><true/><key>x</key><false/></dict></plist>`, `<plist><dict><key>x</key><string>unterminated`, `<plist><dict></dict></plist><other/>`, strings.Repeat("x", backupIndexMaxPlistBytes+1), `<plist>` + strings.Repeat("<array>", 40) + strings.Repeat("</array>", 40) + `</plist>`} {
		if _, err := parseBackupPlist(context.Background(), []byte(input), nil); err == nil {
			t.Fatalf("accepted malformed/oversized input length=%d", len(input))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseBackupPlist(ctx, []byte(`<plist><dict/></plist>`), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	_, err := parseBackupPlist(context.Background(), []byte("bplist00synthetic"), func(context.Context, []byte) ([]byte, error) { return make([]byte, backupIndexMaxPlistBytes+1), nil })
	if !errors.Is(err, errBackupIndexLimit) {
		t.Fatalf("converted size limit: %v", err)
	}
}
func TestBackupIndexOverviewOfflineUnknownAndExplicitFalse(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict><key>IsEncrypted</key><false/></dict></plist>`)
	writeIndexFixture(t, dir, "Info.plist", `<plist><dict><key>Device Name</key><string>Phone</string><key>Last Backup Date</key><date>2026-10-16T02:03:04Z</date></dict></plist>`)
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readBackupIndexOverview(context.Background(), src, nil)
	if err != nil || got.Encrypted == nil || *got.Encrypted || got.SizeBytes == nil || *got.SizeBytes == 0 || got.DeviceName != "Phone" || got.BackupDate == nil || got.ReadAt.IsZero() {
		t.Fatalf("overview=%+v err=%v", got, err)
	}
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict/></plist>`)
	got, err = readBackupIndexOverview(context.Background(), src, nil)
	if err != nil || got.Encrypted != nil {
		t.Fatalf("absence is unknown: %+v %v", got, err)
	}
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict><key>IsEncrypted</key><string>false</string></dict></plist>`)
	if _, err := readBackupIndexOverview(context.Background(), src, nil); !errors.Is(err, errBackupIndexCorrupt) {
		t.Fatalf("wrong type: %v", err)
	}
}
func TestBackupIndexOverviewNoMetadataIsNotEmptyBackup(t *testing.T) {
	app, _ := indexSourceTestApp(t)
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readBackupIndexOverview(context.Background(), src, nil); !errors.Is(err, errBackupIndexCorrupt) {
		t.Fatalf("partial backup: %v", err)
	}
}
func TestBackupIndexManifestEncryptionCorruptionAndRecords(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	streamer := func(ctx context.Context, p string, fn func(backupManifestRow) error) error {
		called = true
		return fn(backupManifestRow{FileID: "id", Domain: "HomeDomain", RelativePath: "a,\"b\"\r\nc", Flags: 99})
	}
	encrypted := true
	if _, err := streamLocalBackupIndex(context.Background(), src, &encrypted, streamer, func(backupIndexRecord) error { return nil }); !errors.Is(err, errBackupIndexEncrypted) || called {
		t.Fatalf("encrypted: called=%v err=%v", called, err)
	}
	writeIndexFixture(t, dir, "Manifest.db", "not sqlite")
	encrypted = false
	if _, err := streamLocalBackupIndex(context.Background(), src, &encrypted, streamer, func(backupIndexRecord) error { return nil }); !errors.Is(err, errBackupIndexCorrupt) || called {
		t.Fatalf("corrupt: called=%v err=%v", called, err)
	}
	writeIndexFixture(t, dir, "Manifest.db", "SQLite format 3\x00synthetic")
	n, err := streamLocalBackupIndex(context.Background(), src, nil, streamer, func(r backupIndexRecord) error {
		if r.RelativePath != "a,\"b\"\r\nc" || r.Flags != 99 {
			t.Fatalf("record=%+v", r)
		}
		return nil
	})
	if err != nil || n != 1 || !called {
		t.Fatalf("count=%d err=%v", n, err)
	}
	n, err = streamLocalBackupIndex(context.Background(), src, nil, func(context.Context, string, func(backupManifestRow) error) error { return nil }, func(backupIndexRecord) error { return nil })
	if err != nil || n != 0 {
		t.Fatalf("empty manifest=%d %v", n, err)
	}
}
func TestBackupIndexCSVPreservesFilenameAndLimits(t *testing.T) {
	var input strings.Builder
	w := csv.NewWriter(&input)
	want := "a,\"b\"\r\nc\nend"
	_ = w.Write([]string{"id", "domain", want, "99"})
	w.Flush()
	seen := 0
	err := readBackupManifestCSV(context.Background(), strings.NewReader(input.String()), func(r backupManifestRow) error {
		seen++
		if r.RelativePath != want || r.Flags != 99 {
			t.Fatalf("row=%+v", r)
		}
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatalf("CSV=%d %v", seen, err)
	}
	for _, input := range []string{"id,domain,\"" + strings.Repeat("x", backupIndexMaxRecordBytes) + "\",1\n", "id,domain,\"unterminated", "id,domain,p,invalid\n", "id,domain,p,1,extra\n"} {
		if err := readBackupManifestCSV(context.Background(), strings.NewReader(input), func(backupManifestRow) error { return nil }); err == nil {
			t.Fatalf("accepted invalid CSV length=%d", len(input))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := readBackupManifestCSV(ctx, strings.NewReader(input.String()), func(backupManifestRow) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestBackupIndexOverviewUsesPublishedStateNames(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict><key>IsEncrypted</key><true/></dict></plist>`)
	source, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	result, err := readBackupIndexOverview(context.Background(), source, nil)
	if err != nil || result.Status != "available" || result.Freshness != "current" {
		t.Fatalf("overview state=%+v err=%v", result, err)
	}
}

func TestBackupIndexOverviewFailureCannotClaimCurrent(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict><key>IsEncrypted</key><true/></dict></plist>`)
	source, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	result, err := readBackupIndexOverview(context.Background(), source, nil)
	if err == nil || result.Status != "unavailable" || result.Freshness != "unknown" || result.ReadAt.IsZero() == false || result.MessageCode == "" {
		t.Fatalf("failed overview=%+v err=%v", result, err)
	}
	if result.Encrypted == nil || !*result.Encrypted {
		t.Fatal("earlier parsed encryption metadata should remain available for list classification")
	}
}

func TestBackupIndexReaderRejectsReplacedSourceSymlinkBeforeReading(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	writeIndexFixture(t, dir, "Manifest.plist", `<plist><dict><key>IsEncrypted</key><true/></dict></plist>`)
	source, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"-old", dir); err != nil {
		t.Fatal(err)
	}
	result, err := readBackupIndexOverview(context.Background(), source, nil)
	if !errors.Is(err, errBackupIndexUnsafeSource) || result.Encrypted != nil {
		t.Fatalf("must not read through replaced source: %+v %v", result, err)
	}
	encrypted := true
	if _, err := streamLocalBackupIndex(context.Background(), source, &encrypted, nil, func(backupIndexRecord) error { return nil }); !errors.Is(err, errBackupIndexUnsafeSource) {
		t.Fatalf("must reject replaced source before classifying manifest: %v", err)
	}
}

func TestBackupIndexMissingPlistToolIsUnavailable(t *testing.T) {
	_, err := convertBackupBinaryPlistCommand(context.Background(), filepath.Join(t.TempDir(), "absent-tool"), []byte("bplist00synthetic"))
	if !errors.Is(err, errBackupIndexUnavailable) || errors.Is(err, errBackupIndexCorrupt) {
		t.Fatalf("missing dependency is not corrupt backup: %v", err)
	}
}

func TestBackupIndexOverviewAnchorsDirectoryDuringConversion(t *testing.T) {
	app, dir := indexSourceTestApp(t)
	writeIndexFixture(t, dir, "Manifest.plist", "bplist00synthetic")
	writeIndexFixture(t, dir, "Info.plist", `<plist><dict><key>Device Name</key><string>original</string></dict></plist>`)
	outside := t.TempDir()
	writeIndexFixture(t, outside, "Info.plist", `<plist><dict><key>Device Name</key><string>outside</string></dict></plist>`)
	source, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	converter := func(context.Context, []byte) ([]byte, error) {
		if err := os.Rename(dir, dir+"-original"); err != nil {
			return nil, err
		}
		if err := os.Symlink(outside, dir); err != nil {
			return nil, err
		}
		return []byte(`<plist><dict><key>IsEncrypted</key><false/></dict></plist>`), nil
	}
	result, err := readBackupIndexOverview(context.Background(), source, converter)
	if err != nil || result.DeviceName != "original" {
		t.Fatalf("scan followed replaced ancestor: %+v %v", result, err)
	}
	if _, err := backupIndexSourceFingerprint(context.Background(), source); !errors.Is(err, errBackupIndexUnsafeSource) {
		t.Fatalf("publication fingerprint must detect replaced source: %v", err)
	}
}

func TestBackupIndexPlistOutputLimitCancelsWithoutTruncation(t *testing.T) {
	cancelled := false
	output := &backupPlistOutput{cancel: func() { cancelled = true }}
	if n, err := output.Write(make([]byte, backupIndexMaxPlistBytes)); err != nil || n != backupIndexMaxPlistBytes {
		t.Fatalf("valid output boundary: %d %v", n, err)
	}
	if n, err := output.Write([]byte("x")); !errors.Is(err, errBackupIndexLimit) || n != 0 || !cancelled || !output.exceeded {
		t.Fatalf("oversize output: %d %v cancelled=%v", n, err, cancelled)
	}
}

func TestBackupIndexPlistCommandDeadlineAndOutputBudget(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "plistutil")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := convertBackupBinaryPlistCommand(ctx, script, []byte("bplist00synthetic"))
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
			t.Fatalf("deadline not honored promptly: %v", err)
		}
	})
	t.Run("output budget", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "plistutil")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/cat \"$0\"\n"+strings.Repeat("#", backupIndexMaxPlistBytes+1)), 0700); err != nil {
			t.Fatal(err)
		}
		_, err := convertBackupBinaryPlistCommand(context.Background(), script, []byte("bplist00synthetic"))
		if !errors.Is(err, errBackupIndexLimit) {
			t.Fatalf("tool output must fail rather than truncate: %v", err)
		}
	})
}

func TestBackupIndexManifestRejectsPendingJournal(t *testing.T) {
	for _, suffix := range []string{"-wal", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			app, dir := indexSourceTestApp(t)
			writeIndexFixture(t, dir, "Manifest.db", "SQLite format 3\x00synthetic")
			source, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
			if err != nil {
				t.Fatal(err)
			}
			writeIndexFixture(t, dir, "Manifest.db"+suffix, "pending transaction")
			called := false
			_, err = streamLocalBackupIndex(context.Background(), source, nil, func(context.Context, string, func(backupManifestRow) error) error { called = true; return nil }, func(backupIndexRecord) error { return nil })
			if err == nil || called {
				t.Fatalf("pending journal must not publish incomplete immutable view: called=%v err=%v", called, err)
			}
			next, err := backupIndexSourceFingerprint(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if next == source.Fingerprint {
				t.Fatal("sidecar appearance must invalidate publication fingerprint")
			}
		})
	}
}

func TestBackupIndexPlistNestingLimit(t *testing.T) {
	input := `<plist><dict><key>nested</key>` + strings.Repeat("<array>", 40) + strings.Repeat("</array>", 40) + `</dict></plist>`
	if _, err := parseBackupPlist(context.Background(), []byte(input), nil); !errors.Is(err, errBackupIndexLimit) {
		t.Fatalf("valid dict root must reach nesting limit: %v", err)
	}
}
