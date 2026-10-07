package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseLatestPromotion(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("workflow shell tests require jq")
	}
	step := namedStep(t, parseNamedWorkflowSteps(t, loadReleaseWorkflow(t)), "Promote newest stable image to latest")
	script := strings.Join(multilineFieldValues(t, step, "run"), "\n") + "\n"
	digest := "sha256:" + strings.Repeat("a", 64)
	stable := `[[{"tag_name":"v1.9.9","prerelease":false,"draft":false}], [{"tag_name":"v1.10.0","prerelease":false,"draft":false},{"tag_name":"v2.0.0-beta.1","prerelease":true,"draft":false},{"tag_name":"v3.0.0","prerelease":false,"draft":true}]]`
	tests := []struct {
		name, version, releases, failure string
		wantPush, wantEdit, wantFailure  bool
	}{
		{name: "stable uses same digest and numeric version order", version: "v1.10.0", releases: stable, wantPush: true, wantEdit: true},
		{name: "old stable rerun cannot move latest backwards", version: "v1.9.9", releases: stable},
		{name: "beta never promotes", version: "v2.0.0-beta.1", releases: stable},
		{name: "rc never promotes", version: "v2.0.0-rc.1", releases: stable},
		{name: "no stable release fails closed", version: "v1.10.0", releases: `[[]]`, wantFailure: true},
		{name: "missing current release fails closed", version: "v1.11.0", releases: stable, wantFailure: true},
		{name: "release API failure", version: "v1.10.0", releases: stable, failure: "api", wantFailure: true},
		{name: "malformed release response", version: "v1.10.0", releases: `{}`, wantFailure: true},
		{name: "push failure does not mark GitHub latest", version: "v1.10.0", releases: stable, failure: "push", wantPush: true, wantFailure: true},
		{name: "digest mismatch does not mark GitHub latest", version: "v1.10.0", releases: stable, failure: "digest", wantPush: true, wantFailure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("releases.json", test.releases, 0600)
			write("gh", `#!/bin/sh
set -eu
printf 'gh %s\n' "$*" >> "$IOSBK_TEST_LOG"
case "$1" in
  api) [ "$IOSBK_TEST_FAILURE" != api ]; cat "$IOSBK_TEST_RELEASES" ;;
  release) test "$2" = edit ;;
  *) exit 2 ;;
esac
`, 0700)
			write("docker", `#!/bin/sh
set -eu
printf 'docker %s\n' "$*" >> "$IOSBK_TEST_LOG"
test "$1 $2" = 'buildx imagetools'
case "$3" in
  create) [ "$IOSBK_TEST_FAILURE" != push ] ;;
  inspect)
    if [ "$IOSBK_TEST_FAILURE" = digest ]; then
      printf '{"digest":"sha256:wrong"}\n'
    else
      printf '{"digest":"%s"}\n' "$IMAGE_DIGEST"
    fi ;;
  *) exit 2 ;;
esac
`, 0700)
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"VERSION="+test.version, "IMAGE_DIGEST="+digest,
				"GITHUB_REPOSITORY=razeencheng/iosbackup",
				"IOSBK_TEST_RELEASES="+filepath.Join(dir, "releases.json"),
				"IOSBK_TEST_LOG="+filepath.Join(dir, "calls"), "IOSBK_TEST_FAILURE="+test.failure)
			output, err := cmd.CombinedOutput()
			if (err != nil) != test.wantFailure {
				t.Fatalf("error = %v, want failure %v; output: %s", err, test.wantFailure, output)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			log := string(calls)
			if got := strings.Contains(log, "docker buildx imagetools create"); got != test.wantPush {
				t.Fatalf("push = %v, want %v; calls: %s", got, test.wantPush, log)
			}
			if got := strings.Contains(log, "gh release edit"); got != test.wantEdit {
				t.Fatalf("GitHub latest update = %v, want %v; calls: %s", got, test.wantEdit, log)
			}
			if test.wantPush && !strings.Contains(log, "--tag ghcr.io/razeencheng/iosbackup:latest ghcr.io/razeencheng/iosbackup@"+digest) {
				t.Fatalf("latest must reuse the verified multi-architecture digest; calls: %s", log)
			}
		})
	}
}

func TestReleasePublicVisibilityGates(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("workflow shell tests require jq")
	}
	steps := parseNamedWorkflowSteps(t, loadReleaseWorkflow(t))
	for _, test := range []struct {
		name, step, event, status, body string
		wantFailure                     bool
	}{
		{"public repository and package", "Check GHCR visibility before push", `{"repository":{"private":false}}`, "200", `{"visibility":"public"}`, false},
		{"private repository rejected", "Check GHCR visibility before push", `{"repository":{"private":true}}`, "200", `{"visibility":"public"}`, true},
		{"existing private package rejected", "Check GHCR visibility before push", `{"repository":{"private":false}}`, "200", `{"visibility":"private"}`, true},
		{"absent package can be created", "Check GHCR visibility before push", `{"repository":{"private":false}}`, "404", `{}`, false},
		{"denied package lookup rejected", "Check GHCR visibility before push", `{"repository":{"private":false}}`, "403", `{}`, true},
		{"malformed visibility rejected", "Check GHCR visibility before push", `{"repository":{"private":false}}`, "200", `{}`, true},
		{"public package passes postflight", "Confirm GHCR visibility after push", `{}`, "200", `{"visibility":"public"}`, false},
		{"new private package blocks release", "Confirm GHCR visibility after push", `{}`, "200", `{"visibility":"private"}`, true},
		{"missing package blocks release", "Confirm GHCR visibility after push", `{}`, "404", `{}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"event.json": test.event,
				"body.json":  test.body,
				"sleep":      "#!/bin/sh\nexit 0\n",
				"curl": `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) shift; cp "$IOSBK_TEST_BODY" "$1" ;;
    -X|--request|--data|--data-raw) exit 2 ;;
  esac
  shift
done
printf '%s' "$IOSBK_TEST_STATUS"
`,
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			step := namedStep(t, steps, test.step)
			cmd := exec.Command("bash", "-c", strings.Join(multilineFieldValues(t, step, "run"), "\n"))
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_EVENT_PATH="+filepath.Join(dir, "event.json"), "RUNNER_TEMP="+dir,
				"GITHUB_TOKEN=offline-test", "IOSBK_TEST_BODY="+filepath.Join(dir, "body.json"),
				"IOSBK_TEST_STATUS="+test.status)
			output, err := cmd.CombinedOutput()
			if (err != nil) != test.wantFailure {
				t.Fatalf("error = %v, want failure %v; output: %s", err, test.wantFailure, output)
			}
		})
	}
}
