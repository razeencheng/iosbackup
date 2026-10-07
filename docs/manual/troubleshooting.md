# Troubleshoot by symptom

[简体中文](troubleshooting.zh-CN.md) · [Operations manual](../OPERATIONS.md)

## Goal and diagnostic order

Locate the failing stage before making the smallest useful check. Preserve backups and pairing records. Do not repeatedly restart components, delete directories, or reset passwords while an operation is active. Run the commands below **on the Linux host, in the deployment directory** . They assume Compose service `iosbackup` and port `9000`; replace custom project/file/port settings consistently.

Record version, connection type, job phase, last activity, phone prompts, and logs spanning a few minutes before/after the failure. Do not capture only the final error caused by an earlier problem. `/healthz`, device visibility, 100% progress, and delivered notifications establish different facts; see the [state reference](states.md).

| Symptom | First read-only checks | Next step and stop condition |
|---|---|---|
| Web page unavailable | Local health request, Compose state, recent logs | Fix local service first; inspect firewall/proxy only after local access works |
| Incorrect password or repeated login | Correct instance, administrator password file, `/configs` mount | Do not generate a new master key; use [installation/authentication](installation.md) |
| Old version after upgrade | `/api/version`, running image reference | Check the Compose image, configuration overrides, and project name; see [upgrade](upgrade.md) |
| USB device missing or pairing fails | Cable, phone passcode/trust prompts, USB/udev mounts | Follow [USB workflow](usb-backup.md) and preserve lockdown |
| Manual backup cannot start | UI reason: offline, busy, battery/charging | Meet the stated condition; do not bypass concurrency guards |
| Automatic backup never starts | Enabled/saved state, Beijing-time window, interval, battery/charging | Check [scheduling](scheduling.md) field by field, without repeatedly restarting |
| Authorization wait or unchanged progress | Phone prompts, phase, last activity | Authorize; distinguish preparation from `backup_stalled` |
| Wi-Fi drops or reconnect loops | Same-version USB comparison, earliest transport error | Start with [Wi-Fi](wifi.md); use [NAT investigation](wifi-nat.md) only with router evidence |
| Empty information, failed list, `MBErrorDomain/207` | Device online, original encryption password/master key, logs | Follow [encryption](encryption.md) and [inspection](inspection.md), not repeated password changes |
| No space, permission, or read-only errors | Actual mounts, free bytes/inodes, directory permissions | Fix the specific storage issue; preserve the only good backup |
| Notification test/real event missing | Global/channel switches, rules, delivery result | Follow [notifications](notifications.md); tests send real messages |

## 1. Check service, identity, and logs

1. On the host, confirm the service exists and is not repeatedly exiting.

   ```bash
   docker compose ps -a
   curl -fsS http://127.0.0.1:9000/healthz
   docker compose logs --since=10m --tail=200 iosbackup
   ```

   A successful health request proves HTTP responsiveness only. For connection refusal, inspect startup logs, port, and listen address first. For a port conflict, identify any old instance before stopping unknown processes.
2. Check runtime version, application state, and image. `curl --user iosbackup` prompts for the password without embedding it in the command line.

   ```bash
   curl --user iosbackup http://127.0.0.1:9000/api/version
   curl --user iosbackup http://127.0.0.1:9000/api/status
   docker inspect --format '{{.Config.Image}} {{.Image}}' iosbackup
   ```

   `/api/status` contains device names/identifiers; do not share it unchanged. `backup_in_progress_count=0` helps diagnose job ownership, but confirm with UI/logs that no other operation is active. `encryption_available=true` does not establish that every device has a correct saved backup password.
3. If local access works but another computer cannot connect, check the browser's host/port, trusted-network routes, and host firewall. The standard deployment uses host networking and needs no separate `ports` rule. With an HTTPS proxy, check the local service first, then proxy authentication/TLS. Do not disable application authentication to hide a proxy problem.
4. For failed login, use this instance's default `admin_password` or explicit custom password file, not the phone passcode or backup password. If the first-start log is gone, use host-side `sudo cat data/configs/admin_password` to retrieve the default password. Check existence and permissions, without pasting contents into terminal transcripts or Issues. A default-file/digest mismatch fails startup; password changes require a separate file selected in the configuration as described in [authentication operations](installation.md).
5. Existing `secrets.enc` without its original key, an incorrect key, or simultaneous key environment/file overrides fails startup. Restore matching `secret_key` or the original external key without generating a new value or deleting ciphertext. If initialization reports unsupported hard links, use a local filesystem supporting them; preserve the original data and follow [instance recovery](instance-recovery.md).

**Expected result:** distinguish startup, network-entry, authentication, and version problems. Once service access works, move to devices; do not change network, storage, and passwords at the same time.

## 2. Devices and pairing

1. Connect the phone to the deployment host with a known data-capable USB cable. Enter the device passcode if a white passcode screen appears; if **Trust This Computer** appears, unlock the phone and tap **Trust** . Keeping the phone unlocked or its screen on is not required. If no prompt appears, try the cable/port or reconnect before discarding pairing records.
2. On the host, check USB/udev and persistent mounts. This command shows only container destinations and writable status.

   ```bash
   docker inspect --format '{{range .Mounts}}{{println .Destination .RW}}{{end}}' iosbackup
   ls -ld /dev/bus/usb /run/udev
   ```

   Expect `/dev/bus/usb`, read-only `/run/udev`, `/backups`, `/configs`, and `/var/lib/lockdown`. If `lsusb` is installed on the host, use it to confirm OS-level detection. Detection is not completed pairing.
3. Refresh/pair only with no active backup. Check **Settings → Removed devices** : removed devices do not return automatically. Restoring one to the list can make its old schedule eligible on a later check; review that schedule using [device management](device-storage.md).
4. For USB mux listener conflicts, identify other backup instances or host services. Establish which instance and volumes are in use before scheduling a conflicting service's shutdown. Do not restart all host USB services together.
5. If `lsusb` sees the phone but the application does not, and logs show `Could not set configuration` with `0xfffffffa`, check whether the Linux desktop mounted the phone as a camera. An iPhone camera mount in the file manager or `gvfsd-gphoto2` can hold the USB interface; `gio mount -l` lists camera mounts. Finish browsing/importing photos, then eject that phone camera mount in the file manager. With no backup running, use the application's **Restart connection** , refresh, and pair again. Ejecting the camera mount does not delete phone data; do not unmount the backup storage drive. The error code is a diagnostic clue; identify the actual process holding the interface first.

**Stop conditions:** persistent Trust/pairing failures, unknown volume origins, or an active job. Preserve `/var/lib/lockdown` and logs and use the [support channel](../../SUPPORT.md).

## 3. Jobs that do not start, stall, or fail

1. If a job cannot start, read the specific UI reason. Manual requests can be rejected for device activity, charging requirements, or battery level. Automatic jobs additionally require an enabled schedule, a Beijing-time window, and the minimum interval. Manual success does not prove a schedule was saved or is due.
2. Once started, check phone authorization prompts. The default authorization limit is `5m`; preparation/device-processing waits allow `30m`, and transfer inactivity allows `10m`. These bound consecutive inactivity, not total time while transfer continues.
3. Incremental backups send the old manifest first; a large encrypted manifest can require minutes of device processing. Overall 0%, current-file 100%, or repeated progress alone does not establish a stall. Check the phase and last activity. Even overall 100% needs an explicit success result.

![Waiting for device authorization](images/15-iosbk-wait-sq.png)

![Sending backup data to the device](images/16-iosbk-s.png)

4. After `backup_stalled`, wait for the old process to exit and release the resources held by the job. Check that the last-success time was not updated by this failure. Retaining the directory does not prove that pre-failure contents are unchanged: an incremental run may already have written data. Protect important copies independently.
5. Once prompts and connectivity are resolved, start one new job. The current interface has no cancel-backup or checkpoint-resume feature. A failed job does not reconnect at its previous offset, and a new attempt can require another phone passcode authorization. Do not replace diagnosis with larger timeouts, repeated refreshes, or restarting every component.
6. After a Wi-Fi job ends, compare USB with the same version and backup path. USB success directs attention toward the Wi-Fi session; `mobilebackup2 (-4)` alone does not identify one cause. Use the [advanced NAT topic](wifi-nat.md) only for cross-subnet connections with supporting RST/idle-reuse evidence.

**Expected result:** a record of the failing phase, first error, and one comparison test. Stop repeated retries when the same failure returns; keep the logs and test results before asking for help.

## 4. Backup reading and storage

1. Empty overview information does not always mean no files exist: disk usage can still be shown when information reading fails. Check the device is online and the original directory is mounted, then follow [read-only inspection](inspection.md).
2. `MBErrorDomain/207` commonly accompanies encrypted-manifest reading without a valid password. Neither the Web password nor the master key is the backup-encryption password. Do not toggle encryption to “repair” an old backup. Preserve the original password and check the matching deployment key/logs in the [encryption guide](encryption.md).
3. Inspect the actual storage filesystem. These paths apply only to the default layout; replace custom paths.

   ```bash
   df -h ./data/backups ./data/configs ./data/lockdown
   df -i ./data/backups ./data/configs ./data/lockdown
   ls -ld ./data ./data/backups ./data/configs ./data/lockdown
   ```

   Inode exhaustion can cause failure even when bytes remain. Unpack needs additional space for a full browsing copy. Phone capacity or current compression ratio alone is not a reliable required-space estimate.
4. For permission/read-only errors, map the failing path to its mount, ownership, and NAS ACL, and confirm that the data volume is writable on this host. The default Compose container root is writable; `/run/udev` remains mounted read-only. Fix the affected data mount or directory permissions without broadly changing permissions across the directory tree. Do not make exploratory writes inside an active backup directory.

**Stop conditions:** I/O errors, unexpectedly missing directories, unknown data formats, or only one valid copy. Protect the data first and plan [instance recovery](instance-recovery.md); do not start deletion/restore experiments.

## 5. Prepare a useful redacted support record

1. Record `/api/version` version/commit, host/NAS model and architecture, iOS version, USB/Wi-Fi, encryption state, and reproduction steps.
2. Include expected/actual behavior, phase, phone prompt, first error, and operation time. User-visible times use Beijing time. Keep only the necessary failure log window, preserving event order and error codes.
3. Remove full device identifiers/names, addresses, personal paths, notification targets/tokens, passwords, and the master key. Do not upload credential configuration files, `secret_key`, `admin_password`, pairing records, `secrets.enc`, backup contents, raw packet captures, or complete container environments. First-start logs may contain a generated administrator password; review every line before sharing.
4. Use [SUPPORT](../../SUPPORT.md) for ordinary problems and the private channel in [SECURITY](../../SECURITY.md) for sensitive issues. Do not repeat a destructive action just to prepare a report.

Next: after recovery, validate the main [USB workflow](usb-backup.md), then re-enable schedules and notifications individually and record what remains unverified.
