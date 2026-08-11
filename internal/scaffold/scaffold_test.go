package scaffold

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	compilerstyle "github.com/spice-framework/toolchain/compiler/style"
	structuralstyle "github.com/spice-framework/toolchain/internal/style"
)

func TestCreateWritesDeterministicValidGoApplication(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "orders")
	result, err := Create(t.Context(), Config{
		Directory:        destination,
		Module:           "example.com/acme/orders",
		SpiceVersion:     "v0.2.0",
		ToolchainVersion: "v0.3.0",
		ToolchainReplace: repositoryRoot(t),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantFiles := []string{".gitignore", "README.md", "go.mod", "main.go"}
	if result.Directory != filepath.Clean(destination) ||
		!slices.Equal(result.Files, wantFiles) {
		t.Fatalf("Create() = %#v", result)
	}
	goMod := readScaffoldFile(t, destination, "go.mod")
	for _, expected := range []string{
		"module example.com/acme/orders",
		"go 1.26.0",
		"toolchain go1.26.5",
		"tool (",
		CLITool,
		AnnotationTool,
		FrameworkModule + " v0.2.0",
		ToolchainModule + " v0.3.0",
		"replace " + ToolchainModule + " => " + filepath.ToSlash(repositoryRoot(t)),
	} {
		if !strings.Contains(goMod, expected) {
			t.Fatalf("go.mod missing %q:\n%s", expected, goMod)
		}
	}
	mainSource := readScaffoldFile(t, destination, "main.go")
	for _, expected := range []string{
		"// @import { Application }",
		"// @Application",
		`spiceapp "example.com/acme/orders/internal/spicegen/orders"`,
		"os.Exit(spiceapp.Main(os.Args[1:]))",
	} {
		if !strings.Contains(mainSource, expected) {
			t.Fatalf("main.go missing %q:\n%s", expected, mainSource)
		}
	}
	readme := readScaffoldFile(t, destination, "README.md")
	for _, command := range []string{
		"go tool " + CLITool + " generate",
		"go tool " + CLITool + " verify",
		"go tool " + CLITool + " run",
	} {
		if !strings.Contains(readme, command) {
			t.Fatalf("README.md missing %q:\n%s", command, readme)
		}
	}
}

func TestCreateWritesJavaStructuredApplicationLayout(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "catalog")
	result, err := Create(t.Context(), Config{
		Directory:        destination,
		Module:           "example.com/acme/catalog",
		SpiceVersion:     "v0.2.0",
		ToolchainVersion: "v0.3.0",
		Profile:          compilerstyle.ProfileJavaStructured,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantFiles := []string{
		".gitignore",
		".spice/style.json",
		"README.md",
		"cmd/catalog/doc.go",
		"cmd/catalog/main.go",
		"go.mod",
		"internal/catalog/doc.go",
	}
	if !slices.Equal(result.Files, wantFiles) {
		t.Fatalf("Create() files = %v", result.Files)
	}
	mainSource := readScaffoldFile(t, destination, "cmd/catalog/main.go")
	if !strings.Contains(mainSource, "// @Application") ||
		!strings.Contains(mainSource, `_ "example.com/acme/catalog/internal/catalog"`) {
		t.Fatalf("main.go = %s", mainSource)
	}
	packageSource := readScaffoldFile(t, destination, "internal/catalog/doc.go")
	for _, expected := range []string{"// @Module", "package catalog"} {
		if !strings.Contains(packageSource, expected) {
			t.Fatalf("doc.go missing %q:\n%s", expected, packageSource)
		}
	}
	commandPackage := readScaffoldFile(t, destination, "cmd/catalog/doc.go")
	if !strings.Contains(commandPackage, `@Module(allowedDependencies=["example.com/acme/catalog/internal/catalog"])`) {
		t.Fatalf("cmd/catalog/doc.go = %s", commandPackage)
	}
	goMod := readScaffoldFile(t, destination, "go.mod")
	if !strings.Contains(goMod, StyleTool) {
		t.Fatalf("go.mod missing style tool: %s", goMod)
	}
	if _, err := structuralstyle.LoadConfiguration(filepath.Join(destination, ".spice", "style.json")); err != nil {
		t.Fatalf("generated style configuration: %v", err)
	}
	readme := readScaffoldFile(t, destination, "README.md")
	if !strings.Contains(readme, "verify --style=.spice/style.json ./...") ||
		!strings.Contains(readme, StyleTool+" --config=.spice/style.json ./...") {
		t.Fatalf("README.md = %s", readme)
	}
}

func TestCreateJavaStructuredFallsBackToValidPackageName(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "my-app")
	result, err := Create(t.Context(), Config{
		Directory: destination, Module: "example.com/acme/my-app",
		SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		Profile: compilerstyle.ProfileJavaStructured,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(result.Files, "internal/application/doc.go") {
		t.Fatalf("Create() files = %v", result.Files)
	}
}

func TestCreateDeclarationWritesTypedDeterministicScaffolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind     DeclarationKind
		name     string
		file     string
		contains []string
	}{
		{DeclarationModule, "orders", "doc.go", []string{"// @Module", "package orders"}},
		{DeclarationService, "OrderService", "order_service.go", []string{"// @Service(constructor=NewOrderService)", "// @Singleton", "type OrderService struct", "func NewOrderService() *OrderService"}},
		{DeclarationRepository, "OrderRepository", "order_repository.go", []string{"// @Repository(constructor=NewOrderRepository)", "// @Singleton", "func NewOrderRepository() *OrderRepository"}},
		{DeclarationController, "OrderController", "order_controller.go", []string{"// @Controller(constructor=NewOrderController)", "// @Singleton", "// @Get(\"/\")", "net/http"}},
		{DeclarationComponent, "PasswordHasher", "password_hasher.go", []string{"// @Component(constructor=NewPasswordHasher)", "// @Singleton", "func NewPasswordHasher() *PasswordHasher"}},
		{DeclarationEnum, "OrderStatus", "order_status.go", []string{"// @Enum", "OrderStatusUnknown OrderStatus = \"unknown\""}},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			t.Parallel()
			directory := filepath.Join(t.TempDir(), string(test.kind))
			result, err := CreateDeclaration(t.Context(), DeclarationConfig{
				Directory: directory,
				Package:   "orders",
				Kind:      test.kind,
				Name:      test.name,
			})
			if err != nil {
				t.Fatalf("CreateDeclaration() error = %v", err)
			}
			if !slices.Equal(result.Files, []string{test.file}) {
				t.Fatalf("CreateDeclaration() files = %v", result.Files)
			}
			content := readScaffoldFile(t, directory, test.file)
			for _, expected := range test.contains {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s missing %q:\n%s", test.file, expected, content)
				}
			}
		})
	}
}

func TestCreateDeclarationPreservesExistingPackageFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	owned := filepath.Join(directory, "owned.go")
	if err := os.WriteFile(owned, []byte("package orders\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := DeclarationConfig{
		Directory: directory,
		Package:   "orders",
		Kind:      DeclarationService,
		Name:      "OrderService",
	}
	if _, err := CreateDeclaration(t.Context(), config); err != nil {
		t.Fatalf("CreateDeclaration() error = %v", err)
	}
	if _, err := CreateDeclaration(t.Context(), config); err == nil {
		t.Fatal("CreateDeclaration() overwrite error = nil")
	}
	if content, err := os.ReadFile(owned); err != nil || string(content) != "package orders\n" {
		t.Fatalf("owned.go = %q, %v", content, err)
	}
}

func TestCreateDeclarationRejectsInvalidAndCanceledRequests(t *testing.T) {
	t.Parallel()
	for name, config := range map[string]DeclarationConfig{
		"directory":  {Package: "orders", Kind: DeclarationService, Name: "Thing"},
		"kind":       {Directory: t.TempDir(), Package: "orders", Kind: "utility", Name: "Thing"},
		"package":    {Directory: t.TempDir(), Package: "bad-name", Kind: DeclarationService, Name: "Thing"},
		"unexported": {Directory: t.TempDir(), Package: "orders", Kind: DeclarationService, Name: "thing"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := CreateDeclaration(t.Context(), config); err == nil {
				t.Fatal("CreateDeclaration() error = nil")
			}
		})
	}
	if _, err := CreateDeclaration(nil, DeclarationConfig{}); err == nil { //nolint:staticcheck // Nil context is an intentional fail-closed boundary case.
		t.Fatal("CreateDeclaration(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateDeclaration(canceled, DeclarationConfig{
		Directory: filepath.Join(t.TempDir(), "orders"),
		Package:   "orders", Kind: DeclarationService, Name: "OrderService",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateDeclaration(canceled) error = %v", err)
	}
}

func TestWriteDeclarationFileRejectsInvalidCanceledAndOwnedTargets(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := writeDeclarationFile(t.Context(), root, plannedFile{
		name: "nested/value.go", content: []byte("package nested\n"), mode: 0o600,
	}); err == nil {
		t.Fatal("writeDeclarationFile(nested) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeDeclarationFile(canceled, root, plannedFile{
		name: "value.go", content: []byte("package value\n"), mode: 0o600,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("writeDeclarationFile(canceled) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "value.go"), []byte("package value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDeclarationFile(t.Context(), root, plannedFile{
		name: "value.go", content: []byte("replacement\n"), mode: 0o600,
	}); err == nil {
		t.Fatal("writeDeclarationFile(owned) error = nil")
	}
}

func TestCreateRejectsInvalidOrOwnedDestinations(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	nonempty := filepath.Join(parent, "nonempty")
	if err := os.Mkdir(nonempty, 0o750); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(nonempty, "owned.txt")
	if err := os.WriteFile(owned, []byte("developer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]Config{
		"missing directory": {
			Module: "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		},
		"invalid module": {
			Directory: filepath.Join(parent, "module"), Module: "../app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		},
		"invalid version": {
			Directory: filepath.Join(parent, "version"), Module: "example.com/app", SpiceVersion: "latest", ToolchainVersion: "v0.3.0",
		},
		"invalid toolchain version": {
			Directory: filepath.Join(parent, "toolchain-version"), Module: "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "latest",
		},
		"nonempty": {
			Directory: nonempty, Module: "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		},
		"missing parent": {
			Directory: filepath.Join(parent, "missing", "app"), Module: "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		},
		"unknown profile": {
			Directory: filepath.Join(parent, "profile"), Module: "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0", Profile: "java",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Create(t.Context(), test)
			if err == nil {
				t.Fatal("Create() error = nil")
			}
		})
	}
	content, err := os.ReadFile(owned)
	if err != nil || string(content) != "developer\n" {
		t.Fatalf("owned file = %q, %v", content, err)
	}
}

func TestCreateRejectsNilAndCanceledContexts(t *testing.T) {
	t.Parallel()
	config := Config{
		Directory: filepath.Join(t.TempDir(), "app"),
		Module:    "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
	}
	if _, err := Create(nil, config); err == nil { //nolint:staticcheck // Nil context is an intentional fail-closed public boundary case.
		t.Fatal("Create(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Create(canceled, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create(canceled) error = %v", err)
	}
}

func TestCreateAcceptsExistingEmptyDirectory(t *testing.T) {
	t.Parallel()
	destination := t.TempDir()
	result, err := Create(t.Context(), Config{
		Directory:        destination,
		Module:           "example.com/app",
		SpiceVersion:     "v0.2.0",
		ToolchainVersion: "v0.3.0",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(result.Files) != 4 {
		t.Fatalf("Create() files = %v", result.Files)
	}
}

func TestValidateConfigRejectsUnsafeLocalReplacements(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, []byte("not a module\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(parent, "empty")
	if err := os.Mkdir(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, replacement := range map[string]string{
		"missing":   filepath.Join(parent, "missing"),
		"file":      file,
		"no go.mod": empty,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := validateConfig(Config{
				Directory: filepath.Join(parent, name),
				Module:    "example.com/app", SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
				Replace: replacement,
			})
			if err == nil {
				t.Fatal("validateConfig() error = nil")
			}
		})
	}
}

func TestCreateAcceptsExplicitCoreReplacement(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "application")
	if _, err := Create(t.Context(), Config{
		Directory: destination, Module: "example.com/application",
		SpiceVersion: "v0.2.0", ToolchainVersion: "v0.3.0",
		Replace: repositoryRoot(t),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if content := readScaffoldFile(t, destination, "go.mod"); !strings.Contains(
		content,
		"replace "+FrameworkModule,
	) {
		t.Fatalf("go.mod = %s", content)
	}
}

func TestApplyRollsBackOnlyOwnedFiles(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "application")
	_, err := apply(t.Context(), destination, []plannedFile{
		{name: "created.txt", content: []byte("created\n"), mode: 0o600},
		{name: "../invalid.txt", content: []byte("invalid\n"), mode: 0o600},
	})
	if err == nil {
		t.Fatal("apply() error = nil")
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rolled-back destination stat error = %v", statErr)
	}
}

func TestApplyRollsBackOwnedNestedDirectories(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "application")
	_, err := apply(t.Context(), destination, []plannedFile{
		{name: "nested/deeper/created.txt", content: []byte("created\n"), mode: 0o600},
		{name: "../invalid.txt", content: []byte("invalid\n"), mode: 0o600},
	})
	if err == nil {
		t.Fatal("apply() error = nil")
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rolled-back destination stat error = %v", statErr)
	}
}

func TestDirectoryPreparationRejectsNonDirectories(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "owned")
	if err := os.WriteFile(file, []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDirectory(file); err == nil {
		t.Fatal("prepareDirectory(file) error = nil")
	}
	if _, err := prepareDeclarationDirectory(file); err == nil {
		t.Fatal("prepareDeclarationDirectory(file) error = nil")
	}
	if err := cleanupDirectories([]string{filepath.Join(t.TempDir(), "missing")}); err != nil {
		t.Fatalf("cleanupDirectories(missing) error = %v", err)
	}
	if _, err := scaffoldEntries(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("scaffoldEntries(missing) error = nil")
	}
}

func TestWritePlannedFilesRejectsDestinationMutation(t *testing.T) {
	t.Parallel()
	destination := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(destination, "developer.txt"),
		[]byte("owned\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := writePlannedFiles(t.Context(), root, []plannedFile{
		{name: "spice.txt", content: []byte("spice\n"), mode: 0o600},
	})
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if writeErr == nil || !strings.Contains(writeErr.Error(), "destination changed") {
		t.Fatalf("writePlannedFiles() error = %v", writeErr)
	}
	if content := readScaffoldFile(t, destination, "developer.txt"); content != "owned\n" {
		t.Fatalf("developer file = %q", content)
	}
}

func TestWritePlannedFilesHonorsCancellation(t *testing.T) {
	t.Parallel()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	created, writeErr := writePlannedFiles(canceled, root, []plannedFile{
		{name: "never.txt", content: []byte("never\n"), mode: 0o600},
	})
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(writeErr, context.Canceled) || len(created) != 0 {
		t.Fatalf("writePlannedFiles() = %v, %v", created, writeErr)
	}
}

func TestApplicationTargetNameIsDeterministic(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"":        "application",
		".":       "application",
		"catalog": "Catalog",
		"9lives":  "9lives",
	} {
		if got := applicationTargetName(input); got != want {
			t.Errorf("applicationTargetName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWriteAllRejectsShortWriter(t *testing.T) {
	t.Parallel()
	if err := writeAll(zeroScaffoldWriter{}, []byte("content")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeAll() error = %v", err)
	}
	if err := writeAll(errorScaffoldWriter{}, []byte("content")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writeAll(error) = %v", err)
	}
}

type zeroScaffoldWriter struct{}

func (zeroScaffoldWriter) Write([]byte) (int, error) { return 0, nil }

type errorScaffoldWriter struct{}

func (errorScaffoldWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readScaffoldFile(t *testing.T, root, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
