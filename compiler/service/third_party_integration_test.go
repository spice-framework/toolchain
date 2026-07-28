package service

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/compiler/generate"
)

func TestThirdPartyAnnotationModuleCompletesCompilerWorkflow(t *testing.T) {
	root, err := filepath.Abs(filepath.Join(
		"..",
		"..",
		"testdata",
		"annotationapp",
	))
	if err != nil {
		t.Fatalf("resolve third-party fixture: %v", err)
	}
	assertFixtureUsesPublicSDK(t, filepath.Join(root, "..", "annotationfixture"))
	service, err := New(Config{SpiceVersion: "integration-test"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if closeErr := service.Close(closeCtx); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./..."},
		Mode:          AnalysisGenerate,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() || !result.GenerationReady() {
		t.Fatalf(
			"Analyze() diagnostics = %+v, generation ready = %t",
			result.Diagnostics().Items(),
			result.GenerationReady(),
		)
	}
	assertThirdPartyDefinitions(t, result.AnnotationDefinitions())
	assertThirdPartyOccurrences(t, result.Annotations())
	assertThirdPartyGeneration(t, root, result)

	mainPath := filepath.Join(root, "main.go")
	source, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	denied := bytes.Replace(
		source,
		[]byte(`mode="strict"`),
		[]byte(`mode="deny"`),
		1,
	)
	failed, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./..."},
		Mode:          AnalysisValidate,
		Overlay: map[string]Document{
			mainPath: {Version: 2, Content: denied},
		},
	})
	if err != nil {
		t.Fatalf("Analyze(denied) error = %v", err)
	}
	items := failed.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code != "spice.annotation-tool.policy-denied" ||
		!strings.Contains(items[0].Message, "deliberately denied") ||
		!strings.EqualFold(
			filepath.Clean(items[0].Location.Path),
			filepath.Clean(mainPath),
		) {
		t.Fatalf("Analyze(denied) diagnostics = %+v", items)
	}
}

func assertFixtureUsesPublicSDK(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range [][]byte{
			[]byte(`github.com/StevenBuglione/spice/compiler`),
			[]byte(`github.com/StevenBuglione/spice/internal`),
		} {
			if bytes.Contains(content, forbidden) {
				t.Errorf(
					"third-party fixture %s imports non-public Spice code",
					path,
				)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect third-party fixture SDK usage: %v", err)
	}
}

func assertThirdPartyDefinitions(
	t *testing.T,
	definitions []AnnotationDefinition,
) {
	t.Helper()
	selected := make(map[string]AnnotationDefinition)
	for _, definition := range definitions {
		selected[definition.Name] = definition
	}
	for _, expected := range []struct {
		name, symbol, descriptorFile, implementation string
	}{
		{
			name:           "fixture.Factory",
			symbol:         "Factory",
			descriptorFile: "/annotation/wiring/factory.go",
			implementation: "/annotation/wiring/factory.go",
		},
		{
			name:           "fixture.Policy",
			symbol:         "Policy",
			descriptorFile: "/annotation/policy/policy.go",
			implementation: "/annotation/policy/policy.go",
		},
	} {
		definition, found := selected[expected.name]
		if !found ||
			definition.DescriptorSymbol != expected.symbol ||
			!definition.HasDescriptorLocation ||
			!definition.Implementation.HasLocation ||
			definition.Provenance.Module !=
				"example.com/spice-annotation-fixture" ||
			definition.Provenance.Version != "v0.0.0" ||
			!definition.Provenance.LocalReplacement ||
			!strings.HasSuffix(
				filepath.ToSlash(
					definition.DescriptorLocation.Path,
				),
				expected.descriptorFile,
			) ||
			!strings.HasSuffix(
				filepath.ToSlash(
					definition.Implementation.Location.Path,
				),
				expected.implementation,
			) {
			t.Fatalf(
				"definition %q = %+v, found = %t",
				expected.name,
				definition,
				found,
			)
		}
	}
}

func assertThirdPartyOccurrences(
	t *testing.T,
	annotations []Annotation,
) {
	t.Helper()
	var aliasFound, namespaceFound bool
	for _, item := range annotations {
		switch item.Spelling {
		case "Construct":
			aliasFound = item.Name == "fixture.Factory" &&
				item.DefinitionSymbol == "Factory"
		case "fixture.Policy":
			namespaceFound = item.Name == "fixture.Policy" &&
				item.DefinitionSymbol == "Policy"
		}
	}
	if !aliasFound || !namespaceFound {
		t.Fatalf(
			"third-party occurrences alias=%t namespace=%t: %+v",
			aliasFound,
			namespaceFound,
			annotations,
		)
	}
}

func assertThirdPartyGeneration(
	t *testing.T,
	root string,
	result Result,
) {
	t.Helper()
	plan, found := result.GenerationPlan()
	if !found || len(plan.Files()) != 2 {
		t.Fatalf(
			"GenerationPlan() found=%t files=%d",
			found,
			len(plan.Files()),
		)
	}
	var generated generate.File
	for _, file := range plan.Files() {
		if file.Role == generate.FileRoleApplication {
			generated = file
		}
	}
	if generated.Path !=
		"internal/spicegen/spice_annotation_app/zz_spice_gen.go" ||
		!bytes.Contains(
			generated.Content(),
			[]byte("component.ProvideMessage()"),
		) {
		t.Fatalf("generated file = %q", generated.Content())
	}
	committed, err := os.ReadFile(filepath.Join(root, generated.Path))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", generated.Path, err)
	}
	if !bytes.Equal(committed, generated.Content()) {
		t.Fatal("committed third-party generated Go is stale")
	}
	manifest, err := os.ReadFile(filepath.Join(
		root,
		".spice",
		"spice_annotation_app.manifest.json",
	))
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	if !bytes.Equal(manifest, plan.ManifestContent()) {
		t.Fatal("committed third-party ownership manifest is stale")
	}
}
