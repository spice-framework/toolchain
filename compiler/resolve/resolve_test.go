package resolve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
)

func TestAnnotationsResolveTypedDeclarationsAndBuildSelection(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/resolver\n\ngo 1.23.0\n",
		"app/a.go": `// @Application
package app

// @Configuration
type Config struct{}

// @Service
type Box[T any] struct{}

// @Feature
var Value int

// @Feature
const Answer = 42

// @Feature
func Build() {}

// @Feature
func (*Config) Pointer() {}

// @Feature
func (Box[T]) Generic() {}

var (
	// @Feature
	Grouped int
)

//line display/z.go:900
// @Feature
var Alpha int
`,
		"app/b.go": `package app

//line display/a.go:1
// @Feature
var Beta int
`,
		"app/excluded.go": `//go:build spice_never

package app

// @UnknownAnnotation
func Excluded() {}
`,
	})

	result := Annotations(mustLoad(t, dir, "./..."))
	if len(result.Diagnostics) != 0 || result.Files != 2 {
		t.Fatalf("files=%d diagnostics=%v", result.Files, diagnosticMessages(result.Diagnostics))
	}
	got := make([]string, len(result.Occurrences))
	ids := make(map[string]struct{}, len(result.Occurrences))
	for i, occurrence := range result.Occurrences {
		got[i] = string(occurrence.Target) + ":" + occurrence.Name + ":" + occurrence.Annotation.Name
		if occurrence.SymbolID == "" || occurrence.PackagePath != "example.com/resolver/app" {
			t.Fatalf("occurrence = %#v", occurrence)
		}
		if _, duplicate := ids[occurrence.SymbolID]; duplicate {
			t.Fatalf("duplicate symbol join %q", occurrence.SymbolID)
		}
		ids[occurrence.SymbolID] = struct{}{}
	}
	want := []string{
		"package:app:Application", "type:Config:Configuration", "type:Box:Service",
		"variable:Value:Feature", "constant:Answer:Feature", "function:Build:Feature",
		"method:Pointer:Feature", "method:Generic:Feature", "variable:Grouped:Feature",
		"variable:Alpha:Feature", "variable:Beta:Feature",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("occurrences = %#v, want %#v", got, want)
	}
	alpha, beta := occurrenceByName(result.Occurrences, "Alpha"), occurrenceByName(result.Occurrences, "Beta")
	if alpha == nil || beta == nil || alpha.DisplayPosition.Line != 900 || beta.DisplayPosition.Line != 1 {
		t.Fatalf("line-mapped occurrences: alpha=%#v beta=%#v", alpha, beta)
	}
	if !strings.HasSuffix(filepath.ToSlash(alpha.DisplayPosition.Filename), "app/display/z.go") ||
		!strings.HasSuffix(filepath.ToSlash(beta.DisplayPosition.Filename), "app/display/a.go") {
		t.Fatalf("display positions: alpha=%s beta=%s", alpha.DisplayPosition, beta.DisplayPosition)
	}
}

func TestAnnotationsRejectAmbiguousMalformedAndUnaddressableDeclarations(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/invalid\n\ngo 1.23.0\n",
		"app/app.go": `package app

// @Configuration
type ( A struct{}; B struct{} )

// @Service
var First, Second int

// @Service
var _ int

// @Feature
func init() {}

// @Feature(value=)
var Broken int
`,
	})
	result := Annotations(mustLoad(t, dir, "./app"))
	if len(result.Occurrences) != 0 {
		t.Fatalf("occurrences = %#v", result.Occurrences)
	}
	joined := strings.Join(diagnosticMessages(result.Diagnostics), "\n")
	for _, expected := range []string{
		"grouped declaration with 2 specs", "value declaration with 2 names",
		"cannot target blank identifier _", "has no stable Spice symbol", "unsupported argument value",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics = %q, missing %q", joined, expected)
		}
	}
}

func TestAnnotationsReportsMissingDefinitionDeterministically(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":     "module example.com/consistency\n\ngo 1.23.0\n",
		"app/app.go": "package app\n\n// @Feature\nvar Missing int\n",
	})
	program := mustLoad(t, dir, "./app")
	packages := program.Packages()
	if len(packages) == 0 {
		t.Fatal("Packages() is empty")
	}
	pkg := packages[0]
	for identifier, object := range pkg.TypesInfo.Defs {
		if object != nil && object.Name() == "Missing" {
			delete(pkg.TypesInfo.Defs, identifier)
		}
	}
	var first []string
	for run := range 10 {
		messages := diagnosticMessages(Annotations(program).Diagnostics)
		if run == 0 {
			first = messages
		} else if !reflect.DeepEqual(messages, first) {
			t.Fatalf("run %d diagnostics = %#v, want %#v", run, messages, first)
		}
	}
	if joined := strings.Join(first, "\n"); !strings.Contains(joined, "has no stable Spice symbol") {
		t.Fatalf("diagnostics = %q", joined)
	}
}

func TestAnnotationsResolveExplicitFileScopedImports(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/imports\n\ngo 1.26.0\n",
		"app/app.go": `package app

// @Application
func Main() {}

// @web.Controller
type Orders struct{}

// @GET("/")
func (Orders) List() {}

// @import { Application } from "example.com/annotations/core"
// @import { Get as GET } from "example.com/annotations/web"
// @import * as web from "example.com/annotations/web"
`,
	})
	definitions := DefinitionIndex{
		{
			Package: "example.com/annotations/core",
			Symbol:  "Application",
		}: "Application",
		{
			Package: "example.com/annotations/web",
			Symbol:  "Controller",
		}: "Controller",
		{
			Package: "example.com/annotations/web",
			Symbol:  "Get",
		}: "Get",
	}
	result := AnnotationsWithDefinitions(
		mustLoad(t, dir, "./app"),
		definitions,
	)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnosticMessages(result.Diagnostics))
	}
	got := make([]string, len(result.Occurrences))
	for index, occurrence := range result.Occurrences {
		got[index] = occurrence.Spelling + "=>" +
			occurrence.Annotation.Name + "@" +
			occurrence.Definition.Package + "." +
			occurrence.Definition.Symbol
	}
	want := []string{
		"Application=>Application@example.com/annotations/core.Application",
		"web.Controller=>Controller@example.com/annotations/web.Controller",
		"GET=>Get@example.com/annotations/web.Get",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("occurrences = %#v, want %#v", got, want)
	}
}

func TestAnnotationsFailClosedForExplicitImports(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/imports\n\ngo 1.26.0\n",
		"app/app.go": `package app

// @import { Application } from "example.com/annotations/core"
// @import * as web from "example.com/annotations/web"

// @Application
func Main() {}

// @Controller
type MissingImport struct{}

// @web.Missing
type MissingDescriptor struct{}
`,
		"app/legacy.go": `package app

// @Service
type LegacyCompatibility struct{}
`,
	})
	result := AnnotationsWithDefinitions(
		mustLoad(t, dir, "./app"),
		DefinitionIndex{{
			Package: "example.com/annotations/core",
			Symbol:  "Application",
		}: "Application"},
	)
	if len(result.Occurrences) != 2 {
		t.Fatalf("occurrences = %#v", result.Occurrences)
	}
	legacy := occurrenceByName(result.Occurrences, "LegacyCompatibility")
	if legacy == nil || legacy.Annotation.Name != "Service" ||
		legacy.Definition != (annotation.DefinitionReference{}) {
		t.Fatalf("legacy occurrence = %#v", legacy)
	}
	joined := strings.Join(diagnosticMessages(result.Diagnostics), "\n")
	for _, expected := range []string{
		"@Controller is not imported in this file",
		"web.Missing",
		"descriptor is unavailable",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics = %q, missing %q", joined, expected)
		}
	}
}

func TestAnnotationsRejectImportCollisionsAtDirective(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/imports\n\ngo 1.26.0\n",
		"app/app.go": `package app

// @import { Application as App } from "example.com/annotations/core"
// @import * as App from "example.com/annotations/web"

// @App
func Main() {}
`,
	})
	result := AnnotationsWithDefinitions(
		mustLoad(t, dir, "./app"),
		DefinitionIndex{{
			Package: "example.com/annotations/core",
			Symbol:  "Application",
		}: "Application"},
	)
	joined := strings.Join(diagnosticMessages(result.Diagnostics), "\n")
	if !strings.Contains(joined, `annotation import name "App" conflicts`) {
		t.Fatalf("diagnostics = %q", joined)
	}
}

func mustLoad(t *testing.T, dir string, patterns ...string) *load.Program {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: dir}, patterns...)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	return program
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func occurrenceByName(occurrences []Occurrence, name string) *Occurrence {
	for i := range occurrences {
		if occurrences[i].Name == name {
			return &occurrences[i]
		}
	}
	return nil
}

func diagnosticMessages(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for i, diagnostic := range diagnostics {
		result[i] = diagnostic.Error()
	}
	return result
}
