package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSynologySPKBuildAndWizardLifecycle(t *testing.T) {
	outDir := t.TempDir()
	builder := repoTestPath(t, "packaging/synology/build_spk.sh")

	// 准备模拟的本地 docker-save 镜像归档以离线验证 --offline --version v1.5.6 打包链路
	fakeImageTar := filepath.Join(t.TempDir(), "fake-image.tar")
	writeMinimalTar(t, fakeImageTar, "manifest.json", []byte(`[{"Config":"cfg.json","RepoTags":["ghcr.io/razeencheng/iosbackup:v1.5.6"],"Layers":[]}]`))

	cmd := exec.Command("/bin/sh", builder,
		"--offline",
		"--arch", "x86_64",
		"--version", "v1.5.6",
		"--image-tar", fakeImageTar,
		"--build-num", "0004",
		"--output-dir", outDir,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build_spk.sh --offline failed: %v\n%s", err, out)
	}

	spkPath := filepath.Join(outDir, "iosbackup-x86_64-1.5.6-0004.spk")
	spkData, err := os.ReadFile(spkPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", spkPath, err)
	}
	if len(spkData) >= 2 && spkData[0] == 0x1f && spkData[1] == 0x8b {
		t.Fatal("outer .spk archive must be uncompressed POSIX tar, not gzip")
	}

	outerEntries := make(map[string][]byte)
	outerModes := make(map[string]int64)
	var firstHeader string

	tr := tar.NewReader(bytes.NewReader(spkData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid outer .spk tar stream: %v", err)
		}
		if firstHeader == "" {
			firstHeader = hdr.Name
		}
		if strings.HasPrefix(hdr.Name, "./") || hdr.Name == "." {
			t.Fatalf("outer .spk must not contain ./ prefix entries: %q", hdr.Name)
		}
		base := filepath.Base(hdr.Name)
		if strings.HasPrefix(base, "._") || base == ".DS_Store" {
			t.Fatalf("outer .spk contains macOS metadata entry: %q", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			t.Fatalf("outer .spk contains PAX extended header %q; expected clean ustar", hdr.Name)
		}
		outerModes[hdr.Name] = hdr.Mode & 0o777
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			outerEntries[hdr.Name] = body
		}
	}

	if firstHeader != "INFO" {
		t.Fatalf("first entry in .spk should be INFO, got %q", firstHeader)
	}

	for _, executable := range []string{
		"WIZARD_UIFILES/install_uifile.sh",
		"WIZARD_UIFILES/upgrade_uifile.sh",
		"scripts/preinst",
		"scripts/postinst",
		"scripts/preuninst",
		"scripts/postuninst",
		"scripts/preupgrade",
		"scripts/postupgrade",
		"scripts/start-stop-status",
	} {
		if _, ok := outerEntries[executable]; !ok {
			t.Errorf("missing executable entry: %s", executable)
		}
		if mode := outerModes[executable]; mode != 0o755 {
			t.Errorf("%s mode = %04o, want 0755", executable, mode)
		}
	}

	infoText := string(outerEntries["INFO"])
	for _, needle := range []string{
		`package="iosbackup"`,
		`displayname="iOS Backup"`,
		`displayname_chs="iOS Backup"`,
		`version="1.5.6-0004"`,
		`arch="x86_64"`,
		`os_min_ver="7.2-64570"`,
		`dsmuidir="ui"`,
		`dsmappname="SYNO.SDS.iOSBackup"`,
		`silent_upgrade="no"`,
		`checkport="no"`,
	} {
		if !strings.Contains(infoText, needle) {
			t.Errorf("INFO missing %s:\n%s", needle, infoText)
		}
	}
	for _, forbidden := range []string{`adminport=`, `adminurl=`} {
		if strings.Contains(infoText, forbidden) {
			t.Errorf("INFO must not set %s (launch URL must come from dsmappname + ui/config):\n%s", forbidden, infoText)
		}
	}

	var resource struct {
		DataShare struct {
			Shares []struct {
				Name       string              `json:"name"`
				Permission map[string][]string `json:"permission"`
			} `json:"shares"`
		} `json:"data-share"`
		DockerProject struct {
			PreloadImage string `json:"preload-image"`
			Projects     []struct {
				Name        string          `json:"name"`
				Path        string          `json:"path"`
				BuildParams map[string]bool `json:"build_params"`
			} `json:"projects"`
		} `json:"docker-project"`
	}
	if err := decodeStrictJSON(outerEntries["conf/resource"], &resource); err != nil {
		t.Fatalf("invalid conf/resource JSON: %v", err)
	}
	if len(resource.DataShare.Shares) != 1 || resource.DataShare.Shares[0].Name != "{{pkgwizard_share_name}}" {
		t.Fatalf("data-share must bind {{pkgwizard_share_name}}, got %+v", resource.DataShare.Shares)
	}
	if resource.DockerProject.PreloadImage != "images/iosbackup.tar.gz" {
		t.Fatalf("preload-image = %q, want images/iosbackup.tar.gz", resource.DockerProject.PreloadImage)
	}
	if len(resource.DockerProject.Projects) != 1 || !resource.DockerProject.Projects[0].BuildParams["force_recreate"] || resource.DockerProject.Projects[0].BuildParams["force_pull"] {
		t.Fatalf("docker-project build_params invalid: %+v", resource.DockerProject.Projects)
	}

	// 解压内层 package.tgz 到模拟的 $SYNOPKG_PKGDEST 目录
	pkgDest := filepath.Join(t.TempDir(), "target")
	gz, err := gzip.NewReader(bytes.NewReader(outerEntries["package.tgz"]))
	if err != nil {
		t.Fatalf("invalid package.tgz gzip stream: %v", err)
	}
	defer gz.Close()

	innerModes := make(map[string]int64)
	innerTar := tar.NewReader(gz)
	for {
		hdr, err := innerTar.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid package.tgz tar stream: %v", err)
		}
		innerModes[hdr.Name] = hdr.Mode & 0o777
		destPath := filepath.Join(pkgDest, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, os.FileMode(hdr.Mode)&0o777); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(innerTar)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destPath, body, os.FileMode(hdr.Mode)&0o777); err != nil {
				t.Fatal(err)
			}
		}
	}

	if innerModes["ui/index.cgi"] != 0o755 || innerModes["ui/index.cgi.in"] != 0o755 {
		t.Fatalf("ui/index.cgi modes = %04o / %04o, want 0755", innerModes["ui/index.cgi"], innerModes["ui/index.cgi.in"])
	}

	// 验证安装向导三步骤 JSON（单卷精简文案、多卷精简文案、端口占用自动避让、全部 JS 正则合法）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	wizardScript := repoTestPath(t, "packaging/synology/spk/WIZARD_UIFILES/install_uifile.sh")

	// 场景 A：单存储空间 /volume1
	singleVolRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(singleVolRoot, "volume1"), 0o755); err != nil {
		t.Fatal(err)
	}
	singleWizardOut := filepath.Join(t.TempDir(), "wizard_single.json")
	swcmd := exec.Command("/bin/sh", wizardScript)
	swcmd.Env = append(os.Environ(),
		"SYNOPKG_TEMP_LOGFILE="+singleWizardOut,
		"SYNOPKG_DSM_LANGUAGE=chs",
		"IOSBK_WIZARD_BASE_PORT="+strconv.Itoa(occupiedPort),
		"IOSBK_TEST_VOL_ROOT="+singleVolRoot,
	)
	if out, err := swcmd.CombinedOutput(); err != nil {
		t.Fatalf("install_uifile.sh (single volume) failed: %v\n%s", err, out)
	}
	rawSingleWizard, err := os.ReadFile(singleWizardOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawSingleWizard), "默认保存在 /volume1") {
		t.Fatalf("single-volume wizard copy did not match concise template:\n%s", rawSingleWizard)
	}

	// 场景 B：多存储空间 /volume1 + /volume2
	multiVolRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(multiVolRoot, "volume1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(multiVolRoot, "volume2"), 0o755); err != nil {
		t.Fatal(err)
	}
	wizardOut := filepath.Join(t.TempDir(), "wizard_multi.json")
	wcmd := exec.Command("/bin/sh", wizardScript)
	wcmd.Env = append(os.Environ(),
		"SYNOPKG_TEMP_LOGFILE="+wizardOut,
		"SYNOPKG_DSM_LANGUAGE=chs",
		"IOSBK_WIZARD_BASE_PORT="+strconv.Itoa(occupiedPort),
		"IOSBK_TEST_VOL_ROOT="+multiVolRoot,
	)
	if out, err := wcmd.CombinedOutput(); err != nil {
		t.Fatalf("install_uifile.sh (multi volume) failed: %v\n%s", err, out)
	}
	rawWizard, err := os.ReadFile(wizardOut)
	if err != nil {
		t.Fatal(err)
	}
	var steps []struct {
		StepTitle string `json:"step_title"`
		Items     []struct {
			Type     string `json:"type"`
			Subitems []struct {
				Key          string `json:"key"`
				DefaultValue any    `json:"defaultValue"`
				Validator    *struct {
					Regex *struct {
						Expr string `json:"expr"`
					} `json:"regex"`
				} `json:"validator"`
			} `json:"subitems"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawWizard, &steps); err != nil {
		t.Fatalf("install_uifile.sh emitted invalid JSON: %v\n%s", err, rawWizard)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 wizard steps, got %d", len(steps))
	}
	if !strings.Contains(string(rawWizard), "新建文件夹将保存在套件安装的目标存储空间（检测到：/volume1") || !strings.Contains(string(rawWizard), "/volume2") {
		t.Fatalf("multi-volume wizard did not surface concise /volume1 + /volume2 notice:\n%s", rawWizard)
	}
	for _, st := range steps {
		for _, it := range st.Items {
			for _, sub := range it.Subitems {
				if sub.Validator != nil && sub.Validator.Regex != nil {
					assertValidSynologyJSRegexLiteral(t, sub.Key, sub.Validator.Regex.Expr)
				}
			}
		}
	}

	// 验证 preinst 拦截非法子目录嵌套、公网/回环 Webhook CIDR、越界并发数
	preinst := repoTestPath(t, "packaging/synology/spk/scripts/preinst")
	for _, badEnv := range [][]string{
		{"pkgwizard_share_name=docker", "pkgwizard_backup_subdir=iosbackup/data", "pkgwizard_config_subdir=iosbackup/data/configs"},
		{"pkgwizard_webhook_cidrs=127.0.0.0/8"},
		{"pkgwizard_webhook_cidrs=8.8.8.0/24"},
		{"pkgwizard_webhook_cidrs=192.168.1.10"},
		{"pkgwizard_webhook_cidrs=fd::/8"},
		{"pkgwizard_external_url=ftp://ios.example.com"},
		{"pkgwizard_external_url=ios.example.com"},
		{"pkgwizard_external_url=https://ios.example.com/iosbackup"},
		{"pkgwizard_external_url=https://ios.example.com:99999"},
		{"pkgwizard_max_heavy_jobs=3"},
		{"pkgwizard_plist_max_mib=512"},
	} {
		badCmd := exec.Command("/bin/sh", preinst)
		badCmd.Env = append(os.Environ(), badEnv...)
		if err := badCmd.Run(); err == nil {
			t.Fatalf("preinst should reject invalid wizard input %v", badEnv)
		}
	}

	goodPreCmd := exec.Command("/bin/sh", preinst)
	goodPreCmd.Env = append(os.Environ(),
		"pkgwizard_share_name=iosbackup",
		"pkgwizard_backup_subdir=backups",
		"pkgwizard_config_subdir=configs",
		"pkgwizard_external_url=https://ios.example.com:8443/",
		"pkgwizard_max_heavy_jobs=2",
		"pkgwizard_plist_max_mib=128",
		"pkgwizard_webhook_cidrs=192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, fd00::/8",
		"pkgwizard_admin_password=Synology-Test-Strong-Admin-Password-2026!",
	)
	if out, err := goodPreCmd.CombinedOutput(); err != nil {
		t.Fatalf("preinst rejected valid private CIDRs and tier-B options: %v\n%s", err, out)
	}

	// 验证 postinst 将自定义端口 9003 及全部参数直接渲染进 $SYNOPKG_PKGDEST/compose/docker-compose.yml 与 ui/config
	pkgVar := filepath.Join(t.TempDir(), "pkgvar")
	installLog := filepath.Join(t.TempDir(), "install.log")
	postinst := repoTestPath(t, "packaging/synology/spk/scripts/postinst")
	wantPwd := "Synology-Test-Strong-Admin-Password-2026!"
	postCmd := exec.Command("/bin/sh", postinst)
	postCmd.Env = append(os.Environ(),
		"SYNOPKG_PKGVAR="+pkgVar,
		"SYNOPKG_PKGDEST="+pkgDest,
		"SYNOPKG_TEMP_LOGFILE="+installLog,
		"pkgwizard_share_name=BackupsVolume2",
		"pkgwizard_backup_subdir=iphone/backups",
		"pkgwizard_config_subdir=iphone/configs",
		"pkgwizard_port=9003",
		"pkgwizard_external_url=https://ios.example.com:8443/",
		"pkgwizard_min_free_gb=25",
		"pkgwizard_timezone=Asia/Tokyo",
		"pkgwizard_log_level=DEBUG",
		"pkgwizard_max_heavy_jobs=2",
		"pkgwizard_plist_max_mib=128",
		"pkgwizard_webhook_cidrs=192.168.0.0/16, 10.0.0.0/8",
		"pkgwizard_enable_experimental=true",
		"pkgwizard_admin_password="+wantPwd,
	)
	if out, err := postCmd.CombinedOutput(); err != nil {
		t.Fatalf("postinst failed: %v\n%s", err, out)
	}

	renderedComposeBytes, err := os.ReadFile(filepath.Join(pkgDest, "compose", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	renderedCompose := string(renderedComposeBytes)
	if strings.Contains(renderedCompose, "@") {
		t.Fatalf("rendered compose/docker-compose.yml still contains unexpanded @placeholder@:\n%s", renderedCompose)
	}
	for _, wantComposeLine := range []string{
		`image: ghcr.io/razeencheng/iosbackup:v1.5.6`,
		`pull_policy: never`,
		`PORT: "9003"`,
		`TZ: "Asia/Tokyo"`,
		`LOG_LEVEL: "DEBUG"`,
		`IOSBK_BACKUPS_DIR: "/data/iphone/backups"`,
		`IOSBK_CONFIGS_DIR: "/data/iphone/configs"`,
		`IOSBK_MIN_FREE_BYTES: "26843545600"`,
		`IOSBK_MAX_HEAVY_JOBS: "2"`,
		`IOSBK_PLIST_MAX_MIB: "128"`,
		`IOSBK_WEBHOOK_ALLOW_CIDRS: "192.168.0.0/16,10.0.0.0/8"`,
		`IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS: "true"`,
		`IOSBK_ADMIN_PASSWORD_FILE: "/run/iosbackup-secrets/admin_password"`,
	} {
		if !strings.Contains(renderedCompose, wantComposeLine) {
			t.Errorf("rendered compose/docker-compose.yml missing %q:\n%s", wantComposeLine, renderedCompose)
		}
	}

	renderedUIConfigBytes, err := os.ReadFile(filepath.Join(pkgDest, "ui", "config"))
	if err != nil {
		t.Fatal(err)
	}
	var uiCfg struct {
		URL map[string]struct {
			Title    string `json:"title"`
			Desc     string `json:"desc"`
			Icon     string `json:"icon"`
			Type     string `json:"type"`
			Protocol string `json:"protocol"`
			Port     string `json:"port"`
			URL      string `json:"url"`
			AllUsers bool   `json:"allUsers"`
		} `json:".url"`
	}
	if err := decodeStrictJSON(renderedUIConfigBytes, &uiCfg); err != nil {
		t.Fatalf("rendered ui/config invalid JSON: %v\n%s", err, renderedUIConfigBytes)
	}
	entry := uiCfg.URL["SYNO.SDS.iOSBackup"]
	if entry.URL != "https://ios.example.com:8443" || entry.Port != "" || entry.Protocol != "" || entry.Title != "iOS Backup" {
		t.Fatalf("rendered ui/config did not bind custom external URL directly: %+v", entry)
	}

	// 验证备用 index.cgi 在配置了自定义反代地址时同样一律直跳该外部 URL
	cgiCmd := exec.Command("/bin/sh", filepath.Join(pkgDest, "ui", "index.cgi"))
	cgiCmd.Env = append(os.Environ(), "HTTP_HOST=192.168.50.10:5000")
	cgiOut, err := cgiCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered ui/index.cgi failed: %v\n%s", err, cgiOut)
	}
	if !strings.Contains(string(cgiOut), "Location: https://ios.example.com:8443\r\n") {
		t.Fatalf("rendered ui/index.cgi did not redirect to custom external URL:\n%s", cgiOut)
	}

	// 验证 start-stop-status 状态生命周期
	sss := repoTestPath(t, "packaging/synology/spk/scripts/start-stop-status")
	runSSS := func(arg string) error {
		c := exec.Command("/bin/sh", sss, arg)
		c.Env = append(os.Environ(), "SYNOPKG_PKGVAR="+pkgVar)
		return c.Run()
	}
	if err := runSSS("status"); err != nil {
		t.Fatalf("expected status=running immediately after postinst, got %v", err)
	}
	if err := runSSS("stop"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if err := runSSS("status"); err == nil {
		t.Fatal("expected status=stopped after stop")
	}
	if err := runSSS("start"); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := runSSS("status"); err != nil {
		t.Fatalf("expected status=running after start, got %v", err)
	}

	// 验证 upgrade_uifile.sh 三步向导自动回填全部已保存配置（含 9003、并发数 2、Webhook CIDR）
	upgOut := filepath.Join(t.TempDir(), "upgrade_wizard.json")
	upgScript := repoTestPath(t, "packaging/synology/spk/WIZARD_UIFILES/upgrade_uifile.sh")
	ucmd := exec.Command("/bin/sh", upgScript)
	ucmd.Env = append(os.Environ(),
		"SYNOPKG_PKGVAR="+pkgVar,
		"SYNOPKG_TEMP_LOGFILE="+upgOut,
		"SYNOPKG_DSM_LANGUAGE=chs",
	)
	if out, err := ucmd.CombinedOutput(); err != nil {
		t.Fatalf("upgrade_uifile.sh failed: %v\n%s", err, out)
	}
	rawUpg, err := os.ReadFile(upgOut)
	if err != nil {
		t.Fatal(err)
	}
	var upgSteps []map[string]any
	if err := json.Unmarshal(rawUpg, &upgSteps); err != nil || len(upgSteps) != 3 {
		t.Fatalf("upgrade_uifile.sh invalid 3-step JSON (err=%v, len=%d):\n%s", err, len(upgSteps), rawUpg)
	}
	for _, needle := range []string{`"BackupsVolume2"`, `"iphone/backups"`, `"9003"`, `"https://ios.example.com:8443"`, `"128"`, `"192.168.0.0/16,10.0.0.0/8"`} {
		if !strings.Contains(string(rawUpg), needle) {
			t.Errorf("upgrade_uifile.sh did not pre-fill %s:\n%s", needle, rawUpg)
		}
	}

	// 模拟升级：密码留空保持不变、清空自定义反代地址与 Webhook 网段、修改端口为 9005
	upgradePostCmd := exec.Command("/bin/sh", postinst)
	upgradePostCmd.Env = append(os.Environ(),
		"SYNOPKG_PKGVAR="+pkgVar,
		"SYNOPKG_PKGDEST="+pkgDest,
		"SYNOPKG_PKG_STATUS=UPGRADE",
		"pkgwizard_share_name=BackupsVolume2",
		"pkgwizard_backup_subdir=iphone/backups",
		"pkgwizard_config_subdir=iphone/configs",
		"pkgwizard_port=9005",
		"pkgwizard_external_url=",
		"pkgwizard_min_free_gb=10",
		"pkgwizard_timezone=Asia/Shanghai",
		"pkgwizard_log_level=WARN",
		"pkgwizard_max_heavy_jobs=1",
		"pkgwizard_plist_max_mib=64",
		"pkgwizard_webhook_cidrs=",
		"pkgwizard_enable_experimental=false",
		"pkgwizard_admin_password=",
	)
	if out, err := upgradePostCmd.CombinedOutput(); err != nil {
		t.Fatalf("postinst UPGRADE failed: %v\n%s", err, out)
	}
	gotPwd, err := readAdminPassword(filepath.Join(pkgVar, "secrets", "admin_password"), false)
	if err != nil || gotPwd != wantPwd {
		t.Fatalf("upgrade with blank password lost existing password: got=%q err=%v", gotPwd, err)
	}
	upgCompose, err := os.ReadFile(filepath.Join(pkgDest, "compose", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(upgCompose), `PORT: "9005"`) || !strings.Contains(string(upgCompose), `IOSBK_WEBHOOK_ALLOW_CIDRS: ""`) {
		t.Fatalf("upgrade did not re-render compose/docker-compose.yml with port 9005 and cleared CIDRs:\n%s", upgCompose)
	}
	upgUIConfig, err := os.ReadFile(filepath.Join(pkgDest, "ui", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(upgUIConfig), `"port": "9005"`) {
		t.Fatalf("upgrade did not re-render ui/config with port 9005:\n%s", upgUIConfig)
	}
}

func TestSynologySPKPrereleaseOutputFileAndDispatchWorkflow(t *testing.T) {
	builder := repoTestPath(t, "packaging/synology/build_spk.sh")
	fakeImageTar := filepath.Join(t.TempDir(), "fake-image.tar")
	writeMinimalTar(t, fakeImageTar, "manifest.json", []byte(`[{"Config":"cfg.json","RepoTags":["ghcr.io/razeencheng/iosbackup:v1.5.7-rc.1"],"Layers":[]}]`))

	customOut := filepath.Join(t.TempDir(), "iosbackup-synology-armv8.spk")
	cmd := exec.Command("/bin/sh", builder,
		"--offline",
		"--arch", "armv8",
		"--version", "v1.5.7-rc.1",
		"--image-tar", fakeImageTar,
		"--build-num", "0001",
		"--output-file", customOut,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build_spk.sh prerelease --output-file failed: %v\n%s", err, out)
	}
	spkData, err := os.ReadFile(customOut)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(spkData))
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "INFO" {
		t.Fatalf("expected first header INFO, got %v (err=%v)", hdr, err)
	}
	infoBytes, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	infoText := string(infoBytes)
	if !strings.Contains(infoText, `version="1.5.7-0001"`) || !strings.Contains(infoText, `arch="armv8"`) {
		t.Fatalf("prerelease SPK INFO version/arch mismatch:\n%s", infoText)
	}

	dispatchWorkflow := readRepoFile(t, ".github/workflows/synology-spk.yml")
	if got := topLevelSection(t, dispatchWorkflow, "permissions"); got != "permissions: {}" {
		t.Fatalf("synology-spk.yml must deny default permissions, got:\n%s", got)
	}
	assertPinnedUses(t, dispatchWorkflow, map[string]string{
		"actions/checkout":          "3d3c42e5aac5ba805825da76410c181273ba90b1",
		"actions/setup-go":          "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/upload-artifact":   "cf430e030ddbb5b0abf93d22962f4752f3646cd9",
		"actions/download-artifact": "9000827ccba6bdab643e8b6fd33ac0654aef8333",
	})
}

func assertValidSynologyJSRegexLiteral(t *testing.T, key, expr string) {
	t.Helper()
	if len(expr) < 3 || expr[0] != '/' || expr[len(expr)-1] != '/' {
		t.Fatalf("wizard %s regex expr must be wrapped in /.../: %q", key, expr)
	}
	body := expr[1 : len(expr)-1]
	for i := 0; i < len(body); i++ {
		if body[i] == '/' && (i == 0 || body[i-1] != '\\') {
			t.Fatalf("wizard %s regex expr contains unescaped '/' (breaks DSM ExtJS eval and causes blank license screen): %q", key, expr)
		}
	}
}

func writeMinimalTar(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	hdr := &tar.Header{
		Name:     entryName,
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}
