#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
source_root=$(cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-license-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

populate_fixture() {
    fixture_root=$1
    mkdir -p \
        "$fixture_root/docs" \
        "$fixture_root/internal/buildinfo" \
        "$fixture_root/internal/app" \
        "$fixture_root/release" \
        "$fixture_root/scripts" \
        "$fixture_root/third_party"
    for file in \
        LICENSE NOTICE THIRD_PARTY_NOTICES.md CONTRIBUTING.md Dockerfile .gitattributes \
        docs/LICENSING.md internal/buildinfo/buildinfo.go release/manifest.env \
        third_party/components.lock.json \
        third_party/runtime-closure-linux-amd64.txt \
        third_party/runtime-closure-linux-arm64.txt \
        third_party/netmuxd-cargo-licenses.tsv \
		third_party/cargo-license-fallbacks.tsv \
		scripts/collect_cargo_licenses.sh \
		scripts/collect_cargo_licenses_test.sh \
        scripts/read_release_manifest.sh \
        scripts/verify_licensing.sh; do
        cp "$source_root/$file" "$fixture_root/$file"
    done
    cp -R "$source_root/internal/app/templates" "$fixture_root/internal/app/"
    cp -R "$source_root/third_party/licenses" "$fixture_root/third_party/"
    cp -R "$source_root/third_party/patches" "$fixture_root/third_party/"
    cp -R "$source_root/third_party/cargo-license-fallbacks" "$fixture_root/third_party/"
}

new_fixture() {
    label=$1
    fixture_root="$test_root/$label"
    populate_fixture "$fixture_root"
}

rewrite_lock() {
    filter=$1
    jq "$filter" "$fixture_root/third_party/components.lock.json" >"$fixture_root/components.lock.tmp"
    mv "$fixture_root/components.lock.tmp" "$fixture_root/third_party/components.lock.json"
}

hash_file() {
    file=$1
    if command -v sha256sum >/dev/null 2>&1; then
        digest=$(sha256sum "$file")
    else
        digest=$(shasum -a 256 "$file")
    fi
    printf '%s\n' "${digest%%[[:space:]]*}"
}

refresh_inventory_digest() {
    inventory="$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
    digest=$(hash_file "$inventory")
    jq --arg digest "$digest" \
        '(.components[] | select(.name == "netmuxd") | .cargo_license_inventory_sha256) = $digest' \
        "$fixture_root/third_party/components.lock.json" >"$fixture_root/components.lock.tmp"
    mv "$fixture_root/components.lock.tmp" "$fixture_root/third_party/components.lock.json"
}

refresh_fallback_digest() {
    registry="$fixture_root/third_party/cargo-license-fallbacks.tsv"
    digest=$(hash_file "$registry")
    jq --arg digest "$digest" \
        '(.components[] | select(.name == "netmuxd") | .cargo_fallback_registry_sha256) = $digest' \
        "$fixture_root/third_party/components.lock.json" >"$fixture_root/components.lock.tmp"
    mv "$fixture_root/components.lock.tmp" "$fixture_root/third_party/components.lock.json"
}

expect_pass() {
    label=$1
    if ! (cd "$fixture_root" && ./scripts/verify_licensing.sh) >"$test_root/output" 2>&1; then
        fail "$label unexpectedly failed: $(sed -n '1p' "$test_root/output")"
    fi
}

expect_fail() {
    label=$1
    if (cd "$fixture_root" && ./scripts/verify_licensing.sh) >"$test_root/output" 2>&1; then
        fail "$label unexpectedly passed"
    fi
}

new_fixture baseline
expect_pass 'complete netmuxd Cargo license closure'

new_fixture missing-fields
rewrite_lock '(.components[] | select(.name == "netmuxd")) |= del(.cargo_root_version, .cargo_lock_sha256, .cargo_license_inventory, .cargo_license_inventory_sha256, .cargo_resolved_packages)'
expect_fail 'missing netmuxd Cargo closure fields'

new_fixture missing-inventory-digest
rewrite_lock '(.components[] | select(.name == "netmuxd")) |= del(.cargo_license_inventory_sha256)'
expect_fail 'missing inventory digest'

new_fixture missing-inventory
rm "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
expect_fail 'missing Cargo license inventory'

new_fixture count-mismatch
rewrite_lock '(.components[] | select(.name == "netmuxd") | .cargo_resolved_packages) += 1'
expect_fail 'resolved package count mismatch'

new_fixture lock-header-mismatch
sed 's/^# Cargo.lock sha256:.*/# Cargo.lock sha256: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff/' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'Cargo.lock digest header mismatch'

new_fixture commit-header-mismatch
sed 's/^# netmuxd commit:.*/# netmuxd commit: ffffffffffffffffffffffffffffffffffffffff/' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'netmuxd commit header mismatch'

new_fixture inventory-digest-mismatch
printf '# post-review mutation\n' >>"$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
expect_fail 'inventory digest mismatch'

new_fixture missing-generation-evidence
sed '/^# Generated with: cargo metadata --locked --offline --format-version 1$/d' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'missing locked offline generation evidence'

new_fixture empty-license
awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } !changed { $3=""; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'empty package license'

new_fixture undeclared-license
awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } !changed { $3="UNDECLARED"; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'undeclared package license'

new_fixture duplicate-root
awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } $1 != "netmuxd" && !changed { $1="netmuxd"; $2="0.4.2"; $3="LGPL-2.1-only"; $4="path"; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'duplicate root package metadata'

new_fixture root-license-mismatch
rewrite_lock '(.components[] | select(.name == "netmuxd") | .license) = "MIT"'
expect_fail 'root package license mismatch'

new_fixture unsafe-source
unsafe_source=$(printf '/%s/%s/%s' Users example private-cache)
awk -F '\t' -v unsafe_source="$unsafe_source" 'BEGIN { OFS="\t" } /^#/ { print; next } $4 != "path" && !changed { $4=unsafe_source; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'local Cargo package source'

new_fixture unknown-spdx-identifier
awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } !changed { $3="LicenseRef-Unknown"; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'unknown SPDX identifier'

new_fixture malformed-spdx-expression
awk -F '\t' 'BEGIN { OFS="\t" } /^#/ { print; next } !changed { $3="MIT OR (Apache-2.0"; changed=1 } { print }' \
    "$fixture_root/third_party/netmuxd-cargo-licenses.tsv" >"$fixture_root/inventory.tmp"
mv "$fixture_root/inventory.tmp" "$fixture_root/third_party/netmuxd-cargo-licenses.tsv"
refresh_inventory_digest
expect_fail 'malformed SPDX expression'

new_fixture missing-mapped-text
rm "$fixture_root/third_party/licenses/MIT.txt"
expect_fail 'missing mapped standard license text'

new_fixture mapped-text-digest-mismatch
printf '\npost-review mutation\n' >>"$fixture_root/third_party/licenses/MIT.txt"
expect_fail 'mapped standard license text digest mismatch'

new_fixture synchronized-license-map-tamper
printf '\ncoordinated mutation\n' >>"$fixture_root/third_party/licenses/MIT.txt"
mutated_sha=$(hash_file "$fixture_root/third_party/licenses/MIT.txt")
awk -F '\t' -v digest="$mutated_sha" 'BEGIN { OFS="\t" } $1 == "MIT" { $4=digest; $5="https://example.invalid/MIT.txt" } { print }' \
    "$fixture_root/third_party/licenses/spdx-license-map.tsv" >"$fixture_root/license-map.tmp"
mv "$fixture_root/license-map.tmp" "$fixture_root/third_party/licenses/spdx-license-map.tsv"
expect_fail 'synchronized standard text, digest, and source tamper'

for exact_text in BSD-2-Clause BSD-3-Clause MPL-2.0; do
    new_fixture "whitespace-exempt-$exact_text-byte-mutation"
    printf 'x' >>"$fixture_root/third_party/licenses/$exact_text.txt"
    expect_fail "whitespace-exempt official text digest mismatch: $exact_text"
done

new_fixture missing-license-map
rm "$fixture_root/third_party/licenses/spdx-license-map.tsv"
expect_fail 'missing SPDX license map'

new_fixture missing-fallback-fields
rewrite_lock '(.components[] | select(.name == "netmuxd")) |= del(.cargo_fallback_registry, .cargo_fallback_registry_sha256, .cargo_pinned_upstream_materials, .cargo_metadata_only_fallbacks)'
expect_fail 'missing locked Cargo fallback fields'

new_fixture missing-fallback-registry
rm "$fixture_root/third_party/cargo-license-fallbacks.tsv"
expect_fail 'missing Cargo fallback registry'

new_fixture fallback-registry-digest-mismatch
printf '# mutation\n' >>"$fixture_root/third_party/cargo-license-fallbacks.tsv"
expect_fail 'Cargo fallback registry digest mismatch'

new_fixture synchronized-fallback-registry-tamper
printf '# coordinated mutation\n' >>"$fixture_root/third_party/cargo-license-fallbacks.tsv"
refresh_fallback_digest
expect_fail 'synchronized fallback registry and lock digest tamper'

new_fixture second-metadata-fallback
awk -F '\t' 'BEGIN { OFS="\t" } /^metadata-only/ { print; $2="other"; $3="9.9.9"; $5="0000000000000000000000000000000000000000"; print; next } { print }' \
    "$fixture_root/third_party/cargo-license-fallbacks.tsv" >"$fixture_root/fallback.tmp"
mv "$fixture_root/fallback.tmp" "$fixture_root/third_party/cargo-license-fallbacks.tsv"
rewrite_lock '(.components[] | select(.name == "netmuxd") | .cargo_metadata_only_fallbacks) = 2'
refresh_fallback_digest
expect_fail 'second metadata-only fallback'

new_fixture fallback-evidence-digest-mismatch
awk -F '\t' 'BEGIN { OFS="\t" } /^metadata-only/ { $10="0000000000000000000000000000000000000000000000000000000000000000" } { print }' \
    "$fixture_root/third_party/cargo-license-fallbacks.tsv" >"$fixture_root/fallback.tmp"
mv "$fixture_root/fallback.tmp" "$fixture_root/third_party/cargo-license-fallbacks.tsv"
refresh_fallback_digest
expect_fail 'metadata-only fallback evidence digest mismatch'

new_fixture missing-pinned-upstream-material
rm "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/LICENSE-MIT"
expect_fail 'missing pinned upstream material'

new_fixture normalized-pinned-upstream-material
awk -F '\t' 'BEGIN { OFS="\t" } /^LICENSE-MIT/ { $5="newline-added" } { print }' \
    "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/materials.tsv" >"$fixture_root/materials.tmp"
mv "$fixture_root/materials.tmp" "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/materials.tsv"
expect_fail 'normalized pinned upstream material'

new_fixture synchronized-pinned-material-tamper
printf 'x' >>"$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/LICENSE-MIT"
mutated_sha=$(hash_file "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/LICENSE-MIT")
awk -F '\t' -v digest="$mutated_sha" 'BEGIN { OFS="\t" } /^LICENSE-MIT/ {
        $2=digest
        $3="https://example.invalid/4a8cdb44891ed57b8ff5a023b6bec7137c48708f/LICENSE-MIT"
        $4=digest
    } { print }' \
    "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/materials.tsv" >"$fixture_root/materials.tmp"
mv "$fixture_root/materials.tmp" "$fixture_root/third_party/cargo-license-fallbacks/defmt-parser-1.0.0/materials.tsv"
expect_fail 'synchronized pinned material, digest, and source tamper'

new_fixture missing-image-copy
sed '/netmuxd-cargo-licenses[.]tsv/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing scratch image inventory copy'

new_fixture unlocked-cargo-build
sed 's/cargo build --release --locked/cargo build --release/' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'netmuxd Cargo build without --locked'

new_fixture missing-actual-lock-check
sed '/sha256sum Cargo[.]lock/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing actual Cargo.lock digest check'

new_fixture missing-attribution-bundle-copy
sed '/netmuxd-cargo\//d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing scratch image attribution bundle copy'

new_fixture missing-collector-source
sed '/scripts\/collect_cargo_licenses[.]sh/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing attribution collector source'

new_fixture missing-fallback-copy
sed '/cargo-license-fallbacks[.]tsv/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing Cargo fallback registry copy'

new_fixture missing-fallback-count
sed '/expected-metadata-only-fallback-count/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing metadata-only fallback count assertion'

new_fixture missing-bundle-checksum-verification
sed '/sha256sum -c SHA256SUMS/d' "$fixture_root/Dockerfile" >"$fixture_root/Dockerfile.tmp"
mv "$fixture_root/Dockerfile.tmp" "$fixture_root/Dockerfile"
expect_fail 'missing generated attribution bundle checksum verification'

new_fixture missing-attribution
sed '/netmuxd-cargo-licenses[.]tsv/d' "$fixture_root/THIRD_PARTY_NOTICES.md" >"$fixture_root/notices.tmp"
mv "$fixture_root/notices.tmp" "$fixture_root/THIRD_PARTY_NOTICES.md"
expect_fail 'missing netmuxd Cargo attribution summary'

echo 'license verification self-test passed'
