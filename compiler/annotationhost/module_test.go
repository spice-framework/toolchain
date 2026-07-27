package annotationhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTargetModuleAndAuthorizeExactTools(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, `module example.com/application

go 1.26.0

tool (
	example.com/acme/cmd/spice-annotations
	example.com/core/cmd/spice-annotations
)
`)
	module, err := ReadTargetModule(root)
	if err != nil {
		t.Fatalf("ReadTargetModule() error = %v", err)
	}
	if module.Path != "example.com/application" ||
		module.GoVersion != "1.26.0" ||
		len(module.Tools) != 2 {
		t.Fatalf("module = %#v", module)
	}
	if authorizeErr := module.AuthorizeTool(
		"example.com/acme/cmd/spice-annotations",
	); authorizeErr != nil {
		t.Fatalf("AuthorizeTool(allowed) error = %v", authorizeErr)
	}
	err = module.AuthorizeTool("example.com/other/cmd/spice-annotations")
	if err == nil || !strings.Contains(err.Error(), "go get -tool") {
		t.Fatalf("AuthorizeTool(missing) error = %v", err)
	}
}

func TestReadTargetModuleDoesNotUseParentOrGoWorkAuthorization(t *testing.T) {
	parent := t.TempDir()
	writeModuleFile(t, parent, `module example.com/parent
go 1.26.0
tool example.com/parent/cmd/annotations
`)
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o750); err != nil {
		t.Fatalf("MkdirAll(child) error = %v", err)
	}
	writeModuleFile(t, child, `module example.com/child
go 1.26.0
`)
	module, err := ReadTargetModule(child)
	if err != nil {
		t.Fatalf("ReadTargetModule() error = %v", err)
	}
	if err := module.AuthorizeTool(
		"example.com/parent/cmd/annotations",
	); err == nil {
		t.Fatal("parent tool authorization was accepted")
	}
}

func TestReadTargetModuleRejectsMissingOrMalformedModule(t *testing.T) {
	for _, content := range []string{"go 1.26.0\n", "not go mod\n"} {
		root := t.TempDir()
		writeModuleFile(t, root, content)
		if _, err := ReadTargetModule(root); err == nil {
			t.Fatalf("ReadTargetModule(%q) error = nil", content)
		}
	}
}

func writeModuleFile(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
}
