# Feature status

[简体中文](FEATURE_STATUS.zh-CN.md) · [Back to README](../README.md)

`v1.5.0-beta.1` is a Docker Beta. These tiers describe the current support boundary; none is a guarantee that a backup can replace an independently tested recovery plan.

## Core

| Capability | Current boundary |
|---|---|
| USB discovery and pairing | First pairing requires an unlocked device, a USB connection, and accepting Trust on the device. |
| Manual USB backup | Primary supported data path. Keep the device connected and verify completed backups. |
| Scheduled backup | Uses per-device time window, interval, battery, and charging settings. A skipped run is retried on a later scheduler check. |
| Read-only inspection | Backup information and file-list/CSV views are read-only. They can take minutes for a large backup. |
| Local Web UI and single-admin auth | No telemetry or remote frontend assets. Authentication is single-administrator only and does not make public exposure safe. |
| Optional notifications | Telegram, SMTP, WeCom, Bark, and webhook delivery is best effort and only sends after explicit configuration. Notification failure does not fail a backup. |

## Preview

| Capability | Current boundary |
|---|---|
| Wi-Fi backup | Requires successful USB pairing first, valid persisted lockdown records, reachable networking, and compatible device behavior. Long transfers can fail after sleep or reconnect events; USB is the fallback. |
| Encryption enable/disable | Requires `IOSBK_SECRET_KEY`, an online unlocked device, and the backup password. Keep the device password and master key outside this application. |
| Backup password change | Requires the old and new password and an online device. Losing a backup password can make encrypted backups unusable. |

Preview is not production-stable. Verify each device and OS release with a non-critical backup before enabling a schedule.

## Experimental and disabled by default

| Capability | Default gate | Risk |
|---|---|---|
| Whole-device restore | Per-device `restore_enabled=false` | Destructive device operation. It also requires CSRF protection and explicit confirmations. Not a routine disaster-recovery promise. |
| Local unpack | `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false` | Only complete, unencrypted backups with a plain SQLite `Manifest.db` are supported. It creates a second copy and can consume substantial disk space. |
| Delete local backup | `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false` | Irreversibly removes that device's local backup after device-name confirmation. |

The global experimental switch accepts only the strict value `true`. When false, the unpack/delete controls are hidden and their APIs return `403` before backup state or filesystem access. Enabling the switch does not remove CSRF, validation, concurrency, or confirmation checks.

Do not enable restore, unpack, or delete on important data until you have an isolated test, a current copy of `/backups`, `/configs`, and `/var/lib/lockdown`, and a rollback plan.
