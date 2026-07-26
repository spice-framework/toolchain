package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/builtin"
	compilerservice "github.com/StevenBuglione/spice/compiler/service"
)

func TestAnnotationOccurrencesSelectOnlyDeclarationComments(t *testing.T) {
	t.Parallel()
	content := []byte(
		"const text = \"@Application\"\n" +
			"// ordinary @Application text\n" +
			"// @Application\n" +
			"\t// @management.Enable(expose=[\"health\"])\n",
	)
	occurrences := annotationOccurrences(content)
	if len(occurrences) != 2 ||
		occurrences[0].name != "Application" ||
		occurrences[1].name != "management.Enable" {
		t.Fatalf("annotationOccurrences() = %#v", occurrences)
	}
	offset := strings.Index(string(content), "@Application\n")
	got, found := annotationOccurrenceAt(content, offset+2)
	if !found || got.name != "Application" {
		t.Fatalf("annotationOccurrenceAt() = %#v, %t", got, found)
	}
	if _, found := annotationOccurrenceAt(
		content,
		strings.Index(string(content), "\"@Application\"")+2,
	); found {
		t.Fatal("annotationOccurrenceAt(string) found = true")
	}
}

func TestLocalAnnotationReferenceFindsExactDefinitionRow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(annotationReferencePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "# Definitions\n\n| Annotation | Target |\n|---|---|\n" +
		"| `@Application` | Function |\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	reference, found := localAnnotationReference(root, "Application")
	if !found ||
		!strings.HasSuffix(reference.uri, "/docs/annotations.md") ||
		reference.rangeItem.Start.Line != 4 ||
		reference.rangeItem.Start.Character != 3 {
		t.Fatalf("localAnnotationReference() = %#v, %t", reference, found)
	}
	target := localReferenceTarget(reference)
	if !strings.HasSuffix(target, "#5,4") {
		t.Fatalf("localReferenceTarget() = %q", target)
	}
	if _, found := localAnnotationReference(root, "Unknown"); found {
		t.Fatal("localAnnotationReference(unknown) found = true")
	}
}

func TestKnownAnnotationUsesCompilerDefinitions(t *testing.T) {
	t.Parallel()
	definitions := []compilerservice.AnnotationDefinition{{
		Name:    "Application",
		Targets: []annotation.Target{annotation.TargetFunction},
	}}
	if !knownAnnotation(definitions, "Application") ||
		knownAnnotation(definitions, "Unknown") {
		t.Fatalf("knownAnnotation() accepted the wrong definition")
	}
}

func TestBuiltInDefinitionsHaveExactReferenceRows(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root) error = %v", err)
	}
	for _, definition := range builtin.Registry().Definitions() {
		reference, found := localAnnotationReference(root, definition.Name)
		if !found {
			t.Errorf(
				"localAnnotationReference(%q) found = false",
				definition.Name,
			)
			continue
		}
		if reference.rangeItem.Start == reference.rangeItem.End {
			t.Errorf(
				"localAnnotationReference(%q) has an empty range",
				definition.Name,
			)
		}
	}
}
