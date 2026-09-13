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

### Wi-Fi online but backup stays at 0%: inspect idle NAT connections

Discovery, heartbeats, and backup data can use different TCP connections. A visible mDNS device or a working heartbeat does not prove that the backup stream is usable. While the phone scans files and prepares its manifest locally, the backup connection can be idle for several minutes; a temporary 0% alone is not a failure.

In one routed setup using SNAT, USB completed with the same version and backup set, while Wi-Fi received `Connection reset by peer` after the phone finished scanning. An independent read-only query succeeded initially but failed when reusing the same connection after 240 idle seconds. Captures at both ends showed the phone sending an ACK to the original NAT port, the router immediately returning RST, and the NAS receiving neither that phone packet nor the reset. Bypassing flow offload only for this device pair made the identical idle test pass; a complete Wi-Fi incremental backup then succeeded.

This located the failure in that router's software flow-offload path: its idle NAT mapping expired early, preventing reply translation back to the NAS. A missing entry in one conntrack sample is insufficient evidence; combine captures at both ends with a connection-reuse comparison. The 240 seconds describe this test interval, not a universal NAT timeout or a defect in every router.

Diagnosis order:

1. Preserve the original job phase, last activity time, and logs, and confirm authorization for this backup was completed. Do not start by restarting components or deleting pairing records or the backup set.
2. Complete a USB comparison with the same version and backup set, then identify the first phone-side error in the Wi-Fi session. A space-query error following a connection reset does not by itself mean the disk is full.
3. Capture the target connection at both the NAS and router. Distinguish a router-generated RST from a host-generated close forwarded through NAT. Observe conntrack over time and reuse one connection before and after an idle interval.
4. After evidence implicates offload, change only the device pair's offload policy and repeat the same test. A failed backup needs a new session and fresh phone authorization; the old stream cannot resume.

#### Validated OpenWrt fw3 / iptables workaround

This procedure applies to a reviewed fw3 ruleset where existing policies such as MIA still run first, `forwarding_rule` precedes `FLOWOFFLOAD`, and the normal policy already accepts established connections for this pair. Two exact `ESTABLISHED` TCP ACCEPT rules use normal forwarding for the pair while new connections and other devices retain the original rules. Do not apply it unchanged to fw4 / nftables or a different rule order.

Create `/etc/firewall.iosbackup-wifi-nooffload` on the router with the following contents. The example addresses are reserved for documentation: replace them with the actual NAS and phone IPv4 addresses. SNAT relies on connection tracking; preserve any narrowly scoped SNAT rule already verified as necessary.

```sh
#!/bin/sh
NAS_IP=192.0.2.10
PHONE_IP=198.51.100.20
for direction in nas_to_phone phone_to_nas; do
    case "$direction" in
        nas_to_phone) src=$NAS_IP; dst=$PHONE_IP ;;
        phone_to_nas) src=$PHONE_IP; dst=$NAS_IP ;;
    esac
    iptables -w 5 -C forwarding_rule -s "$src/32" -d "$dst/32" -p tcp \
        -m conntrack --ctstate ESTABLISHED \
        -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT 2>/dev/null ||
    iptables -w 5 -I forwarding_rule 1 -s "$src/32" -d "$dst/32" -p tcp \
        -m conntrack --ctstate ESTABLISHED \
        -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT || exit 1
done
```

Back up `/etc/config/firewall`, add this include, and make the script executable. Running the script installs the rules; the include reloads them during firewall startup or reload. Changing the application Compose file or recreating its container does not persist router rules.

```uci
config include 'iosbackup_wifi_nooffload'
    option path '/etc/firewall.iosbackup-wifi-nooffload'
    option reload '1'
    option enabled '1'
```

After reviewing the addresses and rule order, run the script and inspect `iptables -S forwarding_rule` and `iptables -L forwarding_rule -nv` for both rule placement and hits. Repeated execution must not duplicate the rules. The cost is normal forwarding for this pair's TCP traffic. If the phone address changes, update both rules and the original SNAT rule; a fixed DHCP lease can reduce address drift.

To roll back, delete this include and run `uci commit firewall`, then issue two `iptables -D forwarding_rule ...` commands with the same source/destination addresses, TCP, `ESTABLISHED`, comment, and ACCEPT match. Remove only these rules; replacing the entire firewall configuration could discard later changes.

The field validation checked runtime rules, the saved script, and the include. Router reboot and full firewall reload were not exercised; schedule those separately in a maintenance window if needed. Final acceptance requires a real Wi-Fi backup success state, persisted success record, child-process exit, and released job ownership. A 100% display alone is insufficient. No device restore exercise was performed.

The v1.5.2 inactivity guard bounds waiting after a lost stream; the router workaround addresses the reproduced transport failure. Increasing timeouts, repeatedly refreshing mDNS, or rebuilding the same application image cannot repair an expired NAT mapping.

### Disk space

Check free space and inode availability on the filesystem behind `/backups`. Unpack creates a second copy and can require substantial additional disk space. A low-space failure should be fixed before retry; do not delete the only known-good backup as cleanup.

### Permission or read-only filesystem

Verify that host directories exist, are mounted at `/backups`, `/configs`, and `/var/lib/lockdown`, and are writable by the container. Keep the container root filesystem read-only; fix the bind-mount path/permission instead of weakening unrelated host permissions.

### UI unavailable but container is running

Check `/healthz`, port `9000`, host firewall rules, and `docker compose logs`. With host networking there is no separate port-publishing rule. If a reverse proxy is used, test the local endpoint first, then proxy authentication/TLS separately.

For support boundaries and safe report contents, see [SUPPORT.md](../SUPPORT.md) and [SECURITY.md](../SECURITY.md).
