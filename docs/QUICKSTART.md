# Quick start

[简体中文](QUICKSTART.zh-CN.md) · [Back to README](../README.md)

This guide starts the official Docker Beta on a trusted Linux host. The same image supports `linux/amd64` and `linux/arm64`. Windows and macOS native device operation is not supported or promised.

> The container uses `--privileged` and `--network host`. Privileged mode grants host-level access. Do not run unreviewed images, and do not expose the Web UI to the public Internet.

## 1. Prepare the host

Install Docker Engine, the Docker Compose plugin, Git, and OpenSSL. Choose a local filesystem and reserve enough space for a complete device backup.

```bash
git clone https://github.com/razeencheng/iosbackup.git
cd iosbackup
umask 077
mkdir -p data/backups data/configs data/lockdown
openssl rand -base64 24 > data/configs/admin_password
printf 'IOSBK_IMAGE=%s\nIOSBK_SECRET_KEY=%s\n' \
  'ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1' \
  "$(openssl rand -base64 32)" > .env
chmod 600 .env data/configs/admin_password
```

Because `umask 077` is set first, the three data directories are created with owner-only permissions. Keep those permissions when copying or restoring them. Do not rotate `IOSBK_SECRET_KEY` after storing notification secrets or backup passwords. Store an offline copy of `.env` and the admin password.

## 2. Review the required access

The supplied [compose.yaml](../compose.yaml) configures the equivalents of:

- `--privileged`
- `--network host`
- `/dev/bus/usb:/dev/bus/usb`
- `/run/udev:/run/udev:ro`
- persistent `/backups`, `/configs`, and `/var/lib/lockdown` volumes

Use only a trusted host and trusted LAN/VPN. The default Compose port is `9000`. Put any remote access behind an authenticated HTTPS reverse proxy and network ACL; do not publish the service directly to the Internet.

## 3. Start and verify

```bash
docker compose pull
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl -u "iosbackup:$(cat data/configs/admin_password)" \
  http://127.0.0.1:9000/api/version
```

Open `http://<linux-host>:9000/` and enter the password from `data/configs/admin_password`. HTTP does not encrypt the password in transit, so use it only on a trusted network; prefer HTTPS for access beyond the host.

## 4. Pair the first device over USB

1. Unlock the iPhone or iPad and keep its screen awake.
2. Connect it to the Linux host by USB.
3. Tap **Trust** on the device and enter its passcode when asked.
4. In iOS Backup, refresh the device list and start pairing if prompted.
5. Complete one USB backup on a non-critical device and verify the result before enabling a schedule.

If the Trust prompt does not appear, reconnect the cable, unlock the device, and check the USB/udev mounts. Pairing records persist in `/var/lib/lockdown`; losing that volume requires pairing again.

## 5. Configure backups

Use the device card to choose a backup window, interval, battery threshold, charging requirement, and a path under `/backups`. Leave Wi-Fi, encryption, and experimental operations off until the USB path is verified.

The following container paths are state, not disposable cache:

| Path | Contents |
|---|---|
| `/backups` | backup data and optional unpack output |
| `/configs` | backup settings, auth/CSRF files, encrypted notification/password secrets |
| `/var/lib/lockdown` | device pairing records |

Back up all three volumes and `.env` before every upgrade. Continue with the [Operations manual](OPERATIONS.md) and check [Feature status](FEATURE_STATUS.md) before enabling Preview or Experimental capabilities.
