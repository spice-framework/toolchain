package bootstrap

import (
	"go/token"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

func TestCompileBuildsNormalizedImmutableBuiltinMetadata(t *testing.T) {
	t.Parallel()
	resolution := resolve.Result{Occurrences: []resolve.Occurrence{
		bootstrapOccurrence(
			2,
			"app",
			LoggingAnnotation,
			annotation.TargetFunction,
			"Commerce",
		),
		bootstrapOccurrence(
			1,
			"app",
			ManagementAnnotation,
			annotation.TargetFunction,
			"Commerce",
			annotation.Argument{
				Name: "expose",
				Value: annotation.Value{
					Kind: annotation.KindList,
					List: []annotation.Value{
						{Kind: annotation.KindString, String: "metrics"},
						{Kind: annotation.KindString, String: "health"},
						{Kind: annotation.KindString, String: "info"},
						{Kind: annotation.KindString, String: "configprops"},
						{Kind: annotation.KindString, String: "modules"},
					},
				},
			},
			annotation.Argument{
				Name:  "access",
				Value: annotation.Value{Kind: annotation.KindString, String: "loopback"},
			},
		),
	}}

	result := Compile(
		resolution,
		[]Application{{SymbolID: "app", Name: "Commerce"}},
		Builtins(),
	)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %v", diagnosticText(diagnostics))
	}
	metadata := result.Metadata("app")
	features := metadata.Features()
	if len(features) != 2 ||
		features[0].Capability != CapabilityManagement ||
		features[1].Capability != CapabilityLogging {
		t.Fatalf("Features() = %#v", features)
	}
	if !metadata.Enabled(CapabilityLogging) {
		t.Fatal("logging capability was not enabled")
	}
	management, found := metadata.Management()
	if !found {
		t.Fatal("Management() did not find enabled management metadata")
	}
	wantEndpoints := []Endpoint{
		EndpointConfigProps,
		EndpointHealth,
		EndpointInfo,
		EndpointMetrics,
		EndpointModules,
	}
	if endpoints := management.Endpoints(); !slices.Equal(endpoints, wantEndpoints) {
		t.Fatalf("Endpoints() = %v, want %v", endpoints, wantEndpoints)
	}
	if access := management.Access(); access != "loopback" {
		t.Fatalf("Access() = %q, want loopback", access)
	}
	if missing := metadata.MissingRequirements(nil); len(missing) != 1 ||
		missing[0].Capability != CapabilityManagement {
		t.Fatalf("MissingRequirements(nil) = %#v", missing)
	}
	if missing := metadata.MissingRequirements(
		[]RuntimeCapability{RuntimeHTTPServeMux},
	); len(missing) != 0 {
		t.Fatalf("MissingRequirements(http mux) = %#v", missing)
	}

	features[0].Annotation = "changed"
	options := features[0].Options()
	for _, option := range options {
		if option.Name != "expose" {
			continue
		}
		value := option.Value()
		value.List[0].String = "changed"
	}
	endpoints := management.Endpoints()
	endpoints[0] = Endpoint("changed")
	fresh := result.Metadata("app")
	freshManagement, found := fresh.Management()
	if !found ||
		fresh.Features()[0].Annotation != ManagementAnnotation ||
		!slices.Equal(freshManagement.Endpoints(), wantEndpoints) {
		t.Fatal("compiled metadata was mutated through an accessor")
	}
}

func TestManagementAccessDefaultsToLoopback(t *testing.T) {
	t.Parallel()
	resolution := resolve.Result{Occurrences: []resolve.Occurrence{
		bootstrapOccurrence(
			1,
			"app",
			ManagementAnnotation,
			annotation.TargetFunction,
			"Commerce",
			annotation.Argument{
				Name: "expose",
				Value: annotation.Value{
					Kind: annotation.KindList,
					List: []annotation.Value{{
						Kind:   annotation.KindString,
						String: "health",
					}},
				},
			},
		),
	}}
	result := Compile(
		resolution,
		[]Application{{SymbolID: "app", Name: "Commerce"}},
		Builtins(),
	)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Compile() diagnostics = %v", diagnosticText(diagnostics))
	}
	management, found := result.Metadata("app").Management()
	if !found || management.Access() != "loopback" {
		t.Fatalf("Management() = %#v, %v", management, found)
	}
}

func TestLoggerManagementRequiresLoggingAndLoopback(t *testing.T) {
	t.Parallel()
	management := func(access string) resolve.Occurrence {
		arguments := []annotation.Argument{{
			Name: "expose",
			Value: annotation.Value{Kind: annotation.KindList, List: []annotation.Value{{
				Kind: annotation.KindString, String: "loggers",
			}}},
		}}
		if access != "" {
			arguments = append(arguments, annotation.Argument{
				Name: "access", Value: annotation.Value{Kind: annotation.KindString, String: access},
			})
		}
		return bootstrapOccurrence(1, "app", ManagementAnnotation, annotation.TargetFunction, "Commerce", arguments...)
	}
	tests := []struct {
		name        string
		occurrences []resolve.Occurrence
		contains    string
	}{
		{name: "missing logging", occurrences: []resolve.Occurrence{management("")}, contains: "requires @observability.Logging"},
		{name: "public", occurrences: []resolve.Occurrence{
			management("public"),
			bootstrapOccurrence(2, "app", LoggingAnnotation, annotation.TargetFunction, "Commerce"),
		}, contains: `requires access="loopback"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := Compile(
				resolve.Result{Occurrences: test.occurrences},
				[]Application{{SymbolID: "app", Name: "Commerce"}},
				Builtins(),
			)
			if diagnostics := diagnosticText(result.Diagnostics()); !strings.Contains(diagnostics, test.contains) {
				t.Fatalf("Compile() diagnostics = %q", diagnostics)
			}
		})
	}
}

func TestCompileRejectsInvalidManagementAccess(t *testing.T) {
	t.Parallel()
	result := Compile(
		resolve.Result{Occurrences: []resolve.Occurrence{bootstrapOccurrence(
			7,
			"app",
			ManagementAnnotation,
			annotation.TargetFunction,
			"Commerce",
			annotation.Argument{
				Name: "expose",
				Value: annotation.Value{
					Kind: annotation.KindList,
					List: []annotation.Value{{
						Kind:   annotation.KindString,
						String: "health",
					}},
				},
			},
			annotation.Argument{
				Name:  "access",
				Value: annotation.Value{Kind: annotation.KindString, String: "proxy"},
			},
		)}},
		[]Application{{SymbolID: "app", Name: "Commerce"}},
		Builtins(),
	)
	diagnostics := diagnosticText(result.Diagnostics())
	if !strings.Contains(diagnostics, `unsupported value "proxy"`) {
		t.Fatalf("Compile() diagnostics = %q", diagnostics)
	}
}

func TestCompileRejectsInvalidManagementEndpointLists(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []annotation.Value
		want   string
	}{
		{
			name: "unsupported",
			values: []annotation.Value{
				{Kind: annotation.KindString, String: "health"},
				{Kind: annotation.KindString, String: "env"},
			},
			want: `unsupported value "env"`,
		},
		{
			name: "duplicate",
			values: []annotation.Value{
				{Kind: annotation.KindString, String: "health"},
				{Kind: annotation.KindString, String: "health"},
			},
			want: `duplicate value "health"`,
		},
		{
			name:   "empty",
			values: nil,
			want:   "requires at least 1 item",
		},
		{
			name: "wrong item kind",
			values: []annotation.Value{
				{Kind: annotation.KindIdentifier, Identifier: "health"},
			},
			want: "item 0 requires string, got identifier",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			occurrence := bootstrapOccurrence(
				7,
				"app",
				ManagementAnnotation,
				annotation.TargetFunction,
				"Commerce",
				annotation.Argument{
					Name:  "expose",
					Value: annotation.Value{Kind: annotation.KindList, List: test.values},
				},
			)
			result := Compile(
				resolve.Result{Occurrences: []resolve.Occurrence{occurrence}},
				[]Application{{SymbolID: "app", Name: "Commerce"}},
				Builtins(),
			)
			diagnostics := result.Diagnostics()
			if len(diagnostics) == 0 ||
				!strings.Contains(diagnosticText(diagnostics), test.want) {
				t.Fatalf("Compile() diagnostics = %v, want %q", diagnostics, test.want)
			}
			if diagnostics[0].Position.Filename != "app.go" ||
				diagnostics[0].Position.Line != 7 {
				t.Fatalf("diagnostic has wrong position: %#v", diagnostics[0])
			}
		})
	}
}

func TestCompileRejectsDuplicateOrUnsatisfiedCompanionAnnotations(t *testing.T) {
	t.Parallel()
	resolution := resolve.Result{Occurrences: []resolve.Occurrence{
		bootstrapOccurrence(
			1,
			"app",
			LoggingAnnotation,
			annotation.TargetFunction,
			"Commerce",
		),
		bootstrapOccurrence(
			2,
			"app",
			LoggingAnnotation,
			annotation.TargetFunction,
			"Commerce",
		),
		bootstrapOccurrence(
			3,
			"helper",
			LoggingAnnotation,
			annotation.TargetFunction,
			"Helper",
		),
		bootstrapOccurrence(
			4,
			"service",
			LoggingAnnotation,
			annotation.TargetType,
			"Service",
		),
	}}
	result := Compile(
		resolution,
		[]Application{{SymbolID: "app", Name: "Commerce"}},
		Builtins(),
	)
	got := diagnosticText(result.Diagnostics())
	for _, want := range []string{
		"duplicated or conflicting",
		"requires @Application on the same function",
		"must target an @Application function",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics = %q, missing %q", got, want)
		}
	}
}

func TestCompileUsesExplicitDefinitionsAndNormalizesOptionOrder(t *testing.T) {
	t.Parallel()
	definition := Definition{
		Annotation: "example.Feature",
		Capability: "example.feature",
		Options: []OptionDefinition{
			{Name: "name", Kind: annotation.KindString, Required: true},
			{
				Name:          "roles",
				Kind:          annotation.KindList,
				ListItemKinds: []annotation.Kind{annotation.KindString},
				SortItems:     true,
			},
		},
	}
	left := bootstrapOccurrence(
		1,
		"app",
		"example.Feature",
		annotation.TargetFunction,
		"Commerce",
		annotation.Argument{
			Name: "roles",
			Value: annotation.Value{
				Kind: annotation.KindList,
				List: []annotation.Value{
					{Kind: annotation.KindString, String: "writer"},
					{Kind: annotation.KindString, String: "reader"},
				},
			},
		},
		annotation.Argument{
			Name:  "name",
			Value: annotation.Value{Kind: annotation.KindString, String: "orders"},
		},
	)
	right := left
	right.Annotation.Arguments = []annotation.Argument{
		left.Annotation.Arguments[1],
		{
			Name: "roles",
			Value: annotation.Value{
				Kind: annotation.KindList,
				List: []annotation.Value{
					{Kind: annotation.KindString, String: "reader"},
					{Kind: annotation.KindString, String: "writer"},
				},
			},
		},
	}
	applications := []Application{{SymbolID: "app", Name: "Commerce"}}
	leftResult := Compile(
		resolve.Result{Occurrences: []resolve.Occurrence{left}},
		applications,
		[]Definition{definition},
	)
	rightResult := Compile(
		resolve.Result{Occurrences: []resolve.Occurrence{right}},
		applications,
		[]Definition{definition},
	)
	if len(leftResult.Diagnostics()) != 0 || len(rightResult.Diagnostics()) != 0 {
		t.Fatalf(
			"Compile() diagnostics left=%v right=%v",
			leftResult.Diagnostics(),
			rightResult.Diagnostics(),
		)
	}
	if !reflect.DeepEqual(
		leftResult.Metadata("app").Features(),
		rightResult.Metadata("app").Features(),
	) {
		t.Fatalf(
			"normalized metadata differs: left=%#v right=%#v",
			leftResult.Metadata("app").Features(),
			rightResult.Metadata("app").Features(),
		)
	}
}

func TestCompileRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()
	result := Compile(
		resolve.Result{},
		nil,
		[]Definition{
			{Annotation: "example.Feature", Capability: "one"},
			{Annotation: "example.Feature", Capability: "two"},
		},
	)
	diagnostics := result.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "duplicate-definition" ||
		!strings.Contains(diagnostics[0].Error(), "declared more than once") {
		t.Fatalf("Compile() diagnostics = %#v", diagnostics)
	}
}

func bootstrapOccurrence(
	line int,
	symbolID string,
	name string,
	target annotation.Target,
	declaration string,
	arguments ...annotation.Argument,
) resolve.Occurrence {
	position := token.Position{
		Filename: "app.go",
		Offset:   line * 10,
		Line:     line,
		Column:   1,
	}
	return resolve.Occurrence{
		Annotation: annotation.Annotation{
			Name:      name,
			Arguments: arguments,
			Position:  position,
		},
		Target:          target,
		Name:            declaration,
		SymbolID:        symbolID,
		PackagePath:     "example.com/app",
		PhysicalFile:    "app.go",
		PhysicalOffset:  line * 10,
		DisplayPosition: position,
	}
}

func diagnosticText(diagnostics []Diagnostic) string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return strings.Join(result, "\n")
}
