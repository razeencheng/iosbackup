package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type workflowStep struct {
	name string
	body string
}

type workflowJob struct {
	name  string
	body  string
	steps []workflowStep
}

type workflowJobSpec struct {
	name        string
	needs       string
	timeout     string
	permissions map[string]string
	outputs     map[string]string
	steps       []string
}

var pushTrueWorkflowLineRE = regexp.MustCompile(`^push[[:space:]]*:[[:space:]]*true(?:[[:space:]]+#.*)?$`)

var releaseWorkflowStepNames = []string{
	"Checkout release tag",
	"Set up Go",
	"Verify source and release gates",
	"Freeze and validate release metadata",
	"Checkout validated release source",
	"Confirm validated release source",
	"Check GHCR visibility before push",
	"Log in to GHCR",
	"Set up QEMU",
	"Set up Docker Buildx",
	"Build and push multi-architecture image",
	"Confirm GHCR visibility after push",
	"Record image index digest",
	"Export platform manifests and BuildKit SLSA provenance",
	"Generate linux/amd64 SPDX JSON SBOM",
	"Generate linux/arm64 SPDX JSON SBOM",
	"Validate platform SPDX JSON SBOMs",
	"Install Cosign",
	"Sign multi-architecture index with Cosign keyless",
	"Attest linux/amd64 SPDX SBOM with Cosign keyless",
	"Attest linux/arm64 SPDX SBOM with Cosign keyless",
	"Verify Cosign signature and platform SBOM attestations",
	"Generate deterministic release notes",
	"Upload verified release assets",
	"Download verified release assets",
	"Validate downloaded release assets",
	"Create GitHub prerelease",
}

var releaseWorkflowJobSpecs = []workflowJobSpec{
	{
		name:    "validate",
		timeout: "30",
		permissions: map[string]string{
			"contents": "read",
		},
		outputs: map[string]string{
			"version":    "${{ steps.release.outputs.version }}",
			"build_date": "${{ steps.release.outputs.build_date }}",
			"commit":     "${{ steps.release.outputs.commit }}",
			"source_url": "${{ steps.release.outputs.source_url }}",
		},
		steps: releaseWorkflowStepNames[:4],
	},
	{
		name:    "publish",
		needs:   "validate",
		timeout: "180",
		permissions: map[string]string{
			"contents": "read",
			"id-token": "write",
			"packages": "write",
		},
		outputs: map[string]string{
			"image_digest": "${{ steps.image.outputs.digest }}",
			"amd64_digest": "${{ steps.platforms.outputs.amd64_digest }}",
			"arm64_digest": "${{ steps.platforms.outputs.arm64_digest }}",
			"artifact_id":  "${{ steps.assets.outputs.artifact-id }}",
		},
		steps: releaseWorkflowStepNames[4:24],
	},
	{
		name:    "release",
		needs:   "[validate, publish]",
		timeout: "15",
		permissions: map[string]string{
			"contents": "write",
		},
		steps: releaseWorkflowStepNames[24:],
	},
}

var releaseSupplyChainAssetPaths = []string{
	"dist/iosbackup-linux-amd64.spdx.json",
	"dist/iosbackup-linux-arm64.spdx.json",
	"dist/iosbackup-linux-amd64-provenance.json",
	"dist/iosbackup-linux-arm64-provenance.json",
	"dist/iosbackup-platform-manifests.json",
	"dist/iosbackup-image-digest.txt",
	"dist/iosbackup-cosign-signature-verification.json",
	"dist/iosbackup-linux-amd64-cosign-sbom-attestation-verification.json",
	"dist/iosbackup-linux-arm64-cosign-sbom-attestation-verification.json",
}

var releaseTransferPaths = append(
	append([]string{}, releaseSupplyChainAssetPaths...),
	"dist/iosbackup-release-notes.md",
)

func TestReleaseWorkflowTriggerAndPermissions(t *testing.T) {
	workflow := loadReleaseWorkflow(t)

	wantTrigger := strings.Join([]string{
		"on:",
		"  push:",
		"    tags:",
		"      - 'v*'",
	}, "\n")
	if got := topLevelSection(t, workflow, "on"); got != wantTrigger {
		t.Fatalf("release trigger must be exactly a v* tag push\ngot:\n%s\nwant:\n%s", got, wantTrigger)
	}

	if got := topLevelSection(t, workflow, "permissions"); got != "permissions: {}" {
		t.Fatalf("release workflow must deny permissions by default, got:\n%s", got)
	}
	for _, forbidden := range []string{"artifact-metadata:", "attestations:"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("personal private repositories must not request unavailable GitHub-native attestation permission %q", forbidden)
		}
	}
}

func TestReleaseWorkflowJobGraphAndLeastPrivilege(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	jobs := parseWorkflowJobs(t, workflow)
	if len(jobs) != len(releaseWorkflowJobSpecs) {
		t.Fatalf("release workflow job count: got %d, want %d", len(jobs), len(releaseWorkflowJobSpecs))
	}
	for index, spec := range releaseWorkflowJobSpecs {
		job := jobs[index]
		if job.name != spec.name {
			t.Fatalf("release job %d: got %q, want %q", index+1, job.name, spec.name)
		}
		if got := jobScalar(t, job, "needs"); got != spec.needs {
			t.Errorf("job %q needs: got %q, want %q", job.name, got, spec.needs)
		}
		if got := jobScalar(t, job, "timeout-minutes"); got != spec.timeout {
			t.Errorf("job %q timeout: got %q, want %q", job.name, got, spec.timeout)
		}
		if got := jobScalarMap(t, job, "permissions"); !equalStringMap(got, spec.permissions) {
			t.Errorf("job %q permissions: got %v, want %v", job.name, got, spec.permissions)
		}
		if got := jobScalarMap(t, job, "outputs"); !equalStringMap(got, spec.outputs) {
			t.Errorf("job %q outputs: got %v, want %v", job.name, got, spec.outputs)
		}
		gotSteps := make([]string, 0, len(job.steps))
		for _, step := range job.steps {
			gotSteps = append(gotSteps, step.name)
		}
		if strings.Join(gotSteps, "\n") != strings.Join(spec.steps, "\n") {
			t.Errorf("job %q step whitelist mismatch:\ngot:  %q\nwant: %q", job.name, gotSteps, spec.steps)
		}
	}
}

func TestReleaseWorkflowPinsActionsAndAvoidsDockerHub(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	wantUses := map[string]string{
		"actions/checkout":            "d23441a48e516b6c34aea4fa41551a30e30af803",
		"actions/download-artifact":   "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
		"actions/setup-go":            "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/upload-artifact":     "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"anchore/sbom-action":         "3ad7283483fc7af8ff2b4ea19663c2d5ca935e26",
		"docker/build-push-action":    "53b7df96c91f9c12dcc8a07bcb9ccacbed38856a",
		"docker/login-action":         "dbcb813823bdd20940b903addbd779551569679f",
		"docker/setup-buildx-action":  "37fe631027851001ddb9b187196cc803df7f5f0e",
		"docker/setup-qemu-action":    "96fe6ef7f33517b61c61be40b68a1882f3264fb8",
		"sigstore/cosign-installer":   "6f9f17788090df1f26f669e9d70d6ae9567deba6",
		"softprops/action-gh-release": "efb35369e0ad2afab669f228072c1b0d510eae64",
	}
	assertPinnedUses(t, workflow, wantUses)
	if strings.Contains(workflow, "actions/attest@") {
		t.Fatal("private personal repositories cannot run GitHub-native artifact attestations without Enterprise Cloud")
	}

	ciPath := filepath.Join(findModuleRoot(t), ".github", "workflows", "ci.yml")
	ciBytes, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	assertPinnedUses(t, string(ciBytes), map[string]string{
		"actions/checkout": "d23441a48e516b6c34aea4fa41551a30e30af803",
		"actions/setup-go": "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
	})

	allowedDependencyImages := []string{
		"docker.io/tonistiigi/binfmt@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0",
		"docker.io/moby/buildkit@sha256:28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8",
	}
	withoutPinnedDependencies := workflow
	for _, image := range allowedDependencyImages {
		if strings.Count(workflow, image) != 1 {
			t.Errorf("release workflow must reference pinned dependency image exactly once: %s", image)
		}
		withoutPinnedDependencies = strings.ReplaceAll(withoutPinnedDependencies, image, "")
	}
	lower := strings.ToLower(withoutPinnedDependencies)
	for _, forbidden := range []string{"docker.io", "hub.docker", "dockerhub"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("release workflow must be GHCR-only; found %q", forbidden)
		}
	}
	if strings.Count(workflow, "ghcr.io/razeencheng/iosbackup") == 0 {
		t.Fatal("release workflow does not target the official GHCR image")
	}
}

func TestReleaseWorkflowValidatesFrozenInputsBeforePush(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	steps := parseNamedWorkflowSteps(t, workflow)

	ordered := []string{
		"Checkout release tag",
		"Set up Go",
		"Verify source and release gates",
		"Freeze and validate release metadata",
		"Checkout validated release source",
		"Confirm validated release source",
		"Check GHCR visibility before push",
		"Log in to GHCR",
		"Build and push multi-architecture image",
	}
	assertStepOrder(t, steps, ordered)

	gates := namedStep(t, steps, "Verify source and release gates")
	for _, required := range []string{
		"go vet ./...",
		"go test -count=1 -run '^TestPackageDependencyDirection$' ./internal/app",
		"go test -count=1 ./...",
		"test \"$(go list -m all)\" = \"iosbackup\"",
		"./scripts/check_public_repo.sh --directory .",
		"./scripts/verify_licensing.sh",
	} {
		if !hasActiveLine(gates, required) {
			t.Errorf("release gates are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"docs/release/public-files.txt",
		"docs/release/",
		"--public-index",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("public release workflow references private release evidence %q", forbidden)
		}
	}

	metadata := namedStep(t, steps, "Freeze and validate release metadata")
	for _, required := range []string{
		`version="$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_VERSION)"`,
		`build_date="$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_BUILD_DATE)"`,
		`source_url="$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_SOURCE_URL)"`,
		`commit="$(git rev-parse HEAD)"`,
		`test "$GITHUB_REF_TYPE" = "tag"`,
		`test "$GITHUB_REF_NAME" = "$version"`,
		`test "$GITHUB_REPOSITORY" = "razeencheng/iosbackup"`,
		`test "$source_url" = "https://github.com/$GITHUB_REPOSITORY"`,
		`test "$commit" = "$GITHUB_SHA"`,
		`test "$(git rev-parse "$GITHUB_REF^{commit}")" = "$commit"`,
		`buildinfo_version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)"$/\1/p' internal/buildinfo/buildinfo.go)"`,
		`test -n "$buildinfo_version"`,
		`test "$buildinfo_version" = "$version"`,
		`changelog_count="$(awk -v heading="## $version" '$0 == heading { count++ } END { print count + 0 }' CHANGELOG.md)"`,
		`test "$changelog_count" = "1"`,
		`echo "version=$version"`,
		`echo "build_date=$build_date"`,
		`echo "commit=$commit"`,
		`echo "source_url=$source_url"`,
	} {
		if !hasActiveLine(metadata, required) {
			t.Errorf("release metadata validation is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"source release/manifest.env",
		"source ./release/manifest.env",
		". release/manifest.env",
		". ./release/manifest.env",
		"eval ",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release manifest must remain data, found %q", forbidden)
		}
	}

	build := namedStep(t, steps, "Build and push multi-architecture image")
	for _, required := range []string{
		"context: .",
		"platforms: linux/amd64,linux/arm64",
		"push: true",
		"tags: ghcr.io/razeencheng/iosbackup:${{ needs.validate.outputs.version }}",
		"IOSBK_VERSION=${{ needs.validate.outputs.version }}",
		"IOSBK_BUILD_DATE=${{ needs.validate.outputs.build_date }}",
		"IOSBK_COMMIT=${{ needs.validate.outputs.commit }}",
		"IOSBK_SOURCE_URL=${{ needs.validate.outputs.source_url }}",
		"provenance: mode=max,version=v1",
		"sbom: false",
	} {
		if !hasActiveLine(build, required) {
			t.Errorf("multi-architecture build is missing %q", required)
		}
	}
	if strings.Contains(build.body, "secrets:") || regexp.MustCompile(`(?i)(TOKEN|PASSWORD|SECRET)[A-Za-z0-9_]*=`).MatchString(build.body) {
		t.Fatal("max-mode provenance must not capture secret build inputs")
	}
	buildArgLines := regexp.MustCompile(`(?m)^\s+IOSBK_[A-Z_]+=.+$`).FindAllString(build.body, -1)
	if len(buildArgLines) != 4 {
		t.Fatalf("build must expose exactly four validated public metadata arguments, got %v", buildArgLines)
	}
	if regexp.MustCompile(`(?m)^\s*IOSBK_BUILD_DATE=\d{4}-\d{2}-\d{2}\s*$`).MatchString(workflow) {
		t.Fatal("release workflow duplicates the frozen build date")
	}
}

func TestReleaseWorkflowPublishesPerPlatformEvidenceAfterPrivateGate(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	if err := validateReleaseWorkflowSafety(workflow); err != nil {
		t.Fatal(err)
	}
	steps := parseNamedWorkflowSteps(t, workflow)
	for _, forbidden := range []string{"continue-on-error: true", "if: always()", "if: ${{ always() }}"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release gates must fail closed; found %q", forbidden)
		}
	}
	ordered := []string{
		"Check GHCR visibility before push",
		"Build and push multi-architecture image",
		"Confirm GHCR visibility after push",
		"Export platform manifests and BuildKit SLSA provenance",
		"Generate linux/amd64 SPDX JSON SBOM",
		"Generate linux/arm64 SPDX JSON SBOM",
		"Validate platform SPDX JSON SBOMs",
		"Sign multi-architecture index with Cosign keyless",
		"Attest linux/amd64 SPDX SBOM with Cosign keyless",
		"Attest linux/arm64 SPDX SBOM with Cosign keyless",
		"Verify Cosign signature and platform SBOM attestations",
		"Generate deterministic release notes",
		"Upload verified release assets",
		"Download verified release assets",
		"Validate downloaded release assets",
		"Create GitHub prerelease",
	}
	assertStepOrder(t, steps, ordered)

	preflight := namedStep(t, steps, "Check GHCR visibility before push")
	for _, required := range []string{
		`jq -e '.repository.private == true' "$GITHUB_EVENT_PATH" >/dev/null`,
		`api="https://api.github.com/users/razeencheng/packages/container/iosbackup"`,
		`case "$status" in`,
		"200)",
		"404)",
		`if [ "$visibility" != "private" ]; then`,
	} {
		if !hasActiveLine(preflight, required) {
			t.Errorf("pre-push visibility gate is missing %q", required)
		}
	}
	if strings.Contains(preflight.body, "PATCH") || strings.Contains(preflight.body, "visibility=private") {
		t.Fatal("pre-push gate must never modify package visibility")
	}

	postflight := namedStep(t, steps, "Confirm GHCR visibility after push")
	for _, required := range []string{
		`api="https://api.github.com/users/razeencheng/packages/container/iosbackup"`,
		`test "$status" = "200"`,
		`test "$visibility" = "private"`,
	} {
		if !hasActiveLine(postflight, required) {
			t.Errorf("post-push visibility gate is missing %q", required)
		}
	}
	if strings.Contains(postflight.body, "404)") || strings.Contains(postflight.body, "PATCH") {
		t.Fatal("post-push gate must require an existing private package without mutating it")
	}

	build := namedStep(t, steps, "Build and push multi-architecture image")
	if !hasActiveLine(build, "id: build") {
		t.Fatal("build step must expose the pushed image digest")
	}

	for _, platform := range []struct {
		name   string
		output string
	}{
		{name: "linux/amd64", output: "amd64_digest"},
		{name: "linux/arm64", output: "arm64_digest"},
	} {
		sbom := namedStep(t, steps, "Generate "+platform.name+" SPDX JSON SBOM")
		filename := strings.ReplaceAll(platform.name, "/", "-")
		for _, required := range []string{
			"image: ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs." + platform.output + " }}",
			"format: spdx-json",
			"output-file: dist/iosbackup-" + filename + ".spdx.json",
			"upload-artifact: false",
			"upload-release-assets: false",
		} {
			if !hasActiveLine(sbom, required) {
				t.Errorf("%s SBOM generation is missing %q", platform.name, required)
			}
		}
	}
	if strings.Contains(workflow, "dist/iosbackup.spdx.json") {
		t.Fatal("release workflow must not use one ambiguous index-level SPDX document")
	}

	validateSBOM := namedStep(t, steps, "Validate platform SPDX JSON SBOMs")
	for _, required := range []string{
		"for platform in linux-amd64 linux-arm64; do",
		`sbom="dist/iosbackup-${platform}.spdx.json"`,
		`test -s "$sbom"`,
		`sbom_size="$(wc -c < "$sbom" | tr -d '[:space:]')"`,
		`test "$sbom_size" -le 16777216`,
		`jq -e '.spdxVersion | type == "string" and startswith("SPDX-")' "$sbom" >/dev/null`,
		`jq -e '.packages | type == "array" and length > 0' "$sbom" >/dev/null`,
		"done",
	} {
		if !hasActiveLine(validateSBOM, required) {
			t.Errorf("SBOM validation is missing %q", required)
		}
	}

	provenance := namedStep(t, steps, "Export platform manifests and BuildKit SLSA provenance")
	for _, sequence := range [][]string{
		{`docker buildx imagetools inspect "$image" \`, `--format '{{ json (index .Provenance "linux/amd64").SLSA }}' > dist/iosbackup-linux-amd64-provenance.json`},
		{`docker buildx imagetools inspect "$image" \`, `--format '{{ json (index .Provenance "linux/arm64").SLSA }}' > dist/iosbackup-linux-arm64-provenance.json`},
	} {
		if !hasActiveLineSequence(provenance, sequence) {
			t.Errorf("BuildKit provenance export is missing active command block %q", sequence)
		}
	}
	for _, required := range []string{
		`EXPECTED_COMMIT: ${{ needs.validate.outputs.commit }}`,
		`image="ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}"`,
		`index_json="$(mktemp "$RUNNER_TEMP/iosbackup-index.XXXXXX.json")"`,
		`trap 'rm -f "$index_json"' EXIT`,
		`docker buildx imagetools inspect "$image" --raw > "$index_json"`,
		`test -s "$index_json"`,
		`| select(.platform.os == "linux")`,
		`| if (($platforms | length) == 2)`,
		`and ($platforms | map(.architecture) | sort == ["amd64", "arm64"])`,
		`and (($platforms | map(.digest) | unique | length) == 2)`,
		`and (all($platforms[]; .digest | test("^sha256:[0-9a-f]{64}$")))`,
		`' "$index_json" > dist/iosbackup-platform-manifests.json`,
		"test -s dist/iosbackup-platform-manifests.json",
		`amd64_digest="$(jq -er '[.[] | select(.platform == "linux/amd64") | .digest] | if length == 1 then .[0] else error("missing amd64 digest") end' dist/iosbackup-platform-manifests.json)"`,
		`arm64_digest="$(jq -er '[.[] | select(.platform == "linux/arm64") | .digest] | if length == 1 then .[0] else error("missing arm64 digest") end' dist/iosbackup-platform-manifests.json)"`,
		`test "$amd64_digest" != "$arm64_digest"`,
		"for platform in linux-amd64 linux-arm64; do",
		`provenance="dist/iosbackup-${platform}-provenance.json"`,
		`test -s "$provenance"`,
		`and (.buildDefinition.buildType | type == "string" and length > 0)`,
		`and (.runDetails.builder | type == "object" and has("id"))`,
		`jq -e --arg commit "$EXPECTED_COMMIT" '([.. | strings | select(. == $commit)] | length) > 0' "$provenance" >/dev/null`,
		"done",
		`echo "amd64_digest=$amd64_digest"`,
		`echo "arm64_digest=$arm64_digest"`,
	} {
		if !hasActiveLine(provenance, required) {
			t.Errorf("BuildKit provenance export is missing %q", required)
		}
	}
	if strings.Contains(provenance.body, "dist/iosbackup-provenance.json") {
		t.Fatal("multi-architecture release must not collapse per-platform provenance into one ambiguous file")
	}
	if strings.Contains(provenance.body, "select(.platform.architecture") {
		t.Fatal("platform mapping must inspect every Linux descriptor before requiring exactly amd64 and arm64")
	}

	for _, platform := range []struct {
		name   string
		output string
	}{
		{name: "linux/amd64", output: "amd64_digest"},
		{name: "linux/arm64", output: "arm64_digest"},
	} {
		filename := strings.ReplaceAll(platform.name, "/", "-")
		attest := namedStep(t, steps, "Attest "+platform.name+" SPDX SBOM with Cosign keyless")
		want := `cosign attest --yes --type spdxjson --predicate dist/iosbackup-` + filename + `.spdx.json "ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.` + platform.output + ` }}"`
		if !hasActiveLine(attest, want) {
			t.Errorf("Cosign does not attest the %s SPDX document against its platform manifest digest", platform.name)
		}
	}

	verify := namedStep(t, steps, "Verify Cosign signature and platform SBOM attestations")
	for _, required := range []string{
		`identity="https://github.com/$GITHUB_REPOSITORY/.github/workflows/release.yml@$GITHUB_REF"`,
		`issuer="https://token.actions.githubusercontent.com"`,
		`jq -e 'type == "array" and length > 0' dist/iosbackup-cosign-signature-verification.json >/dev/null`,
		`jq -e 'type == "array" and length > 0' dist/iosbackup-linux-amd64-cosign-sbom-attestation-verification.json >/dev/null`,
		`jq -e 'type == "array" and length > 0' dist/iosbackup-linux-arm64-cosign-sbom-attestation-verification.json >/dev/null`,
	} {
		if !hasActiveLine(verify, required) {
			t.Errorf("Cosign verification gate is missing %q", required)
		}
	}
	for _, sequence := range [][]string{
		{
			`cosign verify --output json \`,
			`--certificate-identity "$identity" \`,
			`--certificate-oidc-issuer "$issuer" \`,
			`"ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}" \`,
			"> dist/iosbackup-cosign-signature-verification.json",
		},
		{
			`cosign verify-attestation --type spdxjson --output json \`,
			`--certificate-identity "$identity" \`,
			`--certificate-oidc-issuer "$issuer" \`,
			`"ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}" \`,
			"> dist/iosbackup-linux-amd64-cosign-sbom-attestation-verification.json",
		},
		{
			`cosign verify-attestation --type spdxjson --output json \`,
			`--certificate-identity "$identity" \`,
			`--certificate-oidc-issuer "$issuer" \`,
			`"ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}" \`,
			"> dist/iosbackup-linux-arm64-cosign-sbom-attestation-verification.json",
		},
	} {
		if !hasActiveLineSequence(verify, sequence) {
			t.Errorf("Cosign verification gate is missing active command block %q", sequence)
		}
	}

	sign := namedStep(t, steps, "Sign multi-architecture index with Cosign keyless")
	if !hasActiveLine(sign, `cosign sign --yes "ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}"`) {
		t.Fatal("Cosign does not keylessly sign the exact pushed multi-architecture index digest")
	}

	notes := namedStep(t, steps, "Generate deterministic release notes")
	for _, required := range []string{
		"VERSION: ${{ needs.validate.outputs.version }}",
		`heading="## $VERSION"`,
		`heading_count="$(grep -Fxc -- "$heading" CHANGELOG.md)"`,
		`test "$heading_count" = "1"`,
		`next_heading="$(awk -v heading="$heading" '$0 == heading { found=1; next } found && /^## / { print; exit }' CHANGELOG.md)"`,
		`test -n "$next_heading"`,
		`test "$next_heading" != "$heading"`,
		`' CHANGELOG.md > dist/iosbackup-release-notes.md`,
		"test -s dist/iosbackup-release-notes.md",
		`test "$(grep -Ec '^## ' dist/iosbackup-release-notes.md)" = "0"`,
		`grep -Eq '^### ' dist/iosbackup-release-notes.md`,
	} {
		if !hasActiveLine(notes, required) {
			t.Errorf("deterministic release notes step is missing %q", required)
		}
	}

	upload := namedStep(t, steps, "Upload verified release assets")
	for _, required := range []string{
		"id: assets",
		"name: iosbackup-release-assets-${{ github.run_id }}-${{ github.run_attempt }}",
		"if-no-files-found: error",
		"overwrite: false",
	} {
		if !hasActiveLine(upload, required) {
			t.Errorf("verified asset upload is missing %q", required)
		}
	}
	if got := multilineFieldValues(t, upload, "path"); strings.Join(got, "\n") != strings.Join(releaseTransferPaths, "\n") {
		t.Errorf("uploaded release transfer list mismatch: got %v, want %v", got, releaseTransferPaths)
	}

	download := namedStep(t, steps, "Download verified release assets")
	for _, required := range []string{
		"artifact-ids: ${{ needs.publish.outputs.artifact_id }}",
		"path: dist",
		"merge-multiple: true",
		"digest-mismatch: error",
	} {
		if !hasActiveLine(download, required) {
			t.Errorf("verified asset download is missing %q", required)
		}
	}
	if strings.Contains(download.body, "name: iosbackup-release-assets") {
		t.Fatal("release job must download the exact artifact ID, not a reusable artifact name")
	}

	validateAssets := namedStep(t, steps, "Validate downloaded release assets")
	for _, required := range []string{
		`printf '%s' "${{ needs.publish.outputs.artifact_id }}" | grep -Eq '^[0-9]+$'`,
		`diff -u "$expected" "$actual"`,
		`test "$image_ref" = "ghcr.io/razeencheng/iosbackup@${{ needs.publish.outputs.image_digest }}"`,
		`printf '%s' "${{ needs.publish.outputs.image_digest }}" | grep -Eq '^sha256:[0-9a-f]{64}$'`,
		`jq -e --arg amd64 "${{ needs.publish.outputs.amd64_digest }}" --arg arm64 "${{ needs.publish.outputs.arm64_digest }}" '`,
		"test -s dist/iosbackup-release-notes.md",
		`test "$(grep -Ec '^## ' dist/iosbackup-release-notes.md)" = "0"`,
		`grep -Eq '^### ' dist/iosbackup-release-notes.md`,
	} {
		if !hasActiveLine(validateAssets, required) {
			t.Errorf("downloaded asset gate is missing %q", required)
		}
	}
	wantTransferredBasenames := make([]string, 0, len(releaseTransferPaths))
	for _, path := range releaseTransferPaths {
		wantTransferredBasenames = append(wantTransferredBasenames, strings.TrimPrefix(path, "dist/"))
	}
	if got := shellArrayValues(t, validateAssets, "expected_assets"); strings.Join(got, "\n") != strings.Join(wantTransferredBasenames, "\n") {
		t.Errorf("downloaded release transfer list mismatch: got %v, want %v", got, wantTransferredBasenames)
	}

	release := namedStep(t, steps, "Create GitHub prerelease")
	for _, required := range []string{
		"prerelease: true",
		"tag_name: ${{ needs.validate.outputs.version }}",
		"target_commitish: ${{ needs.validate.outputs.commit }}",
		"body_path: dist/iosbackup-release-notes.md",
		"generate_release_notes: false",
		"append_body: false",
	} {
		if !hasActiveLine(release, required) {
			t.Errorf("GitHub prerelease is missing %q", required)
		}
	}
	if got := multilineFieldValues(t, release, "files"); strings.Join(got, "\n") != strings.Join(releaseSupplyChainAssetPaths, "\n") {
		t.Errorf("GitHub prerelease attachment list mismatch: got %v, want %v", got, releaseSupplyChainAssetPaths)
	}
}

func TestReleaseWorkflowRejectsBypassMutations(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	tests := []struct {
		name     string
		old      string
		replace  string
		wantPart string
	}{
		{
			name:     "commented critical command",
			old:      "          go vet ./...\n",
			replace:  "          # go vet ./...\n",
			wantPart: "active release gate",
		},
		{
			name:     "inline comment impersonates repository gate",
			old:      `          jq -e '.repository.private == true' "$GITHUB_EVENT_PATH" >/dev/null` + "\n",
			replace:  `          true # jq -e '.repository.private == true' "$GITHUB_EVENT_PATH" >/dev/null` + "\n",
			wantPart: "active pre-push gate",
		},
		{
			name: "extra early push",
			old:  "      - name: Check GHCR visibility before push\n",
			replace: strings.Join([]string{
				"      - name: Unauthorized early push",
				"        uses: docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a",
				"        with:",
				"          push: true",
				"",
				"      - name: Check GHCR visibility before push",
				"",
			}, "\n"),
			wantPart: "unexpected release workflow step",
		},
		{
			name: "duplicate step name",
			old:  "      - name: Check GHCR visibility before push\n",
			replace: strings.Join([]string{
				"      - name: Check GHCR visibility before push",
				"        run: echo duplicate",
				"",
				"      - name: Check GHCR visibility before push",
				"",
			}, "\n"),
			wantPart: "duplicate workflow step name",
		},
		{
			name: "unique direct image push step",
			old:  "      - name: Check GHCR visibility before push\n",
			replace: strings.Join([]string{
				"      - name: Direct image push",
				"        run: docker image push ghcr.io/razeencheng/iosbackup:unexpected",
				"",
				"      - name: Check GHCR visibility before push",
				"",
			}, "\n"),
			wantPart: "unexpected release workflow step",
		},
		{
			name:     "direct image push inside allowed step",
			old:      "          ./scripts/check_public_repo.sh --directory .\n",
			replace:  "          ./scripts/check_public_repo.sh --directory .\n          docker image push ghcr.io/razeencheng/iosbackup:unexpected\n",
			wantPart: "unexpected publishing side effect",
		},
		{
			name:     "conditional step",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        if: ${{ always() }}\n",
			wantPart: "conditional or non-fatal step",
		},
		{
			name:     "continue on error step",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        continue-on-error: true\n",
			wantPart: "conditional or non-fatal step",
		},
		{
			name:     "conditional release job",
			old:      "  release:\n",
			replace:  "  release:\n    if: ${{ success() }}\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "double quoted conditional step key",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        \"if\": ${{ always() }}\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "double quoted continue job key",
			old:      "  release:\n",
			replace:  "  release:\n    \"continue-on-error\": true\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "single quoted continue job key",
			old:      "  release:\n",
			replace:  "  release:\n    'continue-on-error': true\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "single quoted timeout step key",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        'timeout-minutes': 1\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "background step",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        background: true\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "single quoted background step key",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        'background': true\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "double quoted background step key",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        \"background\": true\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "background release job",
			old:      "  release:\n",
			replace:  "  release:\n    background: true\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "single quoted background release job key",
			old:      "  release:\n",
			replace:  "  release:\n    'background': true\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "explicit yaml control key",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        ? \"if\"\n        : ${{ always() }}\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "flow map control field",
			old:      "      - name: Log in to GHCR\n",
			replace:  "      - name: Log in to GHCR\n        {\"if\": \"${{ always() }}\"}\n",
			wantPart: "forbidden step control",
		},
		{
			name:     "changed release job timeout",
			old:      "    timeout-minutes: 180\n",
			replace:  "    timeout-minutes: 179\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "quoted release job timeout",
			old:      "    timeout-minutes: 180\n",
			replace:  "    \"timeout-minutes\": 180\n",
			wantPart: "forbidden release job control",
		},
		{
			name:     "release skips validated dependency",
			old:      "    needs: [validate, publish]\n",
			replace:  "    needs: publish\n",
			wantPart: "job \"release\" needs",
		},
		{
			name:     "validate job gains write permission",
			old:      "  validate:\n    name: Validate release source and metadata\n    runs-on: ubuntu-24.04\n    timeout-minutes: 30\n    permissions:\n      contents: read\n",
			replace:  "  validate:\n    name: Validate release source and metadata\n    runs-on: ubuntu-24.04\n    timeout-minutes: 30\n    permissions:\n      contents: write\n",
			wantPart: "job \"validate\" permissions",
		},
		{
			name:     "artifact name reused across reruns",
			old:      "          name: iosbackup-release-assets-${{ github.run_id }}-${{ github.run_attempt }}\n",
			replace:  "          name: iosbackup-release-assets\n",
			wantPart: "required active release contract line",
		},
		{
			name:     "release notes regenerated and appended on rerun",
			old:      "          generate_release_notes: false\n          append_body: false\n",
			replace:  "          generate_release_notes: true\n          append_body: true\n",
			wantPart: "required active release contract line",
		},
		{
			name:     "release notes heading uniqueness gate commented out",
			old:      "          test \"$heading_count\" = \"1\"\n",
			replace:  "          # test \"$heading_count\" = \"1\"\n",
			wantPart: "required active release contract line",
		},
		{
			name:     "release notes accidentally attached",
			old:      "          files: |\n            dist/iosbackup-linux-amd64.spdx.json\n",
			replace:  "          files: |\n            dist/iosbackup-release-notes.md\n            dist/iosbackup-linux-amd64.spdx.json\n",
			wantPart: "GitHub prerelease attachment list",
		},
		{
			name:     "qemu registers every platform",
			old:      "          platforms: arm64\n",
			replace:  "          platforms: all\n",
			wantPart: "required active release contract line",
		},
		{
			name:     "buildx uses floating version",
			old:      "          version: v0.36.1\n",
			replace:  "          version: latest\n",
			wantPart: "required active release contract line",
		},
		{
			name:     "amd64 sbom scans index digest",
			old:      "          image: ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}\n",
			replace:  "          image: ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}\n",
			wantPart: "required active release contract line",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, test.old, test.replace, 1)
			if mutated == workflow {
				t.Fatalf("mutation fixture did not match %q", test.old)
			}
			err := validateReleaseWorkflowSafety(mutated)
			if err == nil {
				t.Fatal("mutated workflow unexpectedly passed the release safety contract")
			}
			if !strings.Contains(err.Error(), test.wantPart) {
				t.Fatalf("unexpected mutation rejection: got %q, want %q", err, test.wantPart)
			}
		})
	}
}

func TestPrivateReleaseChecklistPreservesManualCutoverOrder(t *testing.T) {
	path := filepath.Join(findModuleRoot(t), "docs", "release", "PUBLIC_BETA_CHECKLIST.md")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("private release checklist is intentionally absent from the public export")
	}
	if err != nil {
		t.Fatal(err)
	}
	checklist := string(contents)
	markers := []string{
		"all private validation is complete",
		"change the GHCR package from private to public",
		"fresh unauthenticated Docker configuration",
		"change the GitHub repository from private to public",
		"Without authentication",
		"Announce the Beta",
	}
	previous := -1
	for _, marker := range markers {
		position := strings.Index(checklist, marker)
		if position < 0 {
			t.Errorf("private release checklist is missing cutover marker %q", marker)
			continue
		}
		if position <= previous {
			t.Errorf("private release checklist cutover marker %q is out of order", marker)
		}
		previous = position
	}
	for _, required := range []string{
		"cannot be changed back to private",
		"public transparency log",
		"does not publish source code, image layers, credentials, or Actions secrets",
		"generated from the corresponding platform manifest digest",
		"image signature binds to the final multi-architecture index digest",
		"two SPDX attestations bind separately",
		"run-attempt-specific artifact name",
		"download that exact artifact ID",
		"exactly ten files",
		"nine supply-chain files",
		"generate_release_notes: false",
		"append_body: false",
	} {
		if !strings.Contains(checklist, required) {
			t.Errorf("private release checklist is missing irreversible/privacy boundary %q", required)
		}
	}
	if strings.Contains(checklist, "all bind the same") {
		t.Fatal("private release checklist must distinguish platform provenance subjects from the multi-architecture index subject")
	}
}

func loadReleaseWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(findModuleRoot(t), ".github", "workflows", "release.yml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	return strings.ReplaceAll(string(contents), "\r\n", "\n")
}

func topLevelSection(t *testing.T, workflow, key string) string {
	t.Helper()
	section, err := topLevelSectionText(workflow, key)
	if err != nil {
		t.Fatal(err)
	}
	return section
}

func topLevelSectionText(workflow, key string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(workflow, "\n"), "\n")
	start := -1
	for i, line := range lines {
		if line == key+":" || strings.HasPrefix(line, key+": ") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("workflow is missing top-level %s section", key)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if line != "" && line[0] != ' ' && line[0] != '#' {
			end = i
			break
		}
	}
	section := strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
	return section, nil
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func assertPinnedUses(t *testing.T, workflow string, want map[string]string) {
	t.Helper()
	usesPattern := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	matches := usesPattern.FindAllStringSubmatch(workflow, -1)
	if len(matches) == 0 {
		t.Fatal("workflow contains no actions")
	}
	got := make(map[string][]string)
	for _, match := range matches {
		parts := strings.Split(match[1], "@")
		if len(parts) != 2 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(parts[1]) {
			t.Errorf("action is not pinned to a full commit SHA: %s", match[1])
			continue
		}
		got[parts[0]] = append(got[parts[0]], parts[1])
	}
	for action, sha := range want {
		values := got[action]
		if len(values) == 0 {
			t.Errorf("workflow is missing action %s", action)
			continue
		}
		for _, value := range values {
			if value != sha {
				t.Errorf("%s pinned to %s, want %s", action, value, sha)
			}
		}
		delete(got, action)
	}
	if len(got) > 0 {
		unexpected := make([]string, 0, len(got))
		for action := range got {
			unexpected = append(unexpected, action)
		}
		sort.Strings(unexpected)
		t.Errorf("workflow contains unreviewed actions: %v", unexpected)
	}
}

func parseWorkflowJobs(t *testing.T, workflow string) []workflowJob {
	t.Helper()
	jobs, err := parseWorkflowJobsText(workflow)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func parseWorkflowJobsText(workflow string) ([]workflowJob, error) {
	jobsSection, err := topLevelSectionText(workflow, "jobs")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(jobsSection, "\n")
	jobPattern := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):[[:space:]]*$`)
	jobs := make([]workflowJob, 0)
	seen := make(map[string]struct{})
	for index := 1; index < len(lines); {
		match := jobPattern.FindStringSubmatch(lines[index])
		if len(match) != 2 {
			if strings.TrimSpace(lines[index]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[index]), "#") {
				return nil, fmt.Errorf("unexpected jobs entry %q", strings.TrimSpace(lines[index]))
			}
			index++
			continue
		}
		name := match[1]
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate release workflow job %q", name)
		}
		seen[name] = struct{}{}
		start := index
		index++
		for index < len(lines) && len(jobPattern.FindStringSubmatch(lines[index])) != 2 {
			index++
		}
		body := strings.Join(lines[start:index], "\n")
		steps, err := parseNamedWorkflowStepsBodyText(body)
		if err != nil {
			return nil, fmt.Errorf("job %q: %w", name, err)
		}
		jobs = append(jobs, workflowJob{name: name, body: body, steps: steps})
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("release workflow contains no jobs")
	}
	return jobs, nil
}

func jobScalar(t *testing.T, job workflowJob, key string) string {
	t.Helper()
	value, err := jobScalarText(job, key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func jobScalarText(job workflowJob, key string) (string, error) {
	prefix := "    " + key + ":"
	value := ""
	found := false
	for _, line := range strings.Split(job.body, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found {
			return "", fmt.Errorf("job %q repeats %q", job.name, key)
		}
		found = true
		value = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	return value, nil
}

func jobScalarMap(t *testing.T, job workflowJob, key string) map[string]string {
	t.Helper()
	result, err := jobScalarMapText(job, key)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func jobScalarMapText(job workflowJob, key string) (map[string]string, error) {
	lines := strings.Split(job.body, "\n")
	header := "    " + key + ":"
	start := -1
	for index, line := range lines {
		if line == header {
			if start >= 0 {
				return nil, fmt.Errorf("job %q repeats %q", job.name, key)
			}
			start = index + 1
		}
	}
	if start < 0 {
		return map[string]string{}, nil
	}
	result := make(map[string]string)
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, "      ") || strings.HasPrefix(line, "       ") {
			break
		}
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("job %q %s contains non-scalar entry %q", job.name, key, line)
		}
		mapKey := strings.TrimSpace(parts[0])
		if _, exists := result[mapKey]; exists {
			return nil, fmt.Errorf("job %q %s repeats %q", job.name, key, mapKey)
		}
		result[mapKey] = strings.TrimSpace(parts[1])
	}
	return result, nil
}

func parseNamedWorkflowSteps(t *testing.T, workflow string) []workflowStep {
	t.Helper()
	steps, err := parseNamedWorkflowStepsText(workflow)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

func parseNamedWorkflowStepsText(workflow string) ([]workflowStep, error) {
	jobs, err := parseWorkflowJobsText(workflow)
	if err != nil {
		return nil, err
	}
	steps := make([]workflowStep, 0)
	seen := make(map[string]struct{})
	for _, job := range jobs {
		for _, step := range job.steps {
			if _, exists := seen[step.name]; exists {
				return nil, fmt.Errorf("duplicate workflow step name %q", step.name)
			}
			seen[step.name] = struct{}{}
			steps = append(steps, step)
		}
	}
	return steps, nil
}

func parseNamedWorkflowStepsBodyText(workflow string) ([]workflowStep, error) {
	const stepMarker = "      - "
	const stepsMarker = "\n    steps:\n"
	stepsStart := strings.Index(workflow, stepsMarker)
	if stepsStart < 0 {
		return nil, fmt.Errorf("release workflow contains no job steps section")
	}
	workflow = workflow[stepsStart+len(stepsMarker):]
	namePattern := regexp.MustCompile(`^      -[[:space:]]+name[[:space:]]*:[[:space:]]*(.+?)[[:space:]]*$`)
	lines := strings.Split(workflow, "\n")
	steps := make([]workflowStep, 0)
	seen := make(map[string]struct{})
	for index := 0; index < len(lines); {
		if !strings.HasPrefix(lines[index], stepMarker) {
			index++
			continue
		}
		match := namePattern.FindStringSubmatch(lines[index])
		if len(match) != 2 {
			return nil, fmt.Errorf("every release workflow step must have a name: %q", strings.TrimSpace(lines[index]))
		}
		name := strings.TrimSpace(match[1])
		if len(name) >= 2 && ((name[0] == '"' && name[len(name)-1] == '"') || (name[0] == '\'' && name[len(name)-1] == '\'')) {
			name = name[1 : len(name)-1]
		}
		if name == "" {
			return nil, fmt.Errorf("workflow contains an empty named step")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate workflow step name %q", name)
		}
		seen[name] = struct{}{}
		start := index
		index++
		for index < len(lines) && !strings.HasPrefix(lines[index], stepMarker) {
			index++
		}
		steps = append(steps, workflowStep{name: name, body: strings.Join(lines[start:index], "\n")})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("release workflow contains no named steps")
	}
	return steps, nil
}

func namedStep(t *testing.T, steps []workflowStep, name string) workflowStep {
	t.Helper()
	for _, step := range steps {
		if step.name == name {
			return step
		}
	}
	t.Fatalf("release workflow is missing step %q", name)
	return workflowStep{}
}

func assertStepOrder(t *testing.T, steps []workflowStep, names []string) {
	t.Helper()
	positions := make(map[string]int, len(steps))
	for index, step := range steps {
		if _, exists := positions[step.name]; exists {
			t.Fatalf("step order is ambiguous because %q is duplicated", step.name)
		}
		positions[step.name] = index
	}
	requested := make(map[string]struct{}, len(names))
	previous := -1
	for _, name := range names {
		if _, exists := requested[name]; exists {
			t.Fatalf("step order assertion repeats %q", name)
		}
		requested[name] = struct{}{}
		position, ok := positions[name]
		if !ok {
			t.Errorf("release workflow is missing step %q", name)
			continue
		}
		if position <= previous {
			t.Errorf("step %q is out of order", name)
		}
		previous = position
	}
}

func activeWorkflowLines(step workflowStep) []string {
	lines := make([]string, 0)
	for _, raw := range strings.Split(step.body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func hasActiveLine(step workflowStep, want string) bool {
	for _, line := range activeWorkflowLines(step) {
		if line == want {
			return true
		}
	}
	return false
}

func hasActiveLineSequence(step workflowStep, want []string) bool {
	if len(want) == 0 {
		return true
	}
	lines := activeWorkflowLines(step)
	for start := 0; start+len(want) <= len(lines); start++ {
		matches := true
		for offset := range want {
			if lines[start+offset] != want[offset] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func multilineFieldValues(t *testing.T, step workflowStep, key string) []string {
	t.Helper()
	values, err := multilineFieldValuesText(step, key)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func multilineFieldValuesText(step workflowStep, key string) ([]string, error) {
	lines := strings.Split(step.body, "\n")
	header := key + ": |"
	start := -1
	indent := -1
	for index, raw := range lines {
		if strings.TrimSpace(raw) != header {
			continue
		}
		if start >= 0 {
			return nil, fmt.Errorf("step %q repeats multiline field %q", step.name, key)
		}
		start = index + 1
		indent = len(raw) - len(strings.TrimLeft(raw, " "))
	}
	if start < 0 {
		return nil, fmt.Errorf("step %q is missing multiline field %q", step.name, key)
	}
	values := make([]string, 0)
	for _, raw := range lines[start:] {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		lineIndent := len(raw) - len(strings.TrimLeft(raw, " "))
		if lineIndent <= indent {
			break
		}
		values = append(values, strings.TrimSpace(raw))
	}
	return values, nil
}

func shellArrayValues(t *testing.T, step workflowStep, name string) []string {
	t.Helper()
	values, err := shellArrayValuesText(step, name)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func shellArrayValuesText(step workflowStep, name string) ([]string, error) {
	lines := activeWorkflowLines(step)
	header := name + "=("
	start := -1
	for index, line := range lines {
		if line == header {
			if start >= 0 {
				return nil, fmt.Errorf("step %q repeats shell array %q", step.name, name)
			}
			start = index + 1
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("step %q is missing shell array %q", step.name, name)
	}
	values := make([]string, 0)
	for _, line := range lines[start:] {
		if line == ")" {
			return values, nil
		}
		values = append(values, line)
	}
	return nil, fmt.Errorf("step %q shell array %q is not closed", step.name, name)
}

func activeUsesAction(line, action string) bool {
	if !strings.HasPrefix(line, "uses:") {
		return false
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "uses:"))
	return strings.HasPrefix(value, action+"@")
}

func yamlFieldAtIndent(raw string, indent int) (string, bool) {
	prefix := strings.Repeat(" ", indent)
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	rest := raw[indent:]
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return "", false
	}
	field := strings.TrimSpace(rest)
	if field == "" || strings.HasPrefix(field, "#") {
		return "", false
	}
	return field, true
}

func releaseControlKey(field string) (string, bool) {
	field = strings.TrimSpace(field)
	if strings.HasPrefix(field, "?") || strings.HasPrefix(field, "{") || strings.HasPrefix(field, "[") {
		return "opaque-yaml-control", true
	}
	colon := strings.IndexByte(field, ':')
	if colon < 0 {
		return "", false
	}
	key := strings.TrimSpace(field[:colon])
	if len(key) >= 2 && ((key[0] == '"' && key[len(key)-1] == '"') || (key[0] == '\'' && key[len(key)-1] == '\'')) {
		key = key[1 : len(key)-1]
	}
	switch key {
	case "if", "continue-on-error", "timeout-minutes", "background":
		return key, true
	default:
		return "", false
	}
}

func validateReleaseWorkflowSafety(workflow string) error {
	steps, err := parseNamedWorkflowStepsText(workflow)
	if err != nil {
		return err
	}
	for index, want := range releaseWorkflowStepNames {
		if index >= len(steps) {
			return fmt.Errorf("missing release workflow step %q at position %d", want, index+1)
		}
		if steps[index].name != want {
			return fmt.Errorf("unexpected release workflow step at position %d: got %q, want %q", index+1, steps[index].name, want)
		}
	}
	if len(steps) != len(releaseWorkflowStepNames) {
		return fmt.Errorf("unexpected release workflow step count: got %d, want %d", len(steps), len(releaseWorkflowStepNames))
	}

	if permissions, err := topLevelSectionText(workflow, "permissions"); err != nil || permissions != "permissions: {}" {
		return fmt.Errorf("release workflow must deny permissions by default")
	}
	jobs, err := parseWorkflowJobsText(workflow)
	if err != nil {
		return err
	}
	if len(jobs) != len(releaseWorkflowJobSpecs) {
		return fmt.Errorf("unexpected release workflow job count: got %d, want %d", len(jobs), len(releaseWorkflowJobSpecs))
	}
	for index, spec := range releaseWorkflowJobSpecs {
		job := jobs[index]
		if job.name != spec.name {
			return fmt.Errorf("unexpected release workflow job at position %d: got %q, want %q", index+1, job.name, spec.name)
		}
		needs, err := jobScalarText(job, "needs")
		if err != nil {
			return err
		}
		if needs != spec.needs {
			return fmt.Errorf("job %q needs %q, want %q", job.name, needs, spec.needs)
		}
		timeout, err := jobScalarText(job, "timeout-minutes")
		if err != nil {
			return err
		}
		if timeout != spec.timeout {
			return fmt.Errorf("forbidden release job control: job %q timeout is %q, want %q", job.name, timeout, spec.timeout)
		}
		permissions, err := jobScalarMapText(job, "permissions")
		if err != nil {
			return err
		}
		if !equalStringMap(permissions, spec.permissions) {
			return fmt.Errorf("job %q permissions are %v, want %v", job.name, permissions, spec.permissions)
		}
		outputs, err := jobScalarMapText(job, "outputs")
		if err != nil {
			return err
		}
		if !equalStringMap(outputs, spec.outputs) {
			return fmt.Errorf("job %q outputs are %v, want %v", job.name, outputs, spec.outputs)
		}
		if len(job.steps) != len(spec.steps) {
			return fmt.Errorf("job %q step count is %d, want %d", job.name, len(job.steps), len(spec.steps))
		}
		for stepIndex, want := range spec.steps {
			if job.steps[stepIndex].name != want {
				return fmt.Errorf("unexpected step in job %q at position %d: got %q, want %q", job.name, stepIndex+1, job.steps[stepIndex].name, want)
			}
		}
		for _, raw := range strings.Split(job.body, "\n") {
			field, ok := yamlFieldAtIndent(raw, 4)
			if !ok {
				continue
			}
			key, controlled := releaseControlKey(field)
			if !controlled {
				continue
			}
			if key == "timeout-minutes" && field == "timeout-minutes: "+spec.timeout {
				continue
			}
			return fmt.Errorf("forbidden release job control in %q: %q", job.name, field)
		}
		for _, step := range job.steps {
			for _, raw := range strings.Split(step.body, "\n") {
				field, ok := yamlFieldAtIndent(raw, 8)
				if !ok {
					continue
				}
				if _, controlled := releaseControlKey(field); controlled {
					return fmt.Errorf("forbidden step control (conditional or non-fatal step) in %q: %q", step.name, field)
				}
			}
		}
	}

	positions := make(map[string]int, len(steps))
	for index, step := range steps {
		positions[step.name] = index
	}
	preflight, ok := positions["Check GHCR visibility before push"]
	if !ok {
		return fmt.Errorf("release workflow is missing the pre-push visibility gate")
	}
	postflight, ok := positions["Confirm GHCR visibility after push"]
	if !ok {
		return fmt.Errorf("release workflow is missing the post-push visibility gate")
	}
	if preflight >= postflight {
		return fmt.Errorf("release visibility gates are out of order")
	}

	gatesPosition, ok := positions["Verify source and release gates"]
	if !ok {
		return fmt.Errorf("release workflow is missing the source and release gate")
	}
	gates := steps[gatesPosition]
	for _, command := range []string{
		`test "$(go list -m all)" = "iosbackup"`,
		"go vet ./...",
		"go test -count=1 -run '^TestPackageDependencyDirection$' ./internal/app",
		"go test -count=1 ./...",
		"./scripts/check_public_repo.sh --directory .",
		"./scripts/verify_licensing.sh",
	} {
		if !hasActiveLine(gates, command) {
			return fmt.Errorf("required active release gate %q is missing", command)
		}
	}
	preflightStep := steps[preflight]
	if !hasActiveLine(preflightStep, `jq -e '.repository.private == true' "$GITHUB_EVENT_PATH" >/dev/null`) {
		return fmt.Errorf("required active pre-push gate for the private repository is missing")
	}
	postflightStep := steps[postflight]
	for _, command := range []string{`test "$status" = "200"`, `test "$visibility" = "private"`} {
		if !hasActiveLine(postflightStep, command) {
			return fmt.Errorf("required active post-push gate %q is missing", command)
		}
	}

	requiredStepLines := map[string][]string{
		"Set up QEMU": {
			"image: docker.io/tonistiigi/binfmt@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0",
			"platforms: arm64",
		},
		"Set up Docker Buildx": {
			"version: v0.36.1",
			"image=docker.io/moby/buildkit@sha256:28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8",
		},
		"Generate linux/amd64 SPDX JSON SBOM": {
			"image: ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}",
			"output-file: dist/iosbackup-linux-amd64.spdx.json",
		},
		"Generate linux/arm64 SPDX JSON SBOM": {
			"image: ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}",
			"output-file: dist/iosbackup-linux-arm64.spdx.json",
		},
		"Sign multi-architecture index with Cosign keyless": {
			`cosign sign --yes "ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}"`,
		},
		"Attest linux/amd64 SPDX SBOM with Cosign keyless": {
			`cosign attest --yes --type spdxjson --predicate dist/iosbackup-linux-amd64.spdx.json "ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}"`,
		},
		"Attest linux/arm64 SPDX SBOM with Cosign keyless": {
			`cosign attest --yes --type spdxjson --predicate dist/iosbackup-linux-arm64.spdx.json "ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}"`,
		},
		"Verify Cosign signature and platform SBOM attestations": {
			`"ghcr.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}" \`,
			`"ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}" \`,
			`"ghcr.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}" \`,
		},
		"Generate deterministic release notes": {
			"VERSION: ${{ needs.validate.outputs.version }}",
			`heading_count="$(grep -Fxc -- "$heading" CHANGELOG.md)"`,
			`test "$heading_count" = "1"`,
			`next_heading="$(awk -v heading="$heading" '$0 == heading { found=1; next } found && /^## / { print; exit }' CHANGELOG.md)"`,
			`test -n "$next_heading"`,
			`' CHANGELOG.md > dist/iosbackup-release-notes.md`,
			"test -s dist/iosbackup-release-notes.md",
		},
		"Upload verified release assets": {
			"id: assets",
			"name: iosbackup-release-assets-${{ github.run_id }}-${{ github.run_attempt }}",
			"overwrite: false",
		},
		"Download verified release assets": {
			"artifact-ids: ${{ needs.publish.outputs.artifact_id }}",
			"merge-multiple: true",
			"digest-mismatch: error",
		},
		"Validate downloaded release assets": {
			`test "$image_ref" = "ghcr.io/razeencheng/iosbackup@${{ needs.publish.outputs.image_digest }}"`,
			`diff -u "$expected" "$actual"`,
			"test -s dist/iosbackup-release-notes.md",
		},
		"Create GitHub prerelease": {
			"body_path: dist/iosbackup-release-notes.md",
			"generate_release_notes: false",
			"append_body: false",
		},
	}
	for stepName, requiredLines := range requiredStepLines {
		stepPosition, ok := positions[stepName]
		if !ok {
			return fmt.Errorf("release workflow is missing step %q", stepName)
		}
		for _, line := range requiredLines {
			if !hasActiveLine(steps[stepPosition], line) {
				return fmt.Errorf("required active release contract line %q is missing from %q", line, stepName)
			}
		}
	}

	uploadPaths, err := multilineFieldValuesText(steps[positions["Upload verified release assets"]], "path")
	if err != nil {
		return err
	}
	if strings.Join(uploadPaths, "\n") != strings.Join(releaseTransferPaths, "\n") {
		return fmt.Errorf("uploaded release transfer list is %v, want %v", uploadPaths, releaseTransferPaths)
	}
	wantTransferredBasenames := make([]string, 0, len(releaseTransferPaths))
	for _, path := range releaseTransferPaths {
		wantTransferredBasenames = append(wantTransferredBasenames, strings.TrimPrefix(path, "dist/"))
	}
	expectedAssets, err := shellArrayValuesText(steps[positions["Validate downloaded release assets"]], "expected_assets")
	if err != nil {
		return err
	}
	if strings.Join(expectedAssets, "\n") != strings.Join(wantTransferredBasenames, "\n") {
		return fmt.Errorf("downloaded release transfer list is %v, want %v", expectedAssets, wantTransferredBasenames)
	}
	releaseFiles, err := multilineFieldValuesText(steps[positions["Create GitHub prerelease"]], "files")
	if err != nil {
		return err
	}
	if strings.Join(releaseFiles, "\n") != strings.Join(releaseSupplyChainAssetPaths, "\n") {
		return fmt.Errorf("GitHub prerelease attachment list is %v, want %v", releaseFiles, releaseSupplyChainAssetPaths)
	}

	for index, step := range steps {
		for _, line := range activeWorkflowLines(step) {
			switch {
			case activeUsesAction(line, "docker/login-action"):
				if step.name != "Log in to GHCR" || index <= preflight || index >= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case activeUsesAction(line, "docker/build-push-action"), pushTrueWorkflowLineRE.MatchString(line):
				if step.name != "Build and push multi-architecture image" || index <= preflight || index >= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case activeUsesAction(line, "sigstore/cosign-installer"):
				if step.name != "Install Cosign" || index <= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case strings.HasPrefix(line, "cosign sign "):
				if step.name != "Sign multi-architecture index with Cosign keyless" || index <= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case strings.HasPrefix(line, "cosign attest "):
				if (step.name != "Attest linux/amd64 SPDX SBOM with Cosign keyless" && step.name != "Attest linux/arm64 SPDX SBOM with Cosign keyless") || index <= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case strings.HasPrefix(line, "cosign verify "), strings.HasPrefix(line, "cosign verify-attestation "):
				if step.name != "Verify Cosign signature and platform SBOM attestations" || index <= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case strings.HasPrefix(line, "cosign "):
				return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
			case activeUsesAction(line, "actions/upload-artifact"):
				if step.name != "Upload verified release assets" || index <= postflight {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case activeUsesAction(line, "actions/download-artifact"):
				if step.name != "Download verified release assets" {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case activeUsesAction(line, "softprops/action-gh-release"):
				if step.name != "Create GitHub prerelease" || index <= positions["Validate downloaded release assets"] {
					return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
				}
			case strings.HasPrefix(line, "docker login "), strings.HasPrefix(line, "docker push "), strings.HasPrefix(line, "docker image push "), strings.HasPrefix(line, "gh release create "):
				return fmt.Errorf("unexpected publishing side effect %q in step %q", line, step.name)
			}
		}
	}
	return nil
}
