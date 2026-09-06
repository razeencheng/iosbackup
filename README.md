# iOS Backup

[简体中文](README.zh-CN.md)

iOS Backup is a local-first, single-administrator web application for backing up iPhone and iPad devices to a Linux host or NAS. It wraps libimobiledevice, usbmuxd2, and netmuxd in a privileged container and provides USB discovery, scheduled backups, read-only backup inspection, and optional notifications.

> **Docker Beta:** `v1.5.0-beta.1` is a public Beta, not a production-stable or disaster-recovery guarantee. Test it with non-critical devices and keep an independent backup before relying on it.

![iOS Backup console after a successful USB backup](docs/images/tutorial/09-usb-backup-completed.png)

This Raspberry Pi ARM64 test deployment completed a USB backup and its on-disk completion marker was checked. Decryption of the existing encrypted backup and device restore remain unverified. See the [illustrated quick start](docs/QUICKSTART.md) for real setup, progress, and completion screenshots.

## Feature status

- **Core:** USB discovery and pairing, manual and scheduled backup, and read-only backup information/file-list inspection.
- **Preview:** Wi-Fi backup and backup encryption enable/disable/change-password workflows. Expect device- and network-specific failures.
- **Experimental, disabled by default:** whole-device restore, local unpack, and local backup deletion. See [Feature status](docs/FEATURE_STATUS.md) before enabling any of them.

The local Web UI has no telemetry or remote frontend assets. Optional Telegram, SMTP, WeCom, Bark, and webhook notifications make outbound requests only after an administrator configures them. Built-in authentication is for one administrator, not multi-user access control.

## Official platform and security warning

The supported distribution is the Linux container for `linux/amd64` and `linux/arm64`. Native device operation on Windows or macOS is not promised. The Go module itself uses only the standard library, but the running application still depends on the libimobiledevice/netmuxd toolchain included in the image.

The container requires `--privileged`, `--network host`, `/dev/bus/usb:/dev/bus/usb`, and `/run/udev:/run/udev:ro`. **Privileged mode grants host-level access. Run it only on a trusted host and trusted network. Do not expose the Web UI directly to the public Internet.** Authentication does not replace a firewall, VPN, or authenticated HTTPS reverse proxy.

## Quick start

Requirements: Linux with Docker Engine and Docker Compose, an unlocked iPhone/iPad, and a data directory on a filesystem with enough space for a complete device backup.

```bash
git clone https://github.com/razeencheng/iosbackup.git
cd iosbackup
umask 077
mkdir -p data/backups data/configs data/lockdown
openssl rand -base64 24 > data/configs/admin_password
printf 'IOSBK_IMAGE=%s\nIOSBK_SECRET_KEY=%s\n' \
  'ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1' \
  "$(openssl rand -base64 32)" > .env
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:9000/healthz
```

Because `umask 077` is set first, the three data directories are created with owner-only permissions. Keep those permissions when copying or restoring them.

Open `http://<linux-host>:9000/` on the trusted network and sign in with the password in `data/configs/admin_password`. Connect and unlock the device by USB, tap **Trust** on the device, then refresh/pair it in the UI. Before enabling a schedule, complete one USB backup on a non-critical device and verify the result.

The Compose deployment uses the required equivalents of `--privileged` and `--network host`, mounts `/dev/bus/usb:/dev/bus/usb` and `/run/udev:/run/udev:ro`, and persists all three state locations:

| Container path | Purpose | Back up before upgrade |
|---|---|---|
| `/backups` | iOS backup data | Yes |
| `/configs` | settings, authentication, CSRF state, encrypted secrets | Yes |
| `/var/lib/lockdown` | device pairing records | Yes |

Keep `.env`, the admin password, and all three volumes private. Keep `IOSBK_SECRET_KEY` unchanged after secrets or backup passwords have been stored; losing or changing it makes those stored secrets unreadable.

For a complete walkthrough, see [Quick start](docs/QUICKSTART.md).

## Upgrade and rollback

Back up `data/backups`, `data/configs`, `data/lockdown`, and `.env` first. Then set `IOSBK_IMAGE` in `.env` to an explicit release tag, run `docker compose pull`, and recreate the service with `docker compose up -d`. Verify `/healthz`, `/api/version`, logs, pairing, and a complete USB backup on a non-critical device.

To rollback, restore the previous explicit image tag and run `docker compose up -d` again. Do not roll back persisted data blindly: restore the volume snapshot only when the release notes say the on-disk format is incompatible. Never substitute `:latest` for a reviewed tag.

See [Operations](docs/OPERATIONS.md) for the full upgrade, rollback, volume-backup, and log procedures.

## Troubleshooting

- **No device:** unlock it, reconnect USB, tap **Trust**, verify the USB and udev mounts, then refresh.
- **`mobilebackup2 (-4)` or Wi-Fi disconnect:** reconnect the device, retry over USB, and treat Wi-Fi as Preview.
- **Locked-device/passcode error:** keep the device unlocked during pairing and the start of backup.
- **No space/read-only/permission denied:** check free space and host permissions for `data/backups`, `data/configs`, and `data/lockdown`.
- **Container starts but UI is unavailable:** check `docker compose logs --tail=200 iosbackup` and `curl -fsS http://127.0.0.1:9000/healthz`.

The detailed runbook is in [Operations](docs/OPERATIONS.md).

## Documentation

- [Quick start](docs/QUICKSTART.md)
- [Operations manual](docs/OPERATIONS.md)
- [Feature status](docs/FEATURE_STATUS.md)
- [Notification guide (Chinese)](docs/NOTIFICATION_README.md)
- [Webhook guide (Chinese)](docs/WEBHOOK_GUIDE.md)
- [Privacy](docs/PRIVACY.md)
- [Security policy](SECURITY.md)
- [Support policy](SUPPORT.md)
- [Changelog](CHANGELOG.md)

## Development and contributions

The source layout is `cmd/iosbackup` → `internal/app` → leaf packages. A local source build still needs the external device tools at runtime:

```bash
go test -count=1 ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o iosbackup ./cmd/iosbackup
```

Contributions are licensed inbound = outbound under `AGPL-3.0-only`; no CLA, DCO, or `Signed-off-by` is required. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License and trademarks

Project source is licensed under [GNU AGPL v3.0 only](LICENSE), SPDX `AGPL-3.0-only`. Container components keep their own licenses; see [Licensing](docs/LICENSING.md) and [Third-party notices](THIRD_PARTY_NOTICES.md).

Copyright (C) 2025-2026 Razeen Cheng. This project is not affiliated with, sponsored by, or endorsed by Apple Inc. See [TRADEMARKS.md](TRADEMARKS.md).
