package config_test

import (
	"iosbackup/internal/config"
	"testing"
	"time"
)

func TestLoadRejectsInvalidDeploymentTimezone(t *testing.T) {
	for _, zone := range []string{"Mars/Olympus", "Local", "/etc/localtime", "../UTC"} {
		t.Run(zone, func(t *testing.T) {
			_, err := config.Load(mapEnv(map[string]string{"TZ": zone}))
			if err == nil {
				t.Fatalf("invalid explicit TZ %q must prevent startup", zone)
			}
		})
	}
}

func TestDeploymentTimezones(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		offset     int
	}{
		{"", "Asia/Shanghai", 8 * 3600}, {"UTC", "UTC", 0},
		{"Asia/Kolkata", "Asia/Kolkata", 19800}, {"America/New_York", "America/New_York", -4 * 3600},
	} {
		t.Run(tc.want, func(t *testing.T) {
			cfg, err := config.Load(mapEnv(map[string]string{"TZ": tc.name}))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Timezone != tc.want || cfg.Location.String() != tc.want {
				t.Fatalf("unexpected timezone: %+v", cfg)
			}
			_, offset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).In(cfg.Location).Zone()
			if offset != tc.offset {
				t.Fatalf("offset=%d want %d", offset, tc.offset)
			}
		})
	}
}
