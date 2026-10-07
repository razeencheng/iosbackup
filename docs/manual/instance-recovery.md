# Back up, recover, and move a deployment

[简体中文](instance-recovery.zh-CN.md) · [Operations manual](../OPERATIONS.md)

## Goal and prerequisites

Preserve device backups, application settings, pairing records, and keys; recover them in a separate directory, verify the result, and then decide whether it should replace the original deployment. This page recovers the application on your NAS. Writing data back to an iPhone is a separate [Experimental operation](experimental.md).

These commands assume Linux Docker Compose, the repository's `compose.yaml`, and its `./data` layout. You need administrative access, Python 3, and GNU tar with ACL/xattr support. For custom mounts, external password files, or NAS shared folders, first inventory the actual paths. Do not apply the default directory list unchanged. NAS backup tools may preserve shared-folder ACLs, but must still save all the data and configuration listed below.

This procedure is based on the current configuration format and application behavior. A complete cross-host recovery following this page has not yet been validated. Rehearse on a separate copy and preserve the original deployment.

## 1. Save a consistent deployment copy

1. In the Web UI, turn off **Automatic backup** for each device, save, and record the original schedules. Wait for existing backup, inspection, encryption, unpack, or restore operations to finish. Disabling a schedule does not cancel an active job. Check the UI and logs; `/healthz` alone does not establish that the instance is idle.
2. In the deployment directory, record the actual image, version, mounts, and Compose project name. These commands expose only the image reference, not the complete environment. Do not upload full `docker inspect` or `docker compose config` output; it may contain the master key.

   ```bash
   docker compose ps
   docker inspect --format '{{.Config.Image}} {{.Image}}' iosbackup
   curl --user iosbackup http://127.0.0.1:9000/api/version
   ```

   `curl` prompts for the administrator password. Replace `iosbackup` if the container has a different name, and adjust the port if necessary. Save the exact image reference/digest, version, and all Compose files in a protected record.
3. Prepare separate protected storage with sufficient capacity. Replace `/mnt/protected-backup` with an **already mounted, encrypted, trusted** backup destination. Do not put the archive inside the `data` directory being archived. A `tar.gz` file is compressed, not encrypted.

   ```bash
   set -eu
   umask 077
   archive_dir=$(mktemp -d /mnt/protected-backup/iosbackup-state.XXXXXX)
   docker compose stop
   docker compose ps -a
   ```

   Confirm that the service has stopped. Investigate any remaining related container or operation before continuing. No other instance may use these directories during migration.
4. In the same terminal and deployment directory, create the archive. This example includes Compose and all three complete data directories, adding common Compose override files only when present; do not create absent files. Add custom `-f` files, external password/key files, and additional mounts to the inventory and protected copy separately.

   ```bash
   set -- compose.yaml data/backups data/configs data/lockdown
   for iosbk_extra in compose.override.yaml compose.override.yml docker-compose.override.yaml docker-compose.override.yml; do
     if [ -f "$iosbk_extra" ]; then set -- "$@" "$iosbk_extra"; fi
   done
   sudo tar --acls --xattrs --numeric-owner -czpf "$archive_dir/state.tar.gz" "$@"
   sudo chmod 600 "$archive_dir/state.tar.gz"
   sudo tar -tzf "$archive_dir/state.tar.gz" > "$archive_dir/contents.txt"
   sudo sha256sum "$archive_dir/state.tar.gz" > "$archive_dir/state.sha256"
   ```

   If any command fails, retain the error and stop the migration. Check the complete `data/configs` inventory: default administrator password and digest, `secret_key`, CSRF state, backup settings, and `secrets.enc` when secrets have been saved, plus pairing records and actual backup files. If you use a custom password or external master key, include the actual credential files in the protected copy. File paths in the inventory are private too.
5. Save the image/version record on the protected storage and confirm the archive is readable. For a backup-only operation, restart the original service with `docker compose start`; verify its state before restoring the recorded schedules. For an imminent migration, leave it stopped. Keep the original directory and the only known-good copy.

**Expected result:** a consistent stopped-service copy, the exact image version or digest, and the original master key. A readable archive and checksum establish file readability, not a recovered instance or recoverable iPhone.

## 2. Recover into an empty directory and verify in isolation

1. Prepare a new empty directory on the destination host. Do not overwrite a live deployment. First compare the archive's checksum and confirm that it contains only the expected relative paths you saved. Do not extract untrusted archives. Replace the example paths below.

   ```bash
   (
     set -eu
     umask 077
     sudo mkdir /srv/iosbackup-recovery
     sudo chown "$(id -u):$(id -g)" /srv/iosbackup-recovery
     chmod 700 /srv/iosbackup-recovery
     cd /srv/iosbackup-recovery
     sudo tar --acls --xattrs --numeric-owner -xzpf /mnt/protected-backup/SELECTED/state.tar.gz
   )
   ```

   Continue only if the block above succeeds. Stop if the directory already exists or extraction fails. After success, enter the new directory in this terminal for all following commands:

   ```bash
   cd /srv/iosbackup-recovery
   ```

   Extraction preserves numeric ownership, modes, and supported ACLs/xattrs. Check UID/GID meaning and filesystem ACLs on the destination; do not fix differences with recursive `chmod 777`. Retain configuration-directory mode `0700` and mode `0600` for passwords, master keys, digests, and other secret files. Verify administrator-only access to backup and pairing directories separately instead of assuming the app tightens their permissions.
2. **Do not start the recovered production Compose configuration yet.** Confirm that default `secret_key` and password/digest files were restored, or that original external credential sources and values are unchanged, and that mounts point to the new copies. Keep using the original login password. Migration is not initialization: recover missing keys from original copies instead of letting the app generate a new password or master key.
3. Disable automatic backups and restore permissions in the recovered configuration, since an older archive may contain enabled schedules. The script below changes only the copy in the current directory: it preserves the original file, accepts only the current `schema_version=1` format, preserves other fields, and rejects unknown formats. The original instance must be stopped, and this recovered instance must not have started.

   ```bash
   sudo python3 - <<'PY'
   import json, os, shutil, stat, tempfile
   from pathlib import Path
   p = Path('data/configs/backup_configs.json')
   if not p.is_file() or p.is_symlink():
       raise SystemExit('No regular backup configuration: check the archive and paths before starting')
   data = json.loads(p.read_text())
   if not isinstance(data, dict):
       raise SystemExit('Unknown configuration format')
   if data.get('schema_version') != 1 or not isinstance(data.get('configs'), dict):
       raise SystemExit('Unknown schema: stop and consult the matching release documentation')
   devices = data['configs']
   if any(not isinstance(v, dict) for v in devices.values()):
       raise SystemExit('Unknown device configuration format')
   saved = p.with_name(p.name + '.before-recovery')
   if saved.exists():
       raise SystemExit('An original copy already exists: inspect it; do not overwrite it')
   shutil.copy2(p, saved)
   for cfg in devices.values():
       cfg['auto_backup_enabled'] = False
       cfg['restore_enabled'] = False
   original = p.stat()
   fd, temporary = tempfile.mkstemp(dir=p.parent, prefix='.recovery-')
   try:
       with os.fdopen(fd, 'w') as f:
           os.fchmod(f.fileno(), stat.S_IMODE(original.st_mode))
           os.fchown(f.fileno(), original.st_uid, original.st_gid)
           json.dump(data, f, ensure_ascii=False, indent=2)
           f.write('\n')
           f.flush()
           os.fsync(f.fileno())
       os.replace(temporary, p)
   finally:
       if os.path.exists(temporary):
           os.unlink(temporary)
   print('Disabled automatic backup and restore in the recovery copy; original file preserved')
   PY
   ```

   The script preserves file ownership and mode. Restore any special file-level ACLs/xattrs with the relevant management tools if your deployment relies on them. A missing configuration can mean that no devices were configured, or that the archive is incomplete; establish the cause instead of creating a guessed configuration.
4. Create a separate `compose.recovery.yaml` and **use it alone, without merging the production Compose file** . This validation instance uses an internal bridge network, no USB/udev mounts, no privileged mode, and a Web port bound only to host loopback. It is not a real-device backup deployment.

   ```yaml
   services:
     iosbackup:
       image: "REPLACE_WITH_RECORDED_IMAGE"
       restart: "no"
       tmpfs:
         - /tmp:size=64m,mode=1777
         - /var/run:size=16m,mode=0755
       ports:
         - "127.0.0.1:19000:9000"
       environment:
         PORT: "9000"
         LOG_LEVEL: "INFO"
         IOSBK_LISTEN_ADDR: "0.0.0.0"
         IOSBK_AUTH_ENABLED: "true"
         IOSBK_CONFIGS_DIR: "/configs"
         IOSBK_BACKUPS_DIR: "/backups"
         IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS: "false"
       volumes:
         - ./data/backups:/backups
         - ./data/configs:/configs
         - ./data/lockdown:/var/lib/lockdown
       networks: [recovery]
   networks:
     recovery:
       internal: true
   ```

   Connect only this internal network; do not mount host sockets/devices or add other networks. Confirm that the host has no custom routes connecting this network to the phone's network. If the original configuration used an allowed path outside `/backups`, map that same container path to a separate copy first. Never mount the original production directory.
   This configuration uses the default password and master key restored under `/configs`. If the original instance used a custom password or master key, add its original settings using [advanced credential configuration](configuration.md#advanced-credential-configuration), keeping the password and key values unchanged. Mount **recovered copies** of external files read-only at their original container paths; do not start without them. Do not supply both a key environment value and a key file.
5. Replace `image: "REPLACE_WITH_RECORDED_IMAGE"` in `compose.recovery.yaml` with the recorded original image's complete tag or digest. Do not use `latest` or create another configuration file. Confirm that the image can be obtained and original credential sources are restored before starting.

   ```bash
   docker compose -p iosbackup-recovery -f compose.recovery.yaml pull
   docker compose -p iosbackup-recovery -f compose.recovery.yaml up -d
   docker compose -p iosbackup-recovery -f compose.recovery.yaml logs --tail=100
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:19000/healthz
   curl --user iosbackup http://127.0.0.1:19000/api/version
   ```

   Open `http://127.0.0.1:19000/` in a browser on the destination host. For a remote host, use SSH local forwarding before opening that address: `ssh -L 19000:127.0.0.1:19000 <administrator>@<destination-host>`. Do not expose the validation port to the Internet.
6. Check that the original password works, no new credentials were unexpectedly generated, the version matches, device settings and removed-device status remain, schedules are off, and logs contain no configuration/secret decryption errors. Offline devices and discovery failures are expected in this environment, not proof of lost pairing. Blank secret fields protect saved values and do not prove they are missing. This stage does not test real notifications or establish that encrypted manifests can be decrypted.

**Expected result:** the recovered copy can load application settings and accept login in isolation. The archive and original production directory remain intact. Record what passed and what remains unverified.

## 3. Switch to the production instance and verify a device

1. Confirm that the original service on the old host is stopped and will not be restarted by another Compose project, system task, or restart policy. Keep its directory. Do not enable the same device's schedules in both instances.
2. Stop/remove the isolated container and its internal network. This command does not delete bind-mounted data directories.

   ```bash
   docker compose -p iosbackup-recovery -f compose.recovery.yaml down
   ```

3. Review the recovered production `compose.yaml`: image, port, project name, USB/udev, three separate-copy mounts, and original key must be correct, with Experimental operations off. Connect only one non-critical device at first and start production using the [installation guide](installation.md).
4. Check login/version, USB presence/pairing, existing backup information, and the [file list](inspection.md). Do not clear lockdown on a pairing failure; inspect mounts, Trust, and device state first. For `MBErrorDomain/207`, follow the [encryption guide](encryption.md) to check the original password/key. Generating a new key is not a fix.
5. With schedules still off, complete one [manual USB backup](usb-backup.md). Confirm that the page shows backup completed, check the last-success time, and record which backup information and lists you can read. Restore [schedules](scheduling.md) one device at a time and observe a real run, then test notifications. Notification tests send real messages.

On failure, stop the new instance and preserve its logs/directory before deciding to resume the original. Confirm the new instance is stopped before switching back. Do not copy the recovered tree over the original instance or overwrite data added after migration with an older archive.

Next: record the recovery exercise's image, date, checks, and remaining gaps; maintain versions with [upgrade and rollback](upgrade.md). Successful instance recovery is still not a completed iPhone restore exercise.
