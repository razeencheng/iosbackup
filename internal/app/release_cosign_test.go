package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the actual workflow shell with a stand-in for the network/OIDC CLI.
// Cosign emits one DSSE envelope per line, even with --output json.
func TestReleaseCosignAttestationOutput(t *testing.T) {
	for _, name := range []string{"Verify Cosign signature and platform SBOM attestations", "Verify Docker Hub signature and SBOM attestations"} {
		t.Run(name, func(t *testing.T) { testReleaseCosignOutput(t, name) })
	}
}

func testReleaseCosignOutput(t *testing.T, stepName string) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("release workflow test requires %s: %v", tool, err)
		}
	}
	step := namedStep(t, parseNamedWorkflowSteps(t, loadReleaseWorkflow(t)), stepName)
	_, body, ok := strings.Cut(step.body, "        run: |\n")
	if !ok {
		t.Fatal("verification step has no shell body")
	}
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "          ")
	}
	script := strings.NewReplacer(
		"${{ steps.image.outputs.digest }}", "sha256:index",
		"${{ steps.platforms.outputs.amd64_digest }}", "sha256:amd64",
		"${{ steps.platforms.outputs.arm64_digest }}", "sha256:arm64",
	).Replace(strings.Join(lines, "\n"))
	const envelope = `{"payloadType":"application/vnd.in-toto+json","payload":"e30=","signatures":[{"sig":"fixture"}]}`
	for _, tc := range []struct {
		name    string
		output  string
		fail    string
		count   int
		wantErr bool
	}{
		{name: "single envelope", output: envelope, count: 1},
		{name: "multiple envelopes", output: envelope + "\n" + envelope, count: 2},
		{name: "empty output", wantErr: true},
		{name: "invalid JSON", output: "{", wantErr: true},
		{name: "unexpected array", output: "[" + envelope + "]", wantErr: true},
		{name: "signature verification failure", output: envelope, fail: "sha256:index", wantErr: true},
		{name: "amd64 verification failure", output: envelope, fail: "sha256:amd64", wantErr: true},
		{name: "arm64 verification failure", output: envelope, fail: "sha256:arm64", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "dist"), 0700); err != nil {
				t.Fatal(err)
			}
			// Failed verification may already have written output; pipefail must
			// still stop the workflow even if jq can parse that output.
			mock := `#!/bin/sh
if [ "$1" = verify ]; then
  printf '%s\n' '[{}]'
else
  printf '%s' "$IOSBK_COSIGN_TEST_OUTPUT"
fi
for arg do last=$arg; done
if [ -n "$IOSBK_COSIGN_TEST_FAIL" ]; then
  case "$last" in *@"$IOSBK_COSIGN_TEST_FAIL") exit 7 ;; esac
fi
`
			if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_REPOSITORY=razeencheng/iosbackup", "GITHUB_REF=refs/tags/v1.5.3",
				"IOSBK_COSIGN_TEST_OUTPUT="+tc.output, "IOSBK_COSIGN_TEST_FAIL="+tc.fail)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("verification error = %v, want error %v; output: %s", err, tc.wantErr, output)
			}
			if tc.wantErr || stepName == "Verify Docker Hub signature and SBOM attestations" {
				return
			}
			for _, arch := range []string{"amd64", "arm64"} {
				data, err := os.ReadFile(filepath.Join(dir, "dist", "iosbackup-linux-"+arch+"-cosign-sbom-attestation-verification.json"))
				if err != nil {
					t.Fatal(err)
				}
				var envelopes []json.RawMessage
				if err := json.Unmarshal(data, &envelopes); err != nil || len(envelopes) != tc.count {
					t.Fatalf("%s evidence must be one array of %d envelopes: %s (%v)", arch, tc.count, data, err)
				}
				for _, actual := range envelopes {
					var got, want any
					if err := json.Unmarshal(actual, &got); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(envelope), &want); err != nil {
						t.Fatal(err)
					}
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					if string(gotJSON) != string(wantJSON) {
						t.Fatalf("normalization altered the signed envelope: %s", actual)
					}
				}
			}
		})
	}
}
