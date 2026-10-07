# Operations manual

[简体中文](OPERATIONS.zh-CN.md) · [Back to README](../README.md)

This manual is for people who run and manage iOS Backup at home. Its steps have been checked against the current code, interface, and available test records. Use Docker Compose on a general Linux host, or follow the separate Synology guide. Chinese and English cover the same tasks. If this is your first installation, start with the illustrated [Quick start](QUICKSTART.md).

Before enabling a schedule, **complete one USB backup on a non-critical device** , reserving **space for a complete device backup** . There is no record of a successful physical-device restore using this manual yet. Check backup completion, file-list readability, and device restoration separately.

## Choose a task

| Task | What you should be able to confirm |
|---|---|
| [Check prerequisites and support boundaries](manual/overview.md) | Decide whether the host, access and feature maturity fit your needs |
| [Install, log in and change the administrator password](manual/installation.md) | Create a new instance and verify version and persistence |
| [Deploy on Synology DSM](manual/synology.md) | Check Container Manager, Compose, USB and directory permissions |
| [Pair over USB and complete a first backup](manual/usb-backup.md) | Respond to passcode or trust prompts, pair, start and confirm the actual result |
| [Schedule automatic backups](manual/scheduling.md) | Set window, interval, battery and charging rules, then observe a run |
| [Enable and validate Wi-Fi backup](manual/wifi.md) | Prepare on a computer, pair on the NAS, validate Wi-Fi and fall back to USB |
| [Manage backup encryption and passwords](manual/encryption.md) | Distinguish four credentials and handle existing encrypted backups |
| [Inspect backups and export file lists](manual/inspection.md) | Understand the 5,000-entry limit and what CSV contains |
| [Configure and verify notifications](manual/notifications.md) | Save rules, check test delivery and observe a real event |
| [Manage devices and storage](manual/device-storage.md) | Remove and re-add devices, check capacity and protect copies |
| [Back up, migrate and recover the instance](manual/instance-recovery.md) | Protect the key, configuration, pairing records and backup sets together |
| [Upgrade and rollback](manual/upgrade.md) | Pin images, validate the upgrade and retain a rollback path |
| [Evaluate experimental operations](manual/experimental.md) | Understand the requirements, risks, and tested scope of unpack, deletion, and device restore |
| [Troubleshoot by symptom](manual/troubleshooting.md) | Use the page and logs for simple checks, then decide whether to retry or request help |
| [Look up configuration, paths and applying changes](manual/configuration.md) | Distinguish program/Compose defaults and per-device settings |
| [Understand states, progress and errors](manual/states.md) | Interpret phases, inactivity deadlines and success criteria |
| [Investigate advanced Wi-Fi / NAT issues](manual/wifi-nat.md) | Investigate routed networks and idle connections after a USB comparison |

## Routine checks

Run in the actual Compose deployment directory, replacing `9000` if changed:

```bash
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl --user iosbackup http://127.0.0.1:9000/api/version
docker compose logs --tail=200 iosbackup
```

curl prompts for the administrator password. `/healthz` confirms only that HTTP responds, not device readiness, writable storage or recoverable backups. Logs may include device/network details; sanitize before sharing. There is no public log-download API.

## Access and data boundaries to retain

The privileged container uses host networking and USB/udev mounts. Deploy only on a trusted host and restricted LAN/VPN, keep authentication enabled, and do not expose it to the public Internet. Use suitable HTTPS and access control across networks.

Persist and protect `/backups`, all of `/configs` (including default `secret_key`, password, and digest), `/var/lib/lockdown`, all Compose configuration files, and external credentials in use. Keep administrator passwords, master keys, backup passwords, pairing records and user data out of public issues. The container image does not include this data. See [feature status](FEATURE_STATUS.md) for Core and Experimental boundaries.

## Documentation and verification materials

- [Development guide](DEVELOPMENT.md) and [offline testing guide](TESTING_GUIDE.en.md): reproduce software checks; these are not device-compatibility or restoration guarantees.
- [Support](../SUPPORT.md), [privacy](PRIVACY.md) and [security reporting](../SECURITY.md): collect sanitized context/logs for ordinary issues and use private reporting for vulnerabilities.

“Source checked” means the procedure matches the current implementation. Existing device test records apply only to the devices, versions, and operations recorded. Other devices need their own tests.
