package validate

import (
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/scan"
)

func TestOccurrencesAcceptsValidBuiltInTargets(t *testing.T) {
	t.Parallel()

	occurrences := []scan.Occurrence{
		occurrence("app.go", 1, "Application", scan.TargetFunction, "main"),
		occurrence("config.go", 2, "Configuration", scan.TargetType, "Config"),
		occurrence("controller.go", 3, "Controller", scan.TargetType, "Controller"),
		occurrence("controller.go", 4, "Get", scan.TargetMethod, "GetUser"),
		occurrence("controller.go", 5, "Post", scan.TargetMethod, "CreateUser"),
		occurrence("service.go", 6, "Service", scan.TargetType, "UserService"),
	}

	if diagnostics := Occurrences(occurrences, builtin.Registry()); len(diagnostics) != 0 {
		t.Fatalf("Occurrences() diagnostics = %#v", diagnostics)
	}
}

func TestOccurrencesRejectsInvalidBuiltInTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		annotation string
		target     scan.Target
		name       string
		allowed    []annotation.Target
	}{
		{annotation: "Controller", target: scan.TargetFunction, name: "NewController", allowed: []annotation.Target{annotation.TargetType}},
		{annotation: "Controller", target: scan.TargetMethod, name: "Handle", allowed: []annotation.Target{annotation.TargetType}},
		{annotation: "Service", target: scan.TargetPackage, name: "sample", allowed: []annotation.Target{annotation.TargetType}},
		{annotation: "Get", target: scan.TargetType, name: "Controller", allowed: []annotation.Target{annotation.TargetMethod}},
		{annotation: "Post", target: scan.TargetType, name: "Controller", allowed: []annotation.Target{annotation.TargetMethod}},
		{annotation: "Application", target: scan.TargetVariable, name: "Application", allowed: []annotation.Target{annotation.TargetFunction}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.annotation+"_on_"+string(test.target), func(t *testing.T) {
			t.Parallel()
			diagnostics := Occurrences(
				[]scan.Occurrence{occurrence("invalid.go", 7, test.annotation, test.target, test.name)},
				builtin.Registry(),
			)
			if len(diagnostics) != 1 {
				t.Fatalf("len(diagnostics) = %d, want 1", len(diagnostics))
			}
			diagnostic := diagnostics[0]
			if diagnostic.Annotation != test.annotation || diagnostic.Target != test.target || diagnostic.Name != test.name {
				t.Fatalf("diagnostic = %#v", diagnostic)
			}
			if !reflect.DeepEqual(diagnostic.Allowed, test.allowed) {
				t.Fatalf("Allowed = %#v, want %#v", diagnostic.Allowed, test.allowed)
			}
			message := diagnostic.Error()
			for _, expected := range []string{"invalid.go:7:1", "@" + test.annotation, string(test.target), string(test.allowed[0])} {
				if !strings.Contains(message, expected) {
					t.Fatalf("Error() = %q, missing %q", message, expected)
				}
			}
		})
	}
}

func TestOccurrencesRejectsUnknownAnnotations(t *testing.T) {
	t.Parallel()

	diagnostics := Occurrences(
		[]scan.Occurrence{occurrence("security.go", 12, "security.Authorize", scan.TargetMethod, "Admin")},
		builtin.Registry(),
	)
	if len(diagnostics) != 1 || !diagnostics[0].Unknown {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if message := diagnostics[0].Error(); !strings.Contains(message, "unknown annotation @security.Authorize") || !strings.Contains(message, "registered annotation definition") {
		t.Fatalf("Error() = %q", message)
	}
}

func TestOccurrencesSortsDiagnosticsDeterministically(t *testing.T) {
	t.Parallel()

	input := []scan.Occurrence{
		occurrence("z.go", 2, "Controller", scan.TargetFunction, "Z"),
		occurrence("a.go", 9, "Get", scan.TargetType, "A"),
		occurrence("a.go", 3, "Post", scan.TargetType, "B"),
	}
	want := []string{
		"a.go:3:1: annotation @Post cannot target type \"B\"; allowed target: method",
		"a.go:9:1: annotation @Get cannot target type \"A\"; allowed target: method",
		"z.go:2:1: annotation @Controller cannot target function \"Z\"; allowed target: type",
	}

	for run := 0; run < 5; run++ {
		diagnostics := Occurrences(input, builtin.Registry())
		got := make([]string, len(diagnostics))
		for index, diagnostic := range diagnostics {
			got[index] = diagnostic.Error()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d diagnostics = %#v, want %#v", run, got, want)
		}
	}
}

func occurrence(file string, line int, name string, target scan.Target, declaration string) scan.Occurrence {
	return scan.Occurrence{
		Annotation: annotation.Annotation{
			Name: name,
			Position: token.Position{
				Filename: file,
				Line:     line,
				Column:   1,
			},
		},
		Target: target,
		Name:   declaration,
		File:   file,
	}
}
