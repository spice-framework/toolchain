package moduleenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOfflineModeRespectsModuleAndWorkspaceVendorOwnership(t *testing.T) {
	t.Parallel()
	workspaceRoot := t.TempDir()
	root := filepath.Join(workspaceRoot, "application")
	writeFixture(t, filepath.Join(root, "vendor", "modules.txt"), "# module\n")

	if got := OfflineMode(root, []string{"GOWORK=off"}); got != "vendor" {
		t.Fatalf("OfflineMode(module vendor) = %q", got)
	}
	workspace := filepath.Join(workspaceRoot, "go.work")
	writeFixture(t, workspace, "go 1.26.0\n")
	if got := OfflineMode(root, []string{"PATH=test"}); got != "readonly" {
		t.Fatalf("OfflineMode(automatic workspace) = %q", got)
	}
	if got := OfflineMode(root, []string{"GOWORK=" + workspace}); got != "readonly" {
		t.Fatalf("OfflineMode(explicit workspace) = %q", got)
	}
	writeFixture(
		t,
		filepath.Join(workspaceRoot, "vendor", "modules.txt"),
		"# workspace\n",
	)
	if got := OfflineMode(root, []string{"GOWORK=" + workspace}); got != "vendor" {
		t.Fatalf("OfflineMode(workspace vendor) = %q", got)
	}
}

func TestOfflineModeRejectsDirectoriesNamedModulesFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(
		filepath.Join(root, "vendor", "modules.txt"),
		0o750,
	); err != nil {
		t.Fatal(err)
	}
	if got := OfflineMode(root, []string{"GOWORK=off"}); got != "readonly" {
		t.Fatalf("OfflineMode(directory) = %q", got)
	}
}

func writeFixture(t *testing.T, name string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
