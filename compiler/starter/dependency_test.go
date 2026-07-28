package starter_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/resolve"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

func TestCatalogValidatesExactReviewedModuleVersions(t *testing.T) {
	spec := constructorManifest(t, "example.com/acme/starter/search").Spec()
	spec.Dependencies = []publicstarter.Dependency{
		{Module: "example.com/client", Version: "v1.2.3", License: "Apache-2.0"},
		{Module: "example.com/transport", Version: "v2.0.0", License: "MIT"},
	}
	catalog := newCatalog(t, mustManifest(t, spec))
	requirements := catalog.Dependencies()
	wantRequirements := []compilerstarter.DependencyRequirement{
		{
			SourceID: "example.com/acme/starter/search",
			Module:   "example.com/client",
			Version:  "v1.2.3",
			License:  "Apache-2.0",
		},
		{
			SourceID: "example.com/acme/starter/search",
			Module:   "example.com/transport",
			Version:  "v2.0.0",
			License:  "MIT",
		},
	}
	if !slices.Equal(requirements, wantRequirements) {
		t.Fatalf("Dependencies() = %#v, want %#v", requirements, wantRequirements)
	}
	if len(requirements) != 2 {
		t.Fatalf("Dependencies() count = %d", len(requirements))
	}

	modules := []compilerstarter.ModuleVersion{
		{Path: "example.com/client", Version: "v1.2.3"},
		{Path: "example.com/transport", Version: "v2.0.0"},
	}
	if diagnostics := catalog.ValidateModuleVersions(modules); len(diagnostics) != 0 {
		t.Fatalf("ValidateModuleVersions() diagnostics = %#v", diagnostics)
	}
	requirements[0].Version = "changed"
	freshRequirements := catalog.Dependencies()
	if len(freshRequirements) != 2 {
		t.Fatalf("fresh Dependencies() count = %d", len(freshRequirements))
	}
	if freshRequirements[0].Version == "changed" {
		t.Fatal("Dependencies() returned mutable catalog storage")
	}
}

func TestCatalogRejectsMissingMismatchedAndReplacedDependencies(t *testing.T) {
	spec := constructorManifest(t, "example.com/acme/starter/search").Spec()
	spec.Dependencies = []publicstarter.Dependency{
		{Module: "example.com/alpha", Version: "v1.0.0", License: "MIT"},
		{Module: "example.com/beta", Version: "v2.0.0", License: "MIT"},
		{Module: "example.com/gamma", Version: "v3.0.0", License: "MIT"},
	}
	catalog := newCatalog(t, mustManifest(t, spec))
	diagnostics := catalog.ValidateModuleVersions([]compilerstarter.ModuleVersion{
		{Path: "example.com/alpha", Version: "v1.1.0"},
		{
			Path:            "example.com/beta",
			Version:         "v2.0.0",
			ReplacementPath: "../local-beta",
		},
	})
	joined := dependencyDiagnosticText(diagnostics)
	for _, expected := range []string{
		"application resolves v1.1.0",
		"replaces it with ../local-beta",
		"example.com/gamma at v3.0.0",
		"absent from the application build list",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	if len(diagnostics) != 3 {
		t.Fatalf("ValidateModuleVersions() diagnostic count = %d", len(diagnostics))
	}
	if got := []string{
		diagnostics[0].Module,
		diagnostics[1].Module,
		diagnostics[2].Module,
	}; !slices.Equal(got, []string{
		"example.com/alpha",
		"example.com/beta",
		"example.com/gamma",
	}) {
		t.Fatalf("diagnostic order = %v", got)
	}
}

func TestCatalogAcceptsExactVersionReplacementIdentity(t *testing.T) {
	spec := constructorManifest(t, "example.com/acme/starter/search").Spec()
	spec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/client",
		Version: "v1.2.3",
		License: "MIT",
	}}
	catalog := newCatalog(t, mustManifest(t, spec))
	diagnostics := catalog.ValidateModuleVersions([]compilerstarter.ModuleVersion{{
		Path:               "example.com/client",
		Version:            "v1.0.0",
		ReplacementPath:    "example.com/client",
		ReplacementVersion: "v1.2.3",
	}})
	if len(diagnostics) != 0 {
		t.Fatalf("ValidateModuleVersions() diagnostics = %#v", diagnostics)
	}
}

func TestCatalogSelectsDependenciesFromActiveStarters(t *testing.T) {
	constructorSpec := constructorManifest(
		t,
		"example.com/acme/starter/cache",
	).Spec()
	constructorSpec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/cache-client",
		Version: "v1.0.0",
		License: "MIT",
	}}
	annotationSpec := annotatedManifest(
		t,
		searchID,
		"1.2.0",
		"search.Enable",
		"search.client",
	).Spec()
	annotationSpec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/search-client",
		Version: "v2.0.0",
		License: "Apache-2.0",
	}}
	catalog := newCatalog(
		t,
		mustManifest(t, annotationSpec),
		mustManifest(t, constructorSpec),
	)
	constructorOnly := []compilerstarter.DependencyRequirement{{
		SourceID: "example.com/acme/starter/cache",
		Module:   "example.com/cache-client",
		Version:  "v1.0.0",
		License:  "MIT",
	}}
	if got := catalog.ActiveDependencies(nil); !slices.Equal(
		got,
		constructorOnly,
	) {
		t.Fatalf("ActiveDependencies(nil) = %#v, want %#v", got, constructorOnly)
	}

	application := resolve.Occurrence{
		Annotation: annotation.Annotation{Name: "Application"},
		SymbolID:   "application",
		Contributions: []sdk.Contribution{{
			Kind:        sdk.ContributionApplication,
			Application: &sdk.ApplicationContribution{},
		}},
	}
	feature := resolve.Occurrence{
		Annotation: annotation.Annotation{Name: "search.Enable"},
		SymbolID:   "application",
	}
	active := catalog.ActiveDependencies([]resolve.Occurrence{
		feature,
		application,
	})
	wantActive := []compilerstarter.DependencyRequirement{
		constructorOnly[0],
		{
			SourceID: searchID,
			Module:   "example.com/search-client",
			Version:  "v2.0.0",
			License:  "Apache-2.0",
		},
	}
	if !slices.Equal(active, wantActive) {
		t.Fatalf("ActiveDependencies() = %#v, want %#v", active, wantActive)
	}

	feature.SymbolID = "other-function"
	if got := catalog.ActiveDependencies([]resolve.Occurrence{
		application,
		feature,
	}); !slices.Equal(got, constructorOnly) {
		t.Fatalf("misplaced annotation selected dependencies: %#v", got)
	}
}

func TestCatalogRejectsInvalidModuleGraph(t *testing.T) {
	spec := constructorManifest(t, "example.com/acme/starter/search").Spec()
	spec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/client",
		Version: "v1.2.3",
		License: "MIT",
	}}
	catalog := newCatalog(t, mustManifest(t, spec))
	diagnostics := catalog.ValidateModuleVersions([]compilerstarter.ModuleVersion{
		{Path: " example.com/client", Version: "v1.2.3"},
		{Path: "example.com/client", Version: "v1.2.3"},
		{Path: "example.com/client", Version: "v1.2.3"},
	})
	joined := dependencyDiagnosticText(diagnostics)
	if !strings.Contains(joined, "invalid module path") ||
		!strings.Contains(joined, "duplicate module") {
		t.Fatalf("ValidateModuleVersions() diagnostics = %#v", diagnostics)
	}
}

func TestCatalogRejectsConflictingDependencyReviews(t *testing.T) {
	firstSpec := constructorManifest(t, "example.com/first/starter/search").Spec()
	firstSpec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/client",
		Version: "v1.2.3",
		License: "MIT",
	}}
	secondSpec := constructorManifest(t, "example.com/second/starter/search").Spec()
	secondSpec.Dependencies = []publicstarter.Dependency{{
		Module:  "example.com/client",
		Version: "v1.3.0",
		License: "Apache-2.0",
	}}
	_, err := compilerstarter.NewWithCompatibility(
		publicstarter.APIVersion,
		currentGo,
		mustManifest(t, firstSpec),
		mustManifest(t, secondSpec),
	)
	if err == nil ||
		!strings.Contains(err.Error(), "dependency example.com/client has conflicting reviews") ||
		!strings.Contains(err.Error(), "v1.2.3, MIT") ||
		!strings.Contains(err.Error(), "v1.3.0, Apache-2.0") {
		t.Fatalf("NewWithCompatibility() error = %v", err)
	}
}

func dependencyDiagnosticText(
	diagnostics []compilerstarter.DependencyDiagnostic,
) string {
	values := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		values[index] = diagnostic.Error()
	}
	return strings.Join(values, "\n")
}
