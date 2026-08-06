package moduleenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOfflineModeMatchesModuleAndWorkspaceVendorSelection(t *testing.T) {
	t.Parallel()
	workspaceRoot := t.TempDir()
	application := filepath.Join(workspaceRoot, "application")
	writeModuleEnvironmentContent(t, application, "go.mod", "module example.com/application\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, application, "vendor/modules.txt", "")
	if got := OfflineMode(application, []string{"GOWORK=off"}); got != "vendor" {
		t.Fatalf("OfflineMode(module vendor) = %q", got)
	}
	canonicalApplication, err := canonicalDirectory(application)
	if err != nil {
		t.Fatal(err)
	}
	if got, found := VendorRoot(application, []string{"GOWORK=off"}); !found || got != canonicalApplication {
		t.Fatalf("VendorRoot(module vendor) = %q, %t", got, found)
	}

	workspace := filepath.Join(workspaceRoot, "go.work")
	writeModuleEnvironmentContent(t, workspaceRoot, "go.work", "go 1.26.0\n\nuse ./application\n")
	if got := OfflineMode(application, []string{"GOWORK=" + workspace}); got != "readonly" {
		t.Fatalf("OfflineMode(workspace without vendor) = %q", got)
	}
	writeModuleEnvironmentContent(t, workspaceRoot, "vendor/modules.txt", "# stale module vendor\n")
	if got := OfflineMode(application, []string{"GOWORK=" + workspace}); got != "readonly" {
		t.Fatalf("OfflineMode(workspace vendor without marker) = %q", got)
	}
	writeModuleEnvironmentContent(t, workspaceRoot, "vendor/modules.txt", "## workspace\n")
	if got := OfflineMode(application, []string{"GOWORK=auto"}); got != "vendor" {
		t.Fatalf("OfflineMode(auto workspace vendor) = %q", got)
	}
	canonicalWorkspaceRoot, err := canonicalDirectory(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got, found := VendorRoot(application, []string{"GOWORK=" + workspace}); !found || got != canonicalWorkspaceRoot {
		t.Fatalf("VendorRoot(workspace vendor) = %q, %t", got, found)
	}

	unlisted := filepath.Join(workspaceRoot, "unlisted")
	writeModuleEnvironmentContent(t, unlisted, "go.mod", "module example.com/unlisted\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, unlisted, "vendor/modules.txt", "")
	if got := OfflineMode(unlisted, []string{"GOWORK=" + workspace}); got != "readonly" {
		t.Fatalf("OfflineMode(unlisted workspace module) = %q", got)
	}
	if got := OfflineMode(application, []string{"GOWORK=relative/go.work"}); got != "readonly" {
		t.Fatalf("OfflineMode(relative explicit workspace) = %q", got)
	}
}

func TestWorkspaceSelectionUsesAmbientAndLastDuplicate(t *testing.T) {
	root := t.TempDir()
	application := filepath.Join(root, "application")
	workspace := filepath.Join(root, "go.work")
	writeModuleEnvironmentContent(t, application, "go.mod", "module example.com/application\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, application, "vendor/modules.txt", "")
	writeModuleEnvironmentContent(t, root, "go.work", "go 1.26.0\n\nuse ./application\n")
	writeModuleEnvironmentContent(t, root, "vendor/modules.txt", "## workspace\n")

	if got := WorkspaceFile(application, []string{"GOWORK=" + workspace, "gowork=off"}); got != "" {
		t.Fatalf("WorkspaceFile(last off) = %q", got)
	}
	if got := WorkspaceFile(application, []string{"GOWORK=off", "gowork=" + workspace}); got != workspace {
		t.Fatalf("WorkspaceFile(last workspace) = %q", got)
	}
	t.Setenv("GOWORK", workspace)
	if got := WorkspaceFile(application, nil); got != workspace {
		t.Fatalf("WorkspaceFile(ambient) = %q", got)
	}
}

func TestWorkspaceModulesRequiresMembershipAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	application := filepath.Join(root, "application")
	plugin := filepath.Join(root, "plugin")
	writeModuleEnvironmentContent(t, application, "go.mod", "module example.com/application\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, plugin, "go.mod", "module example.com/plugin\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, root, "go.work", "go 1.26.0\n\nuse (\n\t./application\n\t./plugin\n)\n")
	modules, err := WorkspaceModules(t.Context(), application, []string{"GOWORK=auto"})
	if err != nil {
		t.Fatal(err)
	}
	canonicalApplication, err := canonicalDirectory(application)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPlugin, err := canonicalDirectory(plugin)
	if err != nil {
		t.Fatal(err)
	}
	want := []WorkspaceModule{
		{Path: "example.com/application", Root: canonicalApplication},
		{Path: "example.com/plugin", Root: canonicalPlugin},
	}
	if !slices.Equal(modules, want) {
		t.Fatalf("WorkspaceModules() = %#v, want %#v", modules, want)
	}

	unlisted := filepath.Join(root, "unlisted")
	writeModuleEnvironmentContent(t, unlisted, "go.mod", "module example.com/unlisted\n\ngo 1.26.0\n")
	if _, err := WorkspaceModules(t.Context(), unlisted, []string{"GOWORK=" + filepath.Join(root, "go.work")}); err == nil ||
		!strings.Contains(err.Error(), "not listed") {
		t.Fatalf("WorkspaceModules(unlisted) error = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := WorkspaceModules(canceled, application, []string{"GOWORK=auto"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("WorkspaceModules(canceled) error = %v", err)
	}
}

func TestParseVendoredModulesPreservesReplacementsAndSkipsUnused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	vendor := filepath.Join(root, "vendor")
	for _, modulePath := range []string{"example.com/local", "example.com/remote"} {
		writeModuleEnvironmentContent(t, vendor, modulePath+"/descriptor.go", "package descriptor\n")
	}
	content := []byte(`# example.com/local v1.2.3 => ../local
## explicit; go 1.26.0
example.com/local/annotation
# example.com/remote v1.0.0 => example.com/fork v1.1.0
## explicit; go 1.26.0
example.com/remote/annotation
# example.com/unused => ../unused
`)
	modules, err := parseVendoredModules(t.Context(), root, vendor, content)
	if err != nil {
		t.Fatal(err)
	}
	canonicalVendor, err := canonicalDirectory(vendor)
	if err != nil {
		t.Fatal(err)
	}
	want := []VendoredModule{
		{
			Path:                 "example.com/local",
			Version:              "v1.2.3",
			Directory:            filepath.Join(canonicalVendor, "example.com", "local"),
			ReplacementPath:      "../local",
			ReplacementDirectory: filepath.Clean(filepath.Join(root, "../local")),
			LocalReplacement:     true,
		},
		{
			Path:               "example.com/remote",
			Version:            "v1.0.0",
			Directory:          filepath.Join(canonicalVendor, "example.com", "remote"),
			ReplacementPath:    "example.com/fork",
			ReplacementVersion: "v1.1.0",
		},
	}
	if !slices.Equal(modules, want) {
		t.Fatalf("parseVendoredModules() = %#v, want %#v", modules, want)
	}
}

func TestParseVendoredModulesRejectsEscapeAndCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := parseVendoredModules(
		t.Context(),
		root,
		filepath.Join(root, "vendor"),
		[]byte("# .. v1.2.3\n../package\n"),
	); err == nil || !strings.Contains(err.Error(), "invalid vendored module path") {
		t.Fatalf("parseVendoredModules(escape) error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := parseVendoredModules(ctx, root, filepath.Join(root, "vendor"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("parseVendoredModules(canceled) error = %v", err)
	}
}

func TestVendoredModulesRejectsInconsistentGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeModuleEnvironmentContent(t, root, "go.mod", `module example.com/application

go 1.26.0

require example.com/dependency v1.2.3
`)
	writeModuleEnvironmentContent(t, root, "vendor/modules.txt", `# example.com/dependency v1.2.4
## explicit; go 1.26.0
example.com/dependency/package
`)
	writeModuleEnvironmentContent(t, root, "vendor/example.com/dependency/package/file.go", "package dependency\n")
	if _, err := VendoredModules(t.Context(), root, []string{"GOWORK=off"}); err == nil ||
		!strings.Contains(err.Error(), "inconsistent vendoring") {
		t.Fatalf("VendoredModules(inconsistent) error = %v", err)
	}
}

func TestVendorManifestReadIsBounded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeModuleEnvironmentContent(t, root, "go.mod", "module example.com/application\n\ngo 1.26.0\n")
	writeModuleEnvironmentContent(t, root, "vendor/modules.txt", strings.Repeat("x", maximumVendorBytes+1))
	if _, found := VendorRoot(root, []string{"GOWORK=off"}); found {
		t.Fatal("VendorRoot() accepted oversized manifest")
	}
}

func writeModuleEnvironmentContent(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
