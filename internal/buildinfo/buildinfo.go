// Package buildinfo exposes metadata injected into release binaries at link time.
package buildinfo

// These development defaults can be overridden with go build -ldflags -X.
var (
	Version     = "v1.5.4"
	BuildDate   = "unknown"
	Commit      = "unknown"
	SourceURL   = "https://github.com/razeencheng/iosbackup"
	Description = "Verified multi-architecture releases to GHCR and Docker Hub"
)

const LicenseID = "AGPL-3.0-only"
