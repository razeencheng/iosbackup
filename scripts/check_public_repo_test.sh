#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
checker="$script_dir/check_public_repo.sh"
fixture_rel='scripts/testdata/sensitive-content/fixed-fake-identifiers.txt'
fixture="$source_root/$fixture_rel"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-public-check-test.XXXXXX") || exit 2
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
failures="$test_root/failures"
: >"$failures"

mkdir -p "$test_root/go-cache" "$test_root/go-tmp" "$test_root/bin"
real_git=$(command -v git)
if command -v xcrun >/dev/null 2>&1; then
	developer_git=$(xcrun -f git 2>/dev/null || true)
	[ -z "$developer_git" ] || real_git=$developer_git
fi
ln -s "$real_git" "$test_root/bin/git"
export PATH="$test_root/bin:$PATH"
GOCACHE="$test_root/go-cache" GOTMPDIR="$test_root/go-tmp" \
	go build -trimpath -o "$test_root/bin/sensitive-content-matcher" "$source_root/scripts/sensitive_content_matcher.go"
export IOSBK_SENSITIVE_MATCHER="$test_root/bin/sensitive-content-matcher"

record_failure() {
	printf 'FAIL: %s\n' "$1" >>"$failures"
}

populate_fixture() {
	fixture_root=$1
	mkdir -p "$fixture_root"
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		mkdir -p "$fixture_root/$(dirname -- "$path")"
		cp "$source_root/$path" "$fixture_root/$path"
	done <<'EOF'
LICENSE
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
docs/LICENSING.md
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
third_party/licenses/spdx-license-map.tsv
third_party/licenses/Apache-2.0.txt
third_party/licenses/BSD-1-Clause.txt
third_party/licenses/BSD-2-Clause.txt
third_party/licenses/BSD-3-Clause.txt
third_party/licenses/CC0-1.0.txt
third_party/licenses/ISC.txt
third_party/licenses/LGPL-2.1.txt
third_party/licenses/LGPL-3.0.txt
third_party/licenses/LLVM-exception.txt
third_party/licenses/MIT.txt
third_party/licenses/MIT-0.txt
third_party/licenses/MPL-2.0.txt
third_party/licenses/SQLite-Blessing.txt
third_party/licenses/Unicode-3.0.txt
third_party/licenses/Unlicense.txt
third_party/patches/libimobiledevice/afc-return-after-receive-error.patch
third_party/patches/libimobiledevice/backup-activity-records.patch
third_party/patches/netmuxd/heartbeat-reconnect-after-sleep.patch
third_party/patches/netmuxd/observability.patch
third_party/patches/netmuxd/restore-helper-binaries.patch
third_party/patches/usbmuxd2/client-disconnect-exception.patch
EOF
}

init_index_fixture() {
	repo=$1
	populate_fixture "$repo"
	git -C "$repo" init -q --template=
	git -C "$repo" config user.email test@example.invalid
	git -C "$repo" config user.name 'Public Check Test'
	(
		cd "$repo"
		find . -path './.git' -prune -o -type f ! -name public-files.txt -print | LC_ALL=C sort | while IFS= read -r path; do
			printf 'include\t%s\ttest fixture\n' "${path#./}"
		done >public-files.txt
	)
	mkdir -p "$repo/.cursor"
	sed -n '1p' "$fixture" >"$repo/AGENTS.md"
	sed -n '2p' "$fixture" >"$repo/.cursor/private.md"
	git -C "$repo" add -A
	git -C "$repo" commit -qm fixture
}

assert_tmp_empty() {
	case_tmp=$1
	case_label=$2
	if find "$case_tmp" -mindepth 1 -print -quit | grep -q .; then
		leftovers=$(find "$case_tmp" -mindepth 1 -print | sed -n '1,5p')
		record_failure "$case_label left temporary files behind: $leftovers"
	fi
}

run_check() {
	case_label=$1
	fixture_root=$2
	expectation=$3
	shift 3
	case_tmp="$test_root/tmp-$case_label"
	case_output="$test_root/output-$case_label"
	mkdir -p "$case_tmp"
	if (cd "$fixture_root" && TMPDIR="$case_tmp" "$checker" "$@") >"$case_output" 2>&1; then
		status=0
	else
		status=$?
	fi
	case "$expectation:$status" in
		pass:0) ;;
		fail:0) record_failure "$case_label unexpectedly passed" ;;
		pass:*) record_failure "$case_label unexpectedly failed with status $status: $(sed -n '1p' "$case_output")" ;;
		fail:*) ;;
	esac
	assert_tmp_empty "$case_tmp" "$case_label"
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		if grep -F "$line" "$case_output" >/dev/null 2>&1; then
			record_failure "$case_label disclosed sensitive fixture content"
			break
		fi
	done <"$fixture"
}

directory_fixture="$test_root/directory"
populate_fixture "$directory_fixture"
run_check directory "$directory_fixture" pass --directory .

index_fixture="$test_root/index"
init_index_fixture "$index_fixture"
run_check public-index "$index_fixture" pass --public-index public-files.txt

# Public-index reads only selected index blobs: neither committed private files
# nor unstaged working-tree content can expand its scope.
sed -n '1p' "$fixture" >"$index_fixture/README.md"
run_check public-index-scope "$index_fixture" pass --public-index public-files.txt

# Internal documentation stays private even when an include selects its blobs.
for private_document in CONTEXT.md docs/adr/0001-internal.md; do
	case "$private_document" in
		CONTEXT.md) document_label=context; excluded_path=CONTEXT.md ;;
		*) document_label=adr; excluded_path=docs/adr ;;
	esac
	private_directory="$test_root/private-$document_label-directory"
	populate_fixture "$private_directory"
	mkdir -p "$private_directory/$(dirname -- "$private_document")"
	printf '# Internal project notes\n' >"$private_directory/$private_document"
	run_check "private-$document_label-directory" "$private_directory" fail --directory .
	if ! grep -Fq "private or excluded path is present: $excluded_path" "$test_root/output-private-$document_label-directory"; then
		record_failure "private-$document_label-directory did not reject the internal documentation path"
	fi

	private_index="$test_root/private-$document_label-index"
	init_index_fixture "$private_index"
	mkdir -p "$private_index/$(dirname -- "$private_document")"
	printf '# Internal project notes\n' >"$private_index/$private_document"
	printf 'include\t%s\taccidentally selected internal documentation\n' "$private_document" >>"$private_index/public-files.txt"
	git -C "$private_index" add -- "$private_document" public-files.txt
	run_check "private-$document_label-index" "$private_index" fail --public-index public-files.txt
	if ! grep -Fq "private or excluded path is present: $excluded_path" "$test_root/output-private-$document_label-index"; then
		record_failure "private-$document_label-index did not reject the internal documentation path"
	fi
done

assert_runtime_boundary() {
    case_label=$1
    case "$2" in
        .env*) expected='local environment path is present' ;;
        data*) expected='private or excluded path is present: data' ;;
        *) expected='runtime configuration is present' ;;
    esac
    if ! grep -Fq "$expected" "$test_root/output-$case_label"; then
        record_failure "$case_label did not reject the runtime filename at the policy boundary"
    fi
}

# Filenames alone must block runtime secrets, including partial initialization
# files, even when harmless fixture bytes evade content-based secret detection.
for runtime_path in .env .env.local data/configs/admin_password \
    configs/admin_password configs/secret_key configs/auth_credentials.json configs/csrf_secret.json configs/secrets.enc \
    configs/.admin_password-example configs/.secret_key-example configs/.auth_credentials.json-example \
    configs/.csrf_secret.json-example configs/.secrets.enc-example; do
    runtime_label=$(printf '%s' "$runtime_path" | tr '/.' '__')
    runtime_directory="$test_root/runtime-$runtime_label-directory"
    populate_fixture "$runtime_directory"
    mkdir -p "$runtime_directory/$(dirname -- "$runtime_path")"
    printf 'harmless runtime-secret fixture\n' >"$runtime_directory/$runtime_path"
    run_check "runtime-$runtime_label-directory" "$runtime_directory" fail --directory .
    assert_runtime_boundary "runtime-$runtime_label-directory" "$runtime_path"

    runtime_index="$test_root/runtime-$runtime_label-index"
    init_index_fixture "$runtime_index"
    mkdir -p "$runtime_index/$(dirname -- "$runtime_path")"
    printf 'harmless runtime-secret fixture\n' >"$runtime_index/$runtime_path"
    printf 'include\t%s\taccidentally selected runtime secret\n' "$runtime_path" >>"$runtime_index/public-files.txt"
    git -C "$runtime_index" add -f -- "$runtime_path" public-files.txt
    run_check "runtime-$runtime_label-index" "$runtime_index" fail --public-index public-files.txt
    assert_runtime_boundary "runtime-$runtime_label-index" "$runtime_path"
 done

# The same forbidden names cannot bypass the directory gate by changing type.
for runtime_path in .env.local data configs/secret_key configs/.admin_password-example; do
    runtime_label=$(printf '%s' "$runtime_path" | tr '/.' '__')
    for kind in directory symlink dangling; do
        special="$test_root/runtime-type-$runtime_label-$kind"
        populate_fixture "$special"
        mkdir -p "$special/$(dirname -- "$runtime_path")"
        case "$kind" in
            directory) mkdir "$special/$runtime_path" ;;
            symlink) ln -s "$special/LICENSE" "$special/$runtime_path" ;;
            dangling) ln -s "$special/missing-target" "$special/$runtime_path" ;;
        esac
        run_check "runtime-type-$runtime_label-$kind" "$special" fail --directory .
        assert_runtime_boundary "runtime-type-$runtime_label-$kind" "$runtime_path"
    done
done

missing="$test_root/missing"
populate_fixture "$missing"
rm "$missing/scripts/sensitive-content-patterns.txt"
run_check missing-scanner-asset "$missing" fail --directory .

symlinked="$test_root/symlinked"
populate_fixture "$symlinked"
ln -s LICENSE "$symlinked/license-link"
run_check symlink "$symlinked" fail --directory .

binary="$test_root/binary"
populate_fixture "$binary"
printf 'unknown\000binary\n' >"$binary/unknown.dat"
run_check unknown-binary "$binary" fail --directory .

malformed_image="$test_root/malformed-image"
populate_fixture "$malformed_image"
printf 'not-a-png\000' >"$malformed_image/internal/app/static/fake.png"
run_check malformed-image "$malformed_image" fail --directory .

empty_manifest="$test_root/empty-manifest"
init_index_fixture "$empty_manifest"
: >"$empty_manifest/public-files.txt"
git -C "$empty_manifest" add public-files.txt
run_check empty-manifest "$empty_manifest" fail --public-index public-files.txt

unsafe_manifest="$test_root/unsafe-manifest"
init_index_fixture "$unsafe_manifest"
printf 'include\t../outside\tunsafe\n' >"$unsafe_manifest/public-files.txt"
git -C "$unsafe_manifest" add public-files.txt
run_check unsafe-manifest "$unsafe_manifest" fail --public-index public-files.txt

# A leading colon enables Git pathspec magic, including exclude-only patterns
# that implicitly start from the entire index. The public manifest language
# never permits this syntax, even when the rest of the path looks harmless.
for magic_case in exclude-bang exclude-caret top long-form; do
	case "$magic_case" in
		exclude-bang) magic_pattern=':!README.md' ;;
		exclude-caret) magic_pattern=':^README.md' ;;
		top) magic_pattern=':/README.md' ;;
		long-form) magic_pattern=':(exclude)README.md' ;;
	esac
	magic_manifest="$test_root/magic-$magic_case"
	init_index_fixture "$magic_manifest"
	printf 'include\t%s\tGit pathspec magic is forbidden\n' "$magic_pattern" >"$magic_manifest/public-files.txt"
	git -C "$magic_manifest" add public-files.txt
	run_check "magic-$magic_case" "$magic_manifest" fail --public-index public-files.txt
	if ! grep -Fq 'public allowlist contains an unsafe path pattern' "$test_root/output-magic-$magic_case"; then
		record_failure "magic-$magic_case did not fail at the pathspec boundary"
	fi
done

untracked_manifest="$test_root/untracked-manifest"
init_index_fixture "$untracked_manifest"
git -C "$untracked_manifest" rm -q --cached public-files.txt
run_check untracked-manifest "$untracked_manifest" fail --public-index public-files.txt

run_check missing-mode "$directory_fixture" fail

if [ -s "$failures" ]; then
	cat "$failures" >&2
	exit 1
fi

echo 'public repository check tests passed'
