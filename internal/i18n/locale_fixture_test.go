package i18n

import (
	"encoding/json"
	"os"
	"testing"
)

func TestLocaleNormalizationSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/locale-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Input, Expected string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Input, func(t *testing.T) {
			if got := NormalizeLocale(tc.Input); got != tc.Expected {
				t.Fatalf("%q => %q, want %q", tc.Input, got, tc.Expected)
			}
		})
	}
}
