package autoconfigure

import (
	"testing"

	"github.com/StevenBuglione/spice/internal/cli"
)

func TestDefaultCommandConstructsCommand(t *testing.T) {
	t.Parallel()

	if command := DefaultCommand(); command == nil {
		t.Fatal("DefaultCommand() = nil")
	}
}

func TestDescriptorDocumentsFallbackProvenance(t *testing.T) {
	t.Parallel()

	descriptor := SpiceAutoConfiguration()
	if descriptor.Review != "docs/dogfooding-readiness.md" ||
		len(descriptor.Beans) != 1 ||
		descriptor.Beans[0].Factory == nil ||
		descriptor.Beans[0].Name != "command" ||
		!descriptor.Beans[0].Fallback {
		t.Fatalf("SpiceAutoConfiguration() = %#v", descriptor)
	}
	if _, valid := descriptor.Beans[0].Factory.(func() *cli.Command); !valid {
		t.Fatalf(
			"Factory type = %T, want func() *cli.Command",
			descriptor.Beans[0].Factory,
		)
	}
}
