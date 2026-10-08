package app

import (
	"regexp"
	"strings"
	"testing"
)

func TestDockerPackageTestWorkflowIsManualAndNonPublishing(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/docker-package-test.yml")

	if got := topLevelSection(t, workflow, "on"); got != "on:\n  workflow_dispatch:" {
		t.Fatalf("Docker package test must be manual-only, got:\n%s", got)
	}
	if got := topLevelSection(t, workflow, "permissions"); got != "permissions:\n  contents: read" {
		t.Fatalf("Docker package test must have read-only contents permission, got:\n%s", got)
	}

	assertPinnedUses(t, workflow, map[string]string{
		"actions/checkout":           "3d3c42e5aac5ba805825da76410c181273ba90b1",
		"docker/build-push-action":   "c3c9e263c25d99ce0380d002d59b67737d91b0dc",
		"docker/setup-buildx-action": "f87e5991a6d7451dcb8d9637bfbc97413f497069",
		"docker/setup-qemu-action":   "99012661954931238ded8c8b007157a8430204e1",
	})

	for _, forbidden := range []string{
		"docker/login-action", "actions/upload-artifact", "softprops/action-gh-release",
		"packages: write", "contents: write", "id-token: write", "ghcr.io/",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("non-publishing Docker package test contains %q", forbidden)
		}
	}
	if regexp.MustCompile(`(?m)^\s*push:\s*true\s*$`).MatchString(workflow) {
		t.Fatal("Docker package test must never push an image")
	}

	jobs := parseWorkflowJobs(t, workflow)
	if len(jobs) != 1 || jobs[0].name != "package" {
		t.Fatalf("Docker package test must contain only the package job, got %#v", jobs)
	}
	if got := jobScalar(t, jobs[0], "timeout-minutes"); got != "180" {
		t.Fatalf("Docker package timeout: got %q, want 180", got)
	}

	steps := parseNamedWorkflowSteps(t, workflow)
	assertStepOrder(t, steps, []string{
		"Checkout source",
		"Load validated release metadata",
		"Set up QEMU",
		"Set up Docker Buildx",
		"Build multi-architecture OCI package",
		"Verify ephemeral OCI package",
	})

	build := namedStep(t, steps, "Build multi-architecture OCI package")
	for _, required := range []string{
		"platforms: linux/amd64,linux/arm64",
		"push: false",
		"pull: true",
		"provenance: false",
		"sbom: false",
		"outputs: type=oci,dest=${{ runner.temp }}/iosbackup-multiarch.oci.tar",
	} {
		if !hasActiveLine(build, required) {
			t.Errorf("Docker package build is missing %q", required)
		}
	}

	verify := namedStep(t, steps, "Verify ephemeral OCI package")
	for _, required := range []string{
		`archive="$RUNNER_TEMP/iosbackup-multiarch.oci.tar"`,
		`test -s "$archive"`,
		`tar -xOf "$archive" index.json | jq -e '.schemaVersion == 2 and (.manifests | length > 0)' >/dev/null`,
	} {
		if !hasActiveLine(verify, required) {
			t.Errorf("OCI package verification is missing %q", required)
		}
	}
}
