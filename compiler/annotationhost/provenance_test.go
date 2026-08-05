package annotationhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/internal/identity"
)

func TestValidateDescriptorToolModule(t *testing.T) {
	replacement := filepath.Join(t.TempDir(), "plugin")
	descriptor := annotation.ModuleProvenance{
		Path:             "example.com/plugin",
		Version:          "v1.4.0",
		ReplacementPath:  replacement,
		ReplacementDir:   replacement,
		LocalReplacement: true,
	}
	tool := PackageIdentity{
		Path: "example.com/plugin/cmd/annotations",
		Module: ModuleIdentity{
			Path:    "example.com/plugin",
			Version: "v1.4.0",
			Replacement: &ModuleIdentity{
				Path:      replacement,
				Directory: replacement,
			},
		},
	}
	if err := ValidateDescriptorToolModule(descriptor, tool); err != nil {
		t.Fatalf("ValidateDescriptorToolModule() error = %v", err)
	}
	for _, change := range []func(*PackageIdentity){
		func(value *PackageIdentity) { value.Module.Version = "v1.5.0" },
		func(value *PackageIdentity) {
			value.Module.Replacement.Directory = filepath.Join(
				replacement,
				"other",
			)
		},
	} {
		changed := clonePackageIdentity(tool)
		change(&changed)
		err := ValidateDescriptorToolModule(descriptor, changed)
		if err == nil || !strings.Contains(err.Error(), "does not match") &&
			!strings.Contains(err.Error(), "different replacements") {
			t.Fatalf("ValidateDescriptorToolModule() error = %v", err)
		}
	}
}

func TestValidateDescriptorToolModuleAllowsOfficialExtractionBoundary(
	t *testing.T,
) {
	t.Parallel()
	descriptor := annotation.ModuleProvenance{
		Path:    identity.CoreModule,
		Version: identity.CoreVersion,
	}
	tool := PackageIdentity{
		Path: identity.AnnotationTool,
		Module: ModuleIdentity{
			Path:    identity.ToolchainModule,
			Version: "v0.1.0",
		},
	}
	if err := ValidateDescriptorToolModule(descriptor, tool); err != nil {
		t.Fatalf("ValidateDescriptorToolModule() error = %v", err)
	}
	tool.Path = identity.ToolchainModule + "/cmd/other"
	if err := ValidateDescriptorToolModule(descriptor, tool); err == nil {
		t.Fatal("non-official cross-module tool error = nil")
	}
}

func TestSourceSymbolPositionsFindsExactPackageLevelFunctions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "handler.go"),
		[]byte(`package handlers

type Handler struct{}

func (Handler) ApplicationHandler() {}

func ApplicationHandler() {}
`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	symbol := sdk.Symbol{
		Package: "example.com/plugin/internal/handlers",
		Name:    "ApplicationHandler",
	}
	positions, err := sourceSymbolPositions(goListPackage{
		ImportPath: symbol.Package,
		Dir:        root,
		GoFiles:    []string{"handler.go"},
	}, []sdk.Symbol{symbol})
	if err != nil {
		t.Fatalf("sourceSymbolPositions() error = %v", err)
	}
	position := positions[symbol]
	if !strings.HasSuffix(position.Filename, "handler.go") ||
		position.Line != 7 ||
		position.Column != 6 {
		t.Fatalf("sourceSymbolPositions() = %+v", position)
	}

	for name, listed := range map[string]goListPackage{
		"unsafe": {
			ImportPath: symbol.Package,
			Dir:        root,
			GoFiles:    []string{"../handler.go"},
		},
		"missing": {
			ImportPath: symbol.Package,
			Dir:        root,
			GoFiles:    []string{"handler.go"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requested := []sdk.Symbol{symbol}
			if name == "missing" {
				requested[0].Name = "MissingHandler"
			}
			if _, resolveErr := sourceSymbolPositions(
				listed,
				requested,
			); resolveErr == nil {
				t.Fatal("sourceSymbolPositions() error = nil")
			}
		})
	}
}

func TestResolveSourceSymbolsRejectsNilContext(t *testing.T) {
	t.Parallel()
	_, err := ResolveSourceSymbols(
		nil, //nolint:staticcheck // The API's fail-closed nil-context contract is under test.
		TargetModule{},
		[]sdk.Symbol{{Package: "example.com/plugin", Name: "Handler"}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("ResolveSourceSymbols() error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ResolveSourceSymbols(
		cancelled,
		TargetModule{Root: t.TempDir()},
		[]sdk.Symbol{{Package: "example.com/plugin", Name: "Handler"}},
		nil,
	)
	if err == nil {
		t.Fatal("ResolveSourceSymbols(cancelled) error = nil")
	}
}

func TestListPackageIgnoresModuleVendorInsideActiveWorkspace(t *testing.T) {
	t.Parallel()
	workspaceRoot := t.TempDir()
	applicationRoot := filepath.Join(workspaceRoot, "application")
	dependencyRoot := filepath.Join(workspaceRoot, "dependency")
	writeAnnotationHostFixture(
		t,
		filepath.Join(applicationRoot, "go.mod"),
		"module example.com/application\n\ngo 1.26.0\n\nrequire example.com/dependency v0.0.0\n",
	)
	writeAnnotationHostFixture(
		t,
		filepath.Join(applicationRoot, "handlers", "handler.go"),
		"package handlers\n\nfunc Handler() {}\n",
	)
	writeAnnotationHostFixture(
		t,
		filepath.Join(applicationRoot, "vendor", "modules.txt"),
		"# intentionally module-scoped\n",
	)
	writeAnnotationHostFixture(
		t,
		filepath.Join(dependencyRoot, "go.mod"),
		"module example.com/dependency\n\ngo 1.26.0\n",
	)
	workspace := filepath.Join(workspaceRoot, "go.work")
	writeAnnotationHostFixture(
		t,
		workspace,
		"go 1.26.0\n\nuse (\n\t./application\n\t./dependency\n)\n",
	)
	listed, err := listPackage(
		t.Context(),
		TargetModule{Root: applicationRoot},
		"example.com/application/handlers",
		replaceEnvironmentValue(os.Environ(), "GOWORK", workspace),
	)
	if err != nil {
		t.Fatalf("listPackage(workspace) error = %v", err)
	}
	if listed.ImportPath != "example.com/application/handlers" ||
		listed.Dir != filepath.Join(applicationRoot, "handlers") {
		t.Fatalf("listPackage(workspace) = %+v", listed)
	}
}

func writeAnnotationHostFixture(t *testing.T, name string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
