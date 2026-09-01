#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
scanner="$script_dir/check_sensitive_content.sh"
fixture_rel='scripts/testdata/sensitive-content/fixed-fake-identifiers.txt'
fixture="$repo_root/$fixture_rel"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-sensitive-content-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
isolated_tmp="$test_root/tmp"
test_bin="$test_root/bin"
mkdir -p "$isolated_tmp" "$test_bin"
git_bin=$(command -v git)
if command -v xcrun >/dev/null 2>&1; then
	resolved_git=$(xcrun -f git 2>/dev/null || true)
	[ -z "$resolved_git" ] || git_bin=$resolved_git
fi
ln -s "$git_bin" "$test_bin/git"
mkdir -p "$test_root/matcher-go-cache" "$test_root/matcher-go-tmp"
GOCACHE="$test_root/matcher-go-cache" GOTMPDIR="$test_root/matcher-go-tmp" \
	go build -trimpath -o "$test_bin/sensitive-content-matcher" "$repo_root/scripts/sensitive_content_matcher.go"
export IOSBK_SENSITIVE_MATCHER="$test_bin/sensitive-content-matcher"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_tmp_empty() {
	if find "$isolated_tmp" -mindepth 1 -print -quit | grep -q .; then
		fail 'scanner left temporary files behind'
	fi
}

init_repo() {
	repo=$1
	mkdir -p "$repo"
	git -C "$repo" init -q
	git -C "$repo" config user.email test@example.invalid
	git -C "$repo" config user.name 'Sensitive Content Test'
}

run_expect_pass() {
	label=$1
	shift
	output="$test_root/output"
	if ! PATH="$test_bin:$PATH" TMPDIR="$isolated_tmp" "$scanner" "$@" >"$output" 2>&1; then
		printf '%s\n' "unexpected scanner failure: $label" >&2
		sed -n '1,80p' "$output" >&2
		exit 1
	fi
	assert_tmp_empty
}

run_expect_fail() {
	label=$1
	shift
	output="$test_root/output"
	if PATH="$test_bin:$PATH" TMPDIR="$isolated_tmp" "$scanner" "$@" >"$output" 2>&1; then
		fail "scanner unexpectedly passed: $label"
	fi
	assert_tmp_empty
}

assert_rule_only_output() {
	expected_path=$1
	if ! awk -F '\t' -v expected="$expected_path" '
		NF != 2 || $1 !~ /^[a-z0-9-]+$/ || $2 != expected { exit 1 }
		END { if (NR == 0) exit 1 }
	' "$test_root/output"; then
		fail 'scanner output must contain only rule and path'
	fi
}

assert_all_fixture_rules() {
	for rule in ios-udid-modern ios-udid-legacy-context ios-unique-device-id ios-serial-number private-key access-token private-domain local-users-path; do
		if ! awk -F '\t' -v expected="$rule" '$1 == expected { found=1 } END { exit !found }' "$test_root/output"; then
			fail "fixture did not trigger rule: $rule"
		fi
	done
}

assert_output_omits_fixture() {
	while IFS= read -r fixture_line; do
		[ -n "$fixture_line" ] || continue
		if grep -F "$fixture_line" "$test_root/output" >/dev/null 2>&1; then
			fail 'scanner output disclosed fixture content'
		fi
	done <"$fixture"
}

no_rename_calls=$(grep -Ec 'git .*diff --cached --no-renames ' "$scanner" || true)
[ "$no_rename_calls" -ge 2 ] || fail 'staged discovery and content reads must both disable rename detection'

# Directory mode: only the exact fixture path and exact content are excepted.
directory_root="$test_root/directory"
mkdir -p "$directory_root/$(dirname "$fixture_rel")"
cp "$fixture" "$directory_root/$fixture_rel"
run_expect_pass 'exact fixture exception in directory mode' --directory "$directory_root"

cp "$fixture" "$directory_root/copied-fixture.txt"
run_expect_fail 'same fixture content at a different path' --directory "$directory_root"
assert_rule_only_output 'copied-fixture.txt'
assert_all_fixture_rules
rm "$directory_root/copied-fixture.txt"

sed '1s/^m/n/' "$fixture" >"$directory_root/changed-fixture.txt"
mv "$directory_root/changed-fixture.txt" "$directory_root/$fixture_rel"
run_expect_fail 'one-byte fixture change invalidates exception' --directory "$directory_root"
assert_rule_only_output "$fixture_rel"
cp "$fixture" "$directory_root/$fixture_rel"

sed -n '1p' "$fixture" >>"$directory_root/$fixture_rel"
run_expect_fail 'appended second match invalidates fixture exception' --directory "$directory_root"
assert_rule_only_output "$fixture_rel"

# NUL-bearing content cannot be safely treated as text and must fail closed in
# every mode unless a future exact path + full SHA-256 review exception exists.
binary_directory="$test_root/binary-directory"
mkdir -p "$binary_directory"
printf 'safe\000binary\n' >"$binary_directory/binary.dat"
run_expect_fail 'directory mode rejects NUL content' --directory "$binary_directory"
assert_rule_only_output 'binary.dat'

binary_staged_repo="$test_root/binary-staged"
init_repo "$binary_staged_repo"
printf 'safe\000binary\n' >"$binary_staged_repo/binary.dat"
git -C "$binary_staged_repo" add binary.dat
(
	cd "$binary_staged_repo"
	run_expect_fail 'staged mode rejects NUL content' --staged
)
assert_rule_only_output 'binary.dat'

binary_public_repo="$test_root/binary-public"
init_repo "$binary_public_repo"
printf 'safe\000binary\n' >"$binary_public_repo/binary.dat"
printf 'include\t%s\tbinary must be reviewed by digest\n' 'binary.dat' >"$binary_public_repo/public-files.txt"
git -C "$binary_public_repo" add binary.dat public-files.txt
(
	cd "$binary_public_repo"
	run_expect_fail 'public index rejects NUL content' --public-index public-files.txt
)
assert_rule_only_output 'binary.dat'

# Every index blob must be a regular stage-0 file before it is read. In
# particular, Git symlinks (mode 120000) are rejected without reading or
# disclosing either a harmless or sensitive target. Executable regular files
# (mode 100755) remain supported in every scope.
mode_directory="$test_root/mode-directory"
mkdir -p "$mode_directory"
printf '#!/bin/sh\nprintf safe\\n\n' >"$mode_directory/safe-executable.sh"
chmod 755 "$mode_directory/safe-executable.sh"
run_expect_pass 'directory mode accepts a regular executable' --directory "$mode_directory"
printf 'ordinary text\n' >"$mode_directory/safe-target.txt"
ln -s safe-target.txt "$mode_directory/safe-link"
run_expect_fail 'directory mode rejects a harmless symlink' --directory "$mode_directory"
assert_rule_only_output 'safe-link'
rm "$mode_directory/safe-link"
ln -s "$fixture" "$mode_directory/sensitive-link"
run_expect_fail 'directory mode rejects a sensitive symlink without following it' --directory "$mode_directory"
assert_rule_only_output 'sensitive-link'
assert_output_omits_fixture

mode_staged_repo="$test_root/mode-staged"
init_repo "$mode_staged_repo"
printf '#!/bin/sh\nprintf safe\\n\n' >"$mode_staged_repo/safe-executable.sh"
chmod 755 "$mode_staged_repo/safe-executable.sh"
git -C "$mode_staged_repo" add safe-executable.sh
(
	cd "$mode_staged_repo"
	run_expect_pass 'staged mode accepts index mode 100755' --staged
)
git -C "$mode_staged_repo" commit -qm baseline
printf 'ordinary text\n' >"$mode_staged_repo/safe-target.txt"
ln -s safe-target.txt "$mode_staged_repo/safe-link"
git -C "$mode_staged_repo" add safe-link
(
	cd "$mode_staged_repo"
	run_expect_fail 'staged mode rejects a harmless index symlink' --staged
)
assert_rule_only_output 'safe-link'
git -C "$mode_staged_repo" reset -q safe-link
rm "$mode_staged_repo/safe-link"
ln -s "$fixture" "$mode_staged_repo/sensitive-link"
git -C "$mode_staged_repo" add sensitive-link
(
	cd "$mode_staged_repo"
	run_expect_fail 'staged mode rejects a sensitive index symlink without following it' --staged
)
assert_rule_only_output 'sensitive-link'
assert_output_omits_fixture
git -C "$mode_staged_repo" reset -q sensitive-link
rm "$mode_staged_repo/sensitive-link"
gitlink_oid=$(git -C "$mode_staged_repo" rev-parse HEAD)
git -C "$mode_staged_repo" update-index --add --cacheinfo "160000,$gitlink_oid,gitlink"
(
	cd "$mode_staged_repo"
	run_expect_fail 'staged mode rejects an index gitlink' --staged
)
assert_rule_only_output 'gitlink'

mode_public_repo="$test_root/mode-public"
init_repo "$mode_public_repo"
printf '#!/bin/sh\nprintf safe\\n\n' >"$mode_public_repo/safe-executable.sh"
chmod 755 "$mode_public_repo/safe-executable.sh"
printf 'include\t%s\tregular executable\n' safe-executable.sh >"$mode_public_repo/public-files.txt"
git -C "$mode_public_repo" add safe-executable.sh public-files.txt
(
	cd "$mode_public_repo"
	run_expect_pass 'public index accepts index mode 100755' --public-index public-files.txt
)
git -C "$mode_public_repo" commit -qm baseline
printf 'ordinary text\n' >"$mode_public_repo/safe-target.txt"
ln -s safe-target.txt "$mode_public_repo/safe-link"
printf 'include\t%s\tsymlink must fail\n' safe-link >"$mode_public_repo/public-files.txt"
git -C "$mode_public_repo" add safe-link public-files.txt
(
	cd "$mode_public_repo"
	run_expect_fail 'public index rejects a harmless index symlink' --public-index public-files.txt
)
assert_rule_only_output 'safe-link'
ln -s "$fixture" "$mode_public_repo/sensitive-link"
printf 'include\t%s\tsymlink must fail\n' sensitive-link >"$mode_public_repo/public-files.txt"
git -C "$mode_public_repo" add sensitive-link public-files.txt
(
	cd "$mode_public_repo"
	run_expect_fail 'public index rejects a sensitive index symlink without following it' --public-index public-files.txt
)
assert_rule_only_output 'sensitive-link'
assert_output_omits_fixture
gitlink_oid=$(git -C "$mode_public_repo" rev-parse HEAD)
git -C "$mode_public_repo" update-index --add --cacheinfo "160000,$gitlink_oid,gitlink"
printf 'include\t%s\tgitlink must fail\n' gitlink >"$mode_public_repo/public-files.txt"
git -C "$mode_public_repo" add public-files.txt
(
	cd "$mode_public_repo"
	run_expect_fail 'public index rejects an index gitlink' --public-index public-files.txt
)
assert_rule_only_output 'gitlink'

mode_manifest_repo="$test_root/mode-manifest"
init_repo "$mode_manifest_repo"
printf 'ordinary text\n' >"$mode_manifest_repo/safe.txt"
printf 'include\t%s\tordinary file\n' safe.txt >"$mode_manifest_repo/real-manifest.txt"
ln -s real-manifest.txt "$mode_manifest_repo/public-files.txt"
git -C "$mode_manifest_repo" add safe.txt real-manifest.txt public-files.txt
(
	cd "$mode_manifest_repo"
	run_expect_fail 'public index rejects a symlink manifest before reading it' --public-index public-files.txt
)
assert_rule_only_output 'public-files.txt'

# Supported public image assets are structurally validated and then scanned as
# complete raw bytes by the same rules. This cannot OCR visible text encoded in
# compressed pixels; Task 10 retains visual and metadata review for that layer.
image_source="$test_root/generated-images"
mkdir -p "$test_root/image-go-cache" "$test_root/image-go-tmp"
GOCACHE="$test_root/image-go-cache" GOTMPDIR="$test_root/image-go-tmp" \
	go run "$repo_root/scripts/testdata/sensitive-content/generate_test_images.go" "$image_source" "$fixture"

invalid_images="$test_root/invalid-images"
mkdir -p "$invalid_images"
cp "$image_source/safe.png" "$invalid_images/renamed-png.dat"
cp "$image_source/safe.ico" "$invalid_images/renamed-ico.dat"
printf 'not a png payload\n' >"$invalid_images/fake.png"
printf 'not an ico payload\n' >"$invalid_images/fake.ico"
dd if="$image_source/safe.png" of="$invalid_images/truncated.png" bs=1 count=24 2>/dev/null
dd if="$image_source/safe.ico" of="$invalid_images/truncated.ico" bs=1 count=12 2>/dev/null

image_directory="$test_root/image-directory"
mkdir -p "$image_directory"
cp "$image_source/safe.png" "$image_directory/safe.png"
cp "$image_source/safe.ico" "$image_directory/safe.ico"
run_expect_pass 'directory mode accepts validated PNG and ICO' --directory "$image_directory"
cp "$image_source/sensitive.png" "$image_directory/sensitive.png"
run_expect_fail 'directory mode scans complete PNG bytes' --directory "$image_directory"
assert_rule_only_output 'sensitive.png'
rm "$image_directory/sensitive.png"
cp "$image_source/sensitive.ico" "$image_directory/sensitive.ico"
run_expect_fail 'directory mode scans complete ICO bytes' --directory "$image_directory"
assert_rule_only_output 'sensitive.ico'
rm "$image_directory/sensitive.ico"

for invalid_name in renamed-png.dat renamed-ico.dat fake.png fake.ico truncated.png truncated.ico; do
	cp "$invalid_images/$invalid_name" "$image_directory/$invalid_name"
	run_expect_fail "directory mode rejects malformed image $invalid_name" --directory "$image_directory"
	assert_rule_only_output "$invalid_name"
	rm "$image_directory/$invalid_name"
done

image_staged_repo="$test_root/image-staged"
init_repo "$image_staged_repo"
cp "$image_source/safe.png" "$image_staged_repo/safe.png"
cp "$image_source/safe.ico" "$image_staged_repo/safe.ico"
git -C "$image_staged_repo" add safe.png safe.ico
(
	cd "$image_staged_repo"
	run_expect_pass 'staged mode accepts validated PNG and ICO' --staged
)
cp "$image_source/sensitive.png" "$image_staged_repo/sensitive.png"
git -C "$image_staged_repo" add sensitive.png
(
	cd "$image_staged_repo"
	run_expect_fail 'staged mode scans complete PNG bytes' --staged
)
assert_rule_only_output 'sensitive.png'
git -C "$image_staged_repo" reset -q sensitive.png
rm "$image_staged_repo/sensitive.png"
cp "$image_source/sensitive.ico" "$image_staged_repo/sensitive.ico"
git -C "$image_staged_repo" add sensitive.ico
(
	cd "$image_staged_repo"
	run_expect_fail 'staged mode scans complete ICO bytes' --staged
)
assert_rule_only_output 'sensitive.ico'
git -C "$image_staged_repo" reset -q sensitive.ico
rm "$image_staged_repo/sensitive.ico"
for invalid_name in renamed-png.dat renamed-ico.dat fake.png fake.ico truncated.png truncated.ico; do
	cp "$invalid_images/$invalid_name" "$image_staged_repo/$invalid_name"
	git -C "$image_staged_repo" add "$invalid_name"
	(
		cd "$image_staged_repo"
		run_expect_fail "staged mode rejects malformed image $invalid_name" --staged
	)
	assert_rule_only_output "$invalid_name"
	git -C "$image_staged_repo" reset -q "$invalid_name"
	rm "$image_staged_repo/$invalid_name"
done

image_public_repo="$test_root/image-public"
init_repo "$image_public_repo"
cp "$image_source/safe.png" "$image_public_repo/safe.png"
cp "$image_source/safe.ico" "$image_public_repo/safe.ico"
printf 'include\t%s\tvalidated image\ninclude\t%s\tvalidated image\n' safe.png safe.ico >"$image_public_repo/public-files.txt"
git -C "$image_public_repo" add safe.png safe.ico public-files.txt
(
	cd "$image_public_repo"
	run_expect_pass 'public index accepts validated PNG and ICO' --public-index public-files.txt
)
cp "$image_source/sensitive.png" "$image_public_repo/sensitive.png"
printf 'include\t%s\tscanned image\n' sensitive.png >"$image_public_repo/public-files.txt"
git -C "$image_public_repo" add sensitive.png public-files.txt
(
	cd "$image_public_repo"
	run_expect_fail 'public index scans complete PNG bytes' --public-index public-files.txt
)
assert_rule_only_output 'sensitive.png'
cp "$image_source/sensitive.ico" "$image_public_repo/sensitive.ico"
printf 'include\t%s\tscanned image\n' sensitive.ico >"$image_public_repo/public-files.txt"
git -C "$image_public_repo" add sensitive.ico public-files.txt
(
	cd "$image_public_repo"
	run_expect_fail 'public index scans complete ICO bytes' --public-index public-files.txt
)
assert_rule_only_output 'sensitive.ico'
for invalid_name in renamed-png.dat renamed-ico.dat fake.png fake.ico truncated.png truncated.ico; do
	cp "$invalid_images/$invalid_name" "$image_public_repo/$invalid_name"
	printf 'include\t%s\tmalformed image must fail closed\n' "$invalid_name" >"$image_public_repo/public-files.txt"
	git -C "$image_public_repo" add "$invalid_name" public-files.txt
	(
		cd "$image_public_repo"
		run_expect_fail "public index rejects malformed image $invalid_name" --public-index public-files.txt
	)
	assert_rule_only_output "$invalid_name"
done

# Directory traversal passes each path as an argv item. Spaces are valid;
# control-character names and every symlink are rejected without raw controls.
path_directory="$test_root/path-directory"
mkdir -p "$path_directory"
printf 'ordinary text\n' >"$path_directory/name with spaces.txt"
run_expect_pass 'directory mode handles spaces as one path' --directory "$path_directory"

newline=$(printf '\n_')
newline=${newline%_}
newline_name="line${newline}break.txt"
printf 'ordinary text\n' >"$path_directory/$newline_name"
run_expect_fail 'directory mode safely rejects newline paths' --directory "$path_directory"
assert_rule_only_output '[control-character-path]'
rm "$path_directory/$newline_name"

escape=$(printf '\033')
control_name="escape${escape}name.txt"
printf 'ordinary text\n' >"$path_directory/$control_name"
run_expect_fail 'directory mode safely rejects control-character paths' --directory "$path_directory"
assert_rule_only_output '[control-character-path]'
rm "$path_directory/$control_name"

printf 'ordinary text\n' >"$path_directory/target.txt"
ln -s target.txt "$path_directory/internal-link"
run_expect_fail 'directory mode rejects internal symlinks' --directory "$path_directory"
assert_rule_only_output 'internal-link'
rm "$path_directory/internal-link"

printf 'ordinary text\n' >"$test_root/outside-target.txt"
ln -s "$test_root/outside-target.txt" "$path_directory/outside-link"
run_expect_fail 'directory mode rejects escaping symlinks' --directory "$path_directory"
assert_rule_only_output 'outside-link'
rm "$path_directory/outside-link"

# Project-private legacy source and registry hosts are an exact denylist.
private_domain_directory="$test_root/private-domain-directory"
mkdir -p "$private_domain_directory"
private_git_domain=$(printf '%s.%s.%s' git isw app)
printf 'source=https://%s/project\n' "$private_git_domain" >"$private_domain_directory/source.txt"
run_expect_fail 'project private Git domain' --directory "$private_domain_directory"
assert_rule_only_output 'source.txt'
if ! awk -F '\t' '$1 == "private-project-domain" { found=1 } END { exit !found }' "$test_root/output"; then
	fail 'private Git domain did not trigger its exact rule'
fi

private_hub_domain=$(printf '%s.%s.%s' hub isw app)
printf 'image=%s/project/image\n' "$private_hub_domain" >"$private_domain_directory/registry.txt"
rm "$private_domain_directory/source.txt"
run_expect_fail 'project private registry domain' --directory "$private_domain_directory"
assert_rule_only_output 'registry.txt'
if ! awk -F '\t' '$1 == "private-project-domain" { found=1 } END { exit !found }' "$test_root/output"; then
	fail 'private registry domain did not trigger its exact rule'
fi

# Staged mode scans only added lines, not untouched private repository files.
staged_repo="$test_root/staged"
init_repo "$staged_repo"
sed -n '1p' "$fixture" >"$staged_repo/AGENTS.md"
mkdir -p "$staged_repo/.cursor"
sed -n '2p' "$fixture" >"$staged_repo/.cursor/private-rule.md"
git -C "$staged_repo" add AGENTS.md
git -C "$staged_repo" add .cursor/private-rule.md
git -C "$staged_repo" commit -qm baseline
printf 'ordinary text\n' >"$staged_repo/safe.txt"
git -C "$staged_repo" add safe.txt
(
	cd "$staged_repo"
	run_expect_pass 'staged mode ignores untouched private files' --staged
)
git -C "$staged_repo" reset -q
mkdir -p "$staged_repo/$(dirname "$fixture_rel")"
cp "$fixture" "$staged_repo/$fixture_rel"
git -C "$staged_repo" add "$fixture_rel"
(
	cd "$staged_repo"
	run_expect_pass 'exact staged fixture exception' --staged
)
cp "$fixture" "$staged_repo/copied-fixture.txt"
git -C "$staged_repo" add copied-fixture.txt
(
	cd "$staged_repo"
	run_expect_fail 'staged content at another path' --staged
)
assert_rule_only_output 'copied-fixture.txt'
assert_all_fixture_rules

# Pure renames must be treated as a delete plus a complete added target. Git's
# default rename presentation contains no added hunk, so the scanner must
# explicitly disable rename detection for both discovery and content reading.
rename_sensitive_repo="$test_root/rename-sensitive"
init_repo "$rename_sensitive_repo"
sed -n '1p' "$fixture" >"$rename_sensitive_repo/original.txt"
git -C "$rename_sensitive_repo" add original.txt
git -C "$rename_sensitive_repo" commit -qm baseline
git -C "$rename_sensitive_repo" mv original.txt renamed.txt
(
	cd "$rename_sensitive_repo"
	run_expect_fail 'staged mode scans complete sensitive pure-rename target' --staged
)
assert_rule_only_output 'renamed.txt'
assert_output_omits_fixture

rename_safe_repo="$test_root/rename-safe"
init_repo "$rename_safe_repo"
printf '#!/bin/sh\nprintf safe\\n\n' >"$rename_safe_repo/original.sh"
chmod 755 "$rename_safe_repo/original.sh"
git -C "$rename_safe_repo" add original.sh
git -C "$rename_safe_repo" commit -qm baseline
git -C "$rename_safe_repo" mv original.sh renamed.sh
(
	cd "$rename_safe_repo"
	run_expect_pass 'staged mode accepts safe pure rename with index mode 100755' --staged
)

rename_binary_repo="$test_root/rename-binary"
init_repo "$rename_binary_repo"
printf 'safe\000binary\n' >"$rename_binary_repo/original.dat"
git -C "$rename_binary_repo" add original.dat
git -C "$rename_binary_repo" commit -qm baseline
git -C "$rename_binary_repo" mv original.dat renamed.dat
(
	cd "$rename_binary_repo"
	run_expect_fail 'staged mode rejects binary pure-rename target' --staged
)
assert_rule_only_output 'renamed.dat'

rename_image_repo="$test_root/rename-image"
init_repo "$rename_image_repo"
cp "$image_source/sensitive.png" "$rename_image_repo/original.png"
git -C "$rename_image_repo" add original.png
git -C "$rename_image_repo" commit -qm baseline
git -C "$rename_image_repo" mv original.png renamed.png
(
	cd "$rename_image_repo"
	run_expect_fail 'staged mode scans complete image pure-rename target' --staged
)
assert_rule_only_output 'renamed.png'
assert_output_omits_fixture

delete_only_repo="$test_root/delete-only"
init_repo "$delete_only_repo"
sed -n '1p' "$fixture" >"$delete_only_repo/deleted-sensitive.txt"
git -C "$delete_only_repo" add deleted-sensitive.txt
git -C "$delete_only_repo" commit -qm baseline
git -C "$delete_only_repo" rm -q deleted-sensitive.txt
(
	cd "$delete_only_repo"
	run_expect_pass 'staged mode does not scan a deleted source' --staged
)

# An ordinary commit SHA without device-field context must not match.
ordinary_repo="$test_root/ordinary"
init_repo "$ordinary_repo"
sed -n '9p' "$fixture" >"$ordinary_repo/commit.txt"
git -C "$ordinary_repo" add commit.txt
(
	cd "$ordinary_repo"
	run_expect_pass 'ordinary commit SHA' --staged
)

nongit_root="$test_root/not-a-repository"
mkdir -p "$nongit_root"
(
	cd "$nongit_root"
	run_expect_fail 'staged mode fails closed outside a Git repository' --staged
)

# The public manifest is itself release input and must come from the index.
manifest_repo="$test_root/index-manifest"
init_repo "$manifest_repo"
printf 'ordinary text\n' >"$manifest_repo/safe.txt"
sed -n '1p' "$fixture" >"$manifest_repo/private.txt"
printf 'include\t%s\tindexed selection\n' 'safe.txt' >"$manifest_repo/public-files.txt"
git -C "$manifest_repo" add safe.txt private.txt public-files.txt
printf 'include\t%s\tunstaged selection must be ignored\n' 'private.txt' >"$manifest_repo/public-files.txt"
(
	cd "$manifest_repo"
	run_expect_pass 'public manifest is read from the index' --public-index public-files.txt
)

untracked_manifest_repo="$test_root/untracked-manifest"
init_repo "$untracked_manifest_repo"
printf 'ordinary text\n' >"$untracked_manifest_repo/safe.txt"
git -C "$untracked_manifest_repo" add safe.txt
printf 'include\t%s\tuntracked manifest must fail\n' 'safe.txt' >"$untracked_manifest_repo/public-files.txt"
(
	cd "$untracked_manifest_repo"
	run_expect_fail 'public manifest must exist in the index' --public-index public-files.txt
)

# Git C-quotes control-character paths in line-oriented output. A committed
# safe alias equal to that display string must never substitute for the real
# staged/index path; reject the ambiguous path before reading either blob.
assert_ambiguous_git_path_rejected() {
	case_name=$1
	actual_name=$2
	ambiguous_repo="$test_root/ambiguous-$case_name"
	init_repo "$ambiguous_repo"
	printf 'ordinary text\n' >"$ambiguous_repo/$actual_name"
	git -C "$ambiguous_repo" add -- "$actual_name"
	quoted_display=$(git -c core.quotepath=false -C "$ambiguous_repo" diff --cached --name-only -- "$actual_name")
	case "$quoted_display" in
	\"*) ;;
	*) fail "$case_name setup did not produce a Git C-quoted path" ;;
	esac
	git -C "$ambiguous_repo" reset -q -- "$actual_name"
	rm "$ambiguous_repo/$actual_name"
	printf 'ordinary alias\n' >"$ambiguous_repo/$quoted_display"
	git -C "$ambiguous_repo" add -- "$quoted_display"
	git -C "$ambiguous_repo" commit -qm 'safe quoted alias'

	sed -n '1p' "$fixture" >"$ambiguous_repo/$actual_name"
	git -C "$ambiguous_repo" add -- "$actual_name"
	printf 'ordinary worktree text\n' >"$ambiguous_repo/$actual_name"
	(
		cd "$ambiguous_repo"
		run_expect_fail "staged rejects ambiguous $case_name path" --staged
	)
	assert_rule_only_output '[git-quoted-path]'
	assert_output_omits_fixture

	printf 'include\t%s*\tambiguous path must fail\n' "$case_name" >"$ambiguous_repo/public-files.txt"
	git -C "$ambiguous_repo" add public-files.txt
	printf 'exclude\t*\tworking tree must be ignored\n' >"$ambiguous_repo/public-files.txt"
	(
		cd "$ambiguous_repo"
		run_expect_fail "public index rejects ambiguous $case_name path" --public-index public-files.txt
	)
	assert_rule_only_output '[git-quoted-path]'
	assert_output_omits_fixture
}

git_newline=$(printf '\n_')
git_newline=${git_newline%_}
assert_ambiguous_git_path_rejected newline "newline${git_newline}secret.txt"
git_escape=$(printf '\033')
assert_ambiguous_git_path_rejected control "control${git_escape}secret.txt"

# Ordinary spaces and UTF-8 remain unquoted under core.quotepath=false and are
# still scanned from the index rather than being broadly rejected.
assert_regular_git_path_scanned() {
	case_name=$1
	regular_name=$2
	regular_repo="$test_root/regular-$case_name"
	init_repo "$regular_repo"
	sed -n '1p' "$fixture" >"$regular_repo/$regular_name"
	git -C "$regular_repo" add -- "$regular_name"
	printf 'ordinary worktree text\n' >"$regular_repo/$regular_name"
	(
		cd "$regular_repo"
		run_expect_fail "staged scans regular $case_name path" --staged
	)
	assert_rule_only_output "$regular_name"

	printf 'include\t%s\tregular path\n' "$regular_name" >"$regular_repo/public-files.txt"
	git -C "$regular_repo" add public-files.txt
	printf 'exclude\t*\tworking tree must be ignored\n' >"$regular_repo/public-files.txt"
	(
		cd "$regular_repo"
		run_expect_fail "public index scans regular $case_name path" --public-index public-files.txt
	)
	assert_rule_only_output "$regular_name"
}

assert_regular_git_path_scanned spaces 'name with spaces.txt'
assert_regular_git_path_scanned utf8 '中文文件.txt'

# Public-index mode reads only allowlisted index blobs, never working-tree data.
public_repo="$test_root/public-index"
init_repo "$public_repo"
mkdir -p "$public_repo/$(dirname "$fixture_rel")"
cp "$fixture" "$public_repo/$fixture_rel"
sed -n '1p' "$fixture" >"$public_repo/private.txt"
printf 'include\t%s\ttrusted fixed fixture\n' "$fixture_rel" >"$public_repo/public-files.txt"
git -C "$public_repo" add "$fixture_rel" private.txt public-files.txt
(
	cd "$public_repo"
	run_expect_pass 'public index exact fixture exception' --public-index public-files.txt
)
cp "$fixture" "$public_repo/copied-fixture.txt"
printf 'include\t%s\tunexcepted copy\n' 'copied-fixture.txt' >"$public_repo/public-files.txt"
git -C "$public_repo" add copied-fixture.txt public-files.txt
(
	cd "$public_repo"
	run_expect_fail 'public index scans allowlisted index blob' --public-index public-files.txt
)
assert_rule_only_output 'copied-fixture.txt'
assert_all_fixture_rules

printf 'include\t%s\tpath traversal must fail closed\n' '../outside.txt' >"$public_repo/public-files.txt"
git -C "$public_repo" add public-files.txt
(
	cd "$public_repo"
	run_expect_fail 'public index rejects traversal outside the repository' --public-index public-files.txt
)

for magic_pattern in ':!README.md' ':^README.md' ':/README.md' ':(exclude)README.md'; do
	printf 'include\t%s\tGit pathspec magic must fail closed\n' "$magic_pattern" >"$public_repo/public-files.txt"
	git -C "$public_repo" add public-files.txt
	(
		cd "$public_repo"
		run_expect_fail 'public index rejects Git pathspec magic' --public-index public-files.txt
	)
	assert_rule_only_output '[manifest-pattern]'
done

printf 'include\t%s\tmissing index blob must fail closed\n' 'missing-index-blob.txt' >"$public_repo/public-files.txt"
git -C "$public_repo" add public-files.txt
(
	cd "$public_repo"
	run_expect_fail 'public index rejects an allowlist entry without an index blob' --public-index public-files.txt
)

# Signal cleanup: pause the shared matcher after each mode has prepared its
# input, then signal the live scanner process. Every mode must clean its temp.
signal_repo="$test_root/signal-repository"
init_repo "$signal_repo"
cp "$fixture" "$signal_repo/signal-fixture.txt"
printf 'include\t%s\tsignal cleanup fixture\n' 'signal-fixture.txt' >"$signal_repo/public-files.txt"
git -C "$signal_repo" add signal-fixture.txt public-files.txt

signal_bin="$test_root/signal-bin"
signal_go_cache="$test_root/signal-go-cache"
signal_go_tmp="$test_root/signal-go-tmp"
mkdir -p "$signal_bin" "$signal_go_cache" "$signal_go_tmp"
printf '%s\n' \
	'package main' \
	'import (' \
	'  "os"' \
	'  "os/signal"' \
	'  "syscall"' \
	')' \
	'func main() {' \
	'  if len(os.Args) < 2 { os.Exit(2) }' \
	'  signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)' \
	'  if err := syscall.Exec(os.Args[1], os.Args[1:], os.Environ()); err != nil { os.Exit(127) }' \
	'}' >"$test_root/signal-launcher.go"
GOCACHE="$signal_go_cache" GOTMPDIR="$signal_go_tmp" go build -o "$signal_bin/launch-signal" "$test_root/signal-launcher.go"

printf '%s\n' \
	'#!/bin/sh' \
	': >"$SCANNER_SIGNAL_READY"' \
	'while [ ! -f "$SCANNER_SIGNAL_RELEASE" ]; do sleep 0.02; done' \
	'exec "$SCANNER_REAL_MATCHER" "$@"' >"$signal_bin/pause-matcher"
chmod +x "$signal_bin/pause-matcher"

assert_signal_cleanup() {
	signal_name=$1
	expected_status=$2
	mode_label=$3
	shift 3
	ready="$test_root/signal-$mode_label-$signal_name.ready"
	release="$test_root/signal-$mode_label-$signal_name.release"
	signal_output="$test_root/signal-$mode_label-$signal_name.output"
	rm -f "$ready" "$release" "$signal_output"
	(
		cd "$signal_repo"
		export PATH="$signal_bin:$test_bin:$PATH"
		export TMPDIR="$isolated_tmp"
		export SCANNER_SIGNAL_READY="$ready"
		export SCANNER_SIGNAL_RELEASE="$release"
		export SCANNER_REAL_MATCHER="$test_bin/sensitive-content-matcher"
		export IOSBK_SENSITIVE_MATCHER="$signal_bin/pause-matcher"
		# Async POSIX-shell jobs inherit SIGINT as ignored. The tiny stdlib-only
		# launcher makes the signals catchable; exec then restores their default
		# dispositions before the scanner installs its traps, preserving the PID.
		exec "$signal_bin/launch-signal" "$scanner" "$@"
	) >"$signal_output" 2>&1 &
	scanner_pid=$!

	attempt=0
	while [ ! -f "$ready" ]; do
		if ! kill -0 "$scanner_pid" 2>/dev/null; then
			wait "$scanner_pid" || true
			fail "scanner exited before $signal_name readiness"
		fi
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 250 ]; then
			kill -TERM "$scanner_pid" 2>/dev/null || true
			wait "$scanner_pid" || true
			fail "scanner did not reach $signal_name readiness"
		fi
		sleep 0.02
	done

	kill -s "$signal_name" "$scanner_pid"
	: >"$release"
	if wait "$scanner_pid"; then
		status=0
	else
		status=$?
	fi
	if [ "$status" -ne "$expected_status" ]; then
		fail "$signal_name exit status was $status, expected $expected_status"
	fi
	assert_tmp_empty
	while IFS= read -r fixture_line; do
		[ -n "$fixture_line" ] || continue
		if grep -F "$fixture_line" "$signal_output" >/dev/null 2>&1; then
			fail "$signal_name output disclosed fixture content"
		fi
	done <"$fixture"
}

for mode in public-index staged directory; do
	case "$mode" in
	public-index) set -- --public-index public-files.txt ;;
	staged) set -- --staged ;;
	directory) set -- --directory "$signal_repo" ;;
	esac
	assert_signal_cleanup HUP 129 "$mode" "$@"
	assert_signal_cleanup INT 130 "$mode" "$@"
	assert_signal_cleanup TERM 143 "$mode" "$@"
done

printf 'PASS: sensitive content scanner modes and fixture exception\n'
