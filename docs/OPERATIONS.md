# Operations manual

[简体中文](OPERATIONS.zh-CN.md) · [Back to README](../README.md)

This runbook covers the official Linux container. It does not promise native Windows/macOS device support or guaranteed restore. Keep an independently tested backup.

## Security boundary

The service needs `--privileged`, `--network host`, `/dev/bus/usb:/dev/bus/usb`, and `/run/udev:/run/udev:ro`. Privileged mode grants host-level access. Restrict the container to a trusted host and trusted LAN/VPN, keep authentication enabled, and do not expose it to the public Internet. Use an authenticated HTTPS reverse proxy if access must cross the trusted network.

Persist and protect all three data locations:

- `/backups`: device backups and optional unpack output
- `/configs`: settings, authentication, CSRF state, and encrypted secrets
- `/var/lib/lockdown`: pairing records

Treat `.env`, `data/configs/admin_password`, `IOSBK_SECRET_KEY`, pairing records, device identifiers, and backup contents as secrets. Do not include them in an Issue or log excerpt.

## Daily checks

```bash
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl -u "iosbackup:$(cat data/configs/admin_password)" \
  http://127.0.0.1:9000/api/version
docker compose logs --tail=200 iosbackup
```

`/healthz` proves only that the HTTP process is responding. It does not prove that a device is paired, a backup is readable, or Wi-Fi transport is healthy. Check the UI status and a completed test backup as well.

Application and device logs go to container stdout. Use `docker compose logs`; there is no public log-download API. Redact device identifiers, network addresses, notification targets, and file paths before sharing excerpts.

## First USB pairing and Trust

1. Unlock the device and keep the screen awake.
2. Connect it directly by a known-good USB data cable.
3. Tap **Trust** and enter the device passcode.
4. Refresh the UI. If prompted, start pairing and accept Trust again.
5. Keep the device connected until the first USB backup completes.

If pairing repeatedly fails, reconnect the USB cable, unlock the device, and retry. Confirm that `/dev/bus/usb:/dev/bus/usb` and `/run/udev:/run/udev:ro` are present. Do not delete `/var/lib/lockdown` as routine troubleshooting; that discards pairing state and forces every device to pair again.

## Scheduled backups

The scheduler checks eligible devices against the configured backup window, minimum interval, battery threshold, and charging requirement. User-visible schedule times use Beijing time. A per-device lock prevents overlapping backup/check work, and refresh is blocked while a backup is active. If a condition is not met, the device is considered again on a later scheduler check.

Start with a successful manual USB backup. Then enable the schedule for one device, observe at least one complete run, and confirm the resulting backup information before adding more devices.

## Backup progress semantics

The main bar is the overall progress for the backup. When the device tool reports it, a separate current file progress value restarts for each file. The UI deliberately does not display the filename and does not estimate remaining time. If the Wi-Fi device briefly disappears, the state can show **waiting for device reconnection** while retaining the last trusted progress. Once a task has ended, it is not resumed automatically; start a new backup after connectivity is restored.

### Inactivity timeouts

The UI distinguishes device authorization, sending data, receiving data, and waiting for the device, and shows the last backup activity time in Beijing time. Incremental backups first send the existing manifest to the device. A large encrypted manifest can take several minutes to process; neither overall 0% nor current-file 100% alone indicates a stall. Overall 100% still requires the tool to acknowledge success for this job.

The bundled `idevicebackup2` reports payload-free protocol counters. Only transferred bytes or a successfully received protocol message update activity; discovery, heartbeats, power-assertion renewals, and repeated progress displays do not. Custom unpatched tools fall back to recognized file changes, byte counts, and progress changes, with the limitations of their output.

| Startup environment variable | Default | Phase |
| --- | --- | --- |
| `IOSBK_BACKUP_AUTHORIZATION_TIMEOUT` | `5m` | Waiting for device passcode authorization |
| `IOSBK_BACKUP_INACTIVITY_TIMEOUT` | `10m` | Sending or receiving without new activity |
| `IOSBK_BACKUP_PREPARATION_TIMEOUT` | `30m` | Session setup, device processing, or waiting for the next response |

Each accepts a Go duration from `1s` to `24h`; zero cannot disable the guard. Persist changes in Compose environment settings and recreate the container. These are inactivity limits, not total backup deadlines. Increase the preparation limit for slower devices when needed.

A timeout terminates the old process group and reports `backup_stalled`, preserves the backup set and last successful time, and releases the job for retry. Check device prompts and connectivity before retrying. No immediate retry loop is added; future automatic backups still follow the existing scheduler conditions. Use USB as a comparison if new sessions repeatedly stop at the same point. This guard bounds an indefinite wait; it does not establish why iOS originally stopped responding.

## Wi-Fi prerequisites and fallback

Wi-Fi backup is Preview. Before using it:

1. Complete USB pairing and preserve `/var/lib/lockdown`.
2. Enable wireless visibility once in Finder on a Mac or Apple Devices/iTunes on Windows; this prepares the iOS device but does not mean iOS Backup itself runs natively on those systems.
3. Put the device and host on a mutually reachable network; host networking is required for discovery.
4. Keep the default netmuxd backend unless diagnosing a known compatibility issue.
5. Keep `IOSBK_WIFI_POWER_ASSERTION=true` unless a documented device incompatibility requires a temporary diagnostic fallback.
6. Verify one manual Wi-Fi backup before scheduling it.

If discovery briefly disappears during an active stream, the application allows a bounded reconnect grace period. A finished failed task is not resumed automatically. For `Could not receive from mobilebackup2 (-4)`, repeated reconnect, sleep, or lock-screen failures, unlock/reconnect the device and retry over USB. USB is the supported fallback; do not interpret a visible device card as proof that an active Wi-Fi stream is healthy.

## Back up the volumes

Before an upgrade or an experimental operation:

1. Confirm that no backup, unpack, delete, or restore operation is running.
2. Stop the service so configuration and pairing files cannot change during the snapshot.
3. Copy `.env` and all three data directories to protected storage.

Reserve enough free space for a complete device backup, plus any temporary copy created by a volume snapshot or Experimental unpack operation.

Example archive from the repository directory:

```bash
docker compose stop
umask 077
tar -czf iosbackup-state-backup.tgz \
  .env data/backups data/configs data/lockdown
docker compose start
```

The archive contains credentials, pairing records, and personal data. Encrypt it at rest and test that it can be listed and restored. A container image is not a backup of these volumes.

## Upgrade

1. Read [CHANGELOG.md](../CHANGELOG.md) and [Feature status](FEATURE_STATUS.md).
2. Make and verify the volume backup above.
3. Change `IOSBK_IMAGE` in `.env` to an explicit reviewed tag such as `ghcr.io/razeencheng/iosbackup:v1.5.2`.
4. Pull and recreate the service.
5. Verify health, build identity, logs, pairing, and read-only inspection; then complete one USB backup on a non-critical device and verify the result.

```bash
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:9000/healthz
docker compose logs --tail=200 iosbackup
```

Do not use an unreviewed floating image tag.

## Rollback

Set `IOSBK_IMAGE` back to the exact previously working tag or digest and run `docker compose up -d`. Verify `/api/version` after the rollback. Keep the current volume snapshot until the older application has been checked.

Do not automatically restore old `/backups`, `/configs`, or `/var/lib/lockdown` over newer data. Restore a volume snapshot only when release notes identify an incompatible format or the current data is known to be damaged, and preserve the failed state for diagnosis.

## Experimental operations

Whole-device restore remains off per device with `restore_enabled=false`. Local unpack and local backup deletion remain off globally with `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false`; the Compose file explicitly forwards that default. Only set it to strict `true` after reviewing [Feature status](FEATURE_STATUS.md) and taking a volume backup.

Even when enabled, unpack/delete still require authenticated UI/API access, CSRF protection, validation, and operation-specific confirmation. Restore has separate per-device opt-in and multiple confirmations. None is a routine recovery promise.

## Troubleshooting

### Device not visible or pairing fails

- Unlock the device, reconnect a known-good USB data cable, and accept Trust.
- Check that the USB and udev mounts exist in the container configuration.
- Review `docker compose logs --tail=200 iosbackup` for pairing/usbmux messages.
- Preserve `/var/lib/lockdown`; replace it only as a deliberate last resort with a backup available.

### Device locked or passcode error

Unlock the device before pairing, encryption changes, or backup startup. A lock-screen transition can also affect Preview Wi-Fi behavior. Reconnect and retry over USB if the command has already failed.

### `mobilebackup2 (-4)`, Wi-Fi disconnect, or reconnect loop

Do not repeatedly restart every component while a backup is running. Let the active task reach a terminal state, unlock and reconnect the device, inspect the preceding heartbeat/stream logs, then retry via USB. If USB works, keep Wi-Fi disabled for that device and report a sanitized Preview issue.

### Disk space

Check free space and inode availability on the filesystem behind `/backups`. Unpack creates a second copy and can require substantial additional disk space. A low-space failure should be fixed before retry; do not delete the only known-good backup as cleanup.

### Permission or read-only filesystem

Verify that host directories exist, are mounted at `/backups`, `/configs`, and `/var/lib/lockdown`, and are writable by the container. Keep the container root filesystem read-only; fix the bind-mount path/permission instead of weakening unrelated host permissions.

### UI unavailable but container is running

Check `/healthz`, port `9000`, host firewall rules, and `docker compose logs`. With host networking there is no separate port-publishing rule. If a reverse proxy is used, test the local endpoint first, then proxy authentication/TLS separately.

For support boundaries and safe report contents, see [SUPPORT.md](../SUPPORT.md) and [SECURITY.md](../SECURITY.md).
