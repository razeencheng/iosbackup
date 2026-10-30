# Historical backup storage

This internal package stores historical restore points and plans retention. The application backend integrates isolated backup work, migration, policy changes and generation-aware reads. The history API, export and settings UI are separate follow-up stages; history remains disabled by default.

## Ownership and lifecycle

- The application registers an unpredictable store ID and absolute location before calling `Create`. `Open` requires that ID and the source UDID. Missing or unreadable registered storage is never recreated as an empty store.
- An OS file lock excludes a second writer. `Open` requires `Recover` before writes or leases. Recovery preserves unknown content and blocks mutations when its ownership cannot be established.
- `NewWork` creates an owned transaction and a new parent directory. Device commands may write only into that work directory. The caller must stop and join all external writers before calling `Commit` or `AbortWork`.
- A published point is never a device command destination. Copying uses independent files or a probed Linux FICLONE operation, never hard links. The application serializes preparation through one large-I/O slot.
- `Acquire` holds a read lease until `Close`. Its root handle is for read-only callers. Recycle and store closure reject active leases. `PlanRetention` defines calendar-day retention, and the application supplies the confirmed policy, active epoch and generation. This package additionally protects pinned/manual points, current baselines and active readers at execution time.

## Publication and recovery

A transaction advances from `work` to `sealing` and then `ready`. Sealing syncs data and writes a descriptor. Ready contains the expected source fingerprint. Publication moves the complete work object into `objects/`, syncs both parents, atomically publishes the catalog, then removes the transaction. Existing points remain in the catalog until a separate authorized recycle transaction succeeds.

Errors after a possible persistent change return `ErrUncertain`. The caller must preserve the work/object and run recovery; it must not interpret that error as permission to remove the result. Recovery resyncs visible state, completes ready publications, and reports owned uncommitted work separately. Uncommitted work is not automatically deleted while an external writer might still exist.

The source fingerprint detects file identity, size and timestamp changes, with bounded content digests for the root Info/Status/Manifest plist files. It is not a checksum of every payload and does not establish restorability. Source read leases and the absence of external writers remain necessary. Privileged modifications outside the application are outside the immutable-object contract.

Recycle first records intent and removes the catalog reference durably. Only then does it remove the owned object and descriptor. Recovery checks descriptor consistency and treats a missing descriptor as a completed cleanup only when the object and catalog entry are also gone. Unknown directories and conflicting evidence are preserved.

## Filesystem and toolchain boundaries

The target must support file and directory sync, same-filesystem rename and process locks. The configured Go toolchain supplies root-relative atomic operations; builds with Go earlier than 1.25 return an unsupported error instead of falling back to unconfined path writes. No additional Go dependencies or CGO are required.

Capacity checks reserve metadata headroom, inspect available bytes/inodes when supported and report quota knowledge separately. Actual writes must still handle ENOSPC and other storage errors. Per-file allocated-block counts are not an exclusive physical-space measurement for shared extents.

Tests cover ordinary copy isolation, cancellation, insufficient capacity, partial publication/recycle faults, leases, identity mismatch, unknown data and subprocess exit recovery. Linux tmpfs tests exercise ordinary copy and unsupported-clone fallback. A supporting reflink mount and target NAS durability still need deployment validation. Process termination does not simulate power loss.

## Retention and metadata changes

The default window is three natural days including today, configurable from 1 to 365 days. Within each stored timezone/day bucket, the latest normal point wins. Missing days do not create synthetic points. Pinning, manual retention, old generations/epochs, the latest normal baseline and active read leases take precedence. Pending results have an independent latest-one rule within the current generation and epoch. A new result must be committed before it can supersede an older result.

`PlanGC` returns the catalog revision, eligible IDs and reasons for retaining other points. `GC` rechecks the revision and leases under the catalog lock. An occupied point is skipped without blocking cleanup of other eligible points. Atomic metadata transactions keep descriptors and the catalog consistent when pinning or freezing a store; recovery can finish any interrupted step. Pinning never changes a check result or promotes a pending point to a baseline.

The application persists an enable/disable epoch and source references in `history-stores.json`. Disabling first records intent, freezes all existing points as manual, then reliably prepares a separate writable current set. Re-enabling starts a new epoch and leaves those manual points protected. Shrinking an active policy requires a matching preview; revisions or affected IDs that changed require a new preview. Policy changes hold the same device admission as backups.

Every historical backup follows prepare → device command → quick check → normal/pending publication → eligible cleanup. No compatible baseline means an empty work set and an explicit full request. Tool success updates LastBackup independently of check and publication status. Failures or stops clean only unpublished work after command readers have exited; uncertain commits stay available to recovery. Startup reconciles the latest normal source from committed catalog evidence. Missing registered stores block affected operations but do not stop unrelated devices from starting.

Legacy directories remain untouched. A checked legacy source with a completion date in its own metadata can be adopted; a device-wide LastBackup value is not source evidence. Unknown completion time remains an explicitly marked, manually retained pending result. Existing encrypted-password aliases are copied into immutable credential references, but those candidates alone do not establish baseline compatibility. Imports cannot inherit a verified current password generation solely from an encryption flag. Reads use the source generation's credential, not a device's newer password alias. Writes to a disabled-mode current set invalidate prior completion/credential evidence before the tool starts; only a new successful checked result reinstates it.

Current-backup reads resolve the saved source, with immutable read leases for external queries. Unpack output lives outside backup sets in `.iosbackup-unpacked/`. Experimental device restore receives a disposable independent work copy. The legacy delete action cannot delete an immutable point; after history is disabled it can delete the separate writable current set. Deleting that set does not delete saved points or silently recreate the deleted current data from them.

Copy and lifecycle I/O phases have a six-hour cancellation bound; quick checks retain their separate 30-second bound. Recovery reports keep owned unpublished work visible rather than guessing that an orphaned external writer has exited. Full payload verification, target NAS durability, reflink-capable storage and physical-device restore remain acceptance tasks.

## Historical reads and complete export

`Lease.Export` streams a `.tar` rooted at the lease's original source UDID. Required metadata, safe ordinary entries, source stamps and read/write failures are checked; a failure never closes the tar as a successful archive. Hidden backup files are included. Only the already reserved unpack output is excluded; application indexes, logs and credentials live outside this root. Directory batches and copy buffers are bounded. The HTTP layer holds the lease from session creation until expiry, cancellation or the actual end of streaming; four prepared/live sessions and one active stream bound resource usage. Write deadlines measure idle writes, not a short total download time. Server transfer completion does not claim browser filesystem completion.

Explicit deletion has a separate confirmation contract from automatic recycling. Revision and active readers are rechecked under the store lock before the application records a current-source deletion intent. A durable explicit-delete transaction can remove pins/manual points and reselect or remove that generation's baseline. Metadata review transactions persist check reason and coverage with the point without promoting pending data. The original qualification check remains unchanged.
