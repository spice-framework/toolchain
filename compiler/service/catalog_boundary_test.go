package service

import (
	"context"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/toolchain/compiler/annotationcatalog"
	"github.com/spice-framework/toolchain/compiler/application"
	"github.com/spice-framework/toolchain/compiler/diagnostic"
)

func TestAnnotationCatalogCachesClonesAndInvalidatesWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeServiceCatalogFile(t, root, "go.mod", `module example.com/application

go 1.26.0

tool example.com/plugin/cmd/spice-annotations
`)
	writeServiceCatalogFile(t, root, "vendor/modules.txt", `# example.com/plugin v1.2.3
## explicit; go 1.26.0
example.com/plugin/annotation/web
`)
	descriptorPath := "vendor/example.com/plugin/annotation/web/controller.go"
	writeServiceCatalogFile(t, root, descriptorPath, `package web

import "github.com/spice-framework/spice/annotation/sdk"

// Controller documents the catalog descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{
		Name: "web.Controller",
		Summary: "Catalog controller.",
		Implementation: sdk.Implementation{
			Tool: "example.com/plugin/cmd/spice-annotations",
			Handler: ControllerHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}
`)
	catalogService, serviceErr := New(Config{})
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	var nilContext context.Context
	if _, catalogErr := catalogService.AnnotationCatalog(nilContext, root); catalogErr == nil ||
		!strings.Contains(catalogErr.Error(), "context") {
		t.Fatalf("AnnotationCatalog(nil) error = %v", catalogErr)
	}

	first, catalogErr := catalogService.AnnotationCatalog(context.Background(), root)
	if catalogErr != nil {
		t.Fatalf("AnnotationCatalog(first) error = %v", catalogErr)
	}
	if len(first) != 1 || first[0].Name != "Controller" ||
		first[0].DescriptorPackage != "example.com/plugin/annotation/web" ||
		!first[0].Implementation.Authorized ||
		first[0].Provenance.Version != "v1.2.3" {
		t.Fatalf("AnnotationCatalog(first) = %#v", first)
	}
	first[0].Name = "mutated"
	if removeErr := os.Remove(filepath.Join(root, filepath.FromSlash(descriptorPath))); removeErr != nil {
		t.Fatal(removeErr)
	}
	second, catalogErr := catalogService.AnnotationCatalog(context.Background(), root)
	if catalogErr != nil || len(second) != 1 || second[0].Name != "Controller" {
		t.Fatalf("AnnotationCatalog(cached) = %#v, %v", second, catalogErr)
	}
	if invalidateErr := catalogService.InvalidateAnnotationCatalog(root); invalidateErr != nil {
		t.Fatalf("InvalidateAnnotationCatalog() error = %v", invalidateErr)
	}
	third, catalogErr := catalogService.AnnotationCatalog(context.Background(), root)
	if catalogErr != nil {
		t.Fatalf("AnnotationCatalog(after invalidation) error = %v", catalogErr)
	}
	if len(third) != 0 {
		t.Fatalf("AnnotationCatalog(after invalidation) = %#v", third)
	}
}

func TestAnnotationCatalogRejectsUnsafeRoots(t *testing.T) {
	t.Parallel()
	catalogService, serviceErr := New(Config{})
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	if _, catalogErr := catalogService.AnnotationCatalog(context.Background(), ""); catalogErr == nil ||
		!strings.Contains(catalogErr.Error(), "must not be empty") {
		t.Fatalf("AnnotationCatalog(empty) error = %v", catalogErr)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if catalogErr := catalogService.InvalidateAnnotationCatalog(missing); catalogErr == nil ||
		!strings.Contains(catalogErr.Error(), "inspect annotation catalog") {
		t.Fatalf("InvalidateAnnotationCatalog(missing) error = %v", catalogErr)
	}
	file := filepath.Join(t.TempDir(), "workspace.txt")
	if writeErr := os.WriteFile(file, []byte("not a directory"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, rootErr := normalizedCatalogRoot(file); rootErr == nil ||
		!strings.Contains(rootErr.Error(), "must be a directory") {
		t.Fatalf("normalizedCatalogRoot(file) error = %v", rootErr)
	}
}

func TestCatalogDefinitionCarriesDescriptorAndReplacementProvenance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	position := token.Position{
		Filename: filepath.Join(root, "annotation", "feature.go"),
		Line:     8, Column: 6, Offset: 91,
	}
	definitions := catalogDefinitions(root, []annotationcatalog.Candidate{{
		Package: "example.com/plugin/annotation", Symbol: "Feature",
		Summary: "Feature summary.", Tool: "example.com/plugin/cmd/annotations",
		Handler: "FeatureHandler", Protocol: "spice.annotation/v1alpha2",
		Module: "example.com/plugin", Version: "v1.4.0",
		ReplacementModule: "example.com/local", ReplacementDir: t.TempDir(),
		LocalReplacement: true, ToolAuthorized: true,
		DescriptorPosition: position,
	}})
	if len(definitions) != 1 || !definitions[0].HasDescriptorLocation ||
		definitions[0].DescriptorLocation.Range.Start.Line != 8 ||
		definitions[0].Implementation.Handler != "FeatureHandler" ||
		!definitions[0].Provenance.LocalReplacement {
		t.Fatalf("catalogDefinitions() = %#v", definitions)
	}
}

func TestSelectTargetRequiresOneUnambiguousApplication(t *testing.T) {
	t.Parallel()
	targets := []application.Target{
		{Name: "Server", PackagePath: "example.com/app/cmd/server", SymbolID: "server.Main"},
		{Name: "server", PackagePath: "example.com/app/cmd/worker", SymbolID: "worker.Main"},
	}
	if _, err := selectTarget(nil, ""); err == nil || !strings.Contains(err.Error(), "no @Application") {
		t.Fatalf("selectTarget(empty) error = %v", err)
	}
	if got, err := selectTarget(targets[:1], ""); err != nil || got.SymbolID != "server.Main" {
		t.Fatalf("selectTarget(single) = %#v, %v", got, err)
	}
	if _, err := selectTarget(targets, ""); err == nil || !strings.Contains(err.Error(), "multiple @Application") {
		t.Fatalf("selectTarget(multiple) error = %v", err)
	}
	if got, err := selectTarget(targets, "example.com/app/cmd/worker"); err != nil || got.SymbolID != "worker.Main" {
		t.Fatalf("selectTarget(package) = %#v, %v", got, err)
	}
	if got, err := selectTarget(targets, "server.Main"); err != nil || got.Name != "Server" {
		t.Fatalf("selectTarget(symbol) = %#v, %v", got, err)
	}
	if _, err := selectTarget(targets, "SERVER"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("selectTarget(ambiguous) error = %v", err)
	}
	if _, err := selectTarget(targets, "missing"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("selectTarget(missing) error = %v", err)
	}
}

func TestOverlaySafeFixesConvertRawAnnotationsWithDocumentVersion(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "main.go")
	content := []byte("package main\n\t@Application\nfunc main() {}\n")
	location := diagnostic.SourceLocation("", file, file, 2, 2, len("package main\n\t"))
	set := diagnostic.NewSet(diagnostic.New(
		diagnostic.Code("load", "syntax"),
		diagnostic.SeverityError,
		"invalid character '@'",
		location,
	))
	converted := overlaySafeFixes(set, map[string]Document{
		file: {Version: 17, Content: content},
	}).Items()
	if len(converted) != 1 || len(converted[0].Fixes) != 1 {
		t.Fatalf("overlaySafeFixes() = %#v", converted)
	}
	edit := converted[0].Fixes[0].Edits[0]
	if edit.NewText != "// " || edit.DocumentVersion == nil || *edit.DocumentVersion != 17 ||
		edit.Location.Range.Start.Offset != len("package main\n\t") ||
		edit.Location.Range.Start != edit.Location.Range.End {
		t.Fatalf("raw annotation edit = %#v", edit)
	}

	notLoad := diagnostic.NewSet(diagnostic.New(
		diagnostic.Code("validation", "syntax"),
		diagnostic.SeverityError,
		"not a load diagnostic",
		location,
	))
	if got := overlaySafeFixes(notLoad, map[string]Document{file: {Content: content}}).Items(); len(got[0].Fixes) != 0 {
		t.Fatalf("non-load diagnostic received fixes: %#v", got)
	}
}

func TestSafeFixUtilitiesRejectStaleRangesAndDeduplicate(t *testing.T) {
	t.Parallel()
	location := diagnostic.SourceLocation("", "main.go", "main.go", 3, 1, 12)
	for _, test := range []struct {
		name string
		line int
		doc  Document
	}{
		{name: "zero line", line: 0, doc: Document{Content: []byte("@App\n")}},
		{name: "past eof", line: 3, doc: Document{Content: []byte("@App\n")}},
		{name: "ordinary source", line: 1, doc: Document{Content: []byte("func main() {}\n")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := location
			candidate.Range.Start.Line = test.line
			if _, found := annotationCommentPrefixEdit(candidate, test.doc); found {
				t.Fatal("annotationCommentPrefixEdit() unexpectedly succeeded")
			}
		})
	}

	version := 4
	fix := diagnostic.SuggestedFix{
		Title: "Convert annotation",
		Edits: []diagnostic.TextEdit{{
			Location: location, DocumentVersion: &version, NewText: "// ",
		}},
	}
	set := diagnostic.NewSet(
		diagnostic.New("spice.load.syntax", diagnostic.SeverityError, "one", location).WithFixes(fix),
		diagnostic.New("spice.load.type", diagnostic.SeverityError, "two", location).WithFixes(fix),
	)
	actions := actionsFromDiagnostics(set)
	if len(actions) != 1 || !equalSuggestedFix(actions[0], fix) {
		t.Fatalf("actionsFromDiagnostics() = %#v", actions)
	}
	changed := fix
	changed.Edits = append([]diagnostic.TextEdit(nil), fix.Edits...)
	changed.Edits[0].NewText = "different"
	if equalSuggestedFix(fix, changed) {
		t.Fatal("different edits were treated as equal")
	}
}

func TestVersionDiagnosticsAddsOnlyMissingOverlayVersions(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "main.go")
	location := diagnostic.SourceLocation("", file, file, 1, 1, 0)
	fix := diagnostic.SuggestedFix{Title: "Edit", Edits: []diagnostic.TextEdit{{Location: location, NewText: "// "}}}
	set := diagnostic.NewSet(diagnostic.New(
		"spice.load.syntax", diagnostic.SeverityError, "raw annotation", location,
	).WithFixes(fix))
	items := versionDiagnostics(set, map[string]Document{file: {Version: 9}}).Items()
	version := items[0].Fixes[0].Edits[0].DocumentVersion
	if version == nil || *version != 9 {
		t.Fatalf("versionDiagnostics() = %#v", items)
	}

	expired := catalogCacheEntry{expires: time.Now().Add(-time.Second)}
	if time.Now().Before(expired.expires) {
		t.Fatal("test cache entry unexpectedly current")
	}
}

func writeServiceCatalogFile(t *testing.T, root, relative, content string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
