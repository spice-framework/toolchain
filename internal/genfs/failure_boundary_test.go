package genfs

import (
	"os"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/generate"
)

func TestReplacePathRestoresDestinationWhenPromotionFails(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	const destination = "generated.go"
	const original = "// original remains owned\n"
	if err := os.WriteFile(directory+"/"+destination, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFailureBoundaryRoot(t, root)

	err = replacePath(root, "missing-temporary.go", destination)
	if err == nil {
		t.Fatal("replacePath(missing temporary) error = nil")
	}
	content, readErr := os.ReadFile(directory + "/" + destination)
	if readErr != nil || string(content) != original {
		t.Fatalf("restored destination = %q, %v", content, readErr)
	}
}

func TestGeneratedReplacementRejectsInvalidGoBeforeWriting(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFailureBoundaryRoot(t, root)

	err = replaceFile(
		root,
		"nested/generated.go",
		[]byte(generatedMarker+"\npackage broken\nfunc (\n"),
		0o644,
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "validate generated Go") {
		t.Fatalf("replaceFile(invalid Go) error = %v", err)
	}
	if _, statErr := os.Stat(directory + "/nested/generated.go"); !os.IsNotExist(statErr) {
		t.Fatalf("invalid generated file exists: %v", statErr)
	}
}

func TestOwnedRemovalFailsClosedBeforeDeleting(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFailureBoundaryRoot(t, root)
	content := []byte(generatedMarker + "\npackage generated\n")
	if err := os.WriteFile(directory+"/stale.go", content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeOwnedFile(root, "stale.go", hashContent([]byte("different"))); err == nil ||
		!strings.Contains(err.Error(), "refuse to remove modified") {
		t.Fatalf("removeOwnedFile(modified) error = %v", err)
	}
	if _, err := os.Stat(directory + "/stale.go"); err != nil {
		t.Fatalf("protected stale file was removed: %v", err)
	}
	if err := removeOwnedFile(root, "stale.go", hashContent(content)); err != nil {
		t.Fatalf("removeOwnedFile(owned) error = %v", err)
	}
	if err := removeOwnedFile(root, "stale.go", hashContent(content)); err != nil {
		t.Fatalf("removeOwnedFile(missing) error = %v", err)
	}
}

func TestSourceUnitOwnershipRequiresTraceableMappings(t *testing.T) {
	t.Parallel()
	primary := generate.SourceOrigin{Path: "orders/service.go"}
	valid := generate.SourceMapping{
		Kind:         "provider",
		Contribution: "orders.Service",
		Source:       primary,
		Generated: generate.GeneratedRange{
			StartLine: 2, StartColumn: 1, EndLine: 3, EndColumn: 1,
		},
	}
	tests := []struct {
		name string
		file generate.File
		want string
	}{
		{
			name: "missing primary",
			file: generate.File{Role: generate.FileRoleSourceUnit},
			want: "primary source",
		},
		{
			name: "missing mapping",
			file: generate.File{Role: generate.FileRoleSourceUnit, PrimarySource: &primary},
			want: "at least one source mapping",
		},
		{
			name: "different source",
			file: generate.File{
				Role: generate.FileRoleSourceUnit, PrimarySource: &primary,
				Mappings: []generate.SourceMapping{{
					Kind: "provider", Contribution: "orders.Service",
					Source: generate.SourceOrigin{Path: "other.go"},
				}},
			},
			want: "instead of primary source",
		},
		{
			name: "missing identity",
			file: generate.File{
				Role: generate.FileRoleSourceUnit, PrimarySource: &primary,
				Mappings: []generate.SourceMapping{{Source: primary}},
			},
			want: "kind and contribution",
		},
		{
			name: "invalid range",
			file: generate.File{
				Role: generate.FileRoleSourceUnit, PrimarySource: &primary,
				Mappings: []generate.SourceMapping{{
					Kind: "provider", Contribution: "orders.Service", Source: primary,
				}},
			},
			want: "invalid generated range",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateFileOwnership(test.file)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFileOwnership() error = %v", err)
			}
		})
	}
	validFile := generate.File{
		Role: generate.FileRoleSourceUnit, PrimarySource: &primary,
		Mappings: []generate.SourceMapping{valid},
	}
	if err := validateFileOwnership(validFile); err != nil {
		t.Fatalf("validateFileOwnership(valid) error = %v", err)
	}
}

func TestSchemaTwoOwnershipMatchesOnlyTheSelectedLayout(t *testing.T) {
	t.Parallel()
	selected := generate.Target{
		ID: "app", Layout: generate.LayoutApplicationPackage,
		ModulePath: "example.com/app", BridgeDir: "cmd/server",
		ManifestPath: ".spice/app.manifest.json",
	}
	legacy := generate.TargetSummary{
		ID: "app", Layout: generate.LayoutApplicationPackage,
		ModulePath: "example.com/app", PackagePath: "example.com/app/cmd/server",
		OutputDir: "cmd/server", ManifestPath: ".spice/app.manifest.json",
	}
	if !compatibleSchema2Target(legacy, selected) {
		t.Fatal("compatible schema-two application target was rejected")
	}
	legacy.PackagePath = "example.com/app/other"
	if compatibleSchema2Target(legacy, selected) {
		t.Fatal("schema-two target accepted a different package")
	}
	selected.Layout = generate.Layout("unsupported")
	if compatibleSchema2Target(legacy, selected) {
		t.Fatal("schema-two target accepted an unsupported layout")
	}
}

func TestDirectoryCreationRejectsFileComponents(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.WriteFile(directory+"/occupied", []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFailureBoundaryRoot(t, root)
	if err := ensureDirectories(root, "occupied/child"); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("ensureDirectories(file component) error = %v", err)
	}
}

func closeFailureBoundaryRoot(t *testing.T, root *os.Root) {
	t.Helper()
	if closeErr := root.Close(); closeErr != nil {
		t.Errorf("Close() error = %v", closeErr)
	}
}
