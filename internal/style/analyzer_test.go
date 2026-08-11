package style

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzerAcceptsValidAndRejectsInvalidStructure(t *testing.T) {
	t.Parallel()
	testdata := analysistest.TestData()
	analyzer := NewAnalyzer()
	if err := analyzer.Flags.Set("config", filepath.Join(testdata, "style.json")); err != nil {
		t.Fatalf("set analyzer configuration: %v", err)
	}
	analysistest.Run(
		t,
		testdata,
		analyzer,
		"example.com/valid",
		"example.com/invalid",
	)
}

func TestInitialismSnakeCase(t *testing.T) {
	t.Parallel()
	for input, wanted := range map[string]string{
		"Order":             "order",
		"OrderService":      "order_service",
		"HTTPController":    "http_controller",
		"OrderID":           "order_id",
		"OIDCConfiguration": "oidc_configuration",
		"UTF8Decoder":       "utf8_decoder",
	} {
		if got := initialismSnakeCase(input); got != wanted {
			t.Errorf("initialismSnakeCase(%q) = %q, want %q", input, got, wanted)
		}
	}
}

func TestTypeFileNameMatchesOnlyCanonicalOrSupportedBuildSuffixes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		actual string
		want   bool
	}{
		{actual: "platform_resolver.go", want: true},
		{actual: "platform_resolver_windows.go"},
		{actual: "platform_resolver_linux_amd64.go"},
		{actual: "platform_resolver_amd64.go"},
		{actual: "platform_resolver_unix.go"},
		{actual: "platform_resolver_helper.go"},
		{actual: "platform_resolver_windows_fast.go"},
		{actual: "other_windows.go"},
		{actual: "platform_resolver.go.txt"},
	} {
		if got := typeFileNameMatches(test.actual, "platform_resolver.go"); got != test.want {
			t.Errorf("typeFileNameMatches(%q) = %t, want %t", test.actual, got, test.want)
		}
	}
}

func FuzzInitialismSnakeCase(f *testing.F) {
	for _, seed := range []string{"Order", "HTTPController", "OrderID", "OIDCConfiguration"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first := initialismSnakeCase(input)
		second := initialismSnakeCase(input)
		if first != second {
			t.Fatalf("initialismSnakeCase is nondeterministic")
		}
	})
}
