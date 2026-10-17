package i18n

import (
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCatalogKeyAndParameterParity(t *testing.T) {
	if len(catalog["en"]) == 0 || len(catalog["en"]) != len(catalog["zh-CN"]) {
		t.Fatal("catalog locales have different keys")
	}
	parameters := func(text string) []string {
		values := parameterPattern.FindAllString(text, -1)
		sort.Strings(values)
		return values
	}
	for code, english := range catalog["en"] {
		chinese, ok := catalog["zh-CN"][code]
		if !ok || english == "" || chinese == "" || !strings.HasPrefix(code, "api.") {
			t.Errorf("invalid catalog entry %q", code)
		}
		if !reflect.DeepEqual(parameters(english), parameters(chinese)) {
			t.Errorf("placeholder mismatch for %s", code)
		}
	}
}

func TestRequestLanguageWeightingAndFallback(t *testing.T) {
	for _, tc := range []struct{ accept, want string }{
		{"zh-CN;q=0,en;q=0.8", "en"},
		{"de-DE, zh-CN;q=0.5,en;q=0.9", "en"},
		{"en;q=0.5, zh-CN;q=0.9", "zh-CN"},
		{"en;q=invalid,zh-CN;q=0.1", "zh-CN"},
		{"fr-FR", "en"},
		{"en-AU", "en"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", tc.accept)
		if got := ResolveRequest(r); got != tc.want {
			t.Errorf("%s => %s; want %s", tc.accept, got, tc.want)
		}
	}
}

func TestInterpolationDoesNotReinterpretUserContent(t *testing.T) {
	got := Text("en", "api.unpack_exists", map[string]any{"path": "/backups/{path}/设备"})
	if got != "The unpack destination already exists: /backups/{path}/设备. Choose a different destination." {
		t.Fatal(got)
	}
}

func TestLocaleNormalizationMatchesSupportedLanguageFamilies(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"en-AU", "en"}, {"en-CA", "en"}, {"zh-Hans-CN", "zh-CN"}, {"zh-Hans-SG", "zh-CN"},
		{"zh-TW", ""}, {"zh-HK", ""}, {"zh-Hant", ""}, {"../en", ""}, {"en-<script>", ""},
	} {
		if got := NormalizeLocale(tc.value); got != tc.want {
			t.Errorf("%s => %q, want %q", tc.value, got, tc.want)
		}
	}
}
