package annotationimport

import (
	"go/token"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
)

func TestResolveAndLookup(t *testing.T) {
	table, diagnostics := Resolve([]annotation.ImportDirective{
		{
			Kind:    annotation.ImportNamed,
			Package: "example.com/spice/web",
			Bindings: []annotation.ImportBinding{
				{Imported: "Controller", Local: "Controller"},
				{Imported: "Get", Local: "GET"},
			},
			Position: token.Position{Filename: "main.go", Line: 3, Column: 1},
		},
		{
			Kind:      annotation.ImportNamespace,
			Package:   "example.com/spice/web",
			Namespace: "web",
			Position:  token.Position{Filename: "main.go", Line: 4, Column: 1},
		},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("Resolve() diagnostics = %v", diagnostics)
	}
	tests := []struct {
		name string
		want annotation.DefinitionReference
	}{
		{
			name: "Controller",
			want: annotation.DefinitionReference{
				Package: "example.com/spice/web",
				Symbol:  "Controller",
			},
		},
		{
			name: "GET",
			want: annotation.DefinitionReference{
				Package: "example.com/spice/web",
				Symbol:  "Get",
			},
		},
		{
			name: "web.Post",
			want: annotation.DefinitionReference{
				Package: "example.com/spice/web",
				Symbol:  "Post",
			},
		},
	}
	for _, test := range tests {
		got, found := table.Lookup(test.name)
		if !found || got != test.want {
			t.Fatalf("Lookup(%q) = %#v, %t, want %#v", test.name, got, found, test.want)
		}
	}
	for _, unknown := range []string{
		"Get",
		"missing.Controller",
		"web",
		"web.deep.Controller",
	} {
		if got, found := table.Lookup(unknown); found {
			t.Fatalf("Lookup(%q) = %#v, true", unknown, got)
		}
	}
}

func TestResolveRejectsCollisions(t *testing.T) {
	table, diagnostics := Resolve([]annotation.ImportDirective{
		{
			Kind:      annotation.ImportNamespace,
			Package:   "example.com/one",
			Namespace: "web",
			Position:  token.Position{Filename: "main.go", Line: 2, Column: 1},
		},
		{
			Kind:    annotation.ImportNamed,
			Package: "example.com/two",
			Bindings: []annotation.ImportBinding{{
				Imported: "Controller",
				Local:    "web",
			}},
			Position: token.Position{Filename: "main.go", Line: 7, Column: 1},
		},
	})
	if len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Error(), `name "web" conflicts`) ||
		!strings.Contains(diagnostics[0].Error(), "main.go:2") {
		t.Fatalf("Resolve() diagnostics = %#v", diagnostics)
	}
	bindings := table.Bindings()
	if len(bindings) != 1 ||
		!bindings[0].Namespace ||
		bindings[0].Reference.Package != "example.com/one" {
		t.Fatalf("Bindings() = %#v", bindings)
	}
}

func TestResolveRejectsUnknownKind(t *testing.T) {
	_, diagnostics := Resolve([]annotation.ImportDirective{{
		Kind:     "future",
		Position: token.Position{Filename: "main.go", Line: 1, Column: 1},
	}})
	if len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Message, "unsupported") {
		t.Fatalf("Resolve() diagnostics = %#v", diagnostics)
	}
}
