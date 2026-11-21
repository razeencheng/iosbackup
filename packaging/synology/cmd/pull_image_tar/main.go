// Command pull_image_tar downloads a single-architecture Linux container image
// from an OCI/Docker v2 registry using only the Go standard library, verifies
// every manifest, config, compressed-layer, and uncompressed diff_id SHA-256
// digest, and writes a gzipped `docker save` v1.2 archive ready for `docker load`
// (and Synology Container Manager's `preload-image` worker).
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ociDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Platform    *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform,omitempty"`
}

type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
}

type ociImageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	RootFS       struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

type dockerSaveManifestEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

func main() {
	var (
		imageRef               string
		targetArch             string
		outputPath             string
		expectedManifestDigest string
	)
	flag.StringVar(&imageRef, "image", "", "container image reference (e.g. ghcr.io/razeencheng/iosbackup:v1.5.6)")
	flag.StringVar(&targetArch, "arch", "amd64", "target Linux architecture (amd64 or arm64)")
	flag.StringVar(&outputPath, "output", "", "destination .tar.gz file path")
	flag.StringVar(&expectedManifestDigest, "expected-manifest-digest", "", "optional sha256:<hex> platform manifest digest to enforce")
	flag.Parse()

	if imageRef == "" || outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: pull_image_tar --image <ref> --arch <amd64|arm64> --output <file.tar.gz> [--expected-manifest-digest sha256:...]")
		os.Exit(2)
	}
	if targetArch != "amd64" && targetArch != "arm64" {
		fatalf("unsupported --arch %q (want amd64 or arm64)", targetArch)
	}
	var wantPlatHex string
	if expectedManifestDigest != "" {
		var err error
		wantPlatHex, err = parseSHA256Digest(expectedManifestDigest)
		if err != nil {
			fatalf("invalid --expected-manifest-digest: %v", err)
		}
	}

	registryHost, repoName, reference, repoTag, err := parseImageRef(imageRef)
	if err != nil {
		fatalf("invalid --image %q: %v", imageRef, err)
	}

	client := &registryClient{
		httpClient: &http.Client{Timeout: 10 * time.Minute},
		host:       registryHost,
		repo:       repoName,
	}

	fmt.Fprintf(os.Stderr, "Fetching manifest %s/%s:%s (linux/%s)...\n", registryHost, repoName, reference, targetArch)
	acceptHeaders := strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", ")

	rawRoot, rootHex, err := client.get(fmt.Sprintf("https://%s/v2/%s/manifests/%s", registryHost, repoName, reference), acceptHeaders, "")
	if err != nil {
		fatalf("fetch manifest: %v", err)
	}

	var root ociIndex
	if err := json.Unmarshal(rawRoot, &root); err != nil {
		fatalf("decode manifest: %v", err)
	}

	var manifest ociIndex
	if len(root.Manifests) > 0 {
		var chosen *ociDescriptor
		for i := range root.Manifests {
			m := &root.Manifests[i]
			if m.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
				continue
			}
			if m.Platform != nil && m.Platform.OS == "linux" && m.Platform.Architecture == targetArch {
				chosen = m
				break
			}
		}
		if chosen == nil {
			fatalf("no linux/%s manifest found in %s", targetArch, imageRef)
		}
		if wantPlatHex != "" {
			gotPlatHex, err := parseSHA256Digest(chosen.Digest)
			if err != nil || gotPlatHex != wantPlatHex {
				fatalf("platform manifest digest = %s, want sha256:%s", chosen.Digest, wantPlatHex)
			}
		}
		rawPlat, _, err := client.get(
			fmt.Sprintf("https://%s/v2/%s/manifests/%s", registryHost, repoName, chosen.Digest),
			"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
			chosen.Digest,
		)
		if err != nil {
			fatalf("fetch platform manifest %s: %v", chosen.Digest, err)
		}
		if err := json.Unmarshal(rawPlat, &manifest); err != nil {
			fatalf("decode platform manifest: %v", err)
		}
	} else {
		if wantPlatHex != "" && rootHex != wantPlatHex {
			fatalf("manifest digest = sha256:%s, want sha256:%s", rootHex, wantPlatHex)
		}
		manifest = root
	}

	if manifest.Config.Digest == "" || len(manifest.Layers) == 0 {
		fatalf("manifest lacks config or layers")
	}

	configBytes, configHex, err := client.get(
		fmt.Sprintf("https://%s/v2/%s/blobs/%s", registryHost, repoName, manifest.Config.Digest),
		"application/octet-stream",
		manifest.Config.Digest,
	)
	if err != nil {
		fatalf("fetch image config: %v", err)
	}

	var imgCfg ociImageConfig
	if err := json.Unmarshal(configBytes, &imgCfg); err != nil {
		fatalf("decode image config: %v", err)
	}
	if imgCfg.OS != "linux" || imgCfg.Architecture != targetArch {
		fatalf("image config platform = %s/%s, want linux/%s", imgCfg.OS, imgCfg.Architecture, targetArch)
	}
	if len(imgCfg.RootFS.DiffIDs) != len(manifest.Layers) {
		fatalf("diff_ids count (%d) != manifest layers count (%d)", len(imgCfg.RootFS.DiffIDs), len(manifest.Layers))
	}

	workDir, err := os.MkdirTemp("", "iosbackup-oci-pull-*")
	if err != nil {
		fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(workDir)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fatalf("mkdir output dir: %v", err)
	}
	tmpOut := outputPath + ".tmp"
	defer os.Remove(tmpOut)

	outFile, err := os.OpenFile(tmpOut, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		fatalf("open temp output: %v", err)
	}
	gzOut, err := gzip.NewWriterLevel(outFile, gzip.DefaultCompression)
	if err != nil {
		outFile.Close()
		fatalf("init gzip writer: %v", err)
	}
	tw := tar.NewWriter(gzOut)

	epoch := time.Unix(0, 0).UTC()
	writeTarBytes := func(name string, mode int64, data []byte) error {
		hdr := &tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(data)),
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Uid:      0,
			Gid:      0,
			Uname:    "root",
			Gname:    "root",
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	configEntryName := configHex + ".json"
	if err := writeTarBytes(configEntryName, 0o644, configBytes); err != nil {
		fatalf("write config to tar: %v", err)
	}

	var layerPaths []string
	var lastLayerID string

	for idx, layerDesc := range manifest.Layers {
		wantDiffID := imgCfg.RootFS.DiffIDs[idx]
		diffHex, err := parseSHA256Digest(wantDiffID)
		if err != nil {
			fatalf("invalid diff_id %q: %v", wantDiffID, err)
		}
		// Avoid directory collisions if duplicate empty layers exist.
		layerID := fmt.Sprintf("%02d%s", idx, diffHex[:62])
		lastLayerID = layerID

		fmt.Fprintf(os.Stderr, "Downloading layer %d/%d (%s, %.2f MiB)...\n", idx+1, len(manifest.Layers), layerDesc.Digest[:19], float64(layerDesc.Size)/(1024*1024))
		uncompressedPath := filepath.Join(workDir, fmt.Sprintf("layer-%d.tar", idx))
		uncompressedSize, err := client.downloadAndVerifyLayer(layerDesc, wantDiffID, uncompressedPath)
		if err != nil {
			fatalf("layer %d: %v", idx, err)
		}

		dirHdr := &tar.Header{
			Name:     layerID + "/",
			Mode:     0o755,
			ModTime:  epoch,
			Typeflag: tar.TypeDir,
			Uid:      0,
			Gid:      0,
			Uname:    "root",
			Gname:    "root",
		}
		if err := tw.WriteHeader(dirHdr); err != nil {
			fatalf("write layer dir header: %v", err)
		}

		layerTarRel := layerID + "/layer.tar"
		lf, err := os.Open(uncompressedPath)
		if err != nil {
			fatalf("open uncompressed layer: %v", err)
		}
		fileHdr := &tar.Header{
			Name:     layerTarRel,
			Mode:     0o644,
			Size:     uncompressedSize,
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Uid:      0,
			Gid:      0,
			Uname:    "root",
			Gname:    "root",
		}
		if err := tw.WriteHeader(fileHdr); err != nil {
			lf.Close()
			fatalf("write layer.tar header: %v", err)
		}
		if _, err := io.Copy(tw, lf); err != nil {
			lf.Close()
			fatalf("stream layer.tar: %v", err)
		}
		lf.Close()
		_ = os.Remove(uncompressedPath)

		layerPaths = append(layerPaths, layerTarRel)
	}

	saveManifest := []dockerSaveManifestEntry{{
		Config:   configEntryName,
		RepoTags: []string{repoTag},
		Layers:   layerPaths,
	}}
	manifestBytes, err := json.Marshal(saveManifest)
	if err != nil {
		fatalf("marshal manifest.json: %v", err)
	}
	if err := writeTarBytes("manifest.json", 0o644, manifestBytes); err != nil {
		fatalf("write manifest.json: %v", err)
	}

	repoPart, tagPart := splitRepoAndTag(repoTag)
	repositoriesMap := map[string]map[string]string{
		repoPart: {tagPart: lastLayerID},
	}
	reposBytes, err := json.Marshal(repositoriesMap)
	if err != nil {
		fatalf("marshal repositories: %v", err)
	}
	if err := writeTarBytes("repositories", 0o644, reposBytes); err != nil {
		fatalf("write repositories: %v", err)
	}

	if err := tw.Close(); err != nil {
		fatalf("close tar writer: %v", err)
	}
	if err := gzOut.Close(); err != nil {
		fatalf("close gzip writer: %v", err)
	}
	if err := outFile.Sync(); err != nil {
		fatalf("sync output file: %v", err)
	}
	if err := outFile.Close(); err != nil {
		fatalf("close output file: %v", err)
	}
	if err := os.Rename(tmpOut, outputPath); err != nil {
		fatalf("publish output file: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Saved verified offline Docker archive: %s\n", outputPath)
}

type registryClient struct {
	httpClient *http.Client
	host       string
	repo       string
	token      string
}

func (c *registryClient) doWithAuth(rawURL, accept string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			challenge := resp.Header.Get("Www-Authenticate")
			resp.Body.Close()
			tok, err := c.fetchBearerToken(challenge)
			if err != nil {
				return nil, err
			}
			c.token = tok
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, rawURL, strings.TrimSpace(string(body)))
		}
		return resp, nil
	}
	return nil, errors.New("authentication retry exhausted")
}

func (c *registryClient) fetchBearerToken(header string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", fmt.Errorf("unsupported WWW-Authenticate challenge: %q", header)
	}
	params := parseAuthParams(header[len("Bearer "):])
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("missing realm in WWW-Authenticate")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	scope := params["scope"]
	if scope == "" {
		scope = fmt.Sprintf("repository:%s:pull", c.repo)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", errors.New("empty bearer token response")
}

func (c *registryClient) get(rawURL, accept, expectedDigest string) ([]byte, string, error) {
	resp, err := c.doWithAuth(rawURL, accept)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	gotHex := hex.EncodeToString(sum[:])
	if expectedDigest != "" {
		wantHex, err := parseSHA256Digest(expectedDigest)
		if err != nil {
			return nil, "", err
		}
		if gotHex != wantHex {
			return nil, "", fmt.Errorf("digest mismatch: got sha256:%s, want sha256:%s", gotHex, wantHex)
		}
	}
	return data, gotHex, nil
}

func (c *registryClient) downloadAndVerifyLayer(desc ociDescriptor, wantDiffID, destPath string) (int64, error) {
	wantCompressedHex, err := parseSHA256Digest(desc.Digest)
	if err != nil {
		return 0, err
	}
	wantDiffHex, err := parseSHA256Digest(wantDiffID)
	if err != nil {
		return 0, err
	}

	rawURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", c.host, c.repo, desc.Digest)
	resp, err := c.doWithAuth(rawURL, "application/octet-stream")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	compressedHasher := sha256.New()
	reader := io.TeeReader(resp.Body, compressedHasher)

	var tarStream io.Reader = reader
	var gzReader *gzip.Reader
	if strings.HasSuffix(desc.MediaType, "+gzip") || strings.HasSuffix(desc.MediaType, ".gzip") {
		gzReader, err = gzip.NewReader(reader)
		if err != nil {
			return 0, fmt.Errorf("gzip layer stream: %w", err)
		}
		defer gzReader.Close()
		tarStream = gzReader
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	diffHasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, diffHasher), tarStream)
	if gzReader != nil {
		// Drain any trailing gzip footer bytes so compressedHasher sees the full HTTP stream.
		_, _ = io.Copy(io.Discard, reader)
	}
	closeErr := out.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}

	if gotComp := hex.EncodeToString(compressedHasher.Sum(nil)); gotComp != wantCompressedHex {
		return 0, fmt.Errorf("compressed layer sha256 mismatch: got %s, want %s", gotComp, wantCompressedHex)
	}
	if gotDiff := hex.EncodeToString(diffHasher.Sum(nil)); gotDiff != wantDiffHex {
		return 0, fmt.Errorf("uncompressed diff_id mismatch: got %s, want %s", gotDiff, wantDiffHex)
	}
	return n, nil
}

func parseImageRef(ref string) (host, repo, reference, repoTag string, err error) {
	ref = strings.TrimSpace(ref)
	firstSlash := strings.IndexByte(ref, '/')
	if firstSlash <= 0 {
		return "", "", "", "", errors.New("image reference must include registry/repository (e.g. ghcr.io/owner/repo:tag)")
	}
	host = ref[:firstSlash]
	rest := ref[firstSlash+1:]
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		if at == 0 {
			return "", "", "", "", errors.New("empty repository before digest")
		}
		repoPart := rest[:at]
		digestPart := rest[at+1:]
		if _, err := parseSHA256Digest(digestPart); err != nil {
			return "", "", "", "", fmt.Errorf("invalid image digest: %w", err)
		}
		tagPart := "latest"
		if colon := strings.LastIndexByte(repoPart, ':'); colon >= 0 {
			repo = repoPart[:colon]
			tagPart = repoPart[colon+1:]
			if repo == "" || tagPart == "" || strings.Contains(tagPart, "/") {
				return "", "", "", "", errors.New("invalid repository or tag before digest")
			}
		} else {
			repo = repoPart
		}
		reference = digestPart
		repoTag = host + "/" + repo + ":" + tagPart
		return host, repo, reference, repoTag, nil
	}
	lastColon := strings.LastIndexByte(rest, ':')
	if lastColon >= 0 {
		repo = rest[:lastColon]
		reference = rest[lastColon+1:]
		if strings.Contains(reference, "/") {
			return "", "", "", "", errors.New("invalid image tag")
		}
	} else {
		repo = rest
		reference = "latest"
	}
	if repo == "" || reference == "" {
		return "", "", "", "", errors.New("empty repository or tag")
	}
	repoTag = host + "/" + repo + ":" + reference
	return host, repo, reference, repoTag, nil
}

func splitRepoAndTag(repoTag string) (string, string) {
	idx := strings.LastIndexByte(repoTag, ':')
	if idx <= 0 {
		return repoTag, "latest"
	}
	return repoTag[:idx], repoTag[idx+1:]
}

func parseSHA256Digest(d string) (string, error) {
	if !strings.HasPrefix(d, "sha256:") || len(d) != len("sha256:")+64 {
		return "", fmt.Errorf("expected sha256:<64-hex>, got %q", d)
	}
	hexPart := d[len("sha256:"):]
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", err
	}
	return strings.ToLower(hexPart), nil
}

func parseAuthParams(raw string) map[string]string {
	out := make(map[string]string)
	var key, val bytes.Buffer
	inKey := true
	inQuotes := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inKey {
			if ch == '=' {
				inKey = false
			} else if ch != ',' && ch != ' ' && ch != '\t' {
				key.WriteByte(strings.ToLower(string(ch))[0])
			}
			continue
		}
		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}
		if ch == ',' && !inQuotes {
			out[key.String()] = val.String()
			key.Reset()
			val.Reset()
			inKey = true
			continue
		}
		val.WriteByte(ch)
	}
	if key.Len() > 0 {
		out[key.String()] = val.String()
	}
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "pull_image_tar: "+format+"\n", args...)
	os.Exit(1)
}
