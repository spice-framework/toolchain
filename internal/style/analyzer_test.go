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
