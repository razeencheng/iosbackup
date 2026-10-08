package buildinfo

import "testing"

func TestDevelopmentDefaults(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "version", got: Version, want: "v1.5.4"},
		{name: "build date", got: BuildDate, want: "unknown"},
		{name: "commit", got: Commit, want: "unknown"},
		{name: "source URL", got: SourceURL, want: "https://github.com/razeencheng/iosbackup"},
		{name: "description", got: Description, want: "Verified multi-architecture releases to GHCR and Docker Hub"},
		{name: "license", got: LicenseID, want: "AGPL-3.0-only"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %q, want %q", test.got, test.want)
			}
		})
	}
}
