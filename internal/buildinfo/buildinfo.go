// Package buildinfo exposes metadata injected into release binaries at link time.
package buildinfo

// These development defaults can be overridden with go build -ldflags -X.
var (
	Version     = "v1.5.5"
	BuildDate   = "unknown"
	Commit      = "unknown"
	SourceURL   = "https://github.com/razeencheng/iosbackup"
	Description = "Backup history, offline browsing, and improved backup management"
)

const LicenseID = "AGPL-3.0-only"
