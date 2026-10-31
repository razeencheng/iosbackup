# Development and contributor quick start

[简体中文](DEVELOPMENT.zh-CN.md) · [Contribution rules](../CONTRIBUTING.md) · [Operations manual](OPERATIONS.md)

Set up a development environment for offline tests. Use the operations manual for installation and device work; passing Go tests does not establish device-backup or restore acceptance.

## Prepare the environment

Install Git and Go on a trusted development machine. The current [go.mod](../go.mod) declares minimum version `go 1.25.0` and toolchain `go1.27.3`; use the toolchain matching the project/CI. The minimum supports the root-relative filesystem APIs used by storage and is separate from the selected build toolchain. Automatic toolchain selection may download it. “Offline default tests” means the tests themselves do not access external services, not an offline first-time Go installation. Prepare the toolchain and caches before disconnecting if necessary.

The Go module remains standard-library-only. Do not add third-party Go packages. Image components such as libimobiledevice and netmuxd are separate dependencies recorded in [third_party](../third_party/components.lock.json). Pure Go builds and default unit tests do not require a real phone.

Check the environment from the **source repository root** :

```bash
git status --short
go version
go list -m all
```

`go list -m all` should list only module `iosbackup`. Preserve existing workspace changes and use a separate branch for your work.

## Find the code

| Location | Responsibility |
|---|---|
| `cmd/iosbackup` | Executable entry point, process signals, application startup |
| `internal/app` | Orchestration, HTTP/API, device state, backups, scheduling, templates, and static assets |
| `internal/config` | Startup parameter and path parsing/validation |
| `internal/i18n` | Shared language negotiation and API message resources |
| `internal/notification` | Notification manager, message types, channel adapters |
| `internal/backupstore` | Independent history objects, copy/clone preparation, catalogs, read leases and recovery transactions |
| `internal/persistence` | Atomic file writes and secret-storage support |
| `internal/boundedio` | Bounded output and line-reading utilities |
| `internal/buildinfo` | Build identity and version information |
| `release/manifest.env` | Central release version, date, and source metadata |
| `scripts`, `.github/workflows` | Tests, public-tree/license checks, build and release workflows |

The project is no longer one root-level `package main`. Target `./internal/app` for individual application tests and `./cmd/iosbackup` for the executable. `TestPackageDependencyDirection` checks dependency direction. Read existing module tests before changing behavior.

Historical backup implementation notes and lifecycle contracts are in [the backupstore module](../internal/backupstore/README.md). Retention defaults to three natural days including today; policy confirmation, migration and current-source selection live in `internal/app/backup_history*.go`. History HTTP handlers resolve server-side point IDs, with revision checks and one-use policy previews. Exports stream under read leases and idle deadlines. See [history usage](manual/backup-history.md).

History policy updates persist their target and `history_policy_pending` before starting storage preparation. `PUT /api/backup-history/{udid}/policy` returns `202` with a background operation; disconnecting the request does not cancel that operation. The history list exposes `policy_operation` and `can_retry_policy`. `POST /api/backup-history/{udid}/policy-retry` accepts an empty object and resumes only the saved target. A pending transition must finish before changing the target or backup directory. Restarted tasks also resume preparation before writing new device data. Configuration locks never span backup copying, and the last confirmed source remains available until replacement is durable. Store recovery runs under device write admission and respects read leases.

## Make a local change

1. Read [CONTRIBUTING](../CONTRIBUTING.md) and the affected module. Agree on large behavior changes in an Issue; report vulnerabilities through [SECURITY](../SECURITY.md).
2. For a behavior fix, first add a test reproducing the problem. Use `t.TempDir()`, injectable command runners, and local test servers. Do not read a running instance's `/configs` or connect personal devices.
3. Keep existing Chinese messages and English UI equivalents aligned. Establish compatibility before changing HTTP/JSON, environment variables, or persisted formats. Preserve locking, cancellation, and busy-state release conventions.
4. Run `gofmt -w` only on changed Go files, execute relevant tests first, then the pre-submission checks below.
5. Describe the change, executed checks, and remaining gaps in the PR. Update paired manuals. Do not describe source inspection as device acceptance.

Run all commands from the **source repository root** :

```bash
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go build -trimpath -o iosbackup ./cmd/iosbackup
```

`-race` requires a supported development platform and a suitable C toolchain. Do not globally set `CGO_ENABLED=0` before race tests. The final command only builds the program; it does not start a service, access a phone, or send messages.

## Choose focused checks

```bash
# Notification module and application notification behavior
go test -count=1 ./internal/notification
go test -count=1 -run 'TestNotification|TestSendBackup|TestDeviceStatusNotifications' ./internal/app

# Package dependency constraint
go test -count=1 -run '^TestPackageDependencyDirection$' ./internal/app

# Startup configuration and persistence
go test -count=1 ./internal/config ./internal/persistence
```

See the [testing guide](TESTING_GUIDE.en.md) for the full default suite, coverage, benchmarks, and requirements for enabling real-notification tests. Real notifications require the `integration` tag, an explicit sending-confirmation variable, and a separate configuration path. Leave them disabled during ordinary development.

## Internationalization

The supported application languages are `en` and `zh-CN`. Interface language is a browser preference shared by sign-in, console, onboarding and notification settings. At browser startup, use the first supported value from the `iosbk_lang` cookie, `localStorage.iosbk_lang`, the legacy `localStorage.iosbk_guide_lang`, browser language preferences, then English. Go and JavaScript both trim surrounding whitespace, normalize underscores to hyphens and resolve supported aliases, including legacy `zh`. An unsupported explicit choice leaves the current language unchanged.

The server uses the supported language cookie first, then weighted `Accept-Language`, then English. Initial HTML and live changes use the same catalog. On a first visit with only a legacy localStorage preference, the server cannot read that preference: JavaScript corrects the page after startup and saves the cookie for later requests. Same-origin tabs synchronize available stored preferences through storage events and recheck them on focus or page restoration. Storage failures must not break the current page. Do not rely on this preference for isolation between deployments on different ports of one host.

Resources are local and embedded in the binary:

- `internal/app/static/ui-messages.json`: shared UI source for Go templates and the browser, including titles, placeholders, accessibility labels and image alternatives. `/static/ui-messages.js` serves this catalog as a registration script.
- `internal/app/static/i18n.js`: language resolution, named interpolation, attributes, dates and request headers.
- `internal/i18n/messages.json`: API messages used by Go and served to the browser at `/static/api-messages.js`.
- `internal/notification/messages.json`: generated notification titles, default bodies and channel labels.

API keys in `internal/i18n/messages.json` also generate the Go constants in `internal/i18n/codes.go`. Use `i18n.EncryptionPasswordRequired` in Go call sites instead of a literal such as `"api.encryption_password_required"`. After adding or removing a key, run `go generate ./internal/i18n` and commit the generated file with the catalog change. Do not edit `codes.go` manually or rename existing wire values during a symbol refactor. CI checks generated output without modifying it; a Go source check rejects inline API message keys in production code. Protocol tests may retain literal values to detect accidental compatibility changes.

```bash
go generate ./internal/i18n
go run ./internal/i18n/cmd/gencodes -dir internal/i18n -check
```

Use semantic keys and complete messages with named parameters (`{name}` for UI/API, `${name}` for notification templates). Parameter names must match in every language. Do not concatenate sentence fragments, derive keys from translated text, or branch on a translated label. Preserve device names, paths and custom templates. Known failures return a stable `message_code` and `params`; `code` retains any existing endpoint-specific classification (new endpoints default to the message key). Compatibility `error`/`message` fields remain readable, while raw tool output belongs in diagnostic `detail`. UI actions translate `message_code`, with `code` as a compatibility fallback, including when the language changes after an operation. SSE broadcasts machine states and standard timestamps, never one browser's translated state.

Notification validation may also include the optional `message_context` object below. It identifies the affected channel without replacing the reason in `code`, `message_code` or `params`. The compatibility `error` field includes the localized context; clients render the known context around the current-language reason and ignore unknown context kinds. Preserve the configured name literally, interpolate once and keep credentials, secret URLs and raw diagnostics out of this object.

```json
{"kind":"notification_channel","channel":"Email","name":"Family mail"}
```

Backup terminal state may include `last_backup_message_code` and `last_backup_message_params` in status/API/SSE snapshots. These preserve a specific failure and its values for later language changes. Clients prefer a known message code, then fall back to the existing failure classification for older servers or unknown codes. The fields are in-memory state, cleared for a new task or success; existing result codes, `last_backup_error` and persisted backup data remain compatible. Copy parameter maps when capturing errors and building snapshots.

Keep pending actions, unsaved input and feedback as state plus message descriptions. A language change rerenders that state without restarting an operation, clearing a draft or extending a toast's original expiry.

Keep interface language, notification language and scheduling separate. Notification `language` is persisted per deployment; omitted language remains `zh-CN` for compatibility. An empty body override selects the built-in template when preparing a message. Existing nonempty templates are user content, even when they match an older Chinese default: preserve them until the administrator clears them. Queued messages retain their language and rendered content snapshot; later settings do not translate them again. Switching interface language does not change notification delivery or backup times. Scheduling, UI timestamps, notifications and application logs use the deployment `TZ` (default `Asia/Shanghai`). Invalid explicit IANA names prevent startup. Changing `TZ` keeps configured clock windows and reinterprets them in the new zone; backup intervals remain elapsed hours. Historical instants do not change. Use `app.nowTime()` / `app.inDeploymentTime()` for wall-clock data; standard `last_backup_at` timestamps include an offset, while legacy `last_backup` remains a Shanghai wall clock for older clients. The binary embeds Go timezone data for scratch images.

Notification tests use the saved configuration. Disable configuration editing until the first response has actually been accepted, and guard mutation functions too. The UI and test function must block testing without that baseline, during a load or save, while the draft is dirty, or after a failed save confirmation. Preserve edits made during a save, ignore stale load responses and keep the draft on failure. If confirmation fails after a baseline was accepted, preserve editing and retry-save access while testing remains blocked. A save is confirmed by reading the configuration back; it does not automatically send a test. See the [notification manual](manual/notifications.md) for user steps.

Install Node.js (20 or newer) to run the offline JavaScript behavior tests; no npm packages are needed. CI runs the Node test explicitly, so missing Node cannot silently skip internationalization checks:

```bash
node internal/app/testdata/i18n.test.js
go test -count=1 ./internal/i18n ./internal/notification
go test -count=1 -run 'TestSharedI18n|TestI18n|TestDeviceConnectionsAreLanguageIndependent' ./internal/app
```

For new user-facing text, update both resources and test the relevant success/error path. For a new language, add complete catalogs, update the supported-language resolver and selector, document fallback and review terminology. Test live switching, unsaved forms, storage failure, unknown error fallback, narrow layouts and longer translations. Review the actual browser output as well as key/parameter parity.

Application-generated operational diagnostics use fixed English and stable event markers such as `event=backup.scheduled`. This includes startup failures and underlying errors embedded in logs, not only the outer log format. Do not add log translation catalogs or a log-language setting; browser/notification language changes do not affect logs. Shared message errors may reuse the English catalog for their diagnostic text. Keep UI/API user messages and generated notifications localized at their presentation boundary. Raw tool/OS diagnostics, device names, paths and other user-provided data retain their original text, including non-English content. Test complete error chains and original-text preservation; checking event markers alone does not establish this contract.

## Build an image when needed

Prepare Docker/Buildx and access to image/system-component downloads. Build locally from the **source repository root** :

```bash
make build IMAGE=iosbackup TAG=dev PLATFORM=linux/amd64
```

Use `PLATFORM=linux/arm64` for that target. The current `make build` loads a local image without pushing; `make build-multi` exports a local OCI archive. Publication is a separate explicit operation. See the [Makefile](../Makefile) and [release workflow](../.github/workflows/release.yml); keep development verification separate from publication.

Use the local image to verify code changes. Save `compose.yaml` in a separate new test deployment directory, change its `services.iosbackup.image` to `iosbackup:dev`, and run these commands there. `iosbackup:dev` is already local; do not run a remote pull:

```bash
docker compose up -d
docker compose logs iosbackup
```

The test image selection is saved in Compose, so later recreation uses the same image. Follow [installation](manual/installation.md) to inspect generated files and sign in. Ensure no production instance uses the same device, volumes, or mux service. This starts real device services and belongs in a dedicated test environment. A standalone Go binary does not install the device toolchain for you.

## Documentation and public-tree checks

Maintain documents by purpose: `README` provides the overview, `QUICKSTART` the short route to a first USB backup, `OPERATIONS` navigation, and `docs/manual/` complete task procedures. `FEATURE_STATUS` defines maturity. Notification fields and events belong in the notification manual, the Webhook contract in `WEBHOOK_GUIDE`, and test scope and result-recording requirements in `TESTING_GUIDE`. The old notification guide and test summary retain links only. Update both languages and their entry links when changing procedures.

From the root of a public checkout, run:

New public manuals must also register both language pages and navigation in `website/content-map.mjs`. Documentation links trigger the separate [website.yml](../.github/workflows/website.yml) checks; passing application tests does not validate the website build. With Node.js 24, run:

```sh
npm --prefix website ci
npm --prefix website test
npm --prefix website run build
```

```bash
./scripts/check_public_repo.sh --directory .
./scripts/verify_licensing.sh
```

The first checks which files are allowed in the public repository and what they contain. Do not apply it directly to a source repository containing private development materials; maintainers run it on the actual exported public tree. Run the corresponding `*_test.sh` after changing a checker. [ci.yml](../.github/workflows/ci.yml) defines the authoritative CI sequence; additional steps such as vulnerability scanning may need network tools.

Before submitting, inspect `git diff --check` and `git status --short`. Exclude passwords, private configuration files, pairing records, UDIDs, personal backup contents, coverage artifacts, and temporary builds. Record real-data exercises separately from default offline tests.

Maintainers preparing version metadata, release notes, and signed artifacts should use the [release preparation guide](UPDATE_VERSION.en.md). Installed-container upgrades remain in the operations manual.
