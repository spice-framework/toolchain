package adapt

import (
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/application"
	"github.com/spice-framework/toolchain/compiler/load"
)

func TestApplicationAndLoadAdapters(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	applicationSet := Application(root, []application.Diagnostic{{
		Stage:            application.StageApplication,
		Position:         token.Position{Filename: source, Line: 8, Column: 3},
		PhysicalPosition: token.Position{Filename: source, Offset: 42},
		Kind:             "invalid-target",
		Message:          "application marker is invalid",
	}})
	items := applicationSet.Items()
	if len(items) != 1 ||
		items[0].Code != "spice.application.invalid-target" ||
		items[0].Location.Range.Start.Line != 8 ||
		!strings.HasPrefix(items[0].Location.URI, "file:") {
		t.Fatalf("Application() = %#v", items)
	}

	loadSet := Load(root, []load.Diagnostic{{
		Filename: "broken.go",
		Line:     4,
		Column:   9,
		Kind:     "type",
		Message:  "undefined: Missing",
	}})
	loadItems := loadSet.Items()
	if len(loadItems) != 1 ||
		loadItems[0].Code != "spice.load.type" ||
		loadItems[0].Location.Display == nil ||
		loadItems[0].Location.Display.Path != "broken.go" {
		t.Fatalf("Load() = %#v", loadItems)
	}
}

func TestFailureIsSourceLessAndStable(t *testing.T) {
	t.Parallel()
	items := Failure("metadata", "invalid", "selection is invalid").Items()
	if len(items) != 1 ||
		items[0].Code != "spice.metadata.invalid" ||
		items[0].Location.URI != "" ||
		items[0].Message != "selection is invalid" {
		t.Fatalf("Failure() = %#v", items)
	}
}
