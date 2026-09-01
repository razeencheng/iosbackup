#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

fail() {
    echo "license verification failed: $1" >&2
    exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq is required"

# These whole-file digests are independent trust anchors in verifier code.
# Updating the audited policy data therefore requires an explicit verifier
# change, rather than allowing content, row digests, and source URLs to be
# changed together without review.
readonly expected_license_map_sha256=283bc9256448e0b3e0979661783023c8aa0a8bdb65a54e5e1d405061552b20c8
readonly expected_fallback_registry_sha256=b6193cef2bce33a21a2b782042944ca21789e5b8c2d8f2cbfaaa4f50e7c7ab00
readonly expected_defmt_materials_sha256=ca9d8b9127c4d46c6ea3b5f950ba7f8208b65372d994239eec65f969d15887f8
readonly expected_idevice_materials_sha256=d4b537105f7688ad856ec231b486d2e0253f91c930690855ba17c9286f450028

sha256_file() {
    file=$1
    if command -v sha256sum >/dev/null 2>&1; then
        output=$(sha256sum "$file") || fail "unable to hash $file"
    elif command -v shasum >/dev/null 2>&1; then
        output=$(shasum -a 256 "$file") || fail "unable to hash $file"
    else
        fail "sha256sum or shasum is required"
    fi
    printf '%s\n' "${output%%[[:space:]]*}"
}

for file in \
    LICENSE NOTICE THIRD_PARTY_NOTICES.md CONTRIBUTING.md \
    docs/LICENSING.md internal/buildinfo/buildinfo.go \
    third_party/components.lock.json \
    third_party/runtime-closure-linux-amd64.txt \
    third_party/runtime-closure-linux-arm64.txt \
    third_party/netmuxd-cargo-licenses.tsv \
    third_party/cargo-license-fallbacks.tsv \
    third_party/licenses/README.md \
    third_party/licenses/spdx-license-map.tsv \
    scripts/read_release_manifest.sh \
    scripts/collect_cargo_licenses.sh \
    scripts/collect_cargo_licenses_test.sh \
    release/manifest.env; do
    [[ -f "$file" ]] || fail "missing required file: $file"
done

grep -Fq 'GNU AFFERO GENERAL PUBLIC LICENSE' LICENSE || fail "LICENSE is not the AGPL text"
grep -Fq 'AGPL-3.0-only' NOTICE || fail "NOTICE lacks AGPL-3.0-only"
grep -Fq 'AGPL-3.0-only' internal/buildinfo/buildinfo.go || fail "buildinfo lacks AGPL-3.0-only"
grep -Fq 'AGPL-3.0-only' docs/LICENSING.md || fail "licensing guide lacks AGPL-3.0-only"

manifest_reader=./scripts/read_release_manifest.sh
"$manifest_reader" release/manifest.env >/dev/null || fail "release manifest is missing, malformed, or ambiguous"
release_version=$("$manifest_reader" release/manifest.env IOSBK_VERSION) || fail "invalid release version"
release_date=$("$manifest_reader" release/manifest.env IOSBK_BUILD_DATE) || fail "invalid release build date"
release_source=$("$manifest_reader" release/manifest.env IOSBK_SOURCE_URL) || fail "invalid release source URL"

jq empty third_party/components.lock.json
while IFS=$'\t' read -r name ref source license_file; do
    [[ "$ref" =~ ^[0-9a-f]{40}$ ]] || fail "$name is not pinned to a full commit"
    [[ "$source" == https://* ]] || fail "$name lacks an HTTPS upstream"
    [[ -f "$license_file" ]] || fail "$name license file is missing: $license_file"
done < <(jq -r '.components[] | select(has("ref")) | [.name, .ref, .source, .license_file] | @tsv' third_party/components.lock.json)

while IFS= read -r patch_file; do
    [[ -f "$patch_file" ]] || fail "locked patch is missing: $patch_file"
done < <(jq -r '.components[].patches[]?' third_party/components.lock.json)

for ref_pair in \
    'LIBPLIST_REF libplist' \
    'LIBGLUE_REF libimobiledevice-glue' \
    'LIBUSBMUXD_REF libusbmuxd' \
    'LIBTATSU_REF libtatsu' \
    'LIBIMD_REF libimobiledevice' \
    'LIBGENERAL_REF libgeneral' \
    'USBMUXD2_REF usbmuxd2' \
    'NETMUXD_REF netmuxd'; do
    read -r arg_name component_name <<< "$ref_pair"
    docker_ref=$(sed -n "s/^ARG ${arg_name}=//p" Dockerfile)
    lock_ref=$(jq -r --arg name "$component_name" '.components[] | select(.name == $name) | .ref' third_party/components.lock.json)
    [[ -n "$docker_ref" && "$docker_ref" == "$lock_ref" ]] || fail "Dockerfile $arg_name does not match $component_name"
done

netmuxd_count=$(jq '[.components[] | select(.name == "netmuxd")] | length' third_party/components.lock.json)
[[ "$netmuxd_count" == 1 ]] || fail "component lock must contain exactly one netmuxd entry"
if ! jq -e '
    .components[] | select(.name == "netmuxd") |
    (.ref | type == "string" and test("^[0-9a-f]{40}$")) and
    (.license == "LGPL-2.1-only") and
    (.license_file == "third_party/licenses/LGPL-2.1.txt") and
    (.cargo_root_version | type == "string" and test("^[0-9]+[.][0-9]+[.][0-9]+([+-][0-9A-Za-z.-]+)?$")) and
    (.cargo_lock_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.cargo_license_inventory == "third_party/netmuxd-cargo-licenses.tsv") and
    (.cargo_license_inventory_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.cargo_resolved_packages | type == "number" and . > 0 and floor == .) and
    (.cargo_fallback_registry == "third_party/cargo-license-fallbacks.tsv") and
    (.cargo_fallback_registry_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.cargo_pinned_upstream_materials | type == "number" and . >= 0 and floor == .) and
    (.cargo_metadata_only_fallbacks | type == "number" and . >= 0 and floor == .)
' third_party/components.lock.json >/dev/null; then
    fail "netmuxd Cargo closure metadata is missing or malformed"
fi

netmuxd_ref=$(jq -r '.components[] | select(.name == "netmuxd") | .ref' third_party/components.lock.json)
netmuxd_license=$(jq -r '.components[] | select(.name == "netmuxd") | .license' third_party/components.lock.json)
netmuxd_root_version=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_root_version' third_party/components.lock.json)
cargo_lock_sha=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_lock_sha256' third_party/components.lock.json)
cargo_inventory=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_license_inventory' third_party/components.lock.json)
cargo_inventory_sha=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_license_inventory_sha256' third_party/components.lock.json)
expected_count=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_resolved_packages' third_party/components.lock.json)
fallback_registry=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_fallback_registry' third_party/components.lock.json)
fallback_registry_sha=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_fallback_registry_sha256' third_party/components.lock.json)
expected_pinned=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_pinned_upstream_materials' third_party/components.lock.json)
expected_metadata_only=$(jq -r '.components[] | select(.name == "netmuxd") | .cargo_metadata_only_fallbacks' third_party/components.lock.json)

[[ -f "$cargo_inventory" ]] || fail "netmuxd Cargo license inventory is missing"
[[ "$(sha256_file "$cargo_inventory")" == "$cargo_inventory_sha" ]] || fail "netmuxd Cargo inventory digest does not match the lock"
grep -Fxq '# Generated with: cargo metadata --locked --offline --format-version 1' "$cargo_inventory" || \
    fail "netmuxd Cargo inventory lacks locked offline generation evidence"
inventory_ref=$(sed -n 's/^# netmuxd commit: //p' "$cargo_inventory")
inventory_lock_sha=$(sed -n 's/^# Cargo.lock sha256: //p' "$cargo_inventory")
[[ "$inventory_ref" == "$netmuxd_ref" ]] || fail "netmuxd Cargo inventory commit does not match the lock"
[[ "$inventory_lock_sha" == "$cargo_lock_sha" ]] || fail "netmuxd Cargo.lock digest does not match the inventory"

if ! awk -F '\t' -v expected_count="$expected_count" -v root_version="$netmuxd_root_version" -v root_license="$netmuxd_license" '
    BEGIN { invalid=0 }
    /^#/ || /^[[:space:]]*$/ { next }
    {
        count++
        if (NF != 4 || $1 == "" || $2 == "" || $3 == "" || $4 == "") invalid=1
        if ($3 == "UNDECLARED") invalid=1
        if ($4 != "path" && $4 !~ /^(registry|git)[+]https:\/\//) invalid=1
        key=$1 SUBSEP $2 SUBSEP $4
        if (seen[key]++) invalid=1
        if ($1 == "netmuxd") {
            root_count++
            if ($2 != root_version || $3 != root_license || $4 != "path") invalid=1
        } else if ($4 == "path") {
            invalid=1
        }
    }
    END {
        if (count != expected_count || root_count != 1) invalid=1
        exit invalid
    }
' "$cargo_inventory"; then
    fail "netmuxd Cargo inventory is incomplete, ambiguous, or malformed"
fi

[[ -f "$fallback_registry" && ! -L "$fallback_registry" && -s "$fallback_registry" ]] || fail "Cargo fallback registry is missing, empty, or a symlink"
[[ "$(sha256_file "$fallback_registry")" == "$expected_fallback_registry_sha256" ]] || fail "Cargo fallback registry does not match the verifier trust anchor"
[[ "$(sha256_file "$fallback_registry")" == "$fallback_registry_sha" ]] || fail "Cargo fallback registry digest does not match the lock"
if ! awk -F '\t' -v expected_pinned="$expected_pinned" -v expected_metadata="$expected_metadata_only" '
    /^#/ || /^[[:space:]]*$/ { next }
    {
        if (NF != 14 || ($1 != "vcs-root" && $1 != "metadata-only") ||
            $2 == "" || $3 == "" || $4 !~ /^registry[+]https:\/\// ||
            $5 !~ /^[0-9a-f]+$/ || length($5) != 40 || $6 == "" || $7 == "" || $8 == "" ||
            $9 !~ /^https:\/\// || $10 !~ /^[0-9a-f]+$/ || length($10) != 64 ||
            $11 !~ /^[0-9a-f]+$/ || length($11) != 64 ||
            $12 !~ /^[0-9a-f]+$/ || length($12) != 64) invalid=1
        identity=$2 SUBSEP $3 SUBSEP $4
        if (seen[identity]++) invalid=1
        if ($1 == "vcs-root") {
            pinned++
            if ($13 !~ /^third_party\/cargo-license-fallbacks\/[A-Za-z0-9._+-]+\/materials[.]tsv$/ || $14 != "-") invalid=1
            if ($2 == "defmt-parser") {
                defmt++
                if ($3 != "1.0.0" || $5 != "4a8cdb44891ed57b8ff5a023b6bec7137c48708f" || $6 != "parser" ||
                    $7 != "MIT OR Apache-2.0" || $8 != "The Knurling-rs developers" || $9 != "https://github.com/knurling-rs/defmt" ||
                    $10 != "c4c4df5b09e53450267eacadf1f7f6a0d3d5fc463bc450e199f0d06eba8e6ab3" ||
                    $11 != "a40aae66ea32ccd2b2c30ea976d4ac02fab1025a8dece8bfae6aa83bdcfd9c0e" ||
                    $12 != "7ab8f01085721143c43db5db2528806f393df2058d03c5b8da20634e14886a05") invalid=1
            } else if ($2 == "idevice") {
                idevice++
                if ($3 != "0.1.65" || $5 != "2bc6a05c80daaf8583884cf7f2d2563be17e6c2d" || $6 != "idevice" ||
                    $7 != "MIT" || $8 != "Jackson Coxson" || $9 != "https://github.com/jkcoxson/idevice" ||
                    $10 != "b25b25b4881fe30d23fd901a77074f5ea7e947b350deadb6e68c42003907cb32" ||
                    $11 != "a2b06d6b59c3cdc503535ebd429e391adfa786b80245aac61f5fb9c4a13fdcf3" ||
                    $12 != "fde3f7edde80b658b0018b07bfb01930fe37d317598894e47aab4726b08f3d9a") invalid=1
            } else invalid=1
        } else {
            metadata++
            if ($13 != "-" || $14 !~ /^[A-Za-z0-9.+-]+$/) invalid=1
            if ($2 != "plist-macro" || $3 != "0.1.6" ||
                $5 != "d1d48559ddc8e9bd263f36180bbe1d4f2a3d55e5" || $6 != "." || $7 != "MIT" ||
                $8 != "Jackson Coxson" || $9 != "https://github.com/jkcoxson/plist_macro" ||
                $10 != "f40885eaa05276c979b17c3041889df6b9cf164e0c91de47d9592d9b97ff0cad" ||
                $11 != "5e2c21485fa6e5a3a685a94e3693e5faa4f9a2aa525e1b785875fae2328be864" ||
                $12 != "c939dbbb062e2dadd89390ddcef347e84c5ce95b519aff9e58d1a03e3d4fb4c5" || $14 != "MIT") invalid=1
        }
    }
    END {
        if (pinned != expected_pinned || metadata != expected_metadata || metadata != 1 || defmt != 1 || idevice != 1) invalid=1
        exit invalid
    }
' "$fallback_registry"; then
    fail "Cargo fallback registry is incomplete, ambiguous, or does not match the locked provenance counts"
fi

while IFS=$'\t' read -r mode name version source vcs_commit vcs_path declared_license authors repository cargo_sha readme_sha vcs_sha materials_manifest canonical; do
    case "$mode" in ''|'#'*) continue ;; esac
    if [[ "$mode" == vcs-root ]]; then
        [[ -f "$materials_manifest" && ! -L "$materials_manifest" && -s "$materials_manifest" ]] || fail "pinned upstream material manifest is missing: $name $version"
        case "$name:$version:$repository" in
            defmt-parser:1.0.0:https://github.com/knurling-rs/defmt)
                expected_materials_sha=$expected_defmt_materials_sha256
                ;;
            idevice:0.1.65:https://github.com/jkcoxson/idevice)
                expected_materials_sha=$expected_idevice_materials_sha256
                ;;
            *) fail "pinned upstream material repository is not trusted: $name $version" ;;
        esac
        [[ "$(sha256_file "$materials_manifest")" == "$expected_materials_sha" ]] || fail "pinned upstream material manifest does not match the verifier trust anchor: $name $version"
        repository_slug=${repository#https://github.com/}
        [[ "$repository_slug" != "$repository" && -n "$repository_slug" ]] || fail "pinned upstream repository is malformed: $name $version"
        material_count=0
        while IFS=$'\t' read -r material_file bundled_sha source_url upstream_sha normalization; do
            case "$material_file" in ''|'#'*) continue ;; esac
            [[ "$material_file" =~ ^[A-Za-z0-9._+-]+$ && "$bundled_sha" =~ ^[0-9a-f]{64}$ ]] || fail "pinned upstream material record is malformed: $name $version"
            [[ "$bundled_sha" == "$upstream_sha" && "$normalization" == none ]] || fail "pinned upstream material is not byte-exact: $name $version"
            expected_material_source="https://raw.githubusercontent.com/$repository_slug/$vcs_commit/$material_file"
            [[ "$source_url" == "$expected_material_source" ]] || fail "pinned upstream material URL is not derived from the locked repository and commit: $name $version"
            material_path="$(dirname "$materials_manifest")/$material_file"
            [[ -f "$material_path" && ! -L "$material_path" && -s "$material_path" ]] || fail "pinned upstream material is missing, empty, or a symlink: $name $version"
            [[ "$(sha256_file "$material_path")" == "$bundled_sha" ]] || fail "pinned upstream material digest mismatch: $name $version"
            material_count=$((material_count + 1))
        done <"$materials_manifest"
        [[ "$material_count" -gt 0 ]] || fail "pinned upstream material manifest is empty: $name $version"
    else
        canonical_matches=$(awk -F '\t' -v identifier="$canonical" '!/^#/ && NF && $1 == identifier && $2 == "license" { count++ } END { print count+0 }' third_party/licenses/spdx-license-map.tsv)
        [[ "$canonical_matches" == 1 ]] || fail "metadata-only fallback canonical license is missing or ambiguous: $name $version"
    fi
done <"$fallback_registry"

license_map=third_party/licenses/spdx-license-map.tsv
[[ "$(sha256_file "$license_map")" == "$expected_license_map_sha256" ]] || fail "SPDX license mapping does not match the verifier trust anchor"
if ! awk -F '\t' '
    /^#/ || /^[[:space:]]*$/ { next }
    NF != 5 || $1 !~ /^[A-Za-z0-9.+-]+$/ || ($2 != "license" && $2 != "exception") ||
        $3 !~ /^third_party\/licenses\/[A-Za-z0-9.+-]+[.]txt$/ ||
        $4 !~ /^[0-9a-f]+$/ || length($4) != 64 || $5 !~ /^https:\/\// { exit 1 }
    seen[$1]++ { exit 1 }
    END { if (length(seen) == 0) exit 1 }
' "$license_map"; then
    fail "SPDX license mapping is missing, malformed, or ambiguous"
fi
while IFS=$'\t' read -r identifier kind license_text locked_sha source_url; do
    case "$identifier" in ''|'#'*) continue ;; esac
    [[ -f "$license_text" && ! -L "$license_text" && -s "$license_text" ]] || \
        fail "mapped SPDX text is missing, empty, or a symlink: $identifier"
    [[ "$(sha256_file "$license_text")" == "$locked_sha" ]] || fail "mapped SPDX text digest mismatch: $identifier"
    case "$identifier" in
        Apache-2.0)
            expected_source_url=https://www.apache.org/licenses/LICENSE-2.0.txt
            ;;
        LGPL-2.1-only|LGPL-2.1-or-later)
            expected_source_url=https://www.gnu.org/licenses/old-licenses/lgpl-2.1.txt
            ;;
        *)
            expected_source_url="https://raw.githubusercontent.com/spdx/license-list-data/v3.28.0/text/$identifier.txt"
            ;;
    esac
    [[ "$source_url" == "$expected_source_url" ]] || fail "mapped SPDX text source is not the pinned authoritative URL: $identifier"
done <"$license_map"

if ! awk -F '\t' -v map="$license_map" '
    function parse_primary( token) {
        token=tokens[position]
        if (token == "(") {
            position++
            if (!parse_or()) return 0
            if (tokens[position] != ")") return 0
            position++
            return 1
        }
        if (kinds[token] != "license") return 0
        used[token]=1
        position++
        if (tokens[position] == "WITH") {
            position++
            token=tokens[position]
            if (kinds[token] != "exception") return 0
            used[token]=1
            position++
        }
        return 1
    }
    function parse_and() {
        if (!parse_primary()) return 0
        while (tokens[position] == "AND") {
            position++
            if (!parse_primary()) return 0
        }
        return 1
    }
    function parse_or() {
        if (!parse_and()) return 0
        while (tokens[position] == "OR") {
            position++
            if (!parse_and()) return 0
        }
        return 1
    }
    BEGIN {
        while ((getline line < map) > 0) {
            if (line ~ /^#/ || line ~ /^[[:space:]]*$/) continue
            count=split(line, fields, "\t")
            if (count != 5 || fields[1] in kinds) exit 2
            kinds[fields[1]]=fields[2]
        }
        close(map)
    }
    /^#/ || /^[[:space:]]*$/ { next }
    {
        expression=$3
        gsub(/\//, " OR ", expression)
        gsub(/\(/, " ( ", expression)
        gsub(/\)/, " ) ", expression)
        token_count=split(expression, raw_tokens, /[[:space:]]+/)
        delete tokens
        actual=0
        for (i=1; i<=token_count; i++) if (raw_tokens[i] != "") tokens[++actual]=raw_tokens[i]
        position=1
        if (actual == 0 || !parse_or() || position != actual + 1) exit 3
    }
    END {
        for (identifier in kinds) if (!(identifier in used)) exit 4
    }
' "$cargo_inventory"; then
    fail "Cargo license expression is invalid, unknown, or lacks a mapped standard text"
fi

grep -Fq 'COPY third_party/netmuxd-cargo-licenses.tsv /final/usr/share/licenses/iosbackup/third_party/' Dockerfile || \
    fail "Dockerfile does not copy the netmuxd Cargo inventory into the final image"
grep -Fq 'COPY third_party/components.lock.json /build/iosbackup-lock/components.lock.json' Dockerfile || \
    fail "Dockerfile does not read the public component lock in the netmuxd builder"
grep -Fq 'expected_lock_sha=$(jq -er' Dockerfile || fail "Dockerfile does not read the locked Cargo.lock digest"
grep -Fq 'actual_lock_sha=$(sha256sum Cargo.lock' Dockerfile || fail "Dockerfile does not hash the actual checked-out Cargo.lock"
grep -Fq 'test "$actual_lock_sha" = "$expected_lock_sha"' Dockerfile || fail "Dockerfile does not compare the actual Cargo.lock with the public lock"
grep -Fq 'cargo fetch --locked' Dockerfile || fail "Dockerfile does not fetch the locked Cargo graph"
grep -Fq 'cargo metadata --locked --format-version 1' Dockerfile || fail "Dockerfile does not inspect locked Cargo metadata"
grep -Fq 'cargo build --release --locked' Dockerfile || fail "Dockerfile does not build netmuxd from the locked Cargo graph"
grep -Fq 'scripts/collect_cargo_licenses.sh' Dockerfile || fail "Dockerfile does not copy the Cargo attribution collector"
grep -Fq 'COPY third_party/cargo-license-fallbacks.tsv /build/iosbackup-lock/cargo-license-fallbacks.tsv' Dockerfile || \
    fail "Dockerfile does not copy the locked Cargo fallback registry"
grep -Fq 'COPY third_party/cargo-license-fallbacks/ /build/iosbackup-lock/repository/third_party/cargo-license-fallbacks/' Dockerfile || \
    fail "Dockerfile does not copy pinned upstream Cargo attribution materials"
grep -Fq 'COPY third_party/licenses/ /build/iosbackup-lock/repository/third_party/licenses/' Dockerfile || \
    fail "Dockerfile does not copy canonical license texts into the attribution builder"
grep -Fq -- '--fallback-registry /build/iosbackup-lock/cargo-license-fallbacks.tsv' Dockerfile || \
    fail "Dockerfile does not pass the locked Cargo fallback registry to the collector"
grep -Fq -- '--expected-metadata-only-fallback-count "$expected_metadata_only"' Dockerfile || \
    fail "Dockerfile does not enforce the locked metadata-only fallback count"
grep -Fq 'pinned_upstream_material_count=$expected_pinned' Dockerfile || fail "Dockerfile does not enforce the pinned upstream material count"
grep -Fq 'metadata_only_fallback_count=$expected_metadata_only' Dockerfile || fail "Dockerfile does not enforce the metadata-only fallback count"
grep -Fq '$2 == "plist-macro" && $3 == "0.1.6"' Dockerfile || fail "Dockerfile does not assert the single metadata-only fallback identity"
grep -Fq 'sha256sum -c SHA256SUMS' Dockerfile || fail "Dockerfile does not verify the generated Cargo attribution bundle"
grep -Fq -- '--output /build/netmuxd-cargo-attribution' Dockerfile || fail "Dockerfile does not generate the Cargo attribution bundle"
grep -Fq 'COPY --from=netmuxd-builder /build/netmuxd-cargo-attribution/ /final/usr/share/licenses/iosbackup/third_party/netmuxd-cargo/' Dockerfile || \
    fail "Dockerfile does not copy the Cargo attribution bundle into the final image"
grep -Fq 'org.opencontainers.image.created="${IOSBK_BUILD_DATE}T00:00:00Z"' Dockerfile || \
    fail "Dockerfile OCI created label is not derived from the manifest date as RFC3339"
grep -Fq 'third_party/netmuxd-cargo-licenses.tsv' THIRD_PARTY_NOTICES.md || \
    fail "third-party notices lack the netmuxd Cargo attribution summary"
grep -Fq '/usr/share/licenses/iosbackup/third_party/netmuxd-cargo/' THIRD_PARTY_NOTICES.md || \
    fail "third-party notices do not describe the built Cargo attribution bundle"

template_count=0
source_link_count=0
while IFS= read -r -d '' page; do
    template_count=$((template_count + 1))
    if grep -Fq '{{.SourceURL}}' "$page"; then
        grep -Fq '{{.SourceURL}}/tree/{{.Commit}}' "$page" || fail "$page has an incomplete source link"
        grep -Fq 'AGPL-3.0-only' "$page" || fail "$page source link lacks its license identifier"
        source_link_count=$((source_link_count + 1))
    fi
done < <(find internal/app/templates -type f -name '*.html' -print0)
[[ "$template_count" -gt 0 ]] || fail "no embedded HTML templates were found"
[[ "$source_link_count" -gt 0 ]] || fail "no embedded page exposes source and license information"

for license_file in third_party/licenses/*.txt; do
    [[ -f "$license_file" ]] || fail "third-party license is missing: $license_file"
    [[ -s "$license_file" && ! -L "$license_file" ]] || fail "third-party license is empty or a symlink: $license_file"
done

for exact_text in BSD-2-Clause BSD-3-Clause MPL-2.0; do
    grep -Fxq "third_party/licenses/$exact_text.txt -text -whitespace" .gitattributes || \
        fail ".gitattributes does not preserve exact bytes for $exact_text"
done
if grep -E '^third_party/licenses/[^[:space:]]*[*?]' .gitattributes >/dev/null; then
    fail ".gitattributes broadens exact license-text exceptions with a glob"
fi

echo 'license verification passed'
