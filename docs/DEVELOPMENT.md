# Development and contributor quick start

[简体中文](DEVELOPMENT.zh-CN.md) · [Contribution rules](../CONTRIBUTING.md) · [Operations manual](OPERATIONS.md)

Set up a development environment for offline tests. Use the operations manual for installation and device work; passing Go tests does not establish device-backup or restore acceptance.

## Prepare the environment

Install Git and Go on a trusted development machine. The current [go.mod](../go.mod) declares language version `go 1.21` and toolchain `go1.26.7`; use the toolchain matching the project/CI. Automatic toolchain selection may download it. “Offline default tests” means the tests themselves do not access external services, not an offline first-time Go installation. Prepare the toolchain and caches before disconnecting if necessary.

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
| `internal/notification` | Notification manager, message types, channel adapters |
| `internal/persistence` | Atomic file writes and secret-storage support |
| `internal/boundedio` | Bounded output and line-reading utilities |
| `internal/buildinfo` | Build identity and version information |
| `release/manifest.env` | Central release version, date, and source metadata |
| `scripts`, `.github/workflows` | Tests, public-tree/license checks, build and release workflows |

The project is no longer one root-level `package main`. Target `./internal/app` for individual application tests and `./cmd/iosbackup` for the executable. `TestPackageDependencyDirection` checks dependency direction. Read existing module tests before changing behavior.

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

```bash
./scripts/check_public_repo.sh --directory .
./scripts/verify_licensing.sh
```

The first checks which files are allowed in the public repository and what they contain. Do not apply it directly to a source repository containing private development materials; maintainers run it on the actual exported public tree. Run the corresponding `*_test.sh` after changing a checker. [ci.yml](../.github/workflows/ci.yml) defines the authoritative CI sequence; additional steps such as vulnerability scanning may need network tools.

Before submitting, inspect `git diff --check` and `git status --short`. Exclude passwords, private configuration files, pairing records, UDIDs, personal backup contents, coverage artifacts, and temporary builds. Record real-data exercises separately from default offline tests.

Maintainers preparing version metadata, release notes, and signed artifacts should use the [release preparation guide](UPDATE_VERSION.en.md). Installed-container upgrades remain in the operations manual.
