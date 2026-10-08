# Licensing and source availability

## Project source

iOS Backup project source is licensed under the GNU Affero General Public License v3.0 only, SPDX `AGPL-3.0-only`. The authoritative terms are in [LICENSE](../LICENSE).

The AGPL permits personal use, commercial use, charging fees, and internal company use. If you distribute a modified version, or let users interact with a modified version over a network, the AGPL can require you to offer the complete corresponding source, license, and change notices for that version. This summary is not legal advice.

Contributions are accepted inbound = outbound under `AGPL-3.0-only`; there is no CLA or DCO requirement. The license does not grant trademark rights; see [TRADEMARKS.md](../TRADEMARKS.md).

## Build identity and corresponding source

The Web UI and `GET /api/version` expose `Version`, `BuildDate`, `Commit`, `SourceURL`, and `License`. Release builds obtain version/date/source from the strictly parsed [release/manifest.env](../release/manifest.env), while the commit is the public release commit:

```bash
IOSBK_VERSION=$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_VERSION)
IOSBK_BUILD_DATE=$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_BUILD_DATE)
IOSBK_SOURCE_URL=$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_SOURCE_URL)
IOSBK_COMMIT=$(git rev-parse HEAD)
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w \
  -X iosbackup/internal/buildinfo.Version=$IOSBK_VERSION \
  -X iosbackup/internal/buildinfo.BuildDate=$IOSBK_BUILD_DATE \
  -X iosbackup/internal/buildinfo.Commit=$IOSBK_COMMIT \
  -X iosbackup/internal/buildinfo.SourceURL=$IOSBK_SOURCE_URL" \
  -o iosbackup ./cmd/iosbackup
```

An official Git tag, image tag/digest, runtime `Commit`, and public source commit must refer to the same release. `Commit=unknown`, an empty/placeholder source URL, or manifest drift is not acceptable for a release build. Users can open `<SourceURL>/tree/<Commit>` to locate the exact corresponding source.

That source includes the Go code and embedded UI, Dockerfile/build scripts, patches under `third_party/patches/`, locked component metadata, module files, and the license/notice materials needed to rebuild the published work. A distributor of a modified image must point `SourceURL` to the modified corresponding source, not this upstream unchanged tree.

## Container components

The Go application uses only the Go standard library, but the distributed container is not dependency-free. It builds libimobiledevice-family components, libgeneral, usbmuxd2, and netmuxd from pinned commits and includes Debian runtime libraries and SQLite. Their independent licenses remain in force.

Authoritative component URLs, commits, declared licenses, base-image digests, patches, and license mappings are recorded in [third_party/components.lock.json](../third_party/components.lock.json). Detailed attribution and Cargo audit boundaries are in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md). The image copies reviewed materials under `/usr/share/licenses/iosbackup/`.

The repository also records architecture-specific runtime-closure snapshots. Every published image still needs a current SBOM, runtime-closure check, source/relinking materials, package copyright files, attribution verification, and digest/provenance/signature evidence. A previous snapshot or inventory is not proof of the current image.

## Privacy boundary

Embedded UI assets are local, there is no telemetry, and data is not automatically uploaded to the maintainer. Only administrator-configured notifications are designed to connect to third-party Internet services. Wi-Fi operation still lets netmuxd discover devices on the local network, including through mDNS, and connect to reachable iOS devices; that traffic is not telemetry. See [PRIVACY.md](PRIVACY.md).

## 中文摘要

iOS Backup 项目源码采用 GNU Affero General Public License v3.0 only，SPDX 标识 `AGPL-3.0-only`，正式条款见 [LICENSE](../LICENSE)。AGPL 允许个人、商业、收费和公司内部使用；分发修改版本或通过网络提供修改版本时，可能需要提供该版本对应的完整源码、许可证和修改说明。本文不是法律意见。

贡献采用 inbound = outbound，不要求 CLA/DCO。许可证不授予商标权。运行中的页面与 `/api/version` 会显示版本、日期、commit、源码 URL 和许可证；官方 tag、镜像与公开源码必须一一对应。Go 应用只用标准库，但容器仍包含适用各自许可证的外部组件，具体见 [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) 与 [third_party/components.lock.json](../third_party/components.lock.json)。

内嵌界面资源保存在本地，项目没有遥测，也不会自动向维护者上传数据。只有管理员自行配置的通知功能会按设计连接互联网第三方服务。使用 Wi-Fi 时，netmuxd 仍会在局域网内发现设备（包括 mDNS）并连接可达的 iOS 设备；这类通信不是遥测。详见 [PRIVACY.md](PRIVACY.md)。
