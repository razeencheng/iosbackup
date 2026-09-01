#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
reader="$script_dir/read_release_manifest.sh"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-manifest-test.XXXXXX") || exit 2
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

expect_fail() {
	label=$1
	manifest=$2
	if "$reader" "$manifest" >"$test_root/output" 2>&1; then
		fail "$label unexpectedly passed"
	fi
}

expect_source_fail() {
	label=$1
	source_value=$2
	manifest="$test_root/source-$label.env"
	printf 'IOSBK_VERSION=%s\nIOSBK_BUILD_DATE=%s\nIOSBK_SOURCE_URL=%s\n' "$valid_version" "$valid_date" "$source_value" >"$manifest"
	expect_fail "unsafe source URL: $label" "$manifest"
}

valid="$test_root/valid.env"
cp "$source_root/release/manifest.env" "$valid"
if ! "$reader" "$valid" >"$test_root/actual"; then
	fail 'valid manifest was rejected'
fi
if ! cmp -s "$valid" "$test_root/actual"; then
	fail 'validated manifest output is not deterministic canonical data'
fi
valid_version=$("$reader" "$valid" IOSBK_VERSION) || fail 'valid version lookup failed'
valid_date=$("$reader" "$valid" IOSBK_BUILD_DATE) || fail 'valid build-date lookup failed'
valid_source=$("$reader" "$valid" IOSBK_SOURCE_URL) || fail 'valid source lookup failed'
for key in IOSBK_VERSION IOSBK_BUILD_DATE IOSBK_SOURCE_URL; do
	value=$("$reader" "$valid" "$key") || fail "valid key lookup failed: $key"
	expected_value=$(sed -n "s/^$key=//p" "$valid")
	[ -n "$value" ] && [ "$value" = "$expected_value" ] || fail "valid key lookup did not preserve the manifest value: $key"
done
if "$reader" "$valid" IOSBK_UNKNOWN >"$test_root/output" 2>&1; then
	fail 'unknown requested key unexpectedly passed'
fi

printf 'IOSBK_VERSION=%s\nIOSBK_BUILD_DATE=%s\nIOSBK_SOURCE_URL=%s\n' \
	"$valid_version" "$valid_date" 'https://github.com/example-org/repository_name.2' >"$test_root/alternate-github.env"
if ! "$reader" "$test_root/alternate-github.env" >/dev/null; then
	fail 'safe GitHub owner/repository URL was rejected'
fi

injection_marker="$test_root/injection-ran"
printf 'IOSBK_VERSION=%s\nIOSBK_BUILD_DATE=%s\nIOSBK_SOURCE_URL=$(touch %s)\n' "$valid_version" "$valid_date" "$injection_marker" >"$test_root/dollar-command.env"
expect_fail 'dollar command substitution' "$test_root/dollar-command.env"
[ ! -e "$injection_marker" ] || fail 'manifest command substitution was executed'

printf 'IOSBK_VERSION=%s\nIOSBK_BUILD_DATE=%s\nIOSBK_SOURCE_URL=`touch %s`\n' "$valid_version" "$valid_date" "$injection_marker" >"$test_root/backtick.env"
expect_fail 'backtick command substitution' "$test_root/backtick.env"
[ ! -e "$injection_marker" ] || fail 'manifest backtick substitution was executed'

printf '%s\n' \
	"IOSBK_VERSION=$valid_version;touch-marker" \
	"IOSBK_BUILD_DATE=$valid_date" \
	"IOSBK_SOURCE_URL=$valid_source" >"$test_root/semicolon.env"
expect_fail 'semicolon' "$test_root/semicolon.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" \
	"IOSBK_SOURCE_URL=$valid_source" >"$test_root/duplicate.env"
expect_fail 'duplicate key' "$test_root/duplicate.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" \
	"IOSBK_SOURCE_URL=$valid_source" \
	'IOSBK_COMMIT=deadbeef' >"$test_root/unknown.env"
expect_fail 'unknown key' "$test_root/unknown.env"

printf 'IOSBK_VERSION=%s\r\nIOSBK_BUILD_DATE=%s\r\nIOSBK_SOURCE_URL=%s\r\n' "$valid_version" "$valid_date" "$valid_source" >"$test_root/crlf.env"
expect_fail 'CRLF' "$test_root/crlf.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" >"$test_root/missing.env"
expect_fail 'missing key' "$test_root/missing.env"

printf '%s\n' \
	"IOSBK_VERSION =$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" \
	"IOSBK_SOURCE_URL=$valid_source" >"$test_root/key-whitespace.env"
expect_fail 'key whitespace' "$test_root/key-whitespace.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	'IOSBK_BUILD_DATE=2026-02-30' \
	"IOSBK_SOURCE_URL=$valid_source" >"$test_root/invalid-date.env"
expect_fail 'invalid calendar date' "$test_root/invalid-date.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" \
	'IOSBK_SOURCE_URL=https://example.invalid/other/repository' >"$test_root/wrong-source.env"
expect_fail 'source outside GitHub' "$test_root/wrong-source.env"

printf '%s\n' \
	"IOSBK_VERSION=$valid_version" \
	"IOSBK_BUILD_DATE=$valid_date" \
	"IOSBK_SOURCE_URL=$valid_source#fragment" >"$test_root/source-fragment.env"
expect_fail 'source fragment' "$test_root/source-fragment.env"

expect_source_fail empty-owner 'https://github.com//repository'
expect_source_fail empty-repository 'https://github.com/example/'
expect_source_fail extra-path 'https://github.com/example/repository/extra'
expect_source_fail owner-leading-hyphen 'https://github.com/-example/repository'
expect_source_fail owner-trailing-hyphen 'https://github.com/example-/repository'
expect_source_fail dot-repository 'https://github.com/example/.'
expect_source_fail dotdot-repository 'https://github.com/example/..'
expect_source_fail git-suffix 'https://github.com/example/repository.git'
expect_source_fail query 'https://github.com/example/repository?tab=readme'
expect_source_fail userinfo 'https://user@github.com/example/repository'
expect_source_fail port 'https://github.com:443/example/repository'
expect_source_fail semicolon 'https://github.com/example/repository;command'
expect_source_fail double-quote 'https://github.com/example/repository"quoted'
expect_source_fail single-quote "https://github.com/example/repository'quoted"
expect_source_fail backslash 'https://github.com/example/repository\path'
expect_source_fail whitespace 'https://github.com/example/repository name'
expect_source_fail encoded-newline 'https://github.com/example/repository%0Aname'

ln -s valid.env "$test_root/symlink.env"
expect_fail 'symlink manifest' "$test_root/symlink.env"

echo 'release manifest parser tests passed'
