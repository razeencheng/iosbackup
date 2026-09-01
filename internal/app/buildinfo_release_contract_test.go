package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerBuildContextIncludesBuildInfoPackage(t *testing.T) {
	root := findModuleRoot(t)
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)

	for _, required := range []string{
		"COPY cmd/ ./cmd/",
		"COPY internal/ ./internal/",
		"./cmd/iosbackup",
		"-X iosbackup/internal/buildinfo.Version=",
		"-X iosbackup/internal/buildinfo.BuildDate=",
		"-X iosbackup/internal/buildinfo.Commit=",
		"-X iosbackup/internal/buildinfo.SourceURL=",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Dockerfile missing build metadata closure %q", required)
		}
	}
	if strings.Contains(text, "-X main.") {
		t.Fatal("Dockerfile still injects removed main-package build metadata")
	}
}

func TestPublicRepoCheckTargetsBuildInfoPackage(t *testing.T) {
	root := findModuleRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "scripts/check_public_repo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)

	for _, required := range []string{
		"Dockerfile",
		"internal/buildinfo/buildinfo.go",
		"iosbackup/internal/buildinfo.",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("public repository check missing %q", required)
		}
	}
	if strings.Contains(text, "version.go") {
		t.Fatal("public repository check still references deleted version.go")
	}
	if !strings.Contains(text, "-X main[.]") {
		t.Fatal("public repository check does not reject legacy main-package ldflags")
	}
}
