package main

import (
	"strings"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	validDigest := "sha256:" + strings.Repeat("a", 64)

	tests := []struct {
		name      string
		input     string
		wantHost  string
		wantRepo  string
		wantRef   string
		wantTag   string
		wantError bool
	}{
		{
			name:     "tagged image",
			input:    "ghcr.io/razeencheng/iosbackup:v1.5.6",
			wantHost: "ghcr.io",
			wantRepo: "razeencheng/iosbackup",
			wantRef:  "v1.5.6",
			wantTag:  "ghcr.io/razeencheng/iosbackup:v1.5.6",
		},
		{
			name:     "untagged image defaults to latest",
			input:    "ghcr.io/razeencheng/iosbackup",
			wantHost: "ghcr.io",
			wantRepo: "razeencheng/iosbackup",
			wantRef:  "latest",
			wantTag:  "ghcr.io/razeencheng/iosbackup:latest",
		},
		{
			name:     "digest only image",
			input:    "ghcr.io/razeencheng/iosbackup@" + validDigest,
			wantHost: "ghcr.io",
			wantRepo: "razeencheng/iosbackup",
			wantRef:  validDigest,
			wantTag:  "ghcr.io/razeencheng/iosbackup:latest",
		},
		{
			name:     "tagged and digest-pinned image strips tag from repo and preserves RepoTag",
			input:    "ghcr.io/razeencheng/iosbackup:v1.5.6@" + validDigest,
			wantHost: "ghcr.io",
			wantRepo: "razeencheng/iosbackup",
			wantRef:  validDigest,
			wantTag:  "ghcr.io/razeencheng/iosbackup:v1.5.6",
		},
		{
			name:     "registry with port plus tag and digest",
			input:    "localhost:5000/org/iosbackup:v1.5.6@" + validDigest,
			wantHost: "localhost:5000",
			wantRepo: "org/iosbackup",
			wantRef:  validDigest,
			wantTag:  "localhost:5000/org/iosbackup:v1.5.6",
		},
		{
			name:      "missing registry host",
			input:     "iosbackup:v1.5.6",
			wantError: true,
		},
		{
			name:      "empty repo before digest",
			input:     "ghcr.io/@" + validDigest,
			wantError: true,
		},
		{
			name:      "empty tag before digest",
			input:     "ghcr.io/razeencheng/iosbackup:@" + validDigest,
			wantError: true,
		},
		{
			name:      "malformed digest",
			input:     "ghcr.io/razeencheng/iosbackup:v1.5.6@sha256:nothex",
			wantError: true,
		},
		{
			name:      "empty tag",
			input:     "ghcr.io/razeencheng/iosbackup:",
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, repo, ref, repoTag, err := parseImageRef(tc.input)
			if tc.wantError {
				if err == nil {
					t.Fatalf("parseImageRef(%q) succeeded unexpectedly: host=%q repo=%q ref=%q repoTag=%q", tc.input, host, repo, ref, repoTag)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseImageRef(%q) unexpected error: %v", tc.input, err)
			}
			if host != tc.wantHost || repo != tc.wantRepo || ref != tc.wantRef || repoTag != tc.wantTag {
				t.Fatalf("parseImageRef(%q) = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					tc.input, host, repo, ref, repoTag, tc.wantHost, tc.wantRepo, tc.wantRef, tc.wantTag)
			}
		})
	}
}
