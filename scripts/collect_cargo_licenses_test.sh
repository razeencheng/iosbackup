#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
collector="$script_dir/collect_cargo_licenses.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-cargo-license-collector-test.XXXXXX")
test_root=$(cd "$test_root" && pwd -P)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

sha256_file_for_test() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

write_fixture() {
	case_root=$1
	mkdir -p \
		"$case_root/source/.git" \
		"$case_root/source/crates/root" \
		"$case_root/cargo/registry/src/index.invalid/dep-1.2.3" \
		"$case_root/fallback/third_party/licenses"
	printf '[package]\nname = "netmuxd"\nversion = "0.4.2"\n' >"$case_root/source/crates/root/Cargo.toml"
	printf 'root license material\n' >"$case_root/source/LICENSE"
	printf '[package]\nname = "dep"\nversion = "1.2.3"\n' >"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/Cargo.toml"
	printf 'dependency license material\n' >"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT"
	printf 'dependency notice\n' >"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/NOTICE"
	printf 'alternate spelling license\n' >"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/LICENCE"
	printf 'dependency authors\n' >"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/AUTHORS"
	printf 'locked cargo graph\n' >"$case_root/Cargo.lock"
	printf '# no audited fallback is needed by the baseline fixture\n' >"$case_root/fallback.tsv"
	printf 'canonical MIT fixture\n' >"$case_root/fallback/third_party/licenses/MIT.txt"
	canonical_sha=$(sha256_file_for_test "$case_root/fallback/third_party/licenses/MIT.txt")
	printf 'MIT\tlicense\tthird_party/licenses/MIT.txt\t%s\thttps://spdx.invalid/MIT.txt\n' "$canonical_sha" >"$case_root/canonical-map.tsv"
	cat >"$case_root/inventory.tsv" <<'EOF'
# name	version	declared_license	source
dep	1.2.3	MIT	registry+https://github.com/rust-lang/crates.io-index
netmuxd	0.4.2	LGPL-2.1-only	path
EOF
	cat >"$case_root/metadata.json" <<EOF
{
  "packages": [
    {"name":"netmuxd","version":"0.4.2","id":"path+file://$case_root/source/crates/root#netmuxd@0.4.2","license":"LGPL-2.1-only","license_file":null,"source":null,"manifest_path":"$case_root/source/crates/root/Cargo.toml"},
    {"name":"dep","version":"1.2.3","id":"registry+https://github.com/rust-lang/crates.io-index#dep@1.2.3","license":"MIT","license_file":"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT","source":"registry+https://github.com/rust-lang/crates.io-index","manifest_path":"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/Cargo.toml"}
  ],
  "resolve": {"nodes": [
    {"id":"path+file://$case_root/source/crates/root#netmuxd@0.4.2"},
    {"id":"registry+https://github.com/rust-lang/crates.io-index#dep@1.2.3"}
  ]}
}
EOF
}

run_collector() {
	case_root=$1
	output=$2
	expected_metadata_only=${3:-0}
	expected_packages=${4:-2}
	"$collector" \
		--metadata "$case_root/metadata.json" \
		--lockfile "$case_root/Cargo.lock" \
		--inventory "$case_root/inventory.tsv" \
		--fallback-registry "$case_root/fallback.tsv" \
		--fallback-root "$case_root/fallback" \
		--canonical-license-map "$case_root/canonical-map.tsv" \
		--source-root "$case_root/source" \
		--cargo-root "$case_root/cargo" \
		--expected-count "$expected_packages" \
		--expected-metadata-only-fallback-count "$expected_metadata_only" \
		--output "$output"
}

remove_dependency_materials() {
	case_root=$1
	rm "$case_root/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT" \
		"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/LICENCE" \
		"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/NOTICE" \
		"$case_root/cargo/registry/src/index.invalid/dep-1.2.3/AUTHORS"
}

write_pinned_fallback_fixture() {
	case_root=$1
	write_fixture "$case_root"
	remove_dependency_materials "$case_root"
	package_root="$case_root/cargo/registry/src/index.invalid/dep-1.2.3"
	printf 'dependency readme\n' >"$package_root/README.md"
	printf '{"git":{"sha1":"0123456789abcdef0123456789abcdef01234567"},"path_in_vcs":"crates/dep"}\n' >"$package_root/.cargo_vcs_info.json"
	jq '(.packages[] | select(.name == "dep")) += {authors:["Fixture Author"], repository:"https://example.invalid/dep", license_file:null}' \
		"$case_root/metadata.json" >"$case_root/metadata.tmp"
	mv "$case_root/metadata.tmp" "$case_root/metadata.json"
	mkdir -p "$case_root/fallback/third_party/cargo-license-fallbacks/dep-1.2.3"
	printf 'raw pinned upstream material\n' >"$case_root/fallback/third_party/cargo-license-fallbacks/dep-1.2.3/LICENSE"
	material_sha=$(sha256_file_for_test "$case_root/fallback/third_party/cargo-license-fallbacks/dep-1.2.3/LICENSE")
	printf '# filename\tbundled_sha256\tauthoritative_source\tupstream_blob_sha256\tnormalization\nLICENSE\t%s\thttps://example.invalid/dep/0123456789abcdef0123456789abcdef01234567/LICENSE\t%s\tnone\n' \
		"$material_sha" "$material_sha" >"$case_root/fallback/third_party/cargo-license-fallbacks/dep-1.2.3/materials.tsv"
	cargo_sha=$(sha256_file_for_test "$package_root/Cargo.toml")
	readme_sha=$(sha256_file_for_test "$package_root/README.md")
	vcs_sha=$(sha256_file_for_test "$package_root/.cargo_vcs_info.json")
	printf 'vcs-root\tdep\t1.2.3\tregistry+https://github.com/rust-lang/crates.io-index\t0123456789abcdef0123456789abcdef01234567\tcrates/dep\tMIT\tFixture Author\thttps://example.invalid/dep\t%s\t%s\t%s\tthird_party/cargo-license-fallbacks/dep-1.2.3/materials.tsv\t-\n' \
		"$cargo_sha" "$readme_sha" "$vcs_sha" >"$case_root/fallback.tsv"
}

write_metadata_fallback_fixture() {
	case_root=$1
	write_fixture "$case_root"
	remove_dependency_materials "$case_root"
	package_root="$case_root/cargo/registry/src/index.invalid/dep-1.2.3"
	printf '[package]\nname = "plist-macro"\nversion = "0.1.6"\nlicense = "MIT"\n' >"$package_root/Cargo.toml"
	printf 'plist-macro readme\n' >"$package_root/README.md"
	printf '{"git":{"sha1":"d1d48559ddc8e9bd263f36180bbe1d4f2a3d55e5"},"path_in_vcs":""}\n' >"$package_root/.cargo_vcs_info.json"
	old_id='registry+https://github.com/rust-lang/crates.io-index#dep@1.2.3'
	new_id='registry+https://github.com/rust-lang/crates.io-index#plist-macro@0.1.6'
	jq --arg old_id "$old_id" --arg new_id "$new_id" '
		(.packages[] | select(.name == "dep")) |=
			(.name = "plist-macro" | .version = "0.1.6" | .id = $new_id |
			 .authors = ["Jackson Coxson"] | .repository = "https://github.com/jkcoxson/plist_macro" |
			 .license = "MIT" | .license_file = null) |
		(.resolve.nodes[] | select(.id == $old_id) | .id) = $new_id
	' "$case_root/metadata.json" >"$case_root/metadata.tmp"
	mv "$case_root/metadata.tmp" "$case_root/metadata.json"
	awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } $1 == "dep" { $1="plist-macro"; $2="0.1.6" } { print }' \
		"$case_root/inventory.tsv" >"$case_root/inventory.tmp"
	mv "$case_root/inventory.tmp" "$case_root/inventory.tsv"
	cargo_sha=$(sha256_file_for_test "$package_root/Cargo.toml")
	readme_sha=$(sha256_file_for_test "$package_root/README.md")
	vcs_sha=$(sha256_file_for_test "$package_root/.cargo_vcs_info.json")
	printf 'metadata-only\tplist-macro\t0.1.6\tregistry+https://github.com/rust-lang/crates.io-index\td1d48559ddc8e9bd263f36180bbe1d4f2a3d55e5\t.\tMIT\tJackson Coxson\thttps://github.com/jkcoxson/plist_macro\t%s\t%s\t%s\t-\tMIT\n' \
		"$cargo_sha" "$readme_sha" "$vcs_sha" >"$case_root/fallback.tsv"
}

expect_fail() {
	label=$1
	case_root=$2
	if run_collector "$case_root" "$case_root/output" >"$case_root/result" 2>&1; then
		fail "$label unexpectedly passed"
	fi
}

baseline="$test_root/baseline"
write_fixture "$baseline"
run_collector "$baseline" "$baseline/output-a"
run_collector "$baseline" "$baseline/output-b"
diff -r "$baseline/output-a" "$baseline/output-b" >/dev/null || fail 'collector output is not deterministic'
test -s "$baseline/output-a/packages/0001/LICENSE-MIT" || fail 'dependency license was not collected'
test -s "$baseline/output-a/packages/0001/NOTICE" || fail 'dependency notice was not collected'
test -s "$baseline/output-a/packages/0001/LICENCE" || fail 'alternate-spelling license was not collected'
test -s "$baseline/output-a/packages/0001/AUTHORS" || fail 'authors attribution was not collected'
test -s "$baseline/output-a/packages/0002/LICENSE" || fail 'controlled workspace license fallback was not collected'
if grep -R -F "$baseline" "$baseline/output-a" >/dev/null; then
	leaked=$(grep -R -l -F "$baseline" "$baseline/output-a" | sed -n '1p')
	fail "collector output leaks local builder paths in ${leaked#"$baseline/output-a/"}"
fi

missing="$test_root/missing"
write_fixture "$missing"
remove_dependency_materials "$missing"
expect_fail 'package without attribution material' "$missing"

empty="$test_root/empty"
write_fixture "$empty"
: >"$empty/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT"
expect_fail 'empty attribution material' "$empty"

linked="$test_root/symlink"
write_fixture "$linked"
rm "$linked/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT"
ln -s NOTICE "$linked/cargo/registry/src/index.invalid/dep-1.2.3/LICENSE-MIT"
expect_fail 'symlinked attribution material' "$linked"

escaped="$test_root/escape"
write_fixture "$escaped"
mkdir -p "$escaped/outside"
printf '[package]\n' >"$escaped/outside/Cargo.toml"
jq --arg path "$escaped/outside/Cargo.toml" '(.packages[] | select(.name == "dep") | .manifest_path) = $path' \
	"$escaped/metadata.json" >"$escaped/metadata.tmp"
mv "$escaped/metadata.tmp" "$escaped/metadata.json"
expect_fail 'manifest path outside controlled roots' "$escaped"

duplicate="$test_root/duplicate"
write_fixture "$duplicate"
jq '.packages += [.packages[1]] | .resolve.nodes += [.resolve.nodes[1]]' "$duplicate/metadata.json" >"$duplicate/metadata.tmp"
mv "$duplicate/metadata.tmp" "$duplicate/metadata.json"
expect_fail 'duplicate package identity' "$duplicate"

count="$test_root/count"
write_fixture "$count"
if "$collector" --metadata "$count/metadata.json" --lockfile "$count/Cargo.lock" \
	--inventory "$count/inventory.tsv" --source-root "$count/source" --cargo-root "$count/cargo" \
	--fallback-registry "$count/fallback.tsv" --fallback-root "$count/fallback" \
	--canonical-license-map "$count/canonical-map.tsv" --expected-count 3 \
	--expected-metadata-only-fallback-count 0 --output "$count/output" >"$count/result" 2>&1; then
	fail 'metadata count mismatch unexpectedly passed'
fi

incomplete="$test_root/incomplete"
write_fixture "$incomplete"
jq 'del(.resolve)' "$incomplete/metadata.json" >"$incomplete/metadata.tmp"
mv "$incomplete/metadata.tmp" "$incomplete/metadata.json"
expect_fail 'incomplete metadata' "$incomplete"

pinned="$test_root/pinned"
write_pinned_fallback_fixture "$pinned"
run_collector "$pinned" "$pinned/output"
grep -Fxq 'pinned_upstream_material_count=1' "$pinned/output/EVIDENCE.env" || fail 'pinned upstream material count is wrong'
grep -Fxq 'material_provenance=pinned_upstream_material' "$pinned/output/packages/0001/PROVENANCE.env" || fail 'pinned provenance is missing'
test -s "$pinned/output/packages/0001/pinned-upstream-materials.tsv" || fail 'pinned upstream evidence manifest is missing'

pinned_digest="$test_root/pinned-digest"
write_pinned_fallback_fixture "$pinned_digest"
printf 'mutation\n' >>"$pinned_digest/fallback/third_party/cargo-license-fallbacks/dep-1.2.3/LICENSE"
expect_fail 'pinned upstream material digest mismatch' "$pinned_digest"

metadata_only="$test_root/metadata-only"
write_metadata_fallback_fixture "$metadata_only"
run_collector "$metadata_only" "$metadata_only/output" 1
grep -Fxq 'metadata_only_fallback_count=1' "$metadata_only/output/EVIDENCE.env" || fail 'metadata-only fallback count is wrong'
metadata_sequence=$(awk -F '\t' '$2 == "plist-macro" && $3 == "0.1.6" { print $1 }' "$metadata_only/output/packages.tsv")
[[ "$metadata_sequence" =~ ^[0-9]{4}$ ]] || fail 'metadata-only package identity is absent or ambiguous'
metadata_output="$metadata_only/output/packages/$metadata_sequence"
grep -Fxq 'material_provenance=metadata_only_fallback' "$metadata_output/PROVENANCE.env" || fail 'metadata-only provenance is missing'
grep -Fq 'did not ship a standalone license file' "$metadata_output/NOTICE" || fail 'metadata-only exception notice is missing'
grep -Fq 'canonical text is not presented as an upstream-authored file' "$metadata_output/NOTICE" || fail 'metadata-only notice misstates the canonical text origin'

metadata_digest="$test_root/metadata-digest"
write_metadata_fallback_fixture "$metadata_digest"
awk -F '\t' 'BEGIN { OFS="\t" } { $10="0000000000000000000000000000000000000000000000000000000000000000"; print }' \
	"$metadata_digest/fallback.tsv" >"$metadata_digest/fallback.tmp"
mv "$metadata_digest/fallback.tmp" "$metadata_digest/fallback.tsv"
expect_fail 'metadata-only evidence digest mismatch' "$metadata_digest"

second_missing="$test_root/second-missing"
write_metadata_fallback_fixture "$second_missing"
second_root="$second_missing/cargo/registry/src/index.invalid/other-0.1.0"
mkdir -p "$second_root"
printf '[package]\nname = "other"\nversion = "0.1.0"\nlicense = "MIT"\n' >"$second_root/Cargo.toml"
jq --arg manifest "$second_root/Cargo.toml" '
	.packages += [{name:"other",version:"0.1.0",id:"registry+https://github.com/rust-lang/crates.io-index#other@0.1.0",license:"MIT",license_file:null,source:"registry+https://github.com/rust-lang/crates.io-index",manifest_path:$manifest}] |
	.resolve.nodes += [{id:"registry+https://github.com/rust-lang/crates.io-index#other@0.1.0"}]
' "$second_missing/metadata.json" >"$second_missing/metadata.tmp"
mv "$second_missing/metadata.tmp" "$second_missing/metadata.json"
printf 'other\t0.1.0\tMIT\tregistry+https://github.com/rust-lang/crates.io-index\n' >>"$second_missing/inventory.tsv"
if run_collector "$second_missing" "$second_missing/output" 1 3 >"$second_missing/result" 2>&1; then
	fail 'second package without attribution unexpectedly passed'
fi

echo 'Cargo license collector self-test passed'
