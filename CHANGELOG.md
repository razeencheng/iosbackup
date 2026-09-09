# Changelog

This project follows Semantic Versioning. Release dates and source metadata are frozen in [`release/manifest.env`](release/manifest.env); this file does not duplicate the current release date.

## v1.5.1

### Fixed

- Recover previously registered Wi-Fi devices after heartbeat disconnects with a background worker independent of automatic backups and the browser UI.
- Reuse the last successfully registered network address for up to 24 hours in memory, including mDNS-discovered devices with no manual IP, with bounded retry backoff.
- Serialize network registration, use the correct netmuxd socket, and check the actual device registry before reporting IP-test success, including when the helper times out.
- Skip devices that are busy or being removed, and cancel recovery work when the application stops.

### Packaging

- Use the `v1.5.1` version without a prerelease suffix and update the release metadata and installation examples.
- Keep Wi-Fi and encryption classified as Preview. Recovery requires a reachable, paired device; it does not resume an interrupted backup or prevent device sleep and network interruptions.
- Keep the existing configuration and backup formats; no data migration is required.

## v1.5.0-beta.1

### Packaging

- Reorganized the Go source into a balanced `cmd/iosbackup` and `internal/*` package layout without changing the public HTTP/configuration contract.
- Prepared the official `ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1` image definition for Linux `amd64` and `arm64`.
- Added strict release-manifest parsing, a reproducible public-file allowlist, sensitive-content gates, and offline license verification.

### Security and safety

- Kept single-administrator authentication enabled in the Compose example and documented the privileged-container/network boundary.
- Kept restore disabled per device by default and added a strict, default-off global gate for local unpack and backup deletion.
- Preserved CSRF checks, destructive-action confirmations, bounded command output, per-device concurrency guards, and encrypted secret persistence.

### Documentation

- Replaced the legacy monolithic README with paired English/Chinese quick-start, operations, and feature-status guides.
- Classified USB/scheduled/read-only workflows as Core; Wi-Fi and encryption/change-password as Preview; restore/unpack/delete as Experimental.
- Documented health checks, Trust/pairing, persistent volumes, upgrade/rollback, troubleshooting, security reporting, support, privacy, licensing, and trademarks.

## v1.4.1 - 2026-08-10

### Security

- Restricted `test-ip` to safe IP literals and rejected loopback, link-local, multicast, broadcast, and unspecified addresses before dialing.
- Validated all enabled notification channels before persistence and stopped reporting false success for empty or failed test notifications.
- Required release builds to inject commit, build date, and public source URL.

### Fixed

- Cancelled the full command process group and released task locks after the Wi-Fi disconnect grace period.
- Replaced the unreliable device-side `unback` path with a local, atomic export for unencrypted `Manifest.db` backups.

## v1.4.0 - 2026-06-20

- Added Wi-Fi backup, bilingual UI, SSE status updates, backup inspection, and encryption Preview workflows.

## 中文摘要

`v1.5.1` 修复 Wi-Fi 心跳断连后设备长期离线的问题：后台按退避间隔重试注册，支持手动 IP 与已由 mDNS 成功注册的设备，测试 IP 以真实注册结果为准。地址仅在内存中保留最多 24 小时，应用重启后需要重新发现或使用手动 IP。此版本无需迁移配置或备份数据，Wi-Fi 仍属于预览功能。

`v1.5.0-beta.1` 完成了平衡分包、公开文件/敏感内容/许可证门禁和双语文档；功能分级为 Core（USB、定时、只读查看）、Preview（Wi-Fi、加密/改密）、Experimental（恢复、解包、删除，默认关闭）。当前发布日期只由 `release/manifest.env` 冻结，避免在多处复制后漂移。
