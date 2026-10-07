package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseDockerHubPreflight(t *testing.T) {
	step := namedStep(t, parseNamedWorkflowSteps(t, loadReleaseWorkflow(t)), "Check Docker Hub publication settings")
	script := strings.Join(multilineFieldValues(t, step, "run"), "\n")
	const public = `{"namespace":"razeencheng","name":"iosbackup","is_private":false}`
	for _, tc := range []struct {
		name, username, token, body, curlExit string
		wantErr                               bool
	}{
		{"configured public repository", "razeencheng", "fixture-token", public, "0", false},
		{"missing username", "", "fixture-token", public, "0", true},
		{"wrong username", "someone-else", "fixture-token", public, "0", true},
		{"missing token", "razeencheng", "", public, "0", true},
		{"private repository", "razeencheng", "fixture-token", `{"namespace":"razeencheng","name":"iosbackup","is_private":true}`, "0", true},
		{"missing visibility", "razeencheng", "fixture-token", `{}`, "0", true},
		{"lookup failure", "razeencheng", "fixture-token", public, "22", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mock := "#!/bin/sh\nprintf '%s' \"$IOSBK_TEST_BODY\"\nexit \"$IOSBK_TEST_CURL_EXIT\"\n"
			if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DOCKERHUB_USERNAME="+tc.username, "DOCKERHUB_TOKEN="+tc.token,
				"IOSBK_TEST_BODY="+tc.body, "IOSBK_TEST_CURL_EXIT="+tc.curlExit)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("preflight error = %v, want error %v; output: %s", err, tc.wantErr, output)
			}
			if strings.Contains(string(output), "fixture-token") {
				t.Fatal("preflight exposed the registry token")
			}
		})
	}
}

func TestReleaseDockerHubDigestMatchesBuild(t *testing.T) {
	step := namedStep(t, parseNamedWorkflowSteps(t, loadReleaseWorkflow(t)), "Confirm Docker Hub image digest")
	script := strings.Join(multilineFieldValues(t, step, "run"), "\n")
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, body, dockerExit string
		wantErr                bool
	}{
		{"same index", `{"digest":"` + digest + `"}`, "0", false},
		{"different index", `{"digest":"sha256:wrong"}`, "0", true},
		{"absent index", `{}`, "0", true},
		{"registry failure", `{"digest":"` + digest + `"}`, "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mock := `#!/bin/sh
test "$1 $2 $3 $4" = 'buildx imagetools inspect docker.io/razeencheng/iosbackup:v1.5.4' || exit 99
printf '%s' "$IOSBK_TEST_BODY"
exit "$IOSBK_TEST_DOCKER_EXIT"
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"VERSION=v1.5.4", "IMAGE_DIGEST="+digest,
				"IOSBK_TEST_BODY="+tc.body, "IOSBK_TEST_DOCKER_EXIT="+tc.dockerExit)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("digest gate error = %v, want error %v; output: %s", err, tc.wantErr, output)
			}
		})
	}
}

func TestReleaseDockerHubSharesBuildAndVerifiesEvidence(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	steps := parseNamedWorkflowSteps(t, workflow)
	build := namedStep(t, steps, "Build and push multi-architecture image")
	wantTags := []string{
		"ghcr.io/razeencheng/iosbackup:${{ needs.validate.outputs.version }}",
		"docker.io/razeencheng/iosbackup:${{ needs.validate.outputs.version }}",
	}
	if got := multilineFieldValues(t, build, "tags"); strings.Join(got, "\n") != strings.Join(wantTags, "\n") {
		t.Fatalf("one build must push only the two official version tags: %v", got)
	}
	if strings.Count(workflow, "uses: docker/build-push-action@") != 1 {
		t.Fatal("dual registry publication must use one build")
	}
	assertStepOrder(t, steps, []string{"Check Docker Hub publication settings", "Log in to Docker Hub", "Build and push multi-architecture image", "Confirm Docker Hub image digest", "Verify Docker Hub signature and SBOM attestations", "Upload verified release assets"})
	verify := namedStep(t, steps, "Verify Docker Hub signature and SBOM attestations")
	for _, flag := range []string{`--certificate-identity "$identity"`, `--certificate-oidc-issuer "$issuer"`} {
		if strings.Count(verify.body, flag) != 3 {
			t.Errorf("all Docker Hub evidence must enforce %s", flag)
		}
	}
	for _, line := range []string{
		`identity="https://github.com/$GITHUB_REPOSITORY/.github/workflows/release.yml@$GITHUB_REF"`,
		`issuer="https://token.actions.githubusercontent.com"`,
		`"docker.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}" \`,
		`"docker.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}" \`,
		`"docker.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}" \`,
	} {
		if !hasActiveLine(verify, line) {
			t.Errorf("Docker Hub verification missing %s", line)
		}
	}
	for _, forbidden := range []string{"--insecure-ignore", "--check-claims=false", "--private-infrastructure"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("verification must not bypass signature checks: %s", forbidden)
		}
	}
	for _, name := range []string{"Log in to Docker Hub", "Log in to Docker Hub for promotion"} {
		step := namedStep(t, steps, name)
		for _, line := range []string{"registry: docker.io", "username: ${{ vars.DOCKERHUB_USERNAME }}", "password: ${{ secrets.DOCKERHUB_TOKEN }}"} {
			if !hasActiveLine(step, line) {
				t.Errorf("%s missing %s", name, line)
			}
		}
	}
	for _, item := range []struct{ step, command string }{
		{"Sign multi-architecture index with Cosign keyless", `cosign sign --yes "docker.io/razeencheng/iosbackup@${{ steps.image.outputs.digest }}"`},
		{"Attest linux/amd64 SPDX SBOM with Cosign keyless", `cosign attest --yes --type spdxjson --predicate dist/iosbackup-linux-amd64.spdx.json "docker.io/razeencheng/iosbackup@${{ steps.platforms.outputs.amd64_digest }}"`},
		{"Attest linux/arm64 SPDX SBOM with Cosign keyless", `cosign attest --yes --type spdxjson --predicate dist/iosbackup-linux-arm64.spdx.json "docker.io/razeencheng/iosbackup@${{ steps.platforms.outputs.arm64_digest }}"`},
	} {
		if !hasActiveLine(namedStep(t, steps, item.step), item.command) {
			t.Errorf("Docker Hub evidence missing %s", item.command)
		}
	}
}
