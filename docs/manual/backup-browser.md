# Browse local backup information

[简体中文](backup-browser.zh-CN.md) · [Back to the operations manual](../OPERATIONS.md)

Open a device's **Backups** tab to read the backup stored on the server or NAS. The phone can be offline: the overview and an unencrypted file list are read locally. Starting a backup, changing its encryption password, and restoring a device still require an available device connection.

The list contains paths and metadata, not the contents of photos, messages, or other files. A readable list does not establish that the backup can be restored. Check backup completion, file-list readability, file contents, and a recovery rehearsal separately.

## Read and refresh

1. Open the device card and select **Backups**. Existing backups are indexed on first viewing; starting the application does not scan every device's backup.
2. Wait for the local read to finish. The page shows the overview and a file-list preview automatically. **Refresh** requests another local read; no phone connection is needed.
3. Check the status strip and both read times. The overview and list can have different timestamps when only one part of an update succeeds. **Last successful backup** is the application's separate success record, not the time the information was read.
4. Use **Retry reading** after checking the backup location, mount, permissions, and free space if a read fails. `—` means unknown or unavailable, not zero or unencrypted.

Each successful manual or scheduled backup requests a background index update. An index update failure does not change the successful backup result. Refreshing keeps the previous usable information visible; a failed refresh retains compatible, previously complete information and its original read time. Restarting the application retains saved information but initially treats it as historical until the current source is checked. A phone going offline alone does not make information stale.

Changing a device's backup directory selects a different source; it does not move the old backup or make its list belong to the new location. If the location is unavailable, the page distinguishes that failure from a confirmed missing backup. An incomplete or corrupt read must not be interpreted as an empty backup.

## Size, count, preview and CSV

| Item | Meaning and limit |
|---|---|
| Backup size | Sum of the logical sizes of the original backup's regular files, not allocated disk space. Excludes the root `_unback_` output and application-generated `.iosbackup-unback-*` temporary outputs. Ordinary hidden files and arbitrary `.tmp` files are not excluded merely by name. |
| Manifest entries | Number of list records, including files, folders and links. It is not the number of regular files physically stored in the backup directory. |
| Preview | At most 5,000 entries and 16 MiB of preview data. The byte limit can produce fewer than 5,000 entries. Each page contains at most 200 entries. |
| Search | Searches only the loaded preview. No match does not prove a file is absent from the full backup. Search and page selection are retained during refresh and language changes. |
| Download full CSV | Available only for a complete saved list and its published revision. Contains all records in that saved list, not just the preview or search results; the phone can remain offline. |

Wait for a complete list before downloading. If a newer revision replaces the one displayed before the download starts, refresh the information and try again. A download already opened against a valid revision can finish. CSV records can contain quoted line breaks: use a CSV parser, not a plain line count, to count records. The CSV is neither a copy of device data nor a restore input.

The local reader does not follow symbolic links or accept special files as backup input. An unsafe source is reported as a read failure rather than silently omitted from a successful size or list.

## Encrypted backups and database state

Basic information can be available for an encrypted backup, but this feature does **not decrypt an encrypted `Manifest.db`**. A saved password does not make an encrypted list readable here.

A previously saved list is shown only when it can be matched to the same backup source. For encrypted manifests, the manifest identity must also match. Such a list can originate from a compatible, complete online list query that actually succeeded through the existing inspection API. The page does not automatically retry online queries or decrypt the backup. A failed or partial online query is not saved as a complete list. Output that the legacy filter trims, truncates or changes across multiple lines is also excluded when its completeness cannot be established, and old browser-memory lists are not migrated. Without a valid saved list, the page explains the limitation and disables full CSV download.

The local SQLite reader also rejects a nonempty `Manifest.db-wal` or `Manifest.db-journal`. Those transaction sidecars may contain records that are not yet in the main database. Wait for the backup producer to finish its transaction/checkpoint, or use a stable copy produced after it has finished. Do not delete the sidecars to force the reader to accept the backup. A previous compatible list, if available, remains historical information rather than proof that the current database was fully read.

## Persistence, privacy and resource limits

Indexes are stored under the configuration directory, at `backup-index/v1/<source-key>/` (normally `/configs/backup-index/v1/` in the container). Persist the configuration volume to retain indexes across restarts. Index directories use `0700` and index files use `0600` permissions. These are filesystem access permissions, **not encryption**: saved metadata and CSV paths remain plaintext even when the device backup is encrypted. Protect configuration-volume copies and CSV downloads; do not attach them unredacted to public issues.

Current built-in limits are:

| Resource | Limit |
|---|---|
| Concurrent local index scans | One per application instance; repeated requests for the same source are coalesced |
| Local scan duration | 15 minutes |
| Each plist input and converted output | 8 MiB |
| One CSV record | 1 MiB |
| Preview | 5,000 entries and 16 MiB |
| One complete CSV | 512 MiB |
| Total index storage | 2 GiB across sources, including temporary and metadata storage |

These limits protect NAS memory, CPU and disk use; they are not additional environment settings. Allow free space for the index as well as the original backup. A budget or storage failure does not publish a partial list as complete. Compatible previous data is kept when possible, while the page reports the failed update. The index can be regenerated from a readable unencrypted backup; an encrypted historical list may not be reproducible without another successful compatible online query.

See [Encryption and credentials](encryption.md), [Devices and storage](device-storage.md), and [Instance backup and recovery](instance-recovery.md) for protecting the actual backup and its credentials. Local unpacking remains a separate [experimental operation](experimental.md).

## Task records and backup mode

Select **View task records** under **Backups** to inspect a job's requested mode, execution outcome, phase events and last confirmed backup activity. Records survive restarts and remain available while the device is offline. Page refreshes and connection heartbeats do not advance backup activity time.

The page displays up to 1,000 recent events. **Copy logs** produces readable text; **Download logs** exports a JSONL summary and all retained events. Retention is up to 30 days, capped by 1,000 completed jobs, 10,000 events / 1 MiB per job, and a 256 MiB total budget, whichever is reached first. Active summaries are protected separately; atomic writes need temporary space. Corrupt or orphaned records also have age and capacity limits. Truncation, expiration and saving failures are shown explicitly; exporting cannot recover already-pruned events. Review device names and identifiers in the summary before sharing it.

Under **Advanced options for the next manual backup**, the default **Automatic selection** lets the device and tool choose a compatible mode. You can request **Full backup** instead. The choice resets after admission and does not change the automatic schedule. Observed mode remains **Unknown** without reliable evidence. A full request still updates the current backup set, and task records do not represent historical restore points.

## Read-only backup checks

Every successful backup command is followed by a quick check with a 30-second deadline. It reads required plist metadata, the `SnapshotState=finished` marker and the presence of a nonempty `Manifest.db`; it does not scan every object. Encrypted backups can pass this metadata check without decrypting their manifest.

Select **Check backup** for an offline manifest/object scan with a 15-minute deadline. For readable unencrypted manifests, regular-file entries must reference an existing regular object; directories and symbolic links do not require ordinary file objects. Unknown record types, missing tools, inaccessible sources, cancellation and timeouts retain the completed coverage and cannot count as a complete pass. The page reports **passed**, **issues found**, or **incomplete/unavailable**. Neither check validates every content hash or guarantees a successful restore on a device.

Checks share the local scan limit and exclude writes to the same device's backup. Use **Cancel check** to cancel the selected scan. Completed results are included in task records and exports. Changing the backup source or writing it again marks old evidence stale; after restart, its current validity is unknown until checked again. Backup execution, checking and history saving are shown separately. A tool success remains an execution success even when checking needs attention. Manual checks do not send notifications.
