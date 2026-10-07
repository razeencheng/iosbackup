# Upgrade the application and roll back when needed

[简体中文](upgrade.zh-CN.md) · [Operations manual](../OPERATIONS.md)

## Goal and preparation

Run the intended application version while preserving device settings, pairing records, backup data, and secrets. These instructions use Linux Compose. If you use a custom project name or configuration files, retain the original `-p` / `-f` arguments on every command.

Read the target release's [changelog](../../CHANGELOG.md), [feature status](../FEATURE_STATUS.md), and migration requirements first. Have the exact old image reference/digest, an available new image, original master key, and a [consistent deployment copy](instance-recovery.md). Without release notes or confirmed format compatibility, test on a separate copy before changing the only production instance.

## Upgrade steps

1. Turn off each device's **Automatic backup** in the UI and record its schedule. Wait for active operations to finish. Confirm the UI is idle and inspect logs; `/healthz` only proves HTTP responsiveness. Do not recreate the container during backup, inspection, encryption, unpack, or restore.
2. In the deployment directory, record the current version and image. Replace the default container name `iosbackup` if necessary.

   ```bash
   docker inspect --format '{{.Config.Image}} {{.Image}}' iosbackup
   iosbk_old_image_id=$(docker inspect --format '{{.Image}}' iosbackup)
   docker image inspect --format '{{json .RepoDigests}}' "$iosbk_old_image_id"
   curl --user iosbackup http://127.0.0.1:9000/api/version
   ```

   Save the complete `ghcr.io/razeencheng/iosbackup@sha256:…` reference for rollback; recording only `latest` is insufficient. If a locally built image has no RepoDigest, preserve the image and its build source and confirm it is recoverable before upgrading.

   Save all Compose configuration files in use and the image record to protected storage. Complete environment output may reveal secrets and must not be shared. Follow [deployment backup](instance-recovery.md) to stop the service and preserve all three complete volumes (including `secret_key`, password, and digest in the configuration directory) and external credentials. The old service may run after the archive is completed, with schedules still disabled; check again that it is idle before upgrading.
3. Check the Compose image selection and pull the update. The default `image` uses `latest`; no per-release tag edit is needed.

   ```bash
   docker compose config --quiet
   docker compose config --images
   docker compose pull
   ```

   The default output should be `ghcr.io/razeencheng/iosbackup:latest`. If you pinned a version, change `services.iosbackup.image` in Compose back to that value, preserving other settings. Resolve missing-image, access, network, or platform errors before recreating the service.
4. Preserve existing settings, the default `secret_key`, and any custom password or master key in use. Merge required Compose changes individually after reading the release notes. Do not overwrite original mounts, passwords, or the project name with a new example. Confirm that no operations are active, then recreate the service.

   ```bash
   docker compose up -d
   docker compose ps
   ```

   `latest` does not automatically replace a running container; pull and recreate it to apply an update. To review and pin a specific version, change Compose's `image` directly to an official version tag or digest and follow the same steps. Do not attach complete `docker compose config` output to a report.
5. Check the version actually running and the startup logs.

   ```bash
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
   curl --user iosbackup http://127.0.0.1:9000/api/version
   docker compose logs --tail=200 iosbackup
   ```

   The version/commit returned by `/api/version` must match the image's release notes. Log in and inspect device settings, saved-secret availability, existing backup paths, and pairing. Stop before starting a backup if configuration-format or secret-read errors appear.
6. Complete a [manual USB backup](usb-backup.md) on one non-critical device, then inspect its [information and file list](inspection.md). After an explicit success state and last-success timestamp, re-enable original schedules one device at a time. Wi-Fi and notifications require their own real-event checks.

**Expected result:** the intended version is running, preserved settings/pairing, and a successful real backup on the selected device. Record unverified checks such as encrypted-manifest reading or Wi-Fi. Being able to open the Web page alone does not confirm a successful upgrade.

## Rollback steps

1. For startup failure, incompatible configuration, unreadable existing data, or a previously working main backup function failing, disable schedules first. Let active operations that can finish normally do so, and retain a minimal log window. Do not repeatedly restart a job that is still progressing. Preserve the entire upgraded directory/snapshot for investigation and to retain data created after upgrading.
2. Check the release notes to establish whether the old application can read the current persisted format. If incompatible or uncertain, **stop the in-place rollback here** . Follow [instance recovery](instance-recovery.md) to restore the pre-upgrade copy into a separate directory and test it with the old image in isolation. Do not overwrite current volumes.
3. If formats are compatible, set `services.iosbackup.image` in Compose to the recorded previous tag/digest, not `latest`. Keep other settings, the administrator password, and the master key unchanged. Confirm the image is available before switching.

   ```bash
   docker compose config --quiet
   docker compose config --images
   docker compose pull
   docker compose up -d
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
   curl --user iosbackup http://127.0.0.1:9000/api/version
   docker compose logs --tail=200 iosbackup
   ```

   If the exact digest is already local but the registry is temporarily unreachable, verify that it matches your record before deciding to use it. A failed pull is not a successful rollback.
4. Repeat the upgrade checks in order: login, settings, pairing, read-only inspection, then a USB backup before schedules. If failure persists, preserve both copies and follow [troubleshooting](troubleshooting.md) or submit a redacted report.

**Expected result:** the original runtime version and a working main workflow. Changing images does not undo changes to persisted data. Arrange separate data recovery only for established corruption or incompatibility, with an available recoverable copy.

Next: record the failing version/commit, reproduction conditions, and rollback result. Preserve the pre-upgrade copy and failure evidence until the replacement is validated; the first successful startup is not a reason to remove them.
