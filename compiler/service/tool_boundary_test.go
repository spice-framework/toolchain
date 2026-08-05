package service

import (
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
	compilerbootstrap "github.com/spice-framework/toolchain/compiler/bootstrap"
	"github.com/spice-framework/toolchain/compiler/diagnostic"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

func TestNormalizedToolValueCoversEveryProtocolLiteralKind(t *testing.T) {
	t.Parallel()

	value := annotation.Value{
		Kind: annotation.KindList,
		List: []annotation.Value{
			{Kind: annotation.KindString, String: "stripe"},
			{Kind: annotation.KindInteger, Integer: 42},
			{Kind: annotation.KindBoolean, Boolean: true},
			{Kind: annotation.KindIdentifier, Identifier: "payments.Processor"},
			{
				Kind: annotation.KindList,
				List: []annotation.Value{{Kind: annotation.KindString, String: "nested"}},
			},
		},
	}
	got, err := normalizedToolValue(value)
	if err != nil {
		t.Fatalf("normalizedToolValue() error = %v", err)
	}
	want := []any{"stripe", int64(42), true, "payments.Processor", []any{"nested"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizedToolValue() = %#v, want %#v", got, want)
	}

	invalid := annotation.Value{
		Kind: annotation.KindList,
		List: []annotation.Value{{Kind: annotation.Kind("dynamic")}},
	}
	if _, err := normalizedToolValue(invalid); err == nil ||
		!strings.Contains(err.Error(), "unsupported value kind") {
		t.Fatalf("normalizedToolValue(invalid) error = %v", err)
	}
	if _, err := toolArgumentJSON(invalid); err == nil {
		t.Fatal("toolArgumentJSON(invalid) error = nil")
	}
}

func TestCloneBootstrapDefinitionsOwnsEveryNestedSlice(t *testing.T) {
	t.Parallel()

	input := []compilerbootstrap.Definition{{
		Annotation: "management.Enable",
		Options: []compilerbootstrap.OptionDefinition{{
			Name:           "expose",
			ListItemKinds:  []annotation.Kind{annotation.KindString},
			AllowedStrings: []string{"health", "info"},
		}},
		Requirements: []compilerbootstrap.RuntimeCapability{"http"},
		EntryPoints: []compilerbootstrap.EntryPoint{{
			Package: "example.com/management",
			Symbol:  "New",
		}},
	}}
	cloned := cloneBootstrapDefinitions(input)
	cloned[0].Options[0].ListItemKinds[0] = annotation.KindInteger
	cloned[0].Options[0].AllowedStrings[0] = "metrics"
	cloned[0].Requirements[0] = "metrics"
	cloned[0].EntryPoints[0].Symbol = "Changed"
	if input[0].Options[0].ListItemKinds[0] != annotation.KindString ||
		input[0].Options[0].AllowedStrings[0] != "health" ||
		input[0].Requirements[0] != "http" ||
		input[0].EntryPoints[0].Symbol != "New" {
		t.Fatalf("cloneBootstrapDefinitions() aliased input: %+v", input)
	}
}

func TestAddTagsFromFlagsAcceptsBothGoFlagForms(t *testing.T) {
	t.Parallel()

	tags := map[string]struct{}{}
	addTagsFromFlags(tags, "-race -tags one,two -mod=vendor -tags=three")
	for _, tag := range []string{"one", "two", "three"} {
		if _, found := tags[tag]; !found {
			t.Fatalf("addTagsFromFlags() missing %q: %v", tag, tags)
		}
	}
}

func TestToolResultDiagnosticsValidatesAndMapsHandlerFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	occurrence := resolve.Occurrence{
		PhysicalFile:   "service.go",
		PhysicalOffset: 17,
		DisplayPosition: token.Position{
			Filename: "display.go",
			Line:     9,
			Column:   4,
			Offset:   80,
		},
	}
	for _, test := range []struct {
		name       string
		diagnostic protocol.Diagnostic
		want       string
	}{
		{
			name:       "missing code",
			diagnostic: protocol.Diagnostic{Severity: string(diagnostic.SeverityError), Message: "failed"},
			want:       "requires code and message",
		},
		{
			name:       "missing message",
			diagnostic: protocol.Diagnostic{Code: "invalid", Severity: string(diagnostic.SeverityError)},
			want:       "requires code and message",
		},
		{
			name: "unsupported severity",
			diagnostic: protocol.Diagnostic{
				Code: "invalid", Severity: "fatal", Message: "failed",
			},
			want: "unsupported",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := toolResultDiagnostics(root, occurrence, []protocol.Diagnostic{test.diagnostic})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("toolResultDiagnostics() error = %v, want %q", err, test.want)
			}
		})
	}

	values := []protocol.Diagnostic{
		{Code: "error", Severity: string(diagnostic.SeverityError), Message: "error message"},
		{Code: "warning", Severity: string(diagnostic.SeverityWarning), Message: "warning message"},
		{Code: "info", Severity: string(diagnostic.SeverityInformation), Message: "information message"},
		{Code: "hint", Severity: string(diagnostic.SeverityHint), Message: "hint message"},
	}
	got, err := toolResultDiagnostics(root, occurrence, values)
	if err != nil {
		t.Fatalf("toolResultDiagnostics() error = %v", err)
	}
	if len(got) != len(values) {
		t.Fatalf("toolResultDiagnostics() count = %d, want %d", len(got), len(values))
	}
	for index, item := range got {
		if item.Code != diagnostic.Code("annotation-tool", values[index].Code) ||
			item.Message != values[index].Message ||
			item.Location.Display == nil ||
			item.Location.Range.Start.Offset != occurrence.PhysicalOffset {
			t.Fatalf("tool diagnostic %d = %+v", index, item)
		}
	}
}
