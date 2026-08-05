package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/toolchain/compiler/diagnostic"
	compilerservice "github.com/spice-framework/toolchain/compiler/service"
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

func TestDefinitionForOccurrenceUsesExactImportedDescriptor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.go")
	source := []byte(
		"package main\n\n" +
			"// @import { Application as App } from \"example.com/sdk/core\"\n" +
			"// @App\n" +
			"func main() {}\n",
	)
	descriptorPath := filepath.Join(root, "descriptor.go")
	var occurrence annotationOccurrence
	for _, candidate := range annotationOccurrences(source) {
		if candidate.name == "App" {
			occurrence = candidate
			break
		}
	}
	if occurrence.name == "" {
		t.Fatal("annotationOccurrences() omitted @App")
	}
	location := diagnostic.SourceLocation(
		root,
		sourcePath,
		sourcePath,
		4,
		4,
		occurrence.start,
	)
	definitionLocation := diagnostic.SourceLocation(
		root,
		descriptorPath,
		descriptorPath,
		12,
		6,
		100,
	)
	metadata := metadataView{
		annotations: []compilerservice.Annotation{{
			Name:              "core.Application",
			Spelling:          "App",
			DefinitionPackage: "example.com/sdk/core",
			DefinitionSymbol:  "Application",
			Location:          location,
		}},
		definitions: []compilerservice.AnnotationDefinition{{
			Name:                  "core.Application",
			DescriptorPackage:     "example.com/sdk/core",
			DescriptorSymbol:      "Application",
			DescriptorLocation:    definitionLocation,
			HasDescriptorLocation: true,
		}},
	}
	got, found := definitionForOccurrence(
		document{path: sourcePath, content: source},
		metadata,
		occurrence,
	)
	if !found ||
		got.DescriptorLocation.Path != filepath.ToSlash(descriptorPath) {
		t.Fatalf("definitionForOccurrence() = %+v, %t", got, found)
	}
	link := locationLink(source, occurrence, got.DescriptorLocation)
	if !strings.HasSuffix(link.TargetURI, "/descriptor.go") ||
		link.TargetSelectionRange.Start !=
			(protocolPosition{Line: 11, Character: 5}) {
		t.Fatalf("locationLink() = %+v", link)
	}
}

func TestGoInterfaceReferenceUsesSpiceNamespaceImport(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(
		"C:",
		"workspace",
		"implementation",
		"service.go",
	)
	content := []byte(
		"package implementation\n\n" +
			"// @import { Implements } from \"example.com/sdk/core\"\n" +
			"// @import * as contracts from \"example.com/contracts\"\n\n" +
			"// @Implements(contracts.Processor[string])\n" +
			"type Service struct{}\n",
	)
	target := diagnostic.SourceLocation(
		"C:/workspace",
		"C:/workspace/contracts/processor.go",
		"C:/workspace/contracts/processor.go",
		9,
		6,
		100,
	)
	metadata := metadataView{
		definitions: []compilerservice.AnnotationDefinition{{
			Name:              "core.Implements",
			DescriptorPackage: "example.com/sdk/core",
			DescriptorSymbol:  "Implements",
			Arguments: []compilerservice.AnnotationArgument{{
				Name:        "interfaces",
				ValueDomain: annotation.ValueDomainGoInterface,
				Positional:  true,
				Variadic:    true,
			}},
		}},
		goInterfaces: compilerservice.GoInterfaceCatalog{
			Packages: []compilerservice.GoInterfacePackage{{
				Name: "contracts",
				Path: "example.com/contracts",
				Interfaces: []compilerservice.GoInterface{{
					Name:        "Processor",
					PackageName: "contracts",
					PackagePath: "example.com/contracts",
					Location:    target,
					HasLocation: true,
				}},
			}},
		},
	}
	offset := strings.Index(string(content), "Processor") + 3
	reference, found := goInterfaceReferenceAt(
		document{path: sourcePath, content: content},
		metadata,
		offset,
	)
	if !found ||
		reference.contract.Name != "Processor" ||
		string(content[reference.start:reference.end]) !=
			"contracts.Processor" ||
		reference.contract.Location.Path != target.Path {
		t.Fatalf("goInterfaceReferenceAt() = %#v, %t", reference, found)
	}
}
