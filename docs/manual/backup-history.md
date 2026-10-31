# Historical backups

[简体中文](backup-history.zh-CN.md) · [Restore overview](restore.md)

Each device card has three tabs: **Backups / Task records / Device settings**. Expanding a card shows saved backup dates directly. History retention is edited separately under Device settings and is disabled by default for each device.

## Enable and retain versions

Enable history under **Device settings → History retention → Edit**, choose **1–365 calendar days**, then review and save. The default is **3 days including today**, retaining the last normal point for each day. A day without a successful backup has no point. Shortening the period shows which points become eligible for cleanup during subsequent backups; a stale preview must be refreshed.

Storage preparation continues in the background after submission, so you can leave the page. Settings show whether preparation is running, the policy has taken effect, or it failed. An interrupted operation can retry the submitted settings; incomplete copies are not treated as complete. Reopen settings to check the result.

Each new backup uses an independent work copy. Storage that passes the fast-clone probe can share unchanged blocks; other storage makes complete copies. Three 100 GB copies can require about 300 GB plus work space. The UI shows logical saved-data size; shared blocks, older records with unknown size and filesystem overhead mean it is not an exclusive physical-usage measurement. Insufficient space stops the new backup and preserves protected history.

**Pin** keeps an important point out of automatic cleanup. Unpinning makes it subject to its other retention rules; it is not an immediate deletion. Pins, the latest normal baseline, older encryption versions, manually retained points and active reads can keep more points than the configured day count. The `?` controls explain these terms with mouse, keyboard or touch.

Turning history off stops new points and automatic cleanup. Existing points become manually retained, while later ordinary backups use a separate writable current set. The Backups tab displays that current set separately and retains its check and file-list actions. Turning history back on only manages new points automatically.

## Inspect, check and delete a point

Open **Backups** and select a date or **Details** to open its drawer. Download, pin, check and delete actions apply to that selected point. Execution, original quick check and saved history state are separate. A **pending result** can be exported and checked but is not an incremental baseline. Unknown imported completion times are labelled explicitly.

Checking is read-only. It checks metadata and, when readable, manifest records and object presence; it does not verify every payload hash or prove a successful phone restore. Encrypted manifests may be unavailable to the local checker. Rechecking updates evidence, never promotes a pending result or modifies its backup data. Evidence remains with the point; the task timeline also records the check and point ID within its own retention limits. A point links to its generating job, and a job can open its linked backup. Expired jobs or removed points are reported explicitly; dates are never used to guess links. A saved point followed by a reference-update or cleanup failure remains identified as saved, with the failed phase shown.

Cancellation, timeout and read failure appear as the latest check attempt. The previous completed check retains its own timestamp and conclusion. A completed scan that finds missing files means “check completed, issues found”; it is different from a scan that could not finish.

Deletion requires confirmation of the selected date and current revision. The dialog highlights pinned and baseline points. A point in use cannot be deleted. Explicit deletion can remove the final normal baseline, requiring a full backup next time; it never removes other points or old password references. Removed devices keep their history: open the removed-device drawer and choose View history without reconnecting or restoring the device to the list.

## Export for a computer restore

1. Choose **Download** in a backup's date row to prepare and start the download automatically; its details drawer shows progress. Alternatively, open Details, choose **Prepare full export**, and use the download link within five minutes. One transfer streams at a time, with at most four prepared/live sessions. A busy message lets you retry when the transfer slot is free. The details link is also available if the browser does not start the download.
2. Download the `.tar` file. It contains the complete original `<SOURCE-UDID>/` directory, preserving encryption. The application does not load the entire archive into browser memory or create another full archive on the NAS. Application configuration, credentials, task logs and external indexes are outside that source; reserved unpack output is excluded.
3. The status **Server transfer ended** only confirms the server stream ended. Confirm the download finished on the computer and discard partial files after cancellation or failure. An expired or restarted session requires a new export; resumable downloads are not provided.
4. Extract into a new staging directory, preserving the source UDID and all contents. Do not merge two dates with the same UDID. Follow [Finder](restore-finder.md) or [Apple Devices](restore-apple-devices.md) for local placement and restore steps. For encrypted history, use the password from that backup's date, which may differ from the device's current password.

Computer recognition, decryption and physical-device restore still require the separate acceptance exercise. Restoring a whole phone affects its newer data; individual photo/message extraction is a different capability.
