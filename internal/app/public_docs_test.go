package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"iosbackup/internal/buildinfo"
)

const publicReleaseImage = "ghcr.io/razeencheng/iosbackup:latest"

var markdownLinkRE = regexp.MustCompile(`!?\[[^]]*\]\(([^)[:space:]]+)(?:[[:space:]]+"[^"]*")?\)`)

func TestPublicReadmesDocumentReleaseDeployment(t *testing.T) {
	tests := []struct {
		path       string
		other      string
		markers    []string
		lowerWords []string
	}{
		{
			path:  "README.md",
			other: "README.zh-CN.md",
			markers: []string{
				"ghcr.io/razeencheng/iosbackup", "](compose.yaml)", "](CHANGELOG.md)", "Core", "Experimental",
				"--privileged", "--network host", "/dev/bus/usb:/dev/bus/usb",
				"/run/udev:/run/udev:ro", "/var/lib/lockdown", "/backups", "/configs",
				"Linux", "Windows", "macOS", "/healthz",
			},
			lowerWords: []string{"host-level access", "do not expose", "public internet", "upgrade", "rollback", "troubleshooting"},
		},
		{
			path:  "README.zh-CN.md",
			other: "README.md",
			markers: []string{
				"ghcr.io/razeencheng/iosbackup", "](compose.yaml)", "](CHANGELOG.md)", "核心", "实验",
				"--privileged", "--network host", "/dev/bus/usb:/dev/bus/usb",
				"/run/udev:/run/udev:ro", "/var/lib/lockdown", "/backups", "/configs",
				"Linux", "Windows", "macOS", "/healthz", "宿主机级权限", "不要暴露到公网", "升级", "回滚", "排错",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			text := readRepoFile(t, test.path)
			if !strings.Contains(text, "]("+test.other+")") {
				t.Fatalf("%s does not link to %s", test.path, test.other)
			}
			for _, marker := range test.markers {
				if !strings.Contains(text, marker) {
					t.Errorf("%s missing %q", test.path, marker)
				}
			}
			lower := strings.ToLower(text)
			for _, word := range test.lowerWords {
				if !strings.Contains(lower, word) {
					t.Errorf("%s missing %q", test.path, word)
				}
			}
			for _, forbidden := range []string{"docker.io/", "Docker Hub"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s contains unsupported image reference %q", test.path, forbidden)
				}
			}
		})
	}
}

func TestPublicDocsAreBilingualAndOperational(t *testing.T) {
	pairs := [][2]string{
		{"docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md"},
		{"docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md"},
		{"docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md"},
	}
	for _, path := range publicManualFiles(t) {
		if !strings.HasSuffix(path, ".zh-CN.md") {
			pairs = append(pairs, [2]string{path, strings.TrimSuffix(path, ".md") + ".zh-CN.md"})
		}
	}
	pairs = append(pairs,
		[2]string{"docs/DEVELOPMENT.md", "docs/DEVELOPMENT.zh-CN.md"},
		[2]string{"docs/NOTIFICATION_README.en.md", "docs/NOTIFICATION_README.md"},
		[2]string{"docs/WEBHOOK_GUIDE.en.md", "docs/WEBHOOK_GUIDE.md"},
		[2]string{"docs/TESTING_GUIDE.en.md", "docs/TESTING_GUIDE.md"},
		[2]string{"docs/TEST_SUMMARY.en.md", "docs/TEST_SUMMARY.md"},
		[2]string{"docs/UPDATE_VERSION.en.md", "docs/UPDATE_VERSION.md"},
	)
	for _, pair := range pairs {
		left := readRepoFile(t, pair[0])
		right := readRepoFile(t, pair[1])
		if !strings.Contains(left, "]("+filepath.Base(pair[1])+")") {
			t.Errorf("%s does not link to %s", pair[0], pair[1])
		}
		if !strings.Contains(right, "]("+filepath.Base(pair[0])+")") {
			t.Errorf("%s does not link to %s", pair[1], pair[0])
		}
	}

	for _, path := range []string{"docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md"} {
		text := readRepoFile(t, path)
		for _, marker := range []string{
			"](../compose.yaml)", "docker compose up -d", "docker compose logs iosbackup", "/healthz", "--privileged", "--network host",
			"/dev/bus/usb:/dev/bus/usb", "/run/udev:/run/udev:ro",
			"/var/lib/lockdown", "/backups", "/configs",
		} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing quick-start fact %q", path, marker)
			}
		}
	}

	// OPERATIONS is a task index; procedures live in the linked bilingual pages.
	for _, suffix := range []string{".md", ".zh-CN.md"} {
		path := "docs/OPERATIONS" + suffix
		text := readRepoFile(t, path)
		for _, topic := range []string{
			"overview", "installation", "synology", "usb-backup", "scheduling",
			"wifi", "encryption", "inspection", "notifications", "device-storage",
			"instance-recovery", "upgrade", "experimental", "troubleshooting",
			"configuration", "states", "wifi-nat",
		} {
			if !strings.Contains(text, "](manual/"+topic+suffix+")") {
				t.Errorf("%s missing task link %q", path, topic)
			}
		}
		for _, marker := range []string{"/healthz", "/api/version", "docker compose logs", "/backups", "/configs", "/var/lib/lockdown"} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing routine operations reference %q", path, marker)
			}
		}
	}

	for _, path := range []string{"docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md"} {
		text := readRepoFile(t, path)
		for _, marker := range []string{
			"Core", "Experimental", "restore_enabled=false",
			"IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false", "USB", "Wi-Fi",
		} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing feature status %q", path, marker)
			}
		}
		recoveryBoundary := "recovery plan"
		if strings.Contains(path, ".zh-CN.") {
			recoveryBoundary = "恢复方案"
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(recoveryBoundary)) {
			t.Errorf("%s does not distinguish feature support from recovery validation %q", path, recoveryBoundary)
		}
	}
}

func TestPublicInstallDocumentsAutomaticCredentials(t *testing.T) {
	for _, suffix := range []string{".md", ".zh-CN.md"} {
		quick := readRepoFile(t, "docs/QUICKSTART"+suffix)
		for _, forbidden := range []string{"git clone ", "openssl rand ", "mkdir -p data/backups data/configs data/lockdown", "> .env"} {
			if strings.Contains(quick, forbidden) {
				t.Errorf("quick start requires manual initialization again: %q", forbidden)
			}
		}
		for _, path := range []string{"docs/QUICKSTART" + suffix, "docs/manual/installation" + suffix} {
			text := readRepoFile(t, path)
			for _, marker := range []string{"data/configs", "secret_key", "sudo cat data/configs/admin_password"} {
				if !strings.Contains(text, marker) {
					t.Errorf("%s missing persistent credential/retrieval guidance %q", path, marker)
				}
			}
		}
		installation := readRepoFile(t, "docs/manual/installation"+suffix)
		for _, marker := range []string{"0700", "0600", "auth_credentials.json", "IOSBK_ADMIN_PASSWORD_FILE"} {
			if !strings.Contains(installation, marker) {
				t.Errorf("installation guide missing credential protection/compatibility guidance %q", marker)
			}
		}
	}
}

func TestPublicDocsRequireCompleteUSBBackupValidation(t *testing.T) {
	tests := []struct {
		path       string
		validation string
		capacity   string
	}{
		{path: "README.md", validation: "a complete USB backup on a non-critical device", capacity: "space for a complete device backup"},
		{path: "README.zh-CN.md", validation: "在非关键设备上重新验证一次完整 USB 备份", capacity: "按完整设备备份预留空间"},
		{path: "docs/QUICKSTART.md", validation: "complete a USB backup first", capacity: "space for a complete device backup"},
		{path: "docs/QUICKSTART.zh-CN.md", validation: "建议先完成一次 USB 备份", capacity: "按完整设备备份预留空间"},
		{path: "docs/OPERATIONS.md", validation: "complete one USB backup on a non-critical device", capacity: "space for a complete device backup"},
		{path: "docs/OPERATIONS.zh-CN.md", validation: "在非关键设备上完成一次 USB 备份", capacity: "按完整设备备份预留空间"},
	}
	for _, test := range tests {
		text := readRepoFile(t, test.path)
		semanticText := text
		if !strings.Contains(test.path, ".zh-CN.") {
			semanticText = strings.ToLower(text)
		}
		for _, marker := range []string{test.validation, test.capacity} {
			semanticMarker := marker
			if !strings.Contains(test.path, ".zh-CN.") {
				semanticMarker = strings.ToLower(marker)
			}
			if !strings.Contains(semanticText, semanticMarker) {
				t.Errorf("%s missing complete-backup guidance %q", test.path, marker)
			}
		}
		for _, forbidden := range []string{"small test backup", "small manual USB backup", "a small USB backup", "小规模测试备份", "小规模手动 USB 备份", "小型 USB 备份"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s promises unsupported partial backup wording %q", test.path, forbidden)
			}
		}
	}
}

func TestLicensingDescribesInternetAndLocalNetworkTraffic(t *testing.T) {
	text := readRepoFile(t, "docs/LICENSING.md")
	for _, marker := range []string{
		"Only administrator-configured notifications are designed to connect to third-party Internet services",
		"local network", "mDNS", "netmuxd", "not telemetry",
		"只有管理员自行配置的通知功能会按设计连接互联网第三方服务",
		"局域网", "不是遥测",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("docs/LICENSING.md missing network privacy boundary %q", marker)
		}
	}
}

func TestChinesePublicDocsUseNaturalOperationalLanguage(t *testing.T) {
	for _, path := range []string{
		"README.zh-CN.md", "docs/QUICKSTART.zh-CN.md", "docs/OPERATIONS.zh-CN.md", "docs/FEATURE_STATUS.zh-CN.md",
	} {
		text := strings.ToLower(readRepoFile(t, path))
		// Command options and link targets are identifiers, not untranslated Chinese prose.
		text = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(text, "")
		text = regexp.MustCompile("`[^`]*`").ReplaceAllString(text, "")
		text = regexp.MustCompile(`\]\([^)]*\)`).ReplaceAllString(text, "]")
		for _, foreign := range []string{
			"unlock", "reconnect", "upgrade", "rollback", "free disk space", "permission",
			"host network", "lock-screen", "retry", "heartbeat/stream", "bind mount", "endpoint",
			"disk space", "troubleshooting", "production-stable",
		} {
			if strings.Contains(text, foreign) {
				t.Errorf("%s contains avoidable English prose %q", path, foreign)
			}
		}
	}
}

// publicManualFiles discovers the exported task pages in both source and public trees.
func publicManualFiles(t *testing.T) []string {
	t.Helper()
	root := findModuleRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "docs", "manual", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, match := range matches {
		rel, err := filepath.Rel(root, match)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	return paths
}

func TestPublicDocsLocalLinksExist(t *testing.T) {
	paths := []string{
		"README.md", "README.zh-CN.md", "SECURITY.md", "SUPPORT.md", "CONTRIBUTING.md",
		"CHANGELOG.md", "THIRD_PARTY_NOTICES.md", "TRADEMARKS.md", "docs/PRIVACY.md", "docs/LICENSING.md",
		"docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md", "docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md",
		"docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
	}
	paths = append(paths, publicManualFiles(t)...)
	paths = append(paths,
		"docs/DEVELOPMENT.md", "docs/DEVELOPMENT.zh-CN.md",
		"docs/NOTIFICATION_README.md", "docs/NOTIFICATION_README.en.md",
		"docs/WEBHOOK_GUIDE.md", "docs/WEBHOOK_GUIDE.en.md",
		"docs/TESTING_GUIDE.md", "docs/TESTING_GUIDE.en.md",
		"docs/TEST_SUMMARY.md", "docs/TEST_SUMMARY.en.md",
		"docs/UPDATE_VERSION.md", "docs/UPDATE_VERSION.en.md",
	)
	root := findModuleRoot(t)
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, match := range markdownLinkRE.FindAllStringSubmatch(string(data), -1) {
			target := match[1]
			if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			target = strings.SplitN(target, "?", 2)[0]
			if target == "" {
				continue
			}
			resolved := filepath.Clean(filepath.Join(root, filepath.Dir(filepath.FromSlash(path)), filepath.FromSlash(target)))
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("%s has escaping local link %q", path, match[1])
				continue
			}
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s has missing local link %q: %v", path, match[1], err)
			}
		}
	}
}

func TestPublicMetadataMatchesReleaseManifest(t *testing.T) {
	manifestPath := repoTestPath(t, "release/manifest.env")
	version := readReleaseManifestValue(t, manifestPath, "IOSBK_VERSION")
	buildDate := readReleaseManifestValue(t, manifestPath, "IOSBK_BUILD_DATE")
	if buildinfo.Version != version {
		t.Fatalf("buildinfo version %q does not match manifest %q", buildinfo.Version, version)
	}
	if buildinfo.BuildDate != "unknown" {
		t.Fatalf("development BuildDate=%q, want unknown", buildinfo.BuildDate)
	}
	if !strings.Contains(readRepoFile(t, "CHANGELOG.md"), "## "+version) {
		t.Fatalf("CHANGELOG does not contain %s", version)
	}
	for _, path := range []string{
		"internal/buildinfo/buildinfo.go", "Dockerfile", ".github/workflows/ci.yml", ".github/workflows/release.yml",
		"README.md", "README.zh-CN.md", "docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md",
		"docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md", "docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
	} {
		if strings.Contains(readRepoFile(t, path), buildDate) {
			t.Errorf("%s duplicates frozen build date %s", path, buildDate)
		}
	}
}

func TestPublicAllowlistIncludesBetaDocumentation(t *testing.T) {
	manifestPath := repoTestPath(t, "docs/release/public-files.txt")
	data, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		t.Skip("private export manifest is intentionally absent from the public snapshot")
	}
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(data)
	for _, path := range []string{
		"README.zh-CN.md", "TRADEMARKS.md", "docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md",
		"docs/images/tutorial/**", ".github/workflows/docker-package-test.yml",
		"docs/manual/**", "docs/DEVELOPMENT*.md", "docs/NOTIFICATION_*.md",
		"docs/WEBHOOK_GUIDE*.md", "docs/TESTING_GUIDE*.md", "docs/TEST_SUMMARY*.md", "docs/UPDATE_VERSION*.md",
		"docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md", "docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
		".github/workflows/release.yml", ".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml", ".github/pull_request_template.md",
	} {
		if !strings.Contains(manifest, "include\t"+path+"\t") {
			t.Errorf("public allowlist missing %s", path)
		}
	}
}

func TestPublicComposeUsesOfficialReleaseImage(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	if !strings.Contains(compose, "image: "+publicReleaseImage) {
		t.Fatalf("compose.yaml must default to the official release image")
	}
	for _, forbidden := range []string{"iosbackup:v1.4.1", "docker.io/"} {
		if strings.Contains(compose, forbidden) {
			t.Errorf("compose.yaml contains unsupported image reference %q", forbidden)
		}
	}
}

func TestPublicComposeKeepsExperimentalOperationsOff(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	if !strings.Contains(compose, `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS: "false"`) {
		t.Fatal("compose.yaml must explicitly keep the experimental gate as false")
	}
}

func TestPublicDocsContainNoReleasePlaceholders(t *testing.T) {
	paths := []string{
		"README.md", "README.zh-CN.md", "SECURITY.md", "SUPPORT.md", "CONTRIBUTING.md", "CHANGELOG.md",
		"docs/LICENSING.md", "docs/PRIVACY.md", "docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md",
		"docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md", "docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
	}
	for _, path := range paths {
		text := readRepoFile(t, path)
		for _, forbidden := range []string{
			"registry.example.invalid",
			"example.invalid/private-repository",
			"EXAMPLE_PRIVATE_REPOSITORY",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains release placeholder %q", path, forbidden)
			}
		}
	}
}

func TestPublicPagesUseInjectedSourceIdentity(t *testing.T) {
	for _, path := range []string{
		"templates/index.html", "templates/login.html", "templates/notifications.html", "templates/onboarding.html",
	} {
		data, err := templateFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, marker := range []string{"{{.SourceURL}}/tree/{{.Commit}}", "AGPL-3.0-only"} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing %q", path, marker)
			}
		}
		if strings.Contains(text, buildinfo.SourceURL) {
			t.Errorf("%s hardcodes the source URL instead of using page data", path)
		}
	}
}

func TestPublicComposeAllowsAutomaticCredentials(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	for _, name := range []string{"IOSBK_ADMIN_PASSWORD_FILE", "IOSBK_SECRET_KEY", "IOSBK_SECRET_KEY_FILE"} {
		if !strings.Contains(compose, name+`: "${`+name+`:-}"`) {
			t.Errorf("%s must be optional and forwarded for custom installations", name)
		}
	}
	for _, contract := range []string{`IOSBK_AUTH_ENABLED: "true"`, `privileged: true`, `network_mode: host`, `./data/backups:/backups`, `./data/configs:/configs`, `./data/lockdown:/var/lib/lockdown`} {
		if !strings.Contains(compose, contract) {
			t.Errorf("automatic setup lost deployment contract %q", contract)
		}
	}
}

func TestPublicRuntimeSecretsAreIgnored(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--template=", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initialize ignore fixture: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(readRepoFile(t, ".gitignore")), 0600); err != nil {
		t.Fatal(err)
	}
	privatePaths := []string{".env", ".env.local", "data/configs/admin_password", "data/backups/example", "configs/admin_password", "configs/secret_key", "configs/auth_credentials.json", "configs/csrf_secret.json", "configs/secrets.enc", "configs/.admin_password-example", "configs/.secret_key-example", "configs/.auth_credentials.json-example", "configs/.csrf_secret.json-example", "configs/.secrets.enc-example"}
	for _, path := range privatePaths {
		cmd := exec.Command("git", "-c", "core.excludesfile=/dev/null", "check-ignore", "--no-index", "-q", "--", path)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Errorf("runtime secret/data is not ignored: %s", path)
		}
	}
	for _, path := range []string{"configs/notification_config_example.json", "internal/app/secrets.go", "internal/app/automatic_setup_test.go", "internal/persistence/privatefile_test.go"} {
		cmd := exec.Command("git", "-c", "core.excludesfile=/dev/null", "check-ignore", "--no-index", "-q", "--", path)
		cmd.Dir = root
		if err := cmd.Run(); err == nil {
			t.Errorf("ignore rules exclude distributable source or example: %s", path)
		}
	}
	dockerIgnore := readRepoFile(t, ".dockerignore")
	for _, pattern := range []string{".env*", "data", "configs/admin_password*", "configs/secret_key*", "configs/auth_credentials.json*", "configs/csrf_secret.json*", "configs/secrets.enc*", "configs/.admin_password-*", "configs/.secret_key-*", "configs/.auth_credentials.json-*", "configs/.csrf_secret.json-*", "configs/.secrets.enc-*"} {
		if !strings.Contains("\n"+dockerIgnore, "\n"+pattern+"\n") {
			t.Errorf("Docker build context lacks explicit runtime-secret exclusion %s", pattern)
		}
	}
}
