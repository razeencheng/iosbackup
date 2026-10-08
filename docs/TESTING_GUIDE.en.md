# Testing guide

[简体中文](TESTING_GUIDE.md) · [Development](DEVELOPMENT.md)

This guide describes the current `cmd/` and `internal/` package layout. Run every command from the **source repository root** . Default tests use temporary state, command substitutes, and local test services. They do not send real notifications or require a phone. “Offline” still allows localhost listeners such as `httptest`; initial toolchain downloads and CI vulnerability scans need separate preparation.

## 1. Check the environment

```bash
go version
go list -m all
git status --short
```

Follow [go.mod](../go.mod), currently specifying `go1.26.7`. The module list should contain only `iosbackup`. Do not use production configuration, mount real backups, or provide real notification credentials to default tests.

## 2. Run default tests

For routine changes, run the affected packages before one complete test pass:

```bash
go test -count=1 ./internal/notification
go test -count=1 ./internal/config ./internal/persistence
go test -count=1 ./...
```

For static and concurrency checks:

```bash
go vet ./...
go test -race -count=1 ./...
```

Race tests require a supported platform and C toolchain. Do not run them with the static-release build setting `CGO_ENABLED=0`. On a nonzero exit, retain the package, test name, and error output, and identify the failure before deciding to retry.

The existing `./scripts/run_tests.sh` runs notification/configuration subsets, the full suite, coverage, and all benchmarks, generating `coverage.out` and `coverage.html`. It repeats some tests, so use it when that report set is needed; it is not required for every documentation change. It also verifies that `TestRealNotice` is absent from the default test list.

## 3. Target a test or module

```bash
# Application notification behavior and event messages
go test -v -count=1 -run 'TestNotification|TestSendBackup|TestDeviceStatusNotifications' ./internal/app

# Rules and continuing delivery after an individual failure
go test -v -count=1 -run '^TestManagerFiltersRulesAndContinuesAfterNotifierFailure$' ./internal/notification

# Package dependency direction
go test -v -count=1 -run '^TestPackageDependencyDirection$' ./internal/app

# List current tests without running test functions
go test -list . ./internal/notification ./internal/app
```

`go test ... .` targets only the current package and is no longer the general application-test entry point. Use `./internal/app`, `./internal/notification`, or `./...` for the intended scope. `-list` still builds and initializes test programs; it is not a plain source search.

## 4. Coverage and benchmarks

```bash
go test -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html

go test -run='^$' -bench='^BenchmarkNotificationSend$' -benchmem ./internal/app
```

Coverage artifacts are local generated files in the current directory. Neither a coverage percentage nor a single benchmark proves every path correct. Compare benchmarks on the same machine with the same toolchain and parameters, noting variability; use the report fields at the end of this guide. `-run='^$'` avoids rerunning ordinary tests during a benchmark command.

## 5. Real notifications: disabled by default

`TestRealNotice` is in [`internal/app/notifiers_integration_test.go`](../internal/app/notifiers_integration_test.go). It requires **two explicit opt-ins** : the `integration` build tag and a sending-confirmation variable. A separate configuration path is also mandatory. The build tag alone skips the test; confirmation without a configuration path fails. The test does not guess the location of production `/configs`.

Run the following only in an isolated environment when you explicitly intend to send messages to test recipients you control. It was not executed for this documentation update:

```bash
IOSBK_RUN_REAL_NOTIFICATIONS=YES_I_KNOW_THIS_SENDS_MESSAGES \
IOSBK_INTEGRATION_NOTIFICATION_CONFIG=/absolute/path/to/test-notifications.json \
go test -tags=integration -run '^TestRealNotice$' -count=1 ./internal/app
```

Configuration must enable the global switch and at least one validated test channel. The current helper loads Telegram, Email, WeCom, and Webhook; **it does not include Bark** . Each enabled channel receives 6 directly sent messages: start, success, failure, online, offline, and system error. This is not one rule-filtered test event.

Do not reuse production configuration. It may contain tokens, mail passwords, and full destination addresses; keep it outside the repository with restricted permissions. Clean up test inboxes and credentials afterward. Do not add this command to default CI or persist its confirmation variable in shell startup files.

## 6. Select or add tests by responsibility

This index combines test locations and representative responsibilities, reviewed on 2026-10-06. A corresponding test does not establish coverage of every device, failure condition, or third-party service.

| Change | Test entry point | Representative checks |
|---|---|---|
| Notification manager | [manager_test.go](../internal/notification/manager_test.go) | Event rules, templates, bounded queue, message copies, cancellation, continued processing after one channel fails |
| Channel adapters | [adapters_test.go](../internal/notification/adapters_test.go) | Telegram/SMTP/WeCom/Bark/Webhook requests and errors, validation, outbound address restrictions, SMTP TLS and deadlines |
| Application notification integration | [notification_test.go](../internal/app/notification_test.go), [notification_security_test.go](../internal/app/notification_security_test.go) | Backup/device/system messages, rule integration, notification persistence, secret formats and protection |
| Startup configuration and persistence | `*_test.go` in [config](../internal/config) and [persistence](../internal/persistence) | Parameter validation, defaults, experimental switches, atomic writes, and secret storage |
| Application workflows | Corresponding behavior files in [app](../internal/app) | HTTP authentication/CSRF, device removal, busy states, progress/timeouts, and experimental gates |
| Package structure | [architecture_test.go](../internal/app/architecture_test.go) | Entry-point/leaf dependency direction and application export boundaries |
| Distribution boundaries | Corresponding `*_test.sh` in [scripts](../scripts) in the public checkout | Public files/sensitive content, release metadata, license closure, and checker regressions |

Assert observable behavior through outputs, persisted results, or call boundaries. Wait for asynchronous events with bounded channels/contexts instead of longer fixed sleeps. Use temporary directories and fixed fake identifiers, protect shared state, and close servers/processes afterward.

## 7. Common failures

- **Toolchain download fails:** prepare the matching Go version; this does not imply tests require real notification networks.
- **Sandbox rejects local listeners:** use an environment allowing localhost test servers. Do not replace them with real remote services.
- **Race report or timeout:** retain the first failing stack and output, then inspect ownership, locks, and cancellation. An intermittent passing retry is not a fix.
- **No matching test runs:** use `-list` to check the name, package, and regular expression.

See [ci.yml](../.github/workflows/ci.yml) for the exact CI sequence.

## 8. Record evidence and validation limits

Keep three kinds of results separate; none substitutes for another:

1. **Default offline tests:** validate selected code paths and regression assertions, not real-device communication or delivery by external services.
2. **Real-notification integration:** establishes the sending result of these adapter calls. See section 5 for opt-ins and channel scope. It does not replace Web UI persistence, event-rule routing, or real backup-event acceptance.
3. **Device acceptance:** requires a device, storage, connection, and operator. Record USB/Wi-Fi backups, encrypted reads, and device restoration separately. Neither Go tests nor an online-device screenshot substitutes for these results.

The existing [Beta ARM64 tutorial record](images/tutorial/README.md) includes a successful USB job and an on-disk completion-marker check. It does not validate encrypted-manifest reading or device restoration. The Synology manual was also not repeated step by step on a real NAS during this documentation update.

Retain these fields in a PR, CI run, or test record:

| Field | Content |
|---|---|
| Code identity | Commit SHA, and whether uncommitted changes were present |
| Environment | Go version, OS/architecture; image digest, device OS, and connection type for device tests |
| Commands and actions | Complete commands, tags, package paths, or device actions; describe secret inputs without disclosing credentials |
| Result | Exit code, failing tests, necessary logs, or final job result; distinguish skipped and unexecuted checks |
| Attachments | Generation time and comparison conditions for coverage/benchmarks; screenshot provenance and redaction |
| Limits | Unexecuted scenarios and conclusions still requiring an external service or device |

This guide does not assert that the current workspace passed every check. Results must come from command output or CI for the relevant commit. Without fresh evidence, retain “not run/pending validation” rather than carrying forward an old “all tests passed” claim.
