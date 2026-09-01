# Standard license texts for the locked Cargo closure

`spdx-license-map.tsv` is the machine-verified mapping from every SPDX identifier used by `third_party/netmuxd-cargo-licenses.tsv` to a complete, non-empty standard text or exception text in this directory. Each row pins the file SHA-256 and its authoritative HTTPS source.

The SPDX-sourced files are byte-for-byte copies from the official `spdx/license-list-data` tag `v3.28.0`. `Apache-2.0.txt` and `LGPL-2.1.txt` are byte-for-byte copies from the official Apache Software Foundation and GNU license pages recorded in the map. Both `LGPL-2.1-only` and `LGPL-2.1-or-later` map to the same complete LGPL 2.1 terms; the package's SPDX expression supplies the only/later choice.

`BSD-2-Clause.txt`, `BSD-3-Clause.txt`, and `MPL-2.0.txt` retain intentional trailing spaces from the fixed upstream blobs. `.gitattributes` disables Git line-ending conversion and whitespace diagnostics only for these three exact paths; `scripts/verify_licensing.sh` still requires each exact SHA-256 from `spdx-license-map.tsv`, anchors the complete mapping in verifier code, and its mutation tests reject any byte change.

These standard texts make every declared expression resolvable offline. They do not replace package-specific copyright notices, license variants, or NOTICE files. The Docker build runs `scripts/collect_cargo_licenses.sh` against the actual locked Cargo source tree and ships those original per-package materials separately.
