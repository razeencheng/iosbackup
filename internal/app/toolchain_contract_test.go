package app

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	goDirectiveRE        = regexp.MustCompile(`(?m)^go ([0-9]+)\.([0-9]+)$`)
	toolchainDirectiveRE = regexp.MustCompile(`(?m)^toolchain go([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	goBuilderImageRE     = regexp.MustCompile(`(?m)^FROM golang:([0-9]+\.[0-9]+\.[0-9]+)-alpine@(sha256:[0-9a-f]{64}) AS go-builder$`)
)

func TestGoToolchainSecurityBaselineIsCentralized(t *testing.T) {
	module := readRepoFile(t, "go.mod")
	goMatch := goDirectiveRE.FindStringSubmatch(module)
	if goMatch == nil {
		t.Fatal("go.mod must preserve a major.minor source-language compatibility directive")
	}
	toolchainMatch := toolchainDirectiveRE.FindStringSubmatch(module)
	if toolchainMatch == nil {
		t.Fatal("go.mod must declare an exact Go toolchain patch version")
	}
	version := strings.Join(toolchainMatch[1:], ".")
	if versionBefore(t, toolchainMatch[1:], []int{1, 26, 7}) {
		t.Fatalf("Go %s is below the security baseline 1.26.7", version)
	}

	goVersionFileRE := regexp.MustCompile(`(?m)^[ \t]+go-version-file:[ \t]+go\.mod[ \t]*$`)
	duplicatedVersionRE := regexp.MustCompile(`(?m)^[ \t]+go-version:[ \t]+`)
	for _, name := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		workflow := readRepoFile(t, name)
		if count := len(goVersionFileRE.FindAllString(workflow, -1)); count != 1 {
			t.Errorf("%s must have exactly one active go-version-file: go.mod field, got %d", name, count)
		}
		if duplicatedVersionRE.MatchString(workflow) {
			t.Errorf("%s must not duplicate the Go version", name)
		}
	}

	dockerfile := readRepoFile(t, "Dockerfile")
	imageMatch := goBuilderImageRE.FindStringSubmatch(dockerfile)
	if imageMatch == nil || imageMatch[1] != version {
		t.Fatalf("Dockerfile must use a digest-pinned golang:%s-alpine builder", version)
	}
	imageName := "golang:" + version + "-alpine"
	digest := imageMatch[2]
	assertGoImageSupplyChainRecord(t, imageName, digest)

	contributing := readRepoFile(t, "CONTRIBUTING.md")
	sourceVersion := strings.Join(goMatch[1:], ".")
	if !strings.Contains(contributing, "Go "+sourceVersion+" or newer") || !strings.Contains(contributing, "Go "+version) {
		t.Fatalf("CONTRIBUTING.md must distinguish Go %s source compatibility from the Go %s security toolchain", sourceVersion, version)
	}
}

func assertGoImageSupplyChainRecord(t *testing.T, imageName, digest string) {
	t.Helper()
	var lock struct {
		Components []struct {
			Name      string   `json:"name"`
			Digest    string   `json:"digest"`
			Platforms []string `json:"platforms"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "third_party/components.lock.json")), &lock); err != nil {
		t.Fatalf("parse component lock: %v", err)
	}

	for _, component := range lock.Components {
		if component.Name != imageName {
			continue
		}
		if component.Digest != digest {
			t.Fatalf("component lock digest for %s: got %s, want %s", imageName, component.Digest, digest)
		}
		if strings.Join(component.Platforms, ",") != "linux/amd64,linux/arm64/v8" {
			t.Fatalf("component lock platforms for %s: got %v", imageName, component.Platforms)
		}
		notice := readRepoFile(t, "THIRD_PARTY_NOTICES.md")
		want := imageName + "@" + digest + " (linux/amd64, linux/arm64/v8)"
		if !strings.Contains(notice, want) {
			t.Fatalf("THIRD_PARTY_NOTICES.md must record %s", want)
		}
		return
	}
	t.Fatalf("component lock is missing %s", imageName)
}

func versionBefore(t *testing.T, got []string, minimum []int) bool {
	t.Helper()
	for index, part := range got {
		value, err := strconv.Atoi(part)
		if err != nil {
			t.Fatalf("parse Go version component %q: %v", part, err)
		}
		if value != minimum[index] {
			return value < minimum[index]
		}
	}
	return false
}
