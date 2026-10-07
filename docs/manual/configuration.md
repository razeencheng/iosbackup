# Configuration, paths and applying changes

[简体中文](configuration.zh-CN.md) · [Manual index](../OPERATIONS.md)

This reference describes application settings and the bundled `compose.yaml`.

## Apply configuration changes

Device and notification settings take effect when saved in the UI. Edit process parameters in `compose.yaml` under `services.iosbackup.environment`, and select the image using the same service's `image` field. These settings are read at startup: wait for active jobs, then recreate the container. Saving the file or running `docker compose restart` alone does not update its environment. On Synology, edit the project's YAML Configurations in Container Manager and follow its redeployment prompts.

Write ordinary values directly, such as `PORT: "9000"` and `LOG_LEVEL: "INFO"`. Add only settings you need to adjust and preserve the rest. The application generates and saves default passwords and the master key; you need not enter credentials.

For example, add this entry under the existing `services.iosbackup.environment` to allow 45 minutes of preparation inactivity. Preserve the rest of your configuration; this fragment is not a replacement file:

```yaml
services:
  iosbackup:
    environment:
      IOSBK_BACKUP_PREPARATION_TIMEOUT: "45m"
```

```bash
docker compose config --quiet
docker compose up -d --force-recreate iosbackup
docker compose logs --tail=100 iosbackup
curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
```

Replace `9000` if you use a custom port. Configuration validation does not prove mounts, devices or passwords work; log in again, inspect devices and verify the affected task. Do not share full `docker compose config` output: it can expand the master key.

## Process environment variables

Only the password and master key are credentials themselves, but paths and network ranges may also disclose installation details. Booleans accept `true` / `false` (not `1` / `0`). Durations use forms such as `30s`, `10m`, `1h`; the listed deadlines cannot be disabled with zero.

| Variable | Program default | Compose default | Purpose and range |
|---|---|---|---|
| `IOSBK_LISTEN_ADDR` | `127.0.0.1` | `0.0.0.0` | Listening IP literal; hostnames are not accepted. |
| `PORT` | `8080` | `9000` | Integer 1–65535; edit PORT directly under environment. |
| `LOG_LEVEL` | `INFO` | `INFO` | DEBUG / INFO / WARN / ERROR; unknown values fall back to INFO. Edit LOG_LEVEL directly under environment. |
| `IOSBK_CONFIGS_DIR` | `/configs` | `/configs` | Absolute directory other than /; must be writable and persistent. |
| `IOSBK_BACKUPS_DIR` | `/backups` | `/backups` | Absolute directory other than /; per-device save locations must stay within it. |
| `IOSBK_AUTH_ENABLED` | `false` | `true` | true / false. Keep authentication enabled in the recommended deployment. |
| `IOSBK_ADMIN_PASSWORD_FILE` | `unset` | `unset` | Optional absolute custom-password file path, at least 24 bytes; default password and digest are automatically managed below. Reading removes one trailing CR/LF sequence. |
| `IOSBK_SECRET_KEY` | `unset` | `unset` | Optional environment master key: exactly 32 bytes in standard Base64; mutually exclusive with the key-file override. |
| `IOSBK_SECRET_KEY_FILE` | `unset` | `unset` | Optional absolute container key-file path; with neither override, read or initially create /configs/secret_key. |
| `IOSBK_INSECURE_ALLOW_REMOTE` | `false` | `false` | true / false; explicit exception for unauthenticated non-loopback listening, not a secure access method. |
| `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS` | `false` | `false` | true / false; gates local unpack and backup deletion. Device restore has a separate per-device gate. |
| `IOSBK_MAX_HEAVY_JOBS` | `1` | `1` | Integer 1–2; currently limits concurrent backup jobs, not a universal limit on all experimental operations. |
| `IOSBK_PRESENCE_INTERVAL` | `10s` | `10s` | 1s–10m; device presence polling interval, not proof of active transfer. |
| `IOSBK_DEVICE_DISCONNECT_GRACE` | `30s` | `30s` | 5s–10m; grace for temporary disappearance, not a way to resume terminated jobs. |
| `IOSBK_BACKUP_AUTHORIZATION_TIMEOUT` | `5m` | `5m` | 1s–24h; inactivity deadline while waiting for device authorization. |
| `IOSBK_BACKUP_INACTIVITY_TIMEOUT` | `10m` | `10m` | 1s–24h; inactivity deadline during sending or receiving. |
| `IOSBK_BACKUP_PREPARATION_TIMEOUT` | `30m` | `30m` | 1s–24h; inactivity deadline during session preparation or device processing. |
| `IOSBK_SCHEDULER_INTERVAL` | `30s` | `30s` | 1s–24h; interval for checking automatic-backup eligibility, not the per-device backup interval. |
| `IOSBK_WIFI_BACKEND` | `netmuxd` | `netmuxd` | netmuxd / usbmuxd2; Wi-Fi backend. Normally retain the default. |
| `IOSBK_NETMUXD_LOG_LEVEL` | `warn` | `warn` | error / warn / info / debug / trace; netmuxd logging only. |
| `IOSBK_WIFI_POWER_ASSERTION` | `true` | `true` | true / false; the app requests a Wi-Fi power assertion automatically, without requiring the user to keep the screen on. Phone authorization prompts still need a response; unattended operation is not guaranteed across all devices and networks. |
| `IOSBK_MIN_FREE_BYTES` | `0` | `0` | Unsigned 64-bit integer; currently parsed but not connected to a free-space check. Setting it does not prevent a full disk. |
| `IOSBK_WEBHOOK_ALLOW_CIDRS` | `—` | `—` | Comma-separated private-network CIDRs; allow only the required range. Loopback and link-local destinations remain blocked. |

With authentication enabled, the rules below determine whether to use an existing password or generate one for a new instance. See [installation and login](installation.md) for retrieval. Web sessions last 12 hours and live in memory; a restart requires login again.

Set the image directly in `services.iosbackup.image` in [compose.yaml](../../compose.yaml), defaulting to `ghcr.io/razeencheng/iosbackup:latest`. To pin a version, replace that value with an official version tag or digest; change it back to `latest` to follow updates. See the [installation guide](installation.md) for deployment and automatic initialization. Changing `TZ` does not change device schedules; schedules and user-visible timestamps use Beijing time (UTC+8).

## Advanced credential configuration

The default deployment needs none of these variables. The app sets its dedicated configuration directory to `0700`; generated password, digest, and master-key files use `0600`. Saving the initial password and key requires a **local filesystem supporting hard links** . Unsupported filesystems cause startup failure, without falling back to writes that might overwrite existing files. NAS shared-folder ACLs should also restrict access to administrators.

The default `secret_key` and ciphertext `secrets.enc` share the configuration directory for complete backups. This encryption avoids exposing secrets through API output or a ciphertext file alone; it **does not protect against disclosure of the whole configuration directory** . Use an external key file below if separate storage is needed, and continue protecting the host and backups.

### Administrator-password precedence and customization

1. A nonempty `IOSBK_ADMIN_PASSWORD_FILE` takes priority and must be an absolute container file path. Missing/unreadable files or passwords shorter than 24 bytes fail startup.
2. Without a password file you selected, existing `auth_credentials.json` is retained. If default `admin_password` also exists, it must match the digest.
3. With only default `admin_password`, the app reuses it and creates the digest. Only when neither exists does it generate a random password, restricted file, and digest; only that generation logs the password. Disabled authentication generates no administrator password.

If the default file and digest disagree, restore a matching copy or create a separate password file and select it in the configuration; do not overwrite the default file or delete the digest experimentally. To change the administrator password, run this advanced example in an **already initialized deployment directory** (only this example requires host OpenSSL). The configuration directory normally belongs to container root, so write with sudo instead of changing the directory's owner:

```bash
sudo sh -c '
  set -eu
  umask 077
  set -C
  openssl rand -base64 32 > data/configs/custom_admin_password
'
```

This command refuses to overwrite an existing file. If interrupted, inspect the new file before handling it; leave the original default credentials intact. Merge this entry into the existing Compose environment, preserving other settings:

```yaml
services:
  iosbackup:
    environment:
      IOSBK_ADMIN_PASSWORD_FILE: "/configs/custom_admin_password"
```

Apply the validation and container-recreation commands at the top of this page, then read the new password with host-side `sudo cat data/configs/custom_admin_password` and sign in. Do not share the output. After selecting the custom file, content changes require restart; variable or mount changes require recreation. Setting this entry to `""` returns to the retained default password; the custom password is not automatically written back to its digest.

### Customize the master key

- Choose **only one** nonempty `IOSBK_SECRET_KEY` or `IOSBK_SECRET_KEY_FILE`; supplying both fails startup. Empty values mean unset. A key must contain exactly 32 random bytes encoded with standard Base64.
- If the environment key or key-file setting you supply is invalid, startup fails rather than using a default. A file uses an absolute container path and needs a mount when external; a host path alone is insufficient. A key you supply is not automatically copied into the default file.
- With neither override, the app reads `/configs/secret_key`. It generates a key only when both that file and `secrets.enc` are absent. Existing ciphertext without its original key, or any stored secret that cannot decrypt, causes startup failure while preserving the files.
- When you supply a key, the app does not switch to default `secret_key`. Changing where the key is stored must preserve **the same original key** ; replacing it with a random key is not a supported way to update it.

To store the master key separately, first prepare a protected host file containing **the original key** , then merge these entries into the existing service, preserving the rest of Compose. Replace the example host path with the actual file. An advanced administrator may also supply a correctly formatted random key for a fresh instance.

```yaml
services:
  iosbackup:
    environment:
      IOSBK_SECRET_KEY: ""
      IOSBK_SECRET_KEY_FILE: "/run/secrets/iosbackup-secret-key"
    volumes:
      - /absolute/protected/path/secret_key:/run/secrets/iosbackup-secret-key:ro
```

Keep the file administrator-readable only. Wait for active jobs, back up the original configuration, check Compose and override files for conflicts, and recreate the container. Confirm existing secrets remain readable before continuing. Back up external keys separately; copying `/configs` alone is insufficient. Generated passwords, keys, auth/CSRF files, and first-start logs must not enter Git, image build contexts, or public reports.

## Per-device settings

Edit these through Backup settings; see [automatic backups](scheduling.md) for a complete task.

| Field | New-device default | Meaning |
|---|---|---|
| `start_time / end_time` | `18:00 / 06:00` | Beijing time in HH:mm; may cross midnight. |
| `backup_interval` | `24` | Minimum interval in hours, 1–720. UI offers 6/12/24/48 hours. |
| `min_battery_level` | `20` | 0–100; manual backups also check battery/charging conditions. |
| `only_when_charging` | `true` | Start backups only while charging. |
| `auto_backup_enabled` | `false` | After saving, later scheduler checks decide whether to start; this is not a run-now button. |
| `backup_directory` | `/backups` | Defaults to the backup root; use a container path, not a host path or a path outside that root. |
| `network_address` | `空 / empty` | Optional device IP literal; hostnames and special addresses such as loopback are rejected. |
| `restore_enabled` | `false` | Separate per-device restore permission; leave off for routine backup. |

`udid`, `name` and `device_type` identify/display a device; `last_backup` and `last_backup_connection` record the most recent success; `removed_at` records removal. Do not edit them to control scheduling. Re-adding a removed device may resume its saved automatic schedule.

## Persistent paths

Host paths below are relative to the Compose deployment directory; customized mounts take precedence. Use [instance backup and recovery](instance-recovery.md) for migration.

| Host | Container | Purpose |
|---|---|---|
| `data/backups/` | `/backups` | Per-device <UDID>/ backup sets; not automatic multi-generation snapshots. |
| `data/configs/backup_configs.json` | `/configs/backup_configs.json` | Device settings and last success times; versioned JSON, do not edit while running. |
| `data/configs/notification_configs.json` | `/configs/notification_configs.json` | Notification channels, rules and templates; secrets are stored separately. |
| `data/configs/secrets.enc` | `/configs/secrets.enc` | Encrypted backup passwords and notification secrets; requires the original master key. |
| `data/configs/admin_password` | `/configs/admin_password` | Automatically generated plaintext administrator password, 0600; explicit custom sources may live elsewhere. |
| `data/configs/auth_credentials.json` | `/configs/auth_credentials.json` | Default authentication digest; it cannot recover password plaintext. |
| `data/configs/secret_key` | `/configs/secret_key` | Automatically generated and reused default master key, 0600; back up with secrets.enc. Back up explicit external sources separately. |
| `data/configs/csrf_secret.json` | `/configs/csrf_secret.json` | CSRF secret for state-changing requests. |
| `data/lockdown/` | `/var/lib/lockdown` | Sensitive device trust/pairing records; do not clear them as routine troubleshooting. |
| `compose.yaml` | `无 / none` | Image, process parameters, and mounts; back up with data directories. Also save any additional Compose configuration files in use. |

Compose mounts `/tmp` and `/var/run` as temporary memory filesystems; neither is a backup location. Application logs go to standard output; there is no public log-download API.

For inactivity deadlines and error codes, read [states and errors](states.md). For password changes, see [installation and administrator access](installation.md) and [backup encryption](encryption.md).
