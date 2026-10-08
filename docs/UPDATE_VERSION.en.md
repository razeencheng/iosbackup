# Maintainer release preparation guide

[简体中文](UPDATE_VERSION.md) · [Development guide](DEVELOPMENT.md)

This page helps maintainers prepare **source versions, build metadata, and release artifacts** . Users updating an installed container should follow [instance upgrade and rollback](manual/upgrade.md). The `UPDATE_VERSION.md` entry remains available for existing links.

## Release workflow and image tags

Pushing a `v*` tag triggers [release.yml](../.github/workflows/release.yml) in the designated public GitHub repository, `razeencheng/iosbackup`. The workflow checks that the GHCR package and Docker Hub repository are public, and pushes the same image to `ghcr.io/razeencheng/iosbackup` and `docker.io/razeencheng/iosbackup`.

| Version | GitHub Release | Image tags |
|---|---|---|
| `vX.Y.Z` | Stable release | Version tag; also updates `latest` when it is the highest stable version |
| Suffixed versions such as `vX.Y.Z-beta.N` | Prerelease | Version tag only; leaves `latest` unchanged |

Each version builds the `linux/amd64` and `linux/arm64` multi-architecture image once. After signing, supply-chain verification, and GitHub Release creation succeed, publication assigns `latest` to the same index digest in both registries and verifies both without rebuilding. Publication jobs serialize tag updates and compare numeric stable versions, preventing older reruns or maintenance-branch patches from moving `latest` backwards. GitHub's Latest marker updates after the image digest check. When retrying failures, retain the original tag and candidate commit, inspect existing artifacts, and resolve the failure. If the tagged workflow itself needs a code fix, merge and test that fix and choose a new release version; never move the published tag or bypass verification.

Ordinary [CI](../.github/workflows/ci.yml) and the manual [packaging check](../.github/workflows/docker-package-test.yml) neither push images nor update `latest`. Users still run `docker compose pull` and `docker compose up -d` to replace a running container.

Maintainers manage repository and GHCR package visibility separately; the workflow never changes it. Existing packages must be public. An absent package may be created on its first push, but the post-push public check must pass. If GitHub creates a personal package as private by default, make it public in package settings and rerun. A failed check blocks Release creation and `latest` promotion. See [GitHub's container registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

## Docker Hub setup

1. Keep [razeencheng/iosbackup](https://hub.docker.com/r/razeencheng/iosbackup) public. The preflight checks visibility before the lengthy build.
2. In Docker Account settings → Personal access tokens, create a token for GitHub Actions with **Read & Write** permissions and an appropriate expiration. Delete permission is unnecessary. See [Docker's PAT instructions](https://docs.docker.com/security/access-tokens/personal-access-tokens/).
3. In GitHub repository Settings → Secrets and variables → Actions, add Repository variable `DOCKERHUB_USERNAME` with value `razeencheng`, and Repository secret `DOCKERHUB_TOKEN` with the token value. Do not paste the token into source, build arguments, logs, or chat.
4. A release requires both registry logins. Missing settings, invalid authentication, or a private/missing Docker Hub repository stop publication before building. Keep the token valid when retrying the release job, which logs in again to promote `latest`.

One build pushes both version tags. Docker Hub's index digest must match the build output before signing. Both registries receive keyless index signatures and platform SBOM attestations, verified against the exact tag workflow identity. The nine GitHub Release attachments retain the canonical GHCR references; Docker Hub verification is also recorded in Actions logs. The images share an index digest.

Registry writes are not atomic across services: a push or promotion can succeed on one registry before another fails. A failed workflow is not a completed dual-registry release. Inspect both version and `latest` digests before retrying; the GitHub Latest marker changes only after both final digest checks pass. Re-running a failed build may produce a different candidate digest; preserve the tag's source commit and use evidence from the successful run.

## 1. Align the version and release notes

Choose major/minor/patch according to externally visible compatibility; an internal refactor does not inherently require a major release. Use a version format accepted by the manifest parser, such as `vX.Y.Z` or `vX.Y.Z-beta.N`.

| Location | What to maintain |
|---|---|
| [release/manifest.env](../release/manifest.env) | The release manifest has exactly three keys: `IOSBK_VERSION`, `IOSBK_BUILD_DATE`, and `IOSBK_SOURCE_URL`. Freeze the target version, build date, and public source URL. |
| [internal/buildinfo/buildinfo.go](../internal/buildinfo/buildinfo.go) | Match `Version` to the manifest and summarize current changes in `Description`. Keep development defaults for `BuildDate` and `Commit` as `unknown`. |
| [buildinfo_test.go](../internal/buildinfo/buildinfo_test.go) | Update assertions for the development version and description defaults. |
| [CHANGELOG.md](../CHANGELOG.md) | Add exactly one `## <version>` heading. Group notes under headings such as `### Fixed` and `### Changed`, covering behavior, compatibility, migration, and support boundaries. |
| [compose.yaml](../compose.yaml), related installation manuals | Keep the default image at `latest`. Update both languages when deployment settings or procedures change; version-only releases do not require edits to Compose, README, or the `publicReleaseImage` assertion. Preserve historical versions in release records and old screenshot provenance. |

CHANGELOG owns release notes. READMEs refer to the latest version and link to Compose and CHANGELOG without literal project version numbers; a version-only release does not require a README edit. Search old-version references with `rg -n -F` and review each current default individually. Do not replace historical versions throughout the repository.

Read the manifest with `scripts/read_release_manifest.sh`, never `source` or `eval`. Docker builds inject `Version`, `BuildDate`, `Commit`, and `SourceURL` into `iosbackup/internal/buildinfo`. The commit is the full SHA of the release source; `Description` comes from source. Do not copy the build date into Go source or use the removed root-level `version.go`.

## 2. Check release notes before pushing a tag

Run from the **release source root** . The current workflow requires exactly one matching version heading, another `## ` heading afterward as an extraction boundary, and at least one `### ` subsection within the release. This preflight checks those format requirements:

```bash
(
  set -eu
  iosbk_version=$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_VERSION)
  test "$(grep -Fxc "## $iosbk_version" CHANGELOG.md)" = 1
  awk -v heading="## $iosbk_version" '
    $0 == heading { found=1; next }
    found && /^## / { boundary=1; exit }
    found && /^### / { subsection=1 }
    END { if (!boundary || !subsection) exit 1 }
  ' CHANGELOG.md
  printf 'Release notes format OK: %s\n' "$iosbk_version"
)
```

Fix CHANGELOG before proceeding if it fails. The current workflow extracts notes after pushing and signing the image, so a format error can leave the image pushed without a GitHub Release. Do this check before pushing a tag. For a first release without an older-version boundary, adjust and verify the extraction logic rather than inventing release history.

## 3. Run local checks and packaging rehearsals

Still from the **release source root** :

```bash
./scripts/read_release_manifest.sh release/manifest.env
./scripts/read_release_manifest_test.sh
go test -count=1 ./internal/buildinfo
go test -count=1 -run 'TestReleaseWorkflow|TestReleaseCosign|TestReleaseDockerHub|TestReleaseLatestPromotion|TestReleasePublicVisibilityGates|TestReleaseManifest|TestDockerPackageTestWorkflow|TestDockerBuildContextIncludesBuildInfoPackage|TestPublicMetadataMatchesReleaseManifest|TestPublicComposeUsesOfficialReleaseImage|TestPublicReadmesDocumentReleaseDeployment' ./internal/app
git diff --check
```

These commands check release metadata and workflow requirements. The candidate must also pass the full [CI](../.github/workflows/ci.yml) checks, including `go vet ./...`, default tests, race tests, static builds, and licensing checks. See [development](DEVELOPMENT.md) and [testing](TESTING_GUIDE.en.md) for environments and commands. Do not enable real-notification tests for routine release preparation.

Check distribution contents in the **actual public candidate source tree** :

```bash
./scripts/check_public_repo.sh --directory .
./scripts/verify_licensing.sh
```

A development tree containing private documents is not the target for this public-directory check. Rerun relevant tests in the exported candidate. The release commit must identify the distributed source; do not substitute another checkout's SHA.

| Method | Evidence and output |
|---|---|
| `make build` | Builds and loads a local `linux/amd64` image by default, without pushing; the platform can be selected explicitly. |
| `make build-multi` | Creates a local multi-architecture OCI archive without pushing. |
| [docker-package-test.yml](../.github/workflows/docker-package-test.yml) | Manual amd64/arm64 OCI packaging check; no push, archive upload, signature, or container startup. |
| `make push` | Explicitly pushes an image. It does not include the official workflow's signing, SBOM, visibility, or Release checks and is not a complete release procedure. |

Successful packaging does not prove publication, device operation, or recovery. Use locked project sources for component downloads and investigate transient network failures before changing dependencies.

## 4. Review before publication and retain evidence

Arrange tag publication only after reviewing candidate contents, CI, and the target version. **Pushing a `v*` tag immediately starts the build and release workflow.** Check:

- The tag exactly matches `IOSBK_VERSION`; checkout HEAD, tag target, and workflow event SHA agree. No working-tree changes remain outside the candidate.
- The target repository is the permitted `razeencheng/iosbackup`, with matching `IOSBK_SOURCE_URL`. Include reviewed files only; indiscriminate `git add .` does not replace review.
- The repository is public, any existing GHCR package is public, and the post-push public check can pass. Required Actions/OIDC, package-write, and Release-write permissions are ready.
- Support boundaries, migration/rollback notes, licenses, and test evidence describe this commit. Before retrying a failure, inspect whether images, signatures, or a Release already exist; failure does not imply nothing was published.

The successful workflow builds `linux/amd64` and `linux/arm64` images, records the multi-architecture index and platform digests, generates per-platform SPDX SBOMs and BuildKit SLSA provenance, signs the index with Cosign in both registries, attests each platform's SBOM, and verifies the results.

The GitHub Release body comes from CHANGELOG. It attaches **9 evidence files** : two SBOMs, two provenance files, the platform-manifest list, the index digest, and three Cosign verification results. The intermediate Actions artifact also includes the release notes, for 10 files total, currently retained for only one day. There is no standalone Go binary or full image tar release attachment.

After publication, check both architectures and build identity against the actual digest and retain workflow and verification evidence. Perform controlled acceptance using the user [upgrade and rollback guide](manual/upgrade.md). Also test anonymous source access and image pulls; for the highest stable version, confirm that `latest` and the version tag point to the same multi-architecture index digest. Record USB, encrypted-read, and restore evidence separately. Keep unverified capabilities marked according to [Feature status](FEATURE_STATUS.md); packaging or signing success cannot establish them.
