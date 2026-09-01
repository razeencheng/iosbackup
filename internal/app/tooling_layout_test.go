package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildToolingTargetsNewPackageLayout(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	for _, marker := range []string{
		"COPY cmd/ ./cmd/",
		"COPY internal/ ./internal/",
		"go build -trimpath",
		"./cmd/iosbackup",
		"COPY third_party/netmuxd-cargo-licenses.tsv /final/usr/share/licenses/iosbackup/third_party/",
		"COPY third_party/components.lock.json /build/iosbackup-lock/components.lock.json",
		"cargo fetch --locked",
		"cargo metadata --locked --format-version 1",
		"cargo build --release --locked",
		"sha256sum Cargo.lock",
		"scripts/collect_cargo_licenses.sh",
		"--fallback-registry /build/iosbackup-lock/cargo-license-fallbacks.tsv",
		"--expected-metadata-only-fallback-count \"$expected_metadata_only\"",
		"pinned_upstream_material_count=$expected_pinned",
		"metadata_only_fallback_count=$expected_metadata_only",
		"/usr/share/licenses/iosbackup/third_party/netmuxd-cargo/",
	} {
		if !strings.Contains(dockerfile, marker) {
			t.Errorf("Dockerfile does not target the package layout: missing %q", marker)
		}
	}
	for _, legacy := range []string{"COPY *.go ./", "COPY templates/ ./templates/", "COPY static/ ./static/"} {
		if strings.Contains(dockerfile, legacy) {
			t.Errorf("Dockerfile retains root-package assumption %q", legacy)
		}
	}

	runner := readRepoFile(t, "scripts/run_tests.sh")
	for _, marker := range []string{
		`go test -count=1 ./...`,
		`go test -coverprofile=coverage.out ./...`,
		`go test -bench=. -run='^$' ./...`,
	} {
		if !strings.Contains(runner, marker) {
			t.Errorf("test runner does not cover all packages: missing %q", marker)
		}
	}
}

func TestPublicAllowlistTargetsNewPackageLayoutAndGateAssets(t *testing.T) {
	manifestPath := repoTestPath(t, "docs/release/public-files.txt")
	data, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		t.Skip("private export manifest is intentionally absent from the public snapshot")
	}
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(data)
	for _, required := range []string{
		"include\tcmd/**\t",
		"include\tinternal/**\t",
		"include\trelease/manifest.env\t",
		"include\tscripts/read_release_manifest.sh\t",
		"include\tscripts/read_release_manifest_test.sh\t",
		"include\tscripts/check_public_repo.sh\t",
		"include\tscripts/check_public_repo_test.sh\t",
		"include\tscripts/check_sensitive_content.sh\t",
		"include\tscripts/sensitive-content-patterns.txt\t",
		"include\tscripts/sensitive-content-fixture-exceptions.tsv\t",
		"include\tscripts/check_sensitive_content_test.sh\t",
		"include\tscripts/testdata/sensitive-content/**\t",
		"include\tscripts/verify_licensing.sh\t",
		"include\tscripts/verify_licensing_test.sh\t",
		"include\tscripts/collect_cargo_licenses.sh\t",
		"include\tscripts/collect_cargo_licenses_test.sh\t",
		"include\tscripts/run_tests.sh\t",
		"include\tthird_party/netmuxd-cargo-licenses.tsv\t",
		"include\tthird_party/cargo-license-fallbacks.tsv\t",
		"include\tthird_party/cargo-license-fallbacks/**\t",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("public allowlist missing %q", required)
		}
	}
	for _, legacy := range []string{
		"include\t*.go\t",
		"include\ttemplates/**\t",
		"include\tstatic/**\t",
		"include\tthird_party/**\t",
	} {
		if strings.Contains(manifest, legacy) {
			t.Errorf("public allowlist retains root-package assumption %q", legacy)
		}
	}
}

func TestReleaseManifestIsTheToolingMetadataSource(t *testing.T) {
	root := findModuleRoot(t)
	manifestPath := filepath.Join(root, "release", "manifest.env")
	values := make(map[string]string)
	for _, key := range []string{"IOSBK_VERSION", "IOSBK_BUILD_DATE", "IOSBK_SOURCE_URL"} {
		values[key] = readReleaseManifestValue(t, manifestPath, key)
		if strings.TrimSpace(values[key]) == "" {
			t.Errorf("release manifest key %s is empty or missing", key)
		}
	}
	reader := repoTestPath(t, "scripts/read_release_manifest.sh")
	if output, err := exec.Command(reader, manifestPath, "IOSBK_COMMIT").CombinedOutput(); err == nil {
		t.Fatalf("release manifest unexpectedly returned a frozen commit: %s", output)
	}

	for _, name := range []string{"Dockerfile", "Makefile", ".github/workflows/ci.yml", "scripts/check_public_repo.sh", "scripts/verify_licensing.sh"} {
		text := readRepoFile(t, name)
		if name != "Dockerfile" && !strings.Contains(text, "release/manifest.env") {
			t.Errorf("%s does not read release/manifest.env", name)
		}
		if name != "Dockerfile" && !strings.Contains(text, "read_release_manifest.sh") {
			t.Errorf("%s bypasses the shared strict release-manifest parser", name)
		}
		if strings.Contains(text, values["IOSBK_BUILD_DATE"]) || strings.Contains(text, values["IOSBK_SOURCE_URL"]) {
			t.Errorf("%s duplicates frozen release metadata", name)
		}
		for _, unsafe := range []string{
			"include release/manifest.env",
			". ./release/manifest.env",
			"source release/manifest.env",
			"eval ",
		} {
			if strings.Contains(text, unsafe) {
				t.Errorf("%s executes release metadata with unsafe construct %q", name, unsafe)
			}
		}
	}

	dockerfile := readRepoFile(t, "Dockerfile")
	if !strings.Contains(dockerfile, `org.opencontainers.image.created="${IOSBK_BUILD_DATE}T00:00:00Z"`) {
		t.Fatal("Dockerfile OCI created label is not derived from the manifest date as RFC3339")
	}
}

func TestOfficialLicenseTextsPreserveExactBytes(t *testing.T) {
	attributes := readRepoFile(t, ".gitattributes")
	for _, path := range []string{
		"third_party/licenses/BSD-2-Clause.txt",
		"third_party/licenses/BSD-3-Clause.txt",
		"third_party/licenses/MPL-2.0.txt",
	} {
		expected := path + " -text -whitespace"
		if !containsExactLine(attributes, expected) {
			t.Errorf(".gitattributes missing exact byte-preservation rule %q", expected)
		}
	}
	for _, forbidden := range []string{"third_party/licenses/*.txt", "third_party/licenses/**"} {
		if strings.Contains(attributes, forbidden) {
			t.Errorf(".gitattributes broadens official-text exception with %q", forbidden)
		}
	}
}

func TestGitignoreOnlyIgnoresRootBinary(t *testing.T) {
	data := readRepoFile(t, ".gitignore")
	var rootRule bool
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "/iosbackup" {
			rootRule = true
		}
		if line == "iosbackup" {
			t.Fatal("unanchored iosbackup rule also ignores cmd/iosbackup")
		}
	}
	if !rootRule {
		t.Fatal("root iosbackup binary is not ignored with an anchored rule")
	}
}

func readReleaseManifestValue(t *testing.T, path, key string) string {
	t.Helper()
	reader := repoTestPath(t, "scripts/read_release_manifest.sh")
	output, err := exec.Command(reader, path, key).CombinedOutput()
	if err != nil {
		t.Fatalf("strict release-manifest reader failed for %s: %v: %s", key, err, output)
	}
	return strings.TrimSuffix(string(output), "\n")
}

func containsExactLine(text, expected string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line == expected {
			return true
		}
	}
	return false
}
