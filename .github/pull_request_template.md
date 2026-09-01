## Summary

Describe the user-visible outcome and why this change is needed.

## Compatibility and safety

- [ ] Existing HTTP paths, JSON, environment variables, persisted formats, locking, and cancellation behavior remain compatible, or a migration is documented.
- [ ] No credentials, device identifiers, pairing records, backup data, private plans, or unredacted logs are included.
- [ ] New or changed third-party components include locked source, license, notice, and verification updates.
- [ ] User-facing English and Simplified Chinese text remain semantically aligned.

## Verification

List the exact tests and relevant device/container checks run. Explain any verification that could not be completed.

- [ ] `gofmt` reports no changed Go files.
- [ ] `go vet ./...` passes.
- [ ] `go test -count=1 ./...` passes.
- [ ] `go test -race -count=1 ./...` passes when concurrency behavior changed.
- [ ] Public-repository, sensitive-content, and licensing checks pass when public files changed.

## Release impact

State whether `release/manifest.env`, `internal/buildinfo`, `CHANGELOG.md`, image metadata, documentation, or the feature-status matrix must change.
