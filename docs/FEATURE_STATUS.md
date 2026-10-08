# Feature status

[简体中文](FEATURE_STATUS.zh-CN.md) · [Back to README](../README.md)

The tiers below distinguish everyday features from those that require experimental settings. Whichever features you use, you still need an independently tested recovery plan.

## Core

| Capability | Current boundary |
|---|---|
| USB discovery and pairing | For first pairing, connect over USB and respond on the device: enter its passcode when prompted, or unlock it and accept Trust. Keeping it unlocked or its screen on is not required. |
| Manual USB backup | Use USB for your first backup. Keep the device connected and verify completed backups. |
| Scheduled backup | Uses per-device time window, interval, battery, and charging settings. A skipped run is retried on a later scheduler check. |
| Read-only inspection | Backup information and file-list/CSV views are read-only. They can take minutes for a large backup. |
| Local Web UI and single-admin auth | No telemetry or remote frontend assets. Authentication is single-administrator only and does not make public exposure safe. |
| Optional notifications | Telegram, SMTP, WeCom, Bark, and webhook notifications are sent only after configuration, and delivery is not guaranteed. Notification failure does not fail a backup. |
| Wi-Fi backup | Requires successful USB pairing first, valid persisted lockdown records, reachable networking, and device support for the required wireless communication. Long transfers can fail after sleep or reconnect events; USB is the fallback. |
| Encryption enable/disable | Requires a valid master key (automatically saved as `/configs/secret_key` by default, or the original advanced-configured key), an online device, and the backup password. Respond to any passcode or authorization prompt on the phone. Keep the device password and master key outside this application. |
| Backup password change | Requires the old and new password and an online device. Losing a backup password can make encrypted backups unusable. |

Core capabilities still require validation on your device and network. After changing devices or upgrading the device OS, complete a backup on a non-critical device before enabling a schedule. Encrypted-backup readability and device recovery require separate validation.

## Experimental and disabled by default

| Capability | Default gate | Risk |
|---|---|---|
| Whole-device restore | Per-device `restore_enabled=false` | Destructive device operation. It also requires CSRF protection and explicit confirmations. It is not yet a tested routine recovery method. |
| Local unpack | `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false` | Only complete, unencrypted backups with a plain SQLite `Manifest.db` are supported. It creates a second copy and can consume substantial disk space. |
| Delete local backup | `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false` | Irreversibly removes that device's local backup after device-name confirmation. |

The global experimental switch accepts only the strict value `true`. When false, the unpack/delete controls are hidden and their APIs return `403` before backup state or filesystem access. Enabling the switch does not remove CSRF, validation, concurrency, or confirmation checks.

Do not enable restore, unpack, or delete on important data until you have an isolated test, a current copy of `/backups`, `/configs`, and `/var/lib/lockdown`, and a rollback plan.
