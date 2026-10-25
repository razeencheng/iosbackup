# Configure and observe automatic backups

[简体中文](scheduling.zh-CN.md) · [Back to the operations manual](../OPERATIONS.md)

## Goal and requirements

Configure automatic backup for one device and observe an eligible job from start to finish. Automatic backup is Core functionality. Complete a [first USB backup](usb-backup.md) before enabling a schedule. If jobs will use Wi-Fi, also complete [Wi-Fi validation](wifi.md).

A schedule controls when the application tries to start a job. It does not guarantee completion at a particular minute. When a backup wakes the phone, iOS may display a white passcode screen. Enter the device passcode when prompted; there is no need to unlock the phone in advance or keep its screen on. Automatic backup does not remove that authorization step.

## Before you begin

Make sure the target device is online, no operation is running, the backup location has enough space, and you can respond to phone prompts during the window. Note the last successful backup time: a recent manual backup also delays the earliest eligible automatic backup.

These are the defaults for a new device. For an existing device, use the values saved in its settings.

| Setting | Default | Meaning |
|---|---|---|
| Automatic backup | Off | After you enable and save this setting, the app checks the schedule and starts eligible backups |
| Backup window | 18:00 to 06:00 | Deployment time, across midnight; includes 18:00, excludes 06:00 |
| Backup interval | 24 hours | At least 24 hours since the last successful backup |
| Minimum battery | 20% | A backup should not start below this level |
| Only back up while charging | On | The device must report that it is charging |

Windows use the deployment `TZ` (default `Asia/Shanghai`), regardless of browser timezone or language. Changing `TZ` keeps the entered clock values and runs them in the new zone; review your windows after a change. Backup intervals measure actual elapsed hours, including across daylight-saving changes. A repeated local hour is eligible for the window in both occurrences, but the elapsed interval and active-job guard still apply. The window controls when new jobs start; a running job is not stopped merely because the window ends. Set different start and end times to avoid configuring the wrong backup window.

**Battery and charging conditions also apply to “Back up now.”** Turning automatic backup off does not bypass them.

## Set an overnight schedule

1. **In the Web UI** , find the target device and open **Backup settings** . Check its name and connection type so that you do not change another device.
2. Set **Backup window** to `18:00`–`06:00` and **Backup interval** to **Every 24 hours** . This is an overnight example; adjust the window to a time when you can respond to phone authorization prompts.
3. Set **Minimum battery** to `20%` and leave **Only back up while charging** on. **On the phone** , connect power, then check the charging state reported on its device card.
4. Keep **Save location** at the container path you have already validated, such as the default `/backups`. Do not move the backup directory as part of this schedule change.
5. Turn **Automatic backup** on and select **Save settings** . Wait for **Settings saved** ; the settings are saved only after the **Unsaved changes** indicator clears.
6. When no job is running, reopen the page and confirm that the window, interval, and switches retain their new values.

![Automatic backup settings](images/04-iosbk-setting.png)

## Observe the first automatic run

1. **In the Web UI** , check the last successful backup time. If a manual backup just finished and the interval is 24 hours, another backup will not start immediately even inside the window. Do not delete backups or edit historical timestamps to force a run.
2. At the next eligible window and interval, keep the device online and powered, and watch for authorization prompts. The app checks the conditions every **30 seconds** by default, so jobs do not start at an exact second.
3. Wait for the device card to enter a backup state and authorize the operation on the phone if asked. To confirm the trigger, run `docker compose logs --tail=200 iosbackup` in the deployment directory and look for `event=backup.scheduled` at the matching time. Remove device identifiers, addresses, and private paths before sharing logs.
4. Wait for an explicit **Backup completed** or failure state. On success, check the updated backup time and then inspect the read-only information under [View backups](inspection.md).

![Completed backup status](images/05-iosbk-backup-success.png)


## If it does not start, fails, or needs to be paused

| Symptom | Check and next action |
|---|---|
| No job at the expected time | Check the deployment-time window, last success, interval, and whether the enabled switch was saved |
| Device is offline or removed | Reconnect it; removed devices are not scheduled. See [Device management](device-storage.md) |
| Charging or battery conditions fail | Connect power and wait for the reported state to update; “Back up now” does not bypass these settings |
| Another operation is running | The default permits one backup job at a time. This check can be skipped and a later check can try again if eligible; there is no guaranteed queue order |
| Authorization wait ends in failure | Check the phone and [State reference](states.md), address the cause, then retry when the device is available |
| Repeated failures | Turn automatic backup off and save, preserve the evidence, and follow [Troubleshooting](troubleshooting.md). Failure does not replace the last successful backup timestamp |

To disable future automatic jobs, turn **Automatic backup** off in **Backup settings** and save. This does not stop an active backup; closing the browser does not stop it either.

## Stop a backup and hold automatic scheduling

1. Select **Stop backup** on the active device card. Confirm once after reading the warning: interrupting this incremental backup may affect the recoverability of the previous backup.
2. Wait through **Stopping…** until **Stopped · backup incomplete**. The device remains occupied until the process and output finish. The last-success timestamp remains a historical record, not proof that the current files can be restored.
3. **Automatic backups on hold** persists after a normal restart or upgrade if saving succeeded. Original schedule settings are retained. **Back up once** runs one manual job without clearing the hold and still checks battery and charging requirements.
4. Select **Resume automatic backups** when ready. This clears the hold without turning on a previously disabled schedule. A due backup may start at the next check if the original conditions are satisfied.

If saving the hold fails, stopping still takes effect and automatic jobs are blocked in the current process. Persistence after restart is uncertain. Resolve the storage problem and select **Retry saving**. A failed resume also retains the hold in memory.

Stopping retains existing files but does not roll back incremental updates or guarantee that the previous backup remains recoverable. Retrying starts a new protocol session. A stop just after completion preserves that result and holds future scheduling; an older request cannot stop a newer job. User stops update the page and logs without a failure notification. Actual failures still follow notification rules.

Re-adding a removed device preserves its settings. If automatic backup was on, a later scheduler check can start another job. To keep it disabled, turn it off and save before removing the device.

## Next steps

Configure [notifications](notifications.md) for real success and failure events, and regularly [protect the deployment instance](instance-recovery.md). See [Configuration reference](configuration.md) for field ranges, concurrency limits, and the startup setting for the scheduler interval.
