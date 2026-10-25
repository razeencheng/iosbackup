# Historical backup storage

This internal package is the storage foundation for historical restore points. The application does not expose historical backup retention or export yet. Ordinary backups still use their existing writable location.

## Ownership and lifecycle

- The application registers an unpredictable store ID and absolute location before calling `Create`. `Open` requires that ID and the source UDID. Missing or unreadable registered storage is never recreated as an empty store.
- An OS file lock excludes a second writer. `Open` requires `Recover` before writes or leases. Recovery preserves unknown content and blocks mutations when its ownership cannot be established.
- `NewWork` creates an owned transaction and a new parent directory. Device commands may write only into that work directory. The caller must stop and join all external writers before calling `Commit` or `AbortWork`.
- A published point is never a device command destination. Copying uses independent files or a probed Linux FICLONE operation, never hard links. The application serializes preparation through one large-I/O slot.
- `Acquire` holds a read lease until `Close`. Its root handle is for read-only callers. Recycle and store closure reject active leases. Retention policy and administrator confirmation belong in the application layer; this package additionally protects pinned/manual points and current baselines.

## Publication and recovery

A transaction advances from `work` to `sealing` and then `ready`. Sealing syncs data and writes a descriptor. Ready contains the expected source fingerprint. Publication moves the complete work object into `objects/`, syncs both parents, atomically publishes the catalog, then removes the transaction. Existing points remain in the catalog until a separate authorized recycle transaction succeeds.

Errors after a possible persistent change return `ErrUncertain`. The caller must preserve the work/object and run recovery; it must not interpret that error as permission to remove the result. Recovery resyncs visible state, completes ready publications, and reports owned uncommitted work separately. Uncommitted work is not automatically deleted while an external writer might still exist.

The source fingerprint detects file identity, size and timestamp changes, with bounded content digests for the root Info/Status/Manifest plist files. It is not a checksum of every payload and does not establish restorability. Source read leases and the absence of external writers remain necessary. Privileged modifications outside the application are outside the immutable-object contract.

Recycle first records intent and removes the catalog reference durably. Only then does it remove the owned object and descriptor. Recovery checks descriptor consistency and treats a missing descriptor as a completed cleanup only when the object and catalog entry are also gone. Unknown directories and conflicting evidence are preserved.

## Filesystem and toolchain boundaries

The target must support file and directory sync, same-filesystem rename and process locks. The configured Go toolchain supplies root-relative atomic operations; builds with Go earlier than 1.25 return an unsupported error instead of falling back to unconfined path writes. No additional Go dependencies or CGO are required.

Capacity checks reserve metadata headroom, inspect available bytes/inodes when supported and report quota knowledge separately. Actual writes must still handle ENOSPC and other storage errors. Per-file allocated-block counts are not an exclusive physical-space measurement for shared extents.

Tests cover ordinary copy isolation, cancellation, insufficient capacity, partial publication/recycle faults, leases, identity mismatch, unknown data and subprocess exit recovery. Linux tmpfs tests exercise ordinary copy and unsupported-clone fallback. A supporting reflink mount and target NAS durability still need deployment validation. Process termination does not simulate power loss.
