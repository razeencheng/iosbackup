# Historical backups

[简体中文](backup-history.zh-CN.md) · [Restore overview](restore.md)

Each device card has three tabs: **Backup list / Task records / Device settings**. Expanding a card shows saved backup dates directly. History retention is edited separately under Device settings and is disabled by default for each device.

The card's **Back up now** button uses automatic mode and becomes **Stop backup** while a task is running. To request a complete backup again, open **Device settings → Advanced actions → Force full backup** and confirm after reading the explanation. This affects only that manual backup, not the automatic schedule or retention policy. After a task stops or fails, any measured progress remains static and identifies its scope; without progress data, only the execution result is shown.

## Enable and retain versions

Enable history under **Device settings → Backup retention → Edit**, choose **1–365 backups**, then save. History is off by default and the count is prefilled with **2**. Retain the latest N normal backups by completion time, including the latest version; backups on the same day each count. Pinned backups are retained separately. Without a new successful backup, the retained normal backups do not expire. Enabling, disabling or making points eligible for cleanup requires confirmation. Changes with no cleanup impact save directly after a server preview. Saving an active policy also cleans up eligible points; a stale preview must be refreshed.

Storage preparation continues in the background after submission, so you can leave the page. Settings show whether preparation is running, the policy has taken effect, or it failed. An interrupted operation can retry the submitted settings; incomplete copies are not treated as complete. Reopen settings to check the result. Saved settings and cleanup results are shown separately; active readers can defer cleanup. Local preparation reports waiting, scanning, copying and verification separately from the phone backup. The copy percentage only measures local preparation.

Each new backup uses an independent work copy. Storage that passes the fast-clone probe can share unchanged blocks; other storage makes complete copies. Three 100 GB copies can require about 300 GB plus work space. The UI shows logical saved-data size; shared blocks, older records with unknown size and filesystem overhead mean it is not an exclusive physical-usage measurement. Insufficient space stops the new backup and preserves protected history.

**Pin** keeps an important point out of automatic cleanup. Unpinning makes it subject to its other retention rules; it is not an immediate deletion. Pins, the latest normal baseline, older encryption versions, manually retained points and active reads can keep more points than the configured backup count. The `?` controls explain these terms with mouse, keyboard or touch.

Turning history off stops new points and automatic cleanup. Existing points become manually retained, while later ordinary backups use a separate writable current set. The Backups tab displays that current set separately and retains its check and file-list actions. Turning history back on only manages new points automatically.

## Inspect, check and delete a point

Open **Backups** and select a date or **Details** to open its drawer. Download, pin, check and delete actions apply to that selected point. Execution, original quick check and saved history state are separate. A **pending result** can be exported and, when its manifest is readable, checked, but is not an incremental baseline. Unknown imported completion times are labelled explicitly.

Checking is read-only. It checks metadata and, when readable, manifest records and object presence; it does not verify every payload hash or prove a successful phone restore. Encrypted or unknown sources cannot run a file-manifest check; their existing basic check remains visible. Rechecking updates evidence, never promotes a pending result or modifies its backup data. Evidence remains with the point; the task timeline also records the check and point ID within its own retention limits. A point links to its generating job, and a job can open its linked backup. Expired jobs or removed points are reported explicitly; dates are never used to guess links. A saved point followed by a reference-update or cleanup failure remains identified as saved, with the failed phase shown.

Cancellation, timeout and read failure appear as the latest check attempt. The previous completed check retains its own timestamp and conclusion. A cancelled check is shown neutrally. If the check finishes but its result cannot be saved, the current session shows the new result and a save warning; restart can lose that unsaved result. A completed scan that finds missing files means “check completed, issues found”; it is different from a scan that could not finish.

Deletion is separated from routine actions at the bottom of the details drawer and requires confirmation of the selected date and current revision. The dialog highlights pinned and baseline points. A point in use cannot be deleted. Explicit deletion can remove the final normal baseline, requiring a full backup next time if no readable normal point from the active history epoch and compatible backup generation remains; it never removes other points or old password references. Removed devices keep their history: open the removed-device drawer and choose View history without reconnecting or restoring the device to the list.

## Export for a computer restore

1. Select a backup date or **Details**, choose **Prepare full export**, and use the download link within five minutes after preparation. One transfer streams at a time, with at most four prepared/live sessions. A busy message lets you retry when the transfer slot is free.
2. Download the `.tar` file. Its name identifies the device, backup time and a short point ID; an unknown original completion time uses a labelled saved date. Ensure the computer has space for the complete download and extraction. It contains the complete original `<SOURCE-UDID>/` directory, preserving encryption. The application does not load the entire archive into browser memory or create another full archive on the NAS. Application configuration, credentials, task logs and external indexes are outside that source; reserved unpack output is excluded.
3. The status **Server transfer ended** only confirms the server stream ended. Confirm the download finished on the computer and discard partial files after cancellation or failure. An expired or restarted session requires a new export; resumable downloads are not provided. A Range request is explicitly rejected; retry with a new complete download.
4. Extract into a new staging directory, preserving the source UDID and all contents. Do not merge two dates with the same UDID. Follow [Finder](restore-finder.md) or [Apple Devices](restore-apple-devices.md) for local placement and restore steps. For encrypted history, use the password from that backup's date, which may differ from the device's current password.

Computer recognition, decryption and physical-device restore still require the separate acceptance exercise. Restoring a whole phone affects its newer data; individual photo/message extraction is a different capability.
