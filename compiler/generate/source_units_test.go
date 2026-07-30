package generate

import "testing"

func TestGeneratedSourceDirectoryEscapesGoImportBoundaries(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"_root":                   "_root",
		"orders/service":          "orders/service",
		"internal/cli":            "internal_/cli",
		"modules/internal/client": "modules/internal_/client",
		"vendor/example/client":   "vendor_/example/client",
	}
	for source, expected := range tests {
		t.Run(source, func(t *testing.T) {
			t.Parallel()

			if actual := generatedSourceDirectory(source); actual != expected {
				t.Fatalf(
					"generatedSourceDirectory(%q) = %q, want %q",
					source,
					actual,
					expected,
				)
			}
		})
	}
}
