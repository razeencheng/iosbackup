#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scanner="$script_dir/check_sensitive_content.sh"

usage() {
  printf 'usage: %s (--public-index <manifest> | --directory <export-root>)\n' "$0" >&2
  exit 2
}

check_tmp=''
cleanup() {
  if [ -n "$check_tmp" ] && [ -d "$check_tmp" ]; then
    rm -rf "$check_tmp"
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
check_tmp=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-public-check.XXXXXX") || exit 2

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

require_public_files() {
  root=$1
  required_files='LICENSE
Dockerfile
NOTICE
THIRD_PARTY_NOTICES.md
README.md
CHANGELOG.md
SECURITY.md
CONTRIBUTING.md
CODE_OF_CONDUCT.md
SUPPORT.md
docs/PRIVACY.md
cmd/iosbackup/main.go
internal/app/bootstrap.go
internal/app/templates/index.html
internal/app/static/ui-icons.svg
internal/buildinfo/buildinfo.go
release/manifest.env
go.mod
go.sum
Makefile
compose.yaml
.gitignore
.gitattributes
.dockerignore
.github/workflows/ci.yml
.github/workflows/release.yml
.github/ISSUE_TEMPLATE/bug_report.yml
.github/ISSUE_TEMPLATE/feature_request.yml
.github/pull_request_template.md
scripts/check_public_repo.sh
scripts/check_public_repo_test.sh
scripts/check_sensitive_content.sh
scripts/sensitive_content_matcher.go
scripts/sensitive-content-patterns.txt
scripts/sensitive-content-fixture-exceptions.tsv
scripts/check_sensitive_content_test.sh
scripts/testdata/sensitive-content/fixed-fake-identifiers.txt
scripts/testdata/sensitive-content/generate_test_images.go
scripts/read_release_manifest.sh
scripts/read_release_manifest_test.sh
scripts/verify_licensing.sh
scripts/verify_licensing_test.sh
scripts/collect_cargo_licenses.sh
scripts/collect_cargo_licenses_test.sh
scripts/run_tests.sh
third_party/components.lock.json
third_party/runtime-closure-linux-amd64.txt
third_party/runtime-closure-linux-arm64.txt
third_party/netmuxd-cargo-licenses.tsv
third_party/cargo-license-fallbacks.tsv
third_party/cargo-license-fallbacks/defmt-parser-1.0.0/materials.tsv
third_party/cargo-license-fallbacks/defmt-parser-1.0.0/LICENSE-APACHE
third_party/cargo-license-fallbacks/defmt-parser-1.0.0/LICENSE-MIT
third_party/cargo-license-fallbacks/idevice-0.1.65/materials.tsv
third_party/cargo-license-fallbacks/idevice-0.1.65/LICENSE.txt
third_party/licenses/README.md
third_party/licenses/spdx-license-map.tsv'

  printf '%s\n' "$required_files" | while IFS= read -r file; do
    [ -f "$root/$file" ] || {
      printf 'missing public release file: %s\n' "$file" >&2
      exit 1
    }
  done
}

check_release_manifest() {
  root=$1
  manifest="$root/release/manifest.env"
  manifest_reader="$root/scripts/read_release_manifest.sh"
  if ! "$manifest_reader" "$manifest" >"$check_tmp/release-env"; then
    fail 'release manifest is missing, malformed, or ambiguous'
  fi
  release_date=$("$manifest_reader" "$manifest" IOSBK_BUILD_DATE) || fail 'release build date is invalid'
  release_source=$("$manifest_reader" "$manifest" IOSBK_SOURCE_URL) || fail 'release source URL is invalid'

  for consumer in Dockerfile Makefile .github/workflows/ci.yml .github/workflows/release.yml; do
    if grep -Fq "$release_date" "$root/$consumer" || grep -Fq "$release_source" "$root/$consumer"; then
      fail "$consumer duplicates frozen release metadata"
    fi
  done
  grep -Fq 'read_release_manifest.sh' "$root/Makefile" || fail 'Makefile does not use the strict release manifest reader'
  grep -Fq 'read_release_manifest.sh' "$root/.github/workflows/ci.yml" || fail 'CI does not use the strict release manifest reader'
  manifest_rel=release/manifest.env
  for consumer in Makefile .github/workflows/ci.yml .github/workflows/release.yml scripts/check_public_repo.sh scripts/verify_licensing.sh; do
    if grep -Fq "include $manifest_rel" "$root/$consumer"; then fail "$consumer executes the release manifest"; fi
    if grep -Fq ". ./$manifest_rel" "$root/$consumer"; then fail "$consumer sources the release manifest"; fi
    if grep -Fq "source $manifest_rel" "$root/$consumer"; then fail "$consumer sources the release manifest"; fi
  done
}

check_directory_policy() {
  root_input=$1
  run_sensitive=$2
  [ -d "$root_input" ] || fail 'public directory does not exist'
  [ ! -L "$root_input" ] || fail 'public directory root must not be a symlink'
  root=$(CDPATH= cd -- "$root_input" && pwd -P) || fail 'unable to resolve public directory'

  require_public_files "$root"
  check_release_manifest "$root"
  cargo_inventory=$(sed -n 's/.*"cargo_license_inventory":"\([^"]*\)".*/\1/p' "$root/third_party/components.lock.json")
  [ "$cargo_inventory" = 'third_party/netmuxd-cargo-licenses.tsv' ] || fail 'component lock lacks the required Cargo license inventory'
  [ -f "$root/$cargo_inventory" ] || fail 'component lock references a missing Cargo license inventory'

  for private_path in \
    .cursor AGENTS.md CLAUDE.md designs docs/claude docs/plans docs/qa docs/release docs/research \
    Dockerfile.spike Dockerfile.spike.entrypoint.sh release.sh backups lockdown; do
    [ ! -e "$root/$private_path" ] || fail "private or excluded path is present: $private_path"
  done
  if find "$root/configs" -type f \( -name 'backup_configs.json' -o -name 'notification_configs.json' -o -name '*runtime*.json' -o -name 'secrets.enc*' \) -print -quit 2>/dev/null | grep -q .; then
    fail 'runtime configuration is present in the public tree'
  fi
  if find "$root" -maxdepth 1 -type f -name '*.go' -print -quit | grep -q .; then
    fail 'root-package Go files are present in the public tree'
  fi
  [ ! -e "$root/templates" ] || fail 'legacy root templates directory is present'
  [ ! -e "$root/static" ] || fail 'legacy root static directory is present'

  dockerfile="$root/Dockerfile"
  buildinfo="$root/internal/buildinfo/buildinfo.go"
  if grep -Eq 'github[.]com/OWNER|github[.]com/[^ /]+/REPO|IOSBK_COMMIT=unknown' "$dockerfile" "$buildinfo"; then
    fail 'release metadata still contains a placeholder'
  fi
  grep -Eq -- '-X main[.]' "$dockerfile" && fail 'Dockerfile injects removed main-package metadata'
  grep -Fq 'COPY cmd/ ./cmd/' "$dockerfile" || fail 'Dockerfile does not copy cmd packages'
  grep -Fq 'COPY internal/ ./internal/' "$dockerfile" || fail 'Dockerfile does not copy internal packages'
  grep -Fq './cmd/iosbackup' "$dockerfile" || fail 'Dockerfile does not build cmd/iosbackup'
  grep -Fq 'iosbackup/internal/buildinfo.' "$dockerfile" || fail 'Dockerfile does not inject buildinfo metadata'
  if grep -E '^FROM ' "$dockerfile" | grep -vE '^FROM (scratch|[^ ]+@sha256:[0-9a-f]{64})($| AS )'; then
    fail 'Dockerfile contains an unpinned tagged base image'
  fi
  grep -Fq '此项目基于 MIT 许可证开源' "$root/README.md" && fail 'README contains a license contradiction'
  grep -Fq '"release_blocker":true' "$root/third_party/components.lock.json" && fail 'component lock contains a release blocker'

  if find "$root/internal/app/templates" "$root/internal/app/static" -type f \
    \( -name '*.html' -o -name '*.css' -o -name '*.js' -o -name '*.svg' \) \
    -exec grep -En "(<script|<link|<img)[^>]+(src|href)=['\"](https?:)?//" {} + | grep -q .; then
    fail 'embedded UI contains a remote runtime asset'
  fi

  if [ "$run_sensitive" = yes ]; then
    "$scanner" --directory "$root"
  fi
  "$root/scripts/read_release_manifest_test.sh"
  "$root/scripts/verify_licensing.sh"
  "$root/scripts/collect_cargo_licenses_test.sh"
}

reject_manifest_pattern() {
  pattern=$1
  case "$pattern" in
    ''|/*|..|../*|*/../*|*/..|*'//'*) return 1 ;;
    :*|*'\n'*|*'\r'*|*'\t'*) return 1 ;;
  esac
  return 0
}

export_public_index() {
  manifest_path=$1
  if ! worktree_state=$(git rev-parse --is-inside-work-tree 2>/dev/null) || [ "$worktree_state" != true ]; then
    fail 'public-index checks require a Git worktree'
  fi
  if ! git show ":$manifest_path" >"$check_tmp/manifest" 2>/dev/null; then
    fail 'unable to read public allowlist from the index'
  fi
  [ -s "$check_tmp/manifest" ] || fail 'public allowlist is empty'

  : >"$check_tmp/all-paths"
  include_count=0
  tab=$(printf '\t')
  while IFS="$tab" read -r action pattern reason; do
    case "$action" in
      ''|'#'*) continue ;;
      include)
        reject_manifest_pattern "$pattern" || fail 'public allowlist contains an unsafe path pattern'
        include_count=$((include_count + 1))
        if ! git -c core.quotepath=false ls-files -- "$pattern" >"$check_tmp/matches"; then
          fail 'unable to expand public allowlist entry'
        fi
        [ -s "$check_tmp/matches" ] || fail 'public allowlist entry has no index blob'
        while IFS= read -r path; do
          case "$path" in '"'*) fail 'public allowlist contains a Git-quoted unsafe path' ;; esac
          printf '%s\n' "$path" >>"$check_tmp/all-paths"
        done <"$check_tmp/matches"
        ;;
      exclude)
        reject_manifest_pattern "$pattern" || fail 'public allowlist contains an unsafe exclusion pattern'
        ;;
      *) fail 'public allowlist contains an invalid action' ;;
    esac
  done <"$check_tmp/manifest"
  [ "$include_count" -gt 0 ] || fail 'public allowlist has no include entries'

  LC_ALL=C sort "$check_tmp/all-paths" >"$check_tmp/sorted-paths"
  if uniq -d "$check_tmp/sorted-paths" | grep -q .; then
    fail 'public allowlist expands the same file more than once'
  fi
  [ -s "$check_tmp/sorted-paths" ] || fail 'public allowlist expands to an empty tree'

  export_root="$check_tmp/export"
  mkdir -p "$export_root"
  while IFS= read -r path; do
    mode=$(git ls-files -s -- "$path" | awk 'NR == 1 { print $1 }')
    case "$mode" in 100644|100755) ;; *) fail 'public allowlist includes a symlink or unsupported file mode' ;; esac
    mkdir -p "$export_root/$(dirname -- "$path")"
    if ! git show ":$path" >"$export_root/$path"; then
      fail 'unable to export public index blob'
    fi
    [ "$mode" = 100644 ] || chmod 755 "$export_root/$path"
  done <"$check_tmp/sorted-paths"

  "$scanner" --public-index "$manifest_path"
  check_directory_policy "$export_root" no
}

[ "$#" -ge 1 ] || usage
case "$1" in
  --public-index)
    [ "$#" -eq 2 ] || usage
    export_public_index "$2"
    ;;
  --directory)
    [ "$#" -eq 2 ] || usage
    check_directory_policy "$2" yes
    ;;
  *) usage ;;
esac

echo 'public repository checks passed'
