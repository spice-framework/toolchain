package load

import (
	"context"
	"encoding/json"
	"errors"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func FuzzGeneratedMainBridgeError(f *testing.F) {
	const position = "main.go:7:10"
	positions := map[string]struct{}{
		diagnosticPositionKey("main.go", 7, 10): {},
	}
	f.Add(int(packages.TypeError), position, "undefined: spiceMain")
	f.Add(
		int(packages.ListError),
		"",
		"# example.com/app\nmain.go:7:10: undefined: spiceMain",
	)
	f.Add(int(packages.ParseError), position, "undefined: spiceMain")

	f.Fuzz(func(t *testing.T, rawKind int, rawPosition, message string) {
		kind := packages.ErrorKind(rawKind)
		if kind < packages.UnknownError || kind > packages.TypeError {
			kind = packages.UnknownError
		}
		_ = generatedMainBridgeError(packages.Error{
			Pos:  rawPosition,
			Msg:  message,
			Kind: kind,
		}, positions)
	})
}

func TestLoadMultiPackageProgram(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.23.0\n",
		"contract/contract.go": `package contract

type Reader interface { Read([]byte) (int, error) }
`,
		"app/app.go": `package app

import "example.com/fixture/contract"

type Service struct{}
type Box[T any] struct{}

var Global int
const Answer = 42

func New(reader contract.Reader) *Service { return &Service{} }
func (Service) Value() {}
func (*Service) Pointer() {}
func (Box[T]) Generic() {}
`,
		"app/app_test.go": `package app

var TestOnly int
`,
		"app/external_test.go": `package app_test

var ExternalTestOnly int
`,
	})

	program, err := Load(context.Background(), Options{Dir: dir}, "./app", "./contract")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	packages := program.Packages()
	if got, want := packagePaths(packages), []string{"example.com/fixture/app", "example.com/fixture/contract"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("package paths = %v, want %v", got, want)
	}
	for _, pkg := range packages {
		if pkg.ModulePath != "example.com/fixture" {
			t.Errorf("package %s module path = %q", pkg.Path, pkg.ModulePath)
		}
		if pkg.Dir == "" || !filepath.IsAbs(pkg.Dir) {
			t.Errorf("package %s directory = %q, want absolute directory", pkg.Path, pkg.Dir)
		}
		if !sort.StringsAreSorted(pkg.CompiledGoFiles) {
			t.Errorf("package %s compiled files are not sorted: %v", pkg.Path, pkg.CompiledGoFiles)
		}
	}

	symbols := program.Symbols()
	assertUniqueSymbolIDs(t, symbols)
	for _, id := range []string{
		"example.com/fixture/app",
		"example.com/fixture/app.Answer",
		"example.com/fixture/app.Box",
		"example.com/fixture/app.Box.Generic",
		"example.com/fixture/app.Global",
		"example.com/fixture/app.New",
		"example.com/fixture/app.Service",
		"example.com/fixture/app.Service.Pointer",
		"example.com/fixture/app.Service.Value",
		"example.com/fixture/contract",
		"example.com/fixture/contract.Reader",
	} {
		if symbolByID(symbols, id) == nil {
			t.Errorf("missing symbol %q in %v", id, symbolIDs(symbols))
		}
	}
	for _, symbol := range symbols {
		if strings.Contains(symbol.ID, dir) {
			t.Fatalf("stable ID %q contains fixture directory %q", symbol.ID, dir)
		}
	}
	if symbolByID(symbols, "example.com/fixture/app.TestOnly") != nil || symbolByID(symbols, "example.com/fixture/app_test.ExternalTestOnly") != nil {
		t.Fatalf("default loading returned test declarations: %v", symbolIDs(symbols))
	}

	provider := symbolByID(symbols, "example.com/fixture/app.New")
	if provider == nil || provider.Signature == nil {
		t.Fatal("New provider has no resolved signature")
	}
	parameter := provider.Signature.Params().At(0).Type()
	named, ok := parameter.(*types.Named)
	if !ok {
		t.Fatalf("New parameter type = %T %v, want named imported interface", parameter, parameter)
	}
	if got, want := named.Obj().Pkg().Path(), "example.com/fixture/contract"; got != want {
		t.Fatalf("New parameter package = %q, want %q", got, want)
	}
}

func TestLoadRejectsTestVariantMode(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/testvariants\n\ngo 1.23.0\n",
		"app/app.go": `package app

var Production int
`,
		"app/app_test.go": `package app

var TestOnly int
`,
		"app/external_test.go": `package app_test

var ExternalTestOnly int
`,
	})

	var first []byte
	for iteration := range 3 {
		program, err := Load(context.Background(), Options{Dir: dir, Tests: true}, "./...")
		if err == nil {
			t.Fatalf("Load() iteration %d error = nil, want unsupported-mode error", iteration)
		}
		if got := program.Packages(); len(got) != 0 {
			t.Fatalf("Load() iteration %d packages = %#v, want none", iteration, got)
		}
		if got := program.Symbols(); len(got) != 0 {
			t.Fatalf("Load() iteration %d symbols = %#v, want none", iteration, got)
		}
		diagnostics := program.Diagnostics()
		if len(diagnostics) != 1 || diagnostics[0].Kind != "configuration" ||
			!strings.Contains(diagnostics[0].Message, "test-variant loading is unsupported") {
			t.Fatalf("Load() iteration %d diagnostics = %#v, want deterministic unsupported-mode diagnostic", iteration, diagnostics)
		}
		summary := deterministicSummary(program)
		if iteration == 0 {
			first = summary
			continue
		}
		if !reflect.DeepEqual(summary, first) {
			t.Fatalf("unsupported-mode summary changed between loads:\nfirst: %s\nnext:  %s", first, summary)
		}
	}
}

func TestLoadBuildConstraints(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/tags\n\ngo 1.23.0\n",
		"tagged/active.go": `//go:build !spice_excluded

package tagged

var Active int
`,
		"tagged/excluded.go": `//go:build spice_excluded

package tagged

var Tagged int
`,
	})

	program, err := Load(context.Background(), Options{Dir: dir}, "./...")
	if err != nil {
		t.Fatalf("Load(default) error = %v", err)
	}
	if symbolByID(program.Symbols(), "example.com/tags/tagged.Active") == nil {
		t.Fatal("default load omitted active declaration")
	}
	if symbolByID(program.Symbols(), "example.com/tags/tagged.Tagged") != nil {
		t.Fatal("default load included build-tag-excluded declaration")
	}
	packages := program.Packages()
	if len(packages) == 0 {
		t.Fatal("Packages() is empty")
	}
	for _, file := range packages[0].CompiledGoFiles {
		if strings.HasSuffix(file, "excluded.go") {
			t.Fatalf("default compiled files include excluded.go: %v", packages[0].CompiledGoFiles)
		}
	}

	tagged, err := Load(context.Background(), Options{Dir: dir, BuildFlags: []string{"-tags=spice_excluded"}}, "./...")
	if err != nil {
		t.Fatalf("Load(tagged) error = %v", err)
	}
	if symbolByID(tagged.Symbols(), "example.com/tags/tagged.Tagged") == nil {
		t.Fatal("tagged load omitted tagged declaration")
	}
	if symbolByID(tagged.Symbols(), "example.com/tags/tagged.Active") != nil {
		t.Fatal("tagged load included default-only declaration")
	}
}

func TestLoadTypeErrors(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/broken\n\ngo 1.23.0\n",
		"broken/broken.go": `package broken

var Value string = 1
`,
	})

	program, err := Load(context.Background(), Options{Dir: dir}, "./broken")
	if err == nil {
		t.Fatal("Load() error = nil, want type error")
	}
	loadError, ok := errors.AsType[*LoadError](err)
	if !ok {
		t.Fatalf("Load() error = %T, want *LoadError", err)
	}
	if len(loadError.Diagnostics) == 0 {
		t.Fatal("LoadError.Diagnostics is empty")
	}
	packages := program.Packages()
	if len(packages) != 1 || !packages[0].IllTyped {
		t.Fatalf("packages = %#v, want one ill-typed root", packages)
	}
	foundType := false
	for _, diagnostic := range program.Diagnostics() {
		foundType = foundType || diagnostic.Kind == "type"
	}
	if !foundType {
		t.Fatalf("diagnostics = %#v, want type diagnostic", program.Diagnostics())
	}
	if !strings.Contains(err.Error(), "cannot use") {
		t.Fatalf("error = %q, want actionable Go type error", err)
	}
}

func TestLoadSyntaxErrors(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/syntax\n\ngo 1.23.0\n",
		"broken/broken.go": "package broken\n\nfunc Broken(\n",
	})
	program, err := Load(context.Background(), Options{Dir: dir}, "./broken")
	if err == nil {
		t.Fatal("Load() error = nil, want syntax error")
	}
	if len(program.Diagnostics()) == 0 {
		t.Fatal("syntax error produced no diagnostics")
	}
	foundParse := false
	for _, diagnostic := range program.Diagnostics() {
		foundParse = foundParse || diagnostic.Kind == "parse"
	}
	if !foundParse {
		t.Fatalf("diagnostics = %#v, want parse diagnostic", program.Diagnostics())
	}
}

func TestLoadMissingPattern(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":             "module example.com/missing\n\ngo 1.23.0\n",
		"present/present.go": "package present\n",
	})
	program, err := Load(context.Background(), Options{Dir: dir}, "./does-not-exist")
	if err == nil {
		t.Fatal("Load() error = nil, want unmatched-pattern error")
	}
	if len(program.Diagnostics()) == 0 {
		t.Fatal("unmatched pattern produced no diagnostics")
	}
	if !strings.Contains(err.Error(), "does-not-exist") && !strings.Contains(err.Error(), "matched no packages") {
		t.Fatalf("error = %q, want actionable unmatched-pattern diagnostic", err)
	}
}

func TestLoadCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	program, err := Load(ctx, Options{}, ".")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
	if program != nil {
		t.Fatalf("Load() program = %#v, want nil", program)
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":     "module example.com/overlay\n\ngo 1.23.0\n",
		"app/app.go": "package app\n\nvar Original int\n",
	})
	path := filepath.Join(dir, "app", "app.go")
	program, err := Load(context.Background(), Options{
		Dir: dir,
		Overlay: map[string][]byte{
			path: []byte("package app\n\nvar Overlaid int\n"),
		},
	}, "./app")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if symbolByID(program.Symbols(), "example.com/overlay/app.Overlaid") == nil {
		t.Fatalf("overlay declaration missing: %v", symbolIDs(program.Symbols()))
	}
	if symbolByID(program.Symbols(), "example.com/overlay/app.Original") != nil {
		t.Fatalf("original declaration survived overlay: %v", symbolIDs(program.Symbols()))
	}
}

func TestLoadAllowsOnlyAnnotatedGeneratedMainBridge(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/bridge\n\ngo 1.26.0\n",
		"cmd/shop/main.go": `package main

import "os"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`,
		"feature/feature.go": "package feature\n\nvar Loaded = true\n",
	})
	program, err := Load(context.Background(), Options{
		Dir:                      dir,
		AllowGeneratedMainBridge: true,
	}, "./...")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if diagnostics := program.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %#v", diagnostics)
	}
	if symbolByID(program.Symbols(), "example.com/bridge/cmd/shop.main") == nil ||
		symbolByID(program.Symbols(), "example.com/bridge/feature.Loaded") == nil {
		t.Fatalf("Symbols() = %v", symbolIDs(program.Symbols()))
	}
	for _, pkg := range program.Packages() {
		if pkg.IllTyped {
			t.Fatalf("package %s remained ill typed", pkg.Path)
		}
	}
}

func TestLoadDoesNotBroadenGeneratedMainBridgeException(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "unannotated",
			source: `package main

func main() {
	_ = spiceMain(nil)
}
`,
		},
		{
			name: "different undefined identifier",
			source: `package main

// @Application
func main() {
	_ = spiceMain(nil)
	_ = missing
}
`,
		},
		{
			name: "outside marker body",
			source: `package main

var value = spiceMain(nil)

// @Application
func main() {}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := writeModule(t, map[string]string{
				"go.mod":  "module example.com/rejected\n\ngo 1.26.0\n",
				"main.go": test.source,
			})
			program, err := Load(context.Background(), Options{
				Dir:                      dir,
				AllowGeneratedMainBridge: true,
			}, ".")
			if err == nil {
				t.Fatal("Load() error = nil")
			}
			if len(program.Diagnostics()) == 0 {
				t.Fatal("Load() returned no diagnostics")
			}
		})
	}
}

func TestLoadDeterministic(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/deterministic\n\ngo 1.23.0\n",
		"z/z.go":           "package z\n\nvar Z int\n",
		"a/a.go":           "package a\n\nconst A = 1\n",
		"broken/broken.go": "package broken\n\nvar Value string = 1\n",
	})

	var first []byte
	for iteration := range 3 {
		program, err := Load(context.Background(), Options{Dir: dir}, "./z", "./a", "./broken")
		if err == nil {
			t.Fatalf("Load() iteration %d error = nil, want deterministic type error", iteration)
		}
		if len(program.Diagnostics()) == 0 {
			t.Fatalf("Load() iteration %d returned no diagnostics", iteration)
		}
		summary := deterministicSummary(program)
		if iteration == 0 {
			first = summary
			continue
		}
		if !reflect.DeepEqual(summary, first) {
			t.Fatalf("summary changed between loads:\nfirst: %s\nnext:  %s", first, summary)
		}
	}
}

func TestLoadIsQuiet(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/quiet\n\ngo 1.23.0\n",
		"broken/broken.go": "package broken\n\nvar Value string = 1\n",
	})
	stdout := filepath.Join(t.TempDir(), "stdout")
	stderr := filepath.Join(t.TempDir(), "stderr")
	stdoutFile, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	stderrFile, err := os.Create(stderr)
	if err != nil {
		t.Fatal(err)
	}
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutFile, stderrFile
	if _, loadErr := Load(context.Background(), Options{Dir: dir}, "./broken"); loadErr == nil {
		t.Fatal("Load() error = nil, want broken package error")
	}
	os.Stdout, os.Stderr = oldStdout, oldStderr
	if err := stdoutFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{stdout, stderr} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Fatalf("Load wrote to %s: %q", filepath.Base(path), content)
		}
	}
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		absolute := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", absolute, err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", absolute, err)
		}
	}
	return dir
}

func packagePaths(packages []Package) []string {
	paths := make([]string, len(packages))
	for i, pkg := range packages {
		paths[i] = pkg.Path
	}
	return paths
}

func symbolByID(symbols []Symbol, id string) *Symbol {
	for i := range symbols {
		if symbols[i].ID == id || symbols[i].DisplayLabel == id {
			return &symbols[i]
		}
	}
	return nil
}

func symbolIDs(symbols []Symbol) []string {
	ids := make([]string, len(symbols))
	for i, symbol := range symbols {
		ids[i] = symbol.ID
	}
	return ids
}

func assertUniqueSymbolIDs(t *testing.T, symbols []Symbol) {
	t.Helper()
	seen := make(map[string]Symbol, len(symbols))
	for _, symbol := range symbols {
		if previous, exists := seen[symbol.ID]; exists {
			t.Fatalf(
				"duplicate symbol ID %q for %s at %s and %s at %s",
				symbol.ID,
				previous.Kind,
				previous.Position,
				symbol.Kind,
				symbol.Position,
			)
		}
		seen[symbol.ID] = symbol
	}
}

func deterministicSummary(program *Program) []byte {
	type packageSummary struct {
		ID         string
		Path       string
		Name       string
		ModulePath string
		Files      []string
		IllTyped   bool
	}
	type summary struct {
		Packages    []packageSummary
		Symbols     []string
		Diagnostics []Diagnostic
	}
	value := summary{Symbols: symbolIDs(program.Symbols()), Diagnostics: program.Diagnostics()}
	for _, pkg := range program.Packages() {
		files := make([]string, len(pkg.CompiledGoFiles))
		for i, file := range pkg.CompiledGoFiles {
			files[i] = filepath.Base(file)
		}
		value.Packages = append(value.Packages, packageSummary{
			ID: pkg.ID, Path: pkg.Path, Name: pkg.Name, ModulePath: pkg.ModulePath, Files: files, IllTyped: pkg.IllTyped,
		})
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
