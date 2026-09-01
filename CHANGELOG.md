# Changelog

This project follows Semantic Versioning. Release dates and source metadata are frozen in [`release/manifest.env`](release/manifest.env); this file does not duplicate the current release date.

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

`v1.5.0-beta.1` 完成了平衡分包、公开文件/敏感内容/许可证门禁和双语文档；功能分级为 Core（USB、定时、只读查看）、Preview（Wi-Fi、加密/改密）、Experimental（恢复、解包、删除，默认关闭）。当前发布日期只由 `release/manifest.env` 冻结，避免在多处复制后漂移。
