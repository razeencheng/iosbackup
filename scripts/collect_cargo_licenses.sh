#!/usr/bin/env bash
set -euo pipefail

fail() {
	echo "Cargo license collection failed: $1" >&2
	exit 1
}

usage() {
	echo "usage: $0 --metadata FILE --lockfile FILE --inventory FILE --fallback-registry FILE --fallback-root DIR --canonical-license-map FILE --source-root DIR --cargo-root DIR --expected-count N --expected-metadata-only-fallback-count N --output DIR" >&2
	exit 2
}

sha256_file() {
	local file=$1 output
	if command -v sha256sum >/dev/null 2>&1; then
		output=$(sha256sum "$file") || fail "unable to hash input"
	elif command -v shasum >/dev/null 2>&1; then
		output=$(shasum -a 256 "$file") || fail "unable to hash input"
	else
		fail 'sha256sum or shasum is required'
	fi
	printf '%s\n' "${output%%[[:space:]]*}"
}

canonical_directory() {
	local path=$1
	[[ -d "$path" && ! -L "$path" ]] || fail 'a controlled root is missing or is a symlink'
	(cd "$path" && pwd -P) || fail 'unable to resolve a controlled root'
}

path_is_under() {
	local path=$1 root=$2
	[[ "$path" == "$root" || "$path" == "$root"/* ]]
}

assert_no_symlink_components() {
	local path=$1 root=$2 relative component current
	path_is_under "$path" "$root" || fail 'path escapes its controlled root'
	relative=${path#"$root"}
	relative=${relative#/}
	current=$root
	while [[ -n "$relative" ]]; do
		component=${relative%%/*}
		if [[ "$relative" == */* ]]; then
			relative=${relative#*/}
		else
			relative=''
		fi
		[[ -n "$component" && "$component" != . && "$component" != .. ]] || fail 'path contains an unsafe component'
		current="$current/$component"
		[[ ! -L "$current" ]] || fail 'symlinks are not accepted in package attribution paths'
	done
}

metadata=''
lockfile=''
inventory=''
fallback_registry=''
fallback_root_input=''
canonical_license_map=''
source_root_input=''
cargo_root_input=''
expected_count=''
expected_metadata_only_fallback_count=''
output=''
while [[ $# -gt 0 ]]; do
	case "$1" in
		--metadata) [[ $# -ge 2 ]] || usage; metadata=$2; shift 2 ;;
		--lockfile) [[ $# -ge 2 ]] || usage; lockfile=$2; shift 2 ;;
		--inventory) [[ $# -ge 2 ]] || usage; inventory=$2; shift 2 ;;
		--fallback-registry) [[ $# -ge 2 ]] || usage; fallback_registry=$2; shift 2 ;;
		--fallback-root) [[ $# -ge 2 ]] || usage; fallback_root_input=$2; shift 2 ;;
		--canonical-license-map) [[ $# -ge 2 ]] || usage; canonical_license_map=$2; shift 2 ;;
		--source-root) [[ $# -ge 2 ]] || usage; source_root_input=$2; shift 2 ;;
		--cargo-root) [[ $# -ge 2 ]] || usage; cargo_root_input=$2; shift 2 ;;
		--expected-count) [[ $# -ge 2 ]] || usage; expected_count=$2; shift 2 ;;
		--expected-metadata-only-fallback-count) [[ $# -ge 2 ]] || usage; expected_metadata_only_fallback_count=$2; shift 2 ;;
		--output) [[ $# -ge 2 ]] || usage; output=$2; shift 2 ;;
		*) usage ;;
	esac
done

for command_name in jq find sort cp mv; do
	command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done
[[ "$expected_count" =~ ^[1-9][0-9]*$ ]] || fail 'expected package count must be a positive integer'
[[ "$expected_metadata_only_fallback_count" =~ ^[0-9]+$ ]] || fail 'expected metadata-only fallback count must be a non-negative integer'
for input in "$metadata" "$lockfile" "$inventory" "$fallback_registry" "$canonical_license_map"; do
	[[ -f "$input" && ! -L "$input" && -s "$input" ]] || fail 'an input file is missing, empty, or a symlink'
done
[[ -n "$source_root_input" && -n "$cargo_root_input" && -n "$fallback_root_input" && -n "$output" ]] || usage

source_root=$(canonical_directory "$source_root_input")
cargo_root=$(canonical_directory "$cargo_root_input")
fallback_root=$(canonical_directory "$fallback_root_input")
registry_root="$cargo_root/registry/src"
git_root="$cargo_root/git/checkouts"
[[ ! -e "$output" && ! -L "$output" ]] || fail 'output path already exists'
output_parent=$(dirname "$output")
[[ -d "$output_parent" && ! -L "$output_parent" ]] || fail 'output parent is missing or is a symlink'
output_parent=$(canonical_directory "$output_parent")
output="$output_parent/$(basename "$output")"

work_root=$(mktemp -d "$output_parent/.cargo-license-collector.XXXXXX") || fail 'unable to create temporary output'
cleanup() {
	if [[ -n "${work_root:-}" && -d "$work_root" ]]; then
		rm -rf "$work_root"
	fi
}
trap cleanup EXIT HUP INT TERM
mkdir -p "$work_root/packages"

if ! awk -F '\t' '
    /^#/ || /^[[:space:]]*$/ { next }
    NF != 14 || ($1 != "vcs-root" && $1 != "metadata-only") ||
        $2 == "" || $3 == "" || $4 !~ /^registry[+]https:\/\// ||
        $5 !~ /^[0-9a-f]+$/ || length($5) != 40 || $6 == "" || $7 == "" || $8 == "" ||
        $9 !~ /^https:\/\// || $10 !~ /^[0-9a-f]+$/ || length($10) != 64 ||
        $11 !~ /^[0-9a-f]+$/ || length($11) != 64 ||
        $12 !~ /^[0-9a-f]+$/ || length($12) != 64 { exit 1 }
    $1 == "vcs-root" && ($13 !~ /^third_party\/cargo-license-fallbacks\// || $14 != "-") { exit 1 }
    $1 == "metadata-only" && ($13 != "-" || $14 !~ /^[A-Za-z0-9.+-]+$/) { exit 1 }
    seen[$2 SUBSEP $3 SUBSEP $4]++ { exit 1 }
' "$fallback_registry"; then
	fail 'fallback registry is malformed or ambiguous'
fi

if ! jq -e --argjson expected "$expected_count" '
  (.packages | type == "array" and length == $expected) and
  (.resolve | type == "object") and
  (.resolve.nodes | type == "array" and length == $expected) and
  ([.packages[].id] | length == (unique | length)) and
  ([.resolve.nodes[].id] | length == (unique | length)) and
  ([.packages[] | [.name, .version, (.source // "path")]] | length == (unique | length)) and
  all(.packages[];
    (.name | type == "string" and length > 0) and
    (.version | type == "string" and length > 0) and
    (.id | type == "string" and length > 0) and
    (.license | type == "string" and length > 0) and
    (.manifest_path | type == "string" and length > 0) and
    ((.license_file == null) or (.license_file | type == "string" and length > 0)) and
    ((.source == null) or (.source | type == "string" and (startswith("registry+https://") or startswith("git+https://"))))) and
  (([.resolve.nodes[].id] - [.packages[].id]) | length == 0) and
  (([.packages[].id] - [.resolve.nodes[].id]) | length == 0)
' "$metadata" >/dev/null; then
	fail 'Cargo metadata is incomplete, ambiguous, or does not match the expected package count'
fi

jq -r '.packages[] | [.name, .version, .license, (.source // "path"), .manifest_path, (.license_file // "")] | @tsv' \
	"$metadata" | LC_ALL=C sort >"$work_root/metadata-input.tsv"
awk -F '\t' 'NF != 6 || $1 == "" || $2 == "" || $3 == "" || $4 == "" || $5 == "" { exit 1 }' \
	"$work_root/metadata-input.tsv" || fail 'Cargo metadata contains an unsafe package record'
awk -F '\t' '!/^#/ && NF { if (NF != 4 || $1 == "" || $2 == "" || $3 == "" || $4 == "") exit 1; print $1 "\t" $2 "\t" $3 "\t" $4 }' \
	"$inventory" | LC_ALL=C sort >"$work_root/inventory-input.tsv" || fail 'locked Cargo inventory is malformed'
[[ "$(wc -l <"$work_root/inventory-input.tsv" | tr -d ' ')" == "$expected_count" ]] || fail 'locked Cargo inventory package count does not match metadata'
cut -f1-4 "$work_root/metadata-input.tsv" >"$work_root/metadata-identity.tsv"
cmp -s "$work_root/metadata-identity.tsv" "$work_root/inventory-input.tsv" || fail 'Cargo metadata does not match the locked inventory'

printf '# sequence\tname\tversion\tdeclared_license\tsource\tmaterial_files\n' >"$work_root/packages.tsv"
: >"$work_root/missing-materials.tsv"
: >"$work_root/fallback-used.tsv"

sequence=0
native_material_package_count=0
pinned_upstream_material_count=0
metadata_only_fallback_count=0
while IFS=$'\t' read -r name version declared_license source manifest_path license_file; do
	sequence=$((sequence + 1))
	fallback_mode=''
	fallback_material_manifest=''
	metadata_authors=''
	metadata_repository=''
	vcs_commit=''
	vcs_path=''
	case "$manifest_path" in *$'\n'*|*$'\r'*|*$'\t'*|*/../*|*/..) fail 'manifest path contains unsafe characters or traversal' ;; esac
	[[ "$manifest_path" == /* && "$(basename "$manifest_path")" == Cargo.toml ]] || fail 'package manifest path is not an absolute Cargo.toml path'

	case "$source" in
		path) boundary=$source_root ;;
		registry+https://*) [[ -d "$registry_root" ]] || fail 'registry package root is missing'; boundary=$(canonical_directory "$registry_root") ;;
		git+https://*) [[ -d "$git_root" ]] || fail 'Git package root is missing'; boundary=$(canonical_directory "$git_root") ;;
		*) fail 'package source is not an approved Cargo source' ;;
	esac
	path_is_under "$manifest_path" "$boundary" || fail 'package manifest path escapes its controlled source root'
	assert_no_symlink_components "$manifest_path" "$boundary"
	[[ -f "$manifest_path" && ! -L "$manifest_path" && -s "$manifest_path" ]] || fail 'package manifest is missing, empty, or a symlink'
	manifest_directory=$(canonical_directory "$(dirname "$manifest_path")")
	[[ "$manifest_path" == "$manifest_directory/Cargo.toml" ]] || fail 'package manifest path is not canonical'

	material_list="$work_root/materials-$sequence.tsv"
	: >"$material_list"
	while IFS= read -r -d '' candidate; do
		base=$(basename "$candidate")
		case "$base" in *$'\n'*|*$'\r'*|*$'\t'*|*';'*) fail 'attribution filename contains unsafe delimiters' ;; esac
		upper=$(printf '%s' "$base" | tr '[:lower:]' '[:upper:]')
		case "$upper" in
			LICENSE*|LICENCE*|COPYING*|NOTICE*|COPYRIGHT*|AUTHORS*)
				[[ -f "$candidate" && ! -L "$candidate" && -s "$candidate" ]] || fail 'package attribution material is empty, invalid, or a symlink'
				assert_no_symlink_components "$candidate" "$boundary"
				printf '%s\tpackage\n' "$candidate" >>"$material_list"
				;;
		esac
	done < <(find "$manifest_directory" -mindepth 1 -maxdepth 1 -print0)

	if [[ -n "$license_file" ]]; then
		case "$license_file" in *$'\n'*|*$'\r'*|*$'\t'*|*/../*|*/..) fail 'metadata license file contains unsafe characters or traversal' ;; esac
		[[ "$license_file" == /* ]] || license_file="$manifest_directory/$license_file"
		path_is_under "$license_file" "$boundary" || fail 'metadata license file escapes its controlled source root'
		assert_no_symlink_components "$license_file" "$boundary"
		[[ -f "$license_file" && ! -L "$license_file" && -s "$license_file" ]] || fail 'metadata license file is missing, empty, or a symlink'
		license_directory=$(canonical_directory "$(dirname "$license_file")")
		license_file="$license_directory/$(basename "$license_file")"
		if ! awk -F '\t' -v path="$license_file" '$1 == path { found=1 } END { exit !found }' "$material_list"; then
			printf '%s\tmetadata\n' "$license_file" >>"$material_list"
		fi
	fi

	if [[ ! -s "$material_list" ]]; then
		fallback=''
		if [[ "$source" == path && -e "$source_root/.git" && ! -L "$source_root/.git" ]]; then
			fallback=$source_root
		elif [[ "$source" == git+https://* ]]; then
			cursor=$manifest_directory
			while path_is_under "$cursor" "$boundary"; do
				if [[ -e "$cursor/.git" && ! -L "$cursor/.git" ]]; then fallback=$cursor; break; fi
				[[ "$cursor" != "$boundary" ]] || break
				cursor=$(dirname "$cursor")
			done
		fi
		if [[ -n "$fallback" ]]; then
		while IFS= read -r -d '' candidate; do
			base=$(basename "$candidate")
			case "$base" in *$'\n'*|*$'\r'*|*$'\t'*|*';'*) fail 'workspace attribution filename contains unsafe delimiters' ;; esac
			upper=$(printf '%s' "$base" | tr '[:lower:]' '[:upper:]')
			case "$upper" in
				LICENSE*|LICENCE*|COPYING*|NOTICE*|COPYRIGHT*|AUTHORS*)
					[[ -f "$candidate" && ! -L "$candidate" && -s "$candidate" ]] || fail 'workspace attribution material is empty, invalid, or a symlink'
					assert_no_symlink_components "$candidate" "$boundary"
					printf '%s\tworkspace-root\n' "$candidate" >>"$material_list"
					;;
			esac
		done < <(find "$fallback" -mindepth 1 -maxdepth 1 -print0)
		fi
	fi

	if [[ ! -s "$material_list" ]]; then
		fallback_matches=$(awk -F '\t' -v name="$name" -v version="$version" -v source="$source" \
			'!/^#/ && NF && $2 == name && $3 == version && $4 == source { print }' "$fallback_registry")
		if [[ -z "$fallback_matches" || "$(printf '%s\n' "$fallback_matches" | wc -l | tr -d ' ')" != 1 ]]; then
			printf '%s\t%s\t%s\n' "$name" "$version" "$source" >>"$work_root/missing-materials.tsv"
			continue
		fi
		IFS=$'\t' read -r fallback_mode fallback_name fallback_version fallback_source \
			vcs_commit vcs_path fallback_license fallback_authors fallback_repository \
			cargo_toml_sha readme_sha vcs_info_sha fallback_material_manifest canonical_license <<<"$fallback_matches"
		[[ "$fallback_name" == "$name" && "$fallback_version" == "$version" && "$fallback_source" == "$source" && "$fallback_license" == "$declared_license" ]] || \
			fail 'fallback identity does not match Cargo metadata'
		metadata_authors=$(jq -er --arg name "$name" --arg version "$version" --arg source "$source" \
			'.packages[] | select(.name == $name and .version == $version and .source == $source) | .authors | select(type == "array" and length > 0) | join("; ")' "$metadata")
		metadata_repository=$(jq -er --arg name "$name" --arg version "$version" --arg source "$source" \
			'.packages[] | select(.name == $name and .version == $version and .source == $source) | .repository | select(type == "string" and startswith("https://"))' "$metadata")
		[[ "$metadata_authors" == "$fallback_authors" && "$metadata_repository" == "$fallback_repository" ]] || \
			fail 'fallback authors or repository do not match Cargo metadata'
		for evidence_name in Cargo.toml README.md .cargo_vcs_info.json; do
			evidence_path="$manifest_directory/$evidence_name"
			assert_no_symlink_components "$evidence_path" "$boundary"
			[[ -f "$evidence_path" && ! -L "$evidence_path" && -s "$evidence_path" ]] || fail 'fallback package evidence is missing, empty, or a symlink'
		done
		[[ "$(sha256_file "$manifest_directory/Cargo.toml")" == "$cargo_toml_sha" ]] || fail 'fallback Cargo.toml digest mismatch'
		[[ "$(sha256_file "$manifest_directory/README.md")" == "$readme_sha" ]] || fail 'fallback README digest mismatch'
		[[ "$(sha256_file "$manifest_directory/.cargo_vcs_info.json")" == "$vcs_info_sha" ]] || fail 'fallback VCS evidence digest mismatch'
		actual_vcs_commit=$(jq -er '.git.sha1 | select(type == "string" and test("^[0-9a-f]{40}$"))' "$manifest_directory/.cargo_vcs_info.json")
		actual_vcs_path=$(jq -er '.path_in_vcs | select(type == "string")' "$manifest_directory/.cargo_vcs_info.json")
		[[ "$vcs_path" != . ]] || vcs_path=''
		[[ "$actual_vcs_commit" == "$vcs_commit" && "$actual_vcs_path" == "$vcs_path" ]] || fail 'fallback VCS commit or workspace path mismatch'
		printf '%s\t%s\t%s\t%s\n' "$fallback_mode" "$name" "$version" "$source" >>"$work_root/fallback-used.tsv"

		case "$fallback_mode" in
			vcs-root)
				case "$fallback_material_manifest" in third_party/cargo-license-fallbacks/*/materials.tsv) ;; *) fail 'pinned upstream material manifest path is unsafe' ;; esac
				fallback_material_manifest="$fallback_root/$fallback_material_manifest"
				path_is_under "$fallback_material_manifest" "$fallback_root" || fail 'pinned upstream material manifest escapes its root'
				assert_no_symlink_components "$fallback_material_manifest" "$fallback_root"
				[[ -f "$fallback_material_manifest" && ! -L "$fallback_material_manifest" && -s "$fallback_material_manifest" ]] || fail 'pinned upstream material manifest is missing, empty, or a symlink'
				while IFS=$'\t' read -r fallback_file fallback_sha fallback_url upstream_sha normalization; do
					case "$fallback_file" in ''|'#'*) continue ;; *[!A-Za-z0-9._+-]*|*/*) fail 'pinned upstream material filename is unsafe' ;; esac
					[[ "$fallback_sha" =~ ^[0-9a-f]{64}$ && "$upstream_sha" == "$fallback_sha" && "$normalization" == none ]] || fail 'pinned upstream material digest metadata is invalid'
					[[ "$fallback_url" == https://*"/$vcs_commit/"* ]] || fail 'pinned upstream material URL does not contain the locked commit'
					fallback_file_path="$(dirname "$fallback_material_manifest")/$fallback_file"
					path_is_under "$fallback_file_path" "$fallback_root" || fail 'pinned upstream material escapes its root'
					assert_no_symlink_components "$fallback_file_path" "$fallback_root"
					[[ -f "$fallback_file_path" && ! -L "$fallback_file_path" && -s "$fallback_file_path" ]] || fail 'pinned upstream material is missing, empty, or a symlink'
					[[ "$(sha256_file "$fallback_file_path")" == "$fallback_sha" ]] || fail 'pinned upstream material digest mismatch'
					printf '%s\tpinned-upstream-material\n' "$fallback_file_path" >>"$material_list"
				done <"$fallback_material_manifest"
				[[ -s "$material_list" ]] || fail 'pinned upstream material manifest is empty'
				pinned_upstream_material_count=$((pinned_upstream_material_count + 1))
				;;
			metadata-only)
				[[ "$name" == plist-macro && "$version" == 0.1.6 && "$vcs_commit" == d1d48559ddc8e9bd263f36180bbe1d4f2a3d55e5 ]] || fail 'metadata-only fallback is not the single audited plist-macro exception'
				[[ "$metadata_only_fallback_count" == 0 ]] || fail 'more than one metadata-only fallback is not allowed'
				canonical_row=$(awk -F '\t' -v identifier="$canonical_license" '!/^#/ && NF && $1 == identifier { print }' "$canonical_license_map")
				[[ -n "$canonical_row" && "$(printf '%s\n' "$canonical_row" | wc -l | tr -d ' ')" == 1 ]] || fail 'metadata-only fallback canonical license mapping is missing or ambiguous'
				IFS=$'\t' read -r mapped_identifier mapped_kind mapped_path mapped_sha mapped_source <<<"$canonical_row"
				[[ "$mapped_identifier" == "$canonical_license" && "$mapped_kind" == license && "$mapped_source" == https://* ]] || fail 'metadata-only fallback canonical mapping is invalid'
				canonical_path="$fallback_root/$mapped_path"
				path_is_under "$canonical_path" "$fallback_root" || fail 'canonical license path escapes its root'
				assert_no_symlink_components "$canonical_path" "$fallback_root"
				[[ -f "$canonical_path" && ! -L "$canonical_path" && -s "$canonical_path" ]] || fail 'canonical license text is missing, empty, or a symlink'
				[[ "$(sha256_file "$canonical_path")" == "$mapped_sha" ]] || fail 'canonical license text digest mismatch'
				printf '%s\tcanonical-spdx\n' "$canonical_path" >>"$material_list"
				metadata_only_fallback_count=$((metadata_only_fallback_count + 1))
				;;
			*) fail 'unsupported fallback mode' ;;
		esac
	else
		native_material_package_count=$((native_material_package_count + 1))
	fi

	LC_ALL=C sort -u "$material_list" >"$material_list.sorted"
	package_output=$(printf '%s/packages/%04d' "$work_root" "$sequence")
	mkdir -p "$package_output"
	material_names=''
	while IFS=$'\t' read -r material provenance; do
		base=$(basename "$material")
		[[ ! -e "$package_output/$base" ]] || fail "package $name $version has ambiguous attribution filenames"
		cp "$material" "$package_output/$base"
		chmod 0644 "$package_output/$base"
		if [[ -n "$material_names" ]]; then material_names="$material_names;"; fi
		material_names="$material_names$base@$provenance"
	done <"$material_list.sorted"
	if [[ -n "$fallback_mode" ]]; then
		cp "$manifest_directory/Cargo.toml" "$package_output/Cargo.toml"
		cp "$manifest_directory/README.md" "$package_output/README.md"
		cp "$manifest_directory/.cargo_vcs_info.json" "$package_output/.cargo_vcs_info.json"
		if [[ "$fallback_mode" == vcs-root ]]; then
			cp "$fallback_material_manifest" "$package_output/pinned-upstream-materials.tsv"
		fi
		if [[ "$fallback_mode" == vcs-root ]]; then
			provenance_name=pinned_upstream_material
		else
			provenance_name=metadata_only_fallback
		fi
		{
			printf 'material_provenance=%s\n' "$provenance_name"
			printf 'package_name=%s\npackage_version=%s\npackage_source=%s\n' "$name" "$version" "$source"
			printf 'declared_license=%s\nauthors=%s\nrepository=%s\n' "$declared_license" "$metadata_authors" "$metadata_repository"
			printf 'vcs_commit=%s\npath_in_vcs=%s\n' "$vcs_commit" "$vcs_path"
		} >"$package_output/PROVENANCE.env"
		if [[ "$fallback_mode" == metadata-only ]]; then
			cat >"$package_output/NOTICE" <<'EOF'
The upstream plist-macro 0.1.6 crate package and repository at the locked commit did not ship a standalone license file. Attribution here is based on the package-declared MIT metadata and the canonical SPDX MIT text. The canonical text is not presented as an upstream-authored file.
EOF
		fi
		chmod 0644 "$package_output/Cargo.toml" "$package_output/README.md" "$package_output/.cargo_vcs_info.json" "$package_output/PROVENANCE.env"
		[[ ! -e "$package_output/NOTICE" ]] || chmod 0644 "$package_output/NOTICE"
		[[ ! -e "$package_output/pinned-upstream-materials.tsv" ]] || chmod 0644 "$package_output/pinned-upstream-materials.tsv"
	fi
	printf '%04d\t%s\t%s\t%s\t%s\t%s\n' "$sequence" "$name" "$version" "$declared_license" "$source" "$material_names" >>"$work_root/packages.tsv"
done <"$work_root/metadata-input.tsv"
[[ "$sequence" == "$expected_count" ]] || fail 'collected package count does not match the lock'
if [[ -s "$work_root/missing-materials.tsv" ]]; then
	while IFS=$'\t' read -r missing_name missing_version missing_source; do
		echo "Cargo license material missing: $missing_name $missing_version ($missing_source)" >&2
	done <"$work_root/missing-materials.tsv"
	fail 'one or more packages have no distributed attribution material'
fi
fallback_registry_count=$(awk -F '\t' '!/^#/ && NF { count++ } END { print count+0 }' "$fallback_registry")
fallback_used_count=$(wc -l <"$work_root/fallback-used.tsv" | tr -d ' ')
[[ "$fallback_used_count" == "$fallback_registry_count" ]] || fail 'fallback registry contains an unused or missing package identity'
[[ "$metadata_only_fallback_count" == "$expected_metadata_only_fallback_count" ]] || fail 'metadata-only fallback count does not match the locked expectation'
[[ "$((native_material_package_count + pinned_upstream_material_count + metadata_only_fallback_count))" == "$expected_count" ]] || \
	fail 'material provenance counts do not cover the resolved package graph'
rm -f \
	"$work_root/metadata-input.tsv" \
	"$work_root/metadata-identity.tsv" \
	"$work_root/inventory-input.tsv" \
	"$work_root/missing-materials.tsv" \
	"$work_root/fallback-used.tsv" \
	"$work_root"/materials-*.tsv \
	"$work_root"/materials-*.tsv.sorted

jq --argjson expected "$expected_count" '{
  format_version: 1,
  resolved_package_count: $expected,
  packages: [.packages[] | {
    name, version, authors, license, repository, source: (.source // "path"),
    license_file: (if .license_file == null then null else (.license_file | split("/") | last) end)
  }] | sort_by(.name, .version, .source)
}' "$metadata" >"$work_root/metadata-packages.json"
cp "$lockfile" "$work_root/Cargo.lock"
cp "$inventory" "$work_root/locked-license-inventory.tsv"
chmod 0644 "$work_root/Cargo.lock" "$work_root/locked-license-inventory.tsv" "$work_root/metadata-packages.json" "$work_root/packages.tsv"
lock_sha=$(sha256_file "$lockfile")
inventory_sha=$(sha256_file "$inventory")
{
	printf 'collector_format=2\n'
	printf 'metadata_command=cargo metadata --locked --format-version 1\n'
	printf 'resolved_package_count=%s\n' "$expected_count"
	printf 'native_material_package_count=%s\n' "$native_material_package_count"
	printf 'pinned_upstream_material_count=%s\n' "$pinned_upstream_material_count"
	printf 'metadata_only_fallback_count=%s\n' "$metadata_only_fallback_count"
	printf 'cargo_lock_sha256=%s\n' "$lock_sha"
	printf 'locked_inventory_sha256=%s\n' "$inventory_sha"
} >"$work_root/EVIDENCE.env"
chmod 0644 "$work_root/EVIDENCE.env"

: >"$work_root/SHA256SUMS"
while IFS= read -r bundled_file; do
	relative=${bundled_file#"$work_root/"}
	printf '%s  %s\n' "$(sha256_file "$bundled_file")" "$relative" >>"$work_root/SHA256SUMS"
done < <(find "$work_root" -type f ! -name SHA256SUMS | LC_ALL=C sort)
chmod 0644 "$work_root/SHA256SUMS"
find "$work_root" -type d -exec chmod 0755 {} +

mv "$work_root" "$output"
work_root=''
trap - EXIT HUP INT TERM
echo "Cargo license attribution bundle collected: $expected_count packages; pinned_upstream_material=$pinned_upstream_material_count; metadata_only_fallback=$metadata_only_fallback_count"
