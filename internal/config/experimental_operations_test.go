package config_test

import (
	"strings"
	"testing"

	"iosbackup/internal/config"
)

func TestExperimentalOperationsDefaultToDisabled(t *testing.T) {
	if config.Default().EnableExperimentalOperations {
		t.Fatal("experimental operations must default to disabled")
	}

	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnableExperimentalOperations {
		t.Fatal("empty environment must keep experimental operations disabled")
	}
}

func TestLoadExperimentalOperationsStrictBoolean(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "enabled", value: "true", want: true},
		{name: "disabled", value: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(func(name string) string {
				if name == "IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS" {
					return tt.value
				}
				return ""
			})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.EnableExperimentalOperations != tt.want {
				t.Fatalf("EnableExperimentalOperations=%v, want %v", cfg.EnableExperimentalOperations, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidExperimentalOperationsBoolean(t *testing.T) {
	_, err := config.Load(func(name string) string {
		if name == "IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS" {
			return "enabled"
		}
		return ""
	})
	const want = "IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS must be true or false"
	if err == nil || err.Error() != want {
		t.Fatalf("error=%v, want %q", err, want)
	}
	if !strings.Contains(err.Error(), "IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS") {
		t.Fatalf("startup configuration error must identify the environment variable: %v", err)
	}
}
