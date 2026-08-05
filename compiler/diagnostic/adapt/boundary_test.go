package adapt

import (
	"go/token"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/toolchain/compiler/generate"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/compiler/starter"
	"github.com/spice-framework/toolchain/compiler/validate"
)

func TestSourceAdaptersPreservePhysicalAndDisplayLocations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	display := token.Position{Filename: "generated.go", Line: 7, Column: 4, Offset: 21}
	physical := token.Position{
		Filename: filepath.Join(root, "source.go"),
		Line:     19,
		Column:   2,
		Offset:   73,
	}

	tests := []struct {
		name string
		code string
		set  func() int
	}{
		{
			name: "generation",
			code: "spice.generation.conflict",
			set: func() int {
				return Generation(root, []generate.Diagnostic{{
					Position: display, PhysicalPosition: physical,
					Kind: "conflict", Message: "generated output conflicts",
				}}).Len()
			},
		},
		{
			name: "module",
			code: "spice.module.internal-access",
			set: func() int {
				return Module(root, []modulith.Diagnostic{{
					Position: display, PhysicalPosition: physical,
					Kind: "internal-access", Message: "module boundary crossed",
				}}).Len()
			},
		},
		{
			name: "validation",
			code: "spice.validation.unknown-annotation",
			set: func() int {
				return Validation(root, []validate.Diagnostic{{
					Position: display, PhysicalPosition: physical,
					Annotation: "Missing", Target: annotation.TargetFunction,
					Unknown: true,
				}}).Len()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.set() != 1 {
				t.Fatalf("adapter did not preserve its diagnostic")
			}
		})
	}

	items := Generation(root, []generate.Diagnostic{{
		Position: display, PhysicalPosition: physical,
		Kind: "conflict", Message: "generated output conflicts",
	}}).Items()
	if items[0].Code != "spice.generation.conflict" ||
		items[0].Location.Range.Start.Line != 19 ||
		items[0].Location.Display == nil ||
		items[0].Location.Display.Range.Start.Line != 7 {
		t.Fatalf("mapped generation diagnostic = %#v", items[0])
	}
}

func TestProviderAdaptersConvertApplicableZeroWidthFixes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	display := token.Position{Filename: "mapped.go", Line: 5, Column: 8, Offset: 17}
	physical := token.Position{
		Filename: filepath.Join(root, "provider.go"),
		Line:     12,
		Column:   3,
		Offset:   48,
	}
	diagnostic := provider.Diagnostic{
		Position: display, PhysicalPosition: physical,
		Kind: "missing-interface-assertion", Message: "assert the interface",
		Fixes: []provider.SuggestedFix{{
			Title:     "Add assertion",
			AppliesAt: display, AppliesAtPhysical: physical,
			Edits: []provider.SuggestedEdit{{
				Position: display, PhysicalPosition: physical,
				NewText: "var _ Service = (*Implementation)(nil)\n",
			}},
		}, {
			Title: "Add assertion without an applicability range",
			Edits: []provider.SuggestedEdit{{
				Position: display, PhysicalPosition: physical,
				NewText: "// assertion\n",
			}},
		}},
	}

	for _, set := range []struct {
		name string
		got  func() []provider.Diagnostic
	}{
		{name: "provider", got: func() []provider.Diagnostic { return []provider.Diagnostic{diagnostic} }},
		{name: "starter", got: func() []provider.Diagnostic { return []provider.Diagnostic{diagnostic} }},
	} {
		t.Run(set.name, func(t *testing.T) {
			t.Parallel()
			converted := Provider(root, set.got()).Items()
			if set.name == "starter" {
				converted = StarterProviders(root, set.got()).Items()
			}
			if len(converted) != 1 || len(converted[0].Fixes) != 2 {
				t.Fatalf("converted diagnostics = %#v", converted)
			}
			fix := converted[0].Fixes[0]
			if fix.AppliesTo == nil || len(fix.Edits) != 1 ||
				fix.Edits[0].Location.Range.Start != fix.Edits[0].Location.Range.End ||
				fix.Edits[0].Location.Display == nil ||
				fix.Edits[0].Location.Display.Range.Start !=
					fix.Edits[0].Location.Display.Range.End {
				t.Fatalf("converted fix = %#v", fix)
			}
			if converted[0].Fixes[1].AppliesTo != nil {
				t.Fatalf("source-less applicability = %#v", converted[0].Fixes[1])
			}
		})
	}
}

func TestResolutionAndStarterDependencyFallbacks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	resolution := Resolution(root, []resolve.Diagnostic{{
		Position:       token.Position{Filename: "mapped.go", Line: 3, Column: 2, Offset: 7},
		PhysicalFile:   "physical.go",
		PhysicalOffset: 31,
		Kind:           "unresolved",
		Message:        "annotation is unresolved",
	}}).Items()
	if len(resolution) != 1 || resolution[0].Code != "spice.resolution.unresolved" ||
		resolution[0].Location.Range.Start.Offset != 31 ||
		resolution[0].Location.Display == nil {
		t.Fatalf("Resolution() = %#v", resolution)
	}

	dependencies := StarterDependencies([]starter.DependencyDiagnostic{{
		Kind: "missing-module", Message: "starter dependency is absent",
	}}).Items()
	if len(dependencies) != 1 || dependencies[0].Code != "spice.starter.missing-module" ||
		dependencies[0].Location.URI != "" {
		t.Fatalf("StarterDependencies() = %#v", dependencies)
	}
}

func TestSourceDiagnosticFallsBackToDisplayCoordinates(t *testing.T) {
	t.Parallel()
	item := sourceDiagnostic(
		t.TempDir(),
		"load",
		"fallback",
		"physical coordinates unavailable",
		token.Position{Filename: "main.go", Line: 9, Column: 6, Offset: 44},
		token.Position{},
	)
	if item.Location.Range.Start.Line != 9 ||
		item.Location.Range.Start.Column != 6 ||
		item.Location.Range.Start.Offset != 44 {
		t.Fatalf("fallback location = %#v", item.Location)
	}
}
