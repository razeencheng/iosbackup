#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rules_file="$script_dir/sensitive-content-patterns.txt"
exceptions_file="$script_dir/sensitive-content-fixture-exceptions.tsv"
matcher_source="$script_dir/sensitive_content_matcher.go"

usage() {
	printf 'usage: %s (--staged | --public-index <manifest> | --directory <root>)\n' "$0" >&2
	exit 2
}

if [ ! -f "$rules_file" ] || [ ! -f "$exceptions_file" ] || [ ! -f "$matcher_source" ]; then
	printf 'sensitive-content scanner configuration is missing\n' >&2
	exit 2
fi

scan_tmp=''
cleanup() {
	if [ -n "$scan_tmp" ] && [ -d "$scan_tmp" ]; then
		rm -rf "$scan_tmp"
	fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
scan_tmp=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-sensitive-content.XXXXXX") || exit 2
if [ -n "${IOSBK_SENSITIVE_MATCHER:-}" ]; then
	if [ ! -x "$IOSBK_SENSITIVE_MATCHER" ]; then
		printf 'prebuilt sensitive-content matcher is unavailable\n' >&2
		exit 2
	fi
	matcher=$IOSBK_SENSITIVE_MATCHER
else
	mkdir -p "$scan_tmp/go-cache" "$scan_tmp/go-tmp"
	if ! GOCACHE="$scan_tmp/go-cache" GOTMPDIR="$scan_tmp/go-tmp" \
		go build -trimpath -o "$scan_tmp/matcher" "$matcher_source"; then
		printf 'unable to build sensitive-content matcher\n' >&2
		exit 2
	fi
	matcher="$scan_tmp/matcher"
fi
export IOSBK_SENSITIVE_MATCHER="$matcher"

sha256_file() {
	sha_file=$1
	if command -v sha256sum >/dev/null 2>&1; then
		sha_output=$(sha256sum "$sha_file") || return 2
	elif command -v shasum >/dev/null 2>&1; then
		sha_output=$(shasum -a 256 "$sha_file") || return 2
	else
		printf 'sha256sum or shasum is required\n' >&2
		return 2
	fi
	printf '%s\n' "${sha_output%%[[:space:]]*}"
}

contains_nul() {
	byte_file=$1
	if ! LC_ALL=C od -An -v -t u1 "$byte_file" >"$scan_tmp/bytes"; then
		printf 'unable to inspect scan input bytes\n' >&2
		return 2
	fi
	awk '
		{
			for (i = 1; i <= NF; i++) {
				if ($i == 0) { found=1; exit }
			}
		}
		END { exit found ? 0 : 1 }
	' "$scan_tmp/bytes"
}

require_text_content() {
	report_path=$1
	text_file=$2
	if contains_nul "$text_file"; then
		printf 'binary-content\t%s\n' "$report_path"
		return 1
	else
		byte_status=$?
	fi
	[ "$byte_status" -eq 1 ] || return 2
	return 0
}

is_exception() {
	path=$1
	digest=$2
	awk -F '\t' -v path="$path" -v digest="$digest" '
		$0 !~ /^#/ && NF == 2 && $1 == path && $2 == digest { found=1 }
		END { exit !found }
	' "$exceptions_file"
}

scan_content() {
	scan_path=$1
	content_file=$2
	"$matcher" --scan-text "$rules_file" "$scan_path" "$content_file"
}

scan_file() {
	scan_path=$1
	scan_file_path=$2
	digest=$(sha256_file "$scan_file_path") || return $?
	if is_exception "$scan_path" "$digest"; then
		return 0
	fi
	"$matcher" --scan-file "$rules_file" "$scan_path" "$scan_file_path"
}

require_regular_index_mode() {
	index_path=$1
	if ! git -c core.quotepath=false ls-files --stage -- ":(literal)$index_path" >"$scan_tmp/index-entry"; then
		printf 'unable to inspect index mode\n' >&2
		return 2
	fi
	entry_count=$(wc -l <"$scan_tmp/index-entry" | tr -d ' ')
	if [ "$entry_count" -ne 1 ]; then
		printf 'unsupported-index-mode\t%s\n' "$index_path"
		return 1
	fi
	index_mode=$(awk 'NR == 1 { print $1 }' "$scan_tmp/index-entry")
	index_stage=$(awk 'NR == 1 { print $3 }' "$scan_tmp/index-entry")
	case "$index_mode:$index_stage" in
	100644:0|100755:0) return 0 ;;
	*)
		printf 'unsupported-index-mode\t%s\n' "$index_path"
		return 1
		;;
	esac
}

scan_index_blob() {
	scan_path=$1
	if require_regular_index_mode "$scan_path"; then
		:
	else
		return $?
	fi
	if ! git show ":$scan_path" >"$scan_tmp/blob"; then
		printf 'unable to read public index blob: %s\n' "$scan_path" >&2
		return 2
	fi
	scan_file "$scan_path" "$scan_tmp/blob"
}

scan_staged_path() {
	scan_path=$1
	if require_regular_index_mode "$scan_path"; then
		:
	else
		return $?
	fi
	if ! git show ":$scan_path" >"$scan_tmp/blob"; then
		printf 'unable to read staged index blob: %s\n' "$scan_path" >&2
		return 2
	fi
	digest=$(sha256_file "$scan_tmp/blob") || return $?
	if is_exception "$scan_path" "$digest"; then
		return 0
	fi
	case "$scan_path" in
	*.png|*.PNG|*.ico|*.ICO)
		"$matcher" --scan-file "$rules_file" "$scan_path" "$scan_tmp/blob"
		return $?
		;;
	esac
	if require_text_content "$scan_path" "$scan_tmp/blob"; then
		:
	else
		return $?
	fi
	if ! git diff --cached --no-renames --no-ext-diff --no-color --unified=0 -- ":(literal)$scan_path" >"$scan_tmp/diff"; then
		printf 'unable to read staged diff: %s\n' "$scan_path" >&2
		return 2
	fi
	if ! awk '
			/^diff --git / { in_hunk=0; next }
			/^@@ / { in_hunk=1; next }
			in_hunk && /^\+/ { print substr($0, 2) }
		' "$scan_tmp/diff" >"$scan_tmp/content"; then
		printf 'unable to extract staged additions\n' >&2
		return 2
	fi
	scan_content "$scan_path" "$scan_tmp/content"
}

reject_quoted_git_paths() {
	path_list=$1
	while IFS= read -r listed_path; do
		case "$listed_path" in
		\"*)
			printf 'unsafe-path\t[git-quoted-path]\n'
			return 1
			;;
		esac
	done <"$path_list"
	return 0
}

scan_staged() {
	if ! git -c core.quotepath=false diff --cached --no-renames --name-only --diff-filter=ACMR -- >"$scan_tmp/paths"; then
		printf 'unable to enumerate staged files\n' >&2
		return 2
	fi
	if ! reject_quoted_git_paths "$scan_tmp/paths"; then
		return 1
	fi
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		if scan_staged_path "$path"; then
			:
		else
			return $?
		fi
	done <"$scan_tmp/paths"
}

reject_public_pattern() {
	pattern=$1
	case "$pattern" in
		''|:*|/*|..|../*|*/../*|*/..|*'//'*) return 1 ;;
	esac
	return 0
}

scan_public_index() {
	manifest=$1
	if require_regular_index_mode "$manifest"; then
		:
	else
		return $?
	fi
	if ! git show ":$manifest" >"$scan_tmp/manifest"; then
		printf 'unable to read public allowlist from index\n' >&2
		return 2
	fi
	if require_text_content "$manifest" "$scan_tmp/manifest"; then
		:
	else
		return $?
	fi
	tab=$(printf '\t')
	while IFS="$tab" read -r action pattern reason; do
		case "$action" in
		''|'#'*) continue ;;
		include)
			if ! reject_public_pattern "$pattern"; then
				printf 'unsafe-public-pattern\t[manifest-pattern]\n'
				return 1
			fi
			if ! git -c core.quotepath=false ls-files -- "$pattern" >"$scan_tmp/paths"; then
				printf 'unable to enumerate public index files\n' >&2
				return 2
			fi
			if ! reject_quoted_git_paths "$scan_tmp/paths"; then
				return 1
			fi
			if [ ! -s "$scan_tmp/paths" ]; then
				printf 'public allowlist entry has no index blob\n' >&2
				return 2
			fi
			while IFS= read -r path; do
				[ -n "$path" ] || continue
				if scan_index_blob "$path"; then
					:
				else
					return $?
				fi
			done <"$scan_tmp/paths"
			;;
		exclude)
			if ! reject_public_pattern "$pattern"; then
				printf 'unsafe-public-pattern\t[manifest-pattern]\n'
				return 1
			fi
			;;
		*)
			printf 'invalid public allowlist action\n' >&2
			return 2
			;;
		esac
	done <"$scan_tmp/manifest"
}

scan_directory() {
	root=${1%/}
	[ -n "$root" ] || root=/
	[ -d "$root" ] || {
		printf 'scan directory not found\n' >&2
		return 2
	}
	if [ -L "$root" ]; then
		printf 'symlink\t.\n'
		return 1
	fi
	"$matcher" --scan-directory "$rules_file" "$exceptions_file" "$root"
}

[ "$#" -ge 1 ] || usage
case "$1" in
--staged)
	[ "$#" -eq 1 ] || usage
	scan_staged
	;;
--public-index)
	[ "$#" -eq 2 ] || usage
	scan_public_index "$2"
	;;
--directory)
	[ "$#" -eq 2 ] || usage
	scan_directory "$2"
	;;
*) usage ;;
esac
