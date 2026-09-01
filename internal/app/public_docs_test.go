package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"iosbackup/internal/buildinfo"
)

const publicBetaImage = "ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1"

var markdownLinkRE = regexp.MustCompile(`!?\[[^]]*\]\(([^)[:space:]]+)(?:[[:space:]]+"[^"]*")?\)`)

func TestPublicReadmesDocumentBetaDeployment(t *testing.T) {
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
				publicBetaImage, "Beta", "Core", "Preview", "Experimental",
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
				publicBetaImage, "Beta", "核心", "预览", "实验",
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
			for _, forbidden := range []string{"docker.io/", "Docker Hub", "ghcr.io/razeencheng/iosbackup:latest"} {
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
			publicBetaImage, "docker compose", "/healthz", "--privileged", "--network host",
			"/dev/bus/usb:/dev/bus/usb", "/run/udev:/run/udev:ro",
			"/var/lib/lockdown", "/backups", "/configs",
		} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing quick-start fact %q", path, marker)
			}
		}
	}

	operationsDocs := []struct {
		path    string
		markers []string
	}{
		{
			path: "docs/OPERATIONS.md",
			markers: []string{
				"Trust", "unlock", "USB", "Wi-Fi", "/healthz", "docker compose logs",
				"Upgrade", "Rollback", "mobilebackup2 (-4)", "disk space", "Permission", "reconnect",
			},
		},
		{
			path: "docs/OPERATIONS.zh-CN.md",
			markers: []string{
				"信任", "解锁", "USB", "Wi-Fi", "/healthz", "docker compose logs",
				"升级", "回滚", "mobilebackup2 (-4)", "磁盘空间", "权限", "重新连接",
			},
		},
	}
	for _, doc := range operationsDocs {
		text := readRepoFile(t, doc.path)
		for _, marker := range doc.markers {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing operations topic %q", doc.path, marker)
			}
		}
	}

	for _, path := range []string{"docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md"} {
		text := readRepoFile(t, path)
		for _, marker := range []string{
			"Core", "Preview", "Experimental", "restore_enabled=false",
			"IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false", "USB", "Wi-Fi",
		} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing feature status %q", path, marker)
			}
		}
		maturity := "not production-stable"
		if strings.Contains(path, ".zh-CN.") {
			maturity = "不代表生产稳定"
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(maturity)) {
			t.Errorf("%s does not state Preview maturity with natural language %q", path, maturity)
		}
	}
}

func TestPublicInstallExamplesProtectDataDirectories(t *testing.T) {
	tests := []struct {
		path       string
		permission string
	}{
		{path: "README.md", permission: "three data directories are created with owner-only permissions"},
		{path: "README.zh-CN.md", permission: "三个数据目录将以仅所有者可访问的权限创建"},
		{path: "docs/QUICKSTART.md", permission: "three data directories are created with owner-only permissions"},
		{path: "docs/QUICKSTART.zh-CN.md", permission: "三个数据目录将以仅所有者可访问的权限创建"},
	}
	for _, test := range tests {
		text := readRepoFile(t, test.path)
		umask := strings.Index(text, "umask 077")
		mkdir := strings.Index(text, "mkdir -p data/backups data/configs data/lockdown")
		if umask < 0 || mkdir < 0 {
			t.Errorf("%s missing umask or data-directory creation command", test.path)
			continue
		}
		if umask > mkdir {
			t.Errorf("%s creates data directories before applying umask 077", test.path)
		}
		if !strings.Contains(text, test.permission) {
			t.Errorf("%s does not explain owner-only data-directory permissions", test.path)
		}
	}
}

func TestPublicDocsRequireCompleteUSBBackupValidation(t *testing.T) {
	tests := []struct {
		path       string
		validation string
		capacity   string
	}{
		{path: "README.md", validation: "complete one USB backup on a non-critical device", capacity: "space for a complete device backup"},
		{path: "README.zh-CN.md", validation: "在非关键设备上完成一次 USB 备份", capacity: "按完整设备备份预留空间"},
		{path: "docs/QUICKSTART.md", validation: "complete one USB backup on a non-critical device", capacity: "space for a complete device backup"},
		{path: "docs/QUICKSTART.zh-CN.md", validation: "在非关键设备上完成一次 USB 备份", capacity: "按完整设备备份预留空间"},
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
		"only designed Internet-facing third-party integrations",
		"local network", "mDNS", "netmuxd", "not telemetry",
		"唯一设计用于连接互联网第三方服务的集成",
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

func TestPublicDocsLocalLinksExist(t *testing.T) {
	paths := []string{
		"README.md", "README.zh-CN.md", "SECURITY.md", "SUPPORT.md", "CONTRIBUTING.md",
		"CHANGELOG.md", "THIRD_PARTY_NOTICES.md", "TRADEMARKS.md", "docs/PRIVACY.md", "docs/LICENSING.md",
		"docs/QUICKSTART.md", "docs/QUICKSTART.zh-CN.md", "docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md",
		"docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
	}
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
		"docs/OPERATIONS.md", "docs/OPERATIONS.zh-CN.md", "docs/FEATURE_STATUS.md", "docs/FEATURE_STATUS.zh-CN.md",
		".github/workflows/release.yml", ".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml", ".github/pull_request_template.md",
	} {
		if !strings.Contains(manifest, "include\t"+path+"\t") {
			t.Errorf("public allowlist missing %s", path)
		}
	}
}

func TestPublicComposeUsesOfficialBetaImage(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	if !strings.Contains(compose, "${IOSBK_IMAGE:-"+publicBetaImage+"}") {
		t.Fatalf("compose.yaml must default to the official Beta image")
	}
	for _, forbidden := range []string{"iosbackup:v1.4.1", "docker.io/", "ghcr.io/razeencheng/iosbackup:latest"} {
		if strings.Contains(compose, forbidden) {
			t.Errorf("compose.yaml contains unsupported image reference %q", forbidden)
		}
	}
}

func TestPublicComposeKeepsExperimentalOperationsOff(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	if !strings.Contains(compose, `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS: "${IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS:-false}"`) {
		t.Fatal("compose.yaml must explicitly keep and forward the experimental gate as false")
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
