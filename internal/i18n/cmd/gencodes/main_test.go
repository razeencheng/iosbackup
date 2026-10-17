package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func generate(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	return exec.Command("go", append([]string{"run", "."}, args...)...).CombinedOutput()
}

func writeCatalog(t *testing.T, dir string, keys ...string) {
	t.Helper()
	catalog := map[string]map[string]string{"en": {}, "zh-CN": {}}
	for _, key := range keys {
		catalog["en"][key] = "English"
		catalog["zh-CN"][key] = "中文"
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateConstantsFromCatalog(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "api.ip_required", "api.encryption_password_required")
	if out, err := generate(t, "-dir", dir); err != nil {
		t.Fatalf("generate constants: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "codes.go")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, first, parser.ParseComments)
	if err != nil || f.Name.Name != "i18n" || !ast.IsGenerated(f) {
		t.Fatalf("expected generated Go source for package i18n: %v", err)
	}
	got := map[string]string{}
	for _, decl := range f.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			literal := value.Values[0].(*ast.BasicLit)
			got[value.Names[0].Name], err = strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	want := map[string]string{"EncryptionPasswordRequired": "api.encryption_password_required", "IPRequired": "api.ip_required"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generated constants = %v, want %v", got, want)
	}
	if out, err := generate(t, "-dir", dir); err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out)
	}
	second, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("generation must be deterministic: %v", err)
	}
}

func TestCheckRejectsStaleOutputWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "api.encryption_password_required")
	if out, err := generate(t, "-dir", dir); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := generate(t, "-dir", dir, "-check"); err != nil {
		t.Fatalf("fresh output must pass: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "codes.go")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, dir, "api.encryption_password_required", "api.pairing_required")
	out, err := generate(t, "-dir", dir, "-check")
	if err == nil || !strings.Contains(string(out), "go generate ./internal/i18n") {
		t.Fatalf("stale output must fail with regeneration guidance: %v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("check mode must not repair stale output: %v", err)
	}
}

func TestCheckRejectsMissingOutputWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "api.encryption_password_required")
	out, err := generate(t, "-dir", dir, "-check")
	if err == nil || !strings.Contains(string(out), "go generate ./internal/i18n") {
		t.Fatalf("missing output must fail with regeneration guidance: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "codes.go")); !os.IsNotExist(err) {
		t.Fatalf("check mode must not create output: %v", err)
	}
}

func TestRejectInvalidCatalog(t *testing.T) {
	for _, tc := range []struct{ name, source, diagnostic string }{
		{"missing English", `{"zh-CN":{"api.busy":"忙"}}`, "English catalog"},
		{"invalid key", `{"en":{"ui.bad":"Bad"}}`, "invalid message key"},
		{"different language keys", `{"en":{"api.busy":"Busy"},"zh-CN":{}}`, "keys differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "messages.json"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := generate(t, "-dir", dir)
			if err == nil || !strings.Contains(string(out), tc.diagnostic) {
				t.Fatalf("invalid catalog should be rejected: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(dir, "codes.go")); !os.IsNotExist(err) {
				t.Fatalf("invalid catalog must not create output: %v", err)
			}
		})
	}
}

func TestRepositoryConstantsAreCurrent(t *testing.T) {
	if out, err := generate(t, "-dir", "../..", "-check"); err != nil {
		t.Fatalf("repository constants must match the catalog: %v\n%s", err, out)
	}
}
