package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"iosbackup/internal/buildinfo"
)

var remoteAssetRE = regexp.MustCompile(`(?i)<(?:link|script|img)[^>]+(?:href|src)=["'](?:https?:)?//`)

func TestEmbeddedPagesHaveNoRemoteAssets(t *testing.T) {
	for _, name := range []string{"templates/index.html", "templates/notifications.html"} {
		data, err := templateFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if match := remoteAssetRE.Find(data); match != nil {
			t.Fatalf("%s contains remote runtime asset: %s", name, match)
		}
	}
}

func TestVersionEndpointContainsSourceIdentity(t *testing.T) {
	app := newApplication()
	rr := httptest.NewRecorder()
	app.handleVersion(rr, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["source_url"] != buildinfo.SourceURL || response["commit"] != buildinfo.Commit || response["license"] != buildinfo.LicenseID {
		t.Fatalf("missing source identity: %#v", response)
	}
}

func TestSourceLinkIsVisibleOnEveryPage(t *testing.T) {
	for _, name := range []string{
		"templates/index.html",
		"templates/login.html",
		"templates/notifications.html",
		"templates/onboarding.html",
	} {
		data, err := templateFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		page := string(data)
		if !strings.Contains(page, "{{.SourceURL}}/tree/{{.Commit}}") || !strings.Contains(page, "AGPL-3.0-only") {
			t.Fatalf("%s lacks visible source/license footer", name)
		}
	}
}

func TestPublicReleaseRequiredFilesExist(t *testing.T) {
	required := []string{
		"CHANGELOG.md", "SECURITY.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SUPPORT.md",
		"compose.yaml", ".dockerignore", "docs/PRIVACY.md", "scripts/check_public_repo.sh", ".github/workflows/ci.yml",
		"third_party/components.lock.json", "third_party/runtime-closure-linux-amd64.txt", "third_party/runtime-closure-linux-arm64.txt",
	}
	for _, name := range required {
		if info, err := os.Stat(repoTestPath(t, name)); err != nil || info.IsDir() {
			t.Errorf("公开发布必需文件缺失: %s (err=%v)", name, err)
		}
	}
}

func TestReleaseMetadataAndLicenseTextHaveNoPlaceholdersOrContradiction(t *testing.T) {
	if buildinfo.SourceURL == "" || strings.Contains(buildinfo.SourceURL, "OWNER") || strings.Contains(buildinfo.SourceURL, "REPO") {
		t.Fatalf("SourceURL 仍是发布占位符: %q", buildinfo.SourceURL)
	}
	readme, err := os.ReadFile(repoTestPath(t, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "此项目基于 MIT 许可证开源") {
		t.Fatal("README 同时声明 AGPL 与 MIT")
	}
	dockerfile, err := os.ReadFile(repoTestPath(t, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"github.com/OWNER", "IOSBK_COMMIT=unknown"} {
		if strings.Contains(string(dockerfile), placeholder) {
			t.Fatalf("Dockerfile 含发布占位符 %q", placeholder)
		}
	}
}

func TestDockerBuildDisablesUnusedCythonBindings(t *testing.T) {
	dockerfile, err := os.ReadFile(repoTestPath(t, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)
	if strings.Contains(text, "cython3") || strings.Contains(text, "pip install cython") {
		t.Fatal("正式镜像不应为未使用的 Python bindings 安装 Cython")
	}
	if strings.Count(text, "./autogen.sh --without-cython") < 2 {
		t.Fatal("libplist 与 libimobiledevice 必须显式关闭未使用的 Cython bindings")
	}
}

func TestDockerAndMakeBuildSupportAMD64AndARM64(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	for _, marker := range []string{
		"ARG TARGETOS",
		"ARG TARGETARCH",
		`GOOS="${TARGETOS}" GOARCH="${TARGETARCH}"`,
		"arm64) loader=/lib/ld-linux-aarch64.so.1",
		"target=/final/lib/ld-linux-aarch64.so.1",
	} {
		if !strings.Contains(dockerfile, marker) {
			t.Fatalf("Dockerfile must contain multi-architecture marker %q", marker)
		}
	}
	if strings.Contains(dockerfile, "ENV GOARCH=amd64") {
		t.Fatal("Dockerfile must not hardcode the Go binary to amd64")
	}

	makefile := readRepoFile(t, "Makefile")
	for _, marker := range []string{
		"PLATFORM ?= linux/amd64",
		"MULTI_PLATFORMS ?= linux/amd64,linux/arm64/v8",
		"build-multi:",
		`--output "type=oci,dest=$(OCI_OUTPUT)"`,
	} {
		if !strings.Contains(makefile, marker) {
			t.Fatalf("Makefile must contain multi-architecture marker %q", marker)
		}
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(repoTestPath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func repoTestPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(findModuleRoot(t), filepath.FromSlash(name))
}

func TestDockerBuildAppliesLockedAFCReceivePatch(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	lock := readRepoFile(t, "third_party/components.lock.json")
	patchPath := "third_party/patches/libimobiledevice/afc-return-after-receive-error.patch"

	if !strings.Contains(dockerfile, "patch -p1 < /build/patches/libimobiledevice-afc-return-after-receive-error.patch") {
		t.Fatal("Dockerfile must apply the locked libimobiledevice AFC receive patch")
	}
	if !strings.Contains(lock, patchPath) {
		t.Fatalf("component lock must record %s", patchPath)
	}
	patch := readRepoFile(t, patchPath)
	if !strings.Contains(patch, "return err;") {
		t.Fatal("AFC receive patch must return immediately after the failed receive")
	}
}

func TestDockerBuildAppliesLockedNetmuxdObservabilityPatch(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	lock := readRepoFile(t, "third_party/components.lock.json")
	patchPath := "third_party/patches/netmuxd/observability.patch"

	if !strings.Contains(dockerfile, "pkg-config libssl-dev git cmake patch") {
		t.Fatal("netmuxd builder must install patch in its own build stage")
	}
	if !strings.Contains(dockerfile, "patch -p1 < /build/patches/netmuxd-observability.patch") {
		t.Fatal("Dockerfile must apply the locked netmuxd observability patch")
	}
	if !strings.Contains(lock, patchPath) {
		t.Fatalf("component lock must record %s", patchPath)
	}
	patch := readRepoFile(t, patchPath)
	for _, marker := range []string{
		"event=heartbeat_failed",
		"event=heartbeat_sleep_notification",
		"event=proxy_socket_opened",
		"event=proxy_stream_stopped",
	} {
		if !strings.Contains(patch, marker) {
			t.Fatalf("netmuxd observability patch lacks %q", marker)
		}
	}
	if !strings.Contains(patch, `-                        println!("{}", plist_macro::pretty_print_dictionary(&res));`) {
		t.Fatal("netmuxd observability patch must remove the ListDevices response dump")
	}
	sleepyTimePattern := "Err(IdeviceError::Heartbeat(HeartbeatError::SleepyTime))"
	if !strings.Contains(patch, sleepyTimePattern) {
		t.Fatal("netmuxd patch must keep SleepyTime notifications out of the fatal heartbeat path")
	}
	sleepBranchStart := strings.Index(patch, sleepyTimePattern)
	sleepBranchEnd := strings.Index(patch[sleepBranchStart:], "Err(e) =>")
	if sleepBranchEnd < 0 {
		t.Fatal("netmuxd patch must retain a separate fatal heartbeat receive branch")
	}
	sleepBranch := patch[sleepBranchStart : sleepBranchStart+sleepBranchEnd]
	if !strings.Contains(sleepBranch, "action=send_polo_keep_device_and_streams") {
		t.Fatal("SleepyTime handling must record that Polo keeps the device and streams alive")
	}
	if !strings.Contains(sleepBranch, "heartbeat_client.send_polo().await") {
		t.Fatal("SleepyTime handling must reply with Polo before waiting for the next heartbeat")
	}
}

func TestDockerBuildReconnectsHeartbeatAfterSleepNotification(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	lock := readRepoFile(t, "third_party/components.lock.json")
	patchPath := "third_party/patches/netmuxd/heartbeat-reconnect-after-sleep.patch"

	if !strings.Contains(dockerfile, "patch -p1 < /build/patches/netmuxd-heartbeat-reconnect-after-sleep.patch") {
		t.Fatal("Dockerfile must apply the locked netmuxd heartbeat reconnect patch")
	}
	if !strings.Contains(lock, patchPath) {
		t.Fatalf("component lock must record %s", patchPath)
	}
	patch := readRepoFile(t, patchPath)
	for _, marker := range []string{
		"action=send_polo_and_reconnect_keep_device_and_streams",
		"connect_heartbeat(&device, &pairing_file).await",
		"event=heartbeat_reconnected_after_sleep",
	} {
		if !strings.Contains(patch, marker) {
			t.Fatalf("netmuxd heartbeat reconnect patch lacks %q", marker)
		}
	}
	if !strings.Contains(patch, "device: device.clone()") {
		t.Fatal("initial manager add must clone the device so reconnect can reuse its address")
	}
}

func TestDockerBuildRestoresNetmuxdHelperBinaries(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	lock := readRepoFile(t, "third_party/components.lock.json")
	patchPath := "third_party/patches/netmuxd/restore-helper-binaries.patch"

	if !strings.Contains(dockerfile, "patch -p1 < /build/patches/netmuxd-restore-helper-binaries.patch") {
		t.Fatal("Dockerfile must restore the netmuxd helper binary targets")
	}
	if !strings.Contains(lock, patchPath) {
		t.Fatalf("component lock must record %s", patchPath)
	}
	patch := readRepoFile(t, patchPath)
	for _, binary := range []string{`name = "add_device"`, `name = "passthrough"`} {
		if !strings.Contains(patch, binary) {
			t.Fatalf("netmuxd helper patch lacks %s", binary)
		}
	}
}
