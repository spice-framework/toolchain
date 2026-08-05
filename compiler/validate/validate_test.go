package validate

import (
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/builtin"
	annotationparser "github.com/spice-framework/toolchain/compiler/parser"
	"github.com/spice-framework/toolchain/compiler/scan"
)

func TestOccurrencesAcceptsValidBuiltInsAndArguments(t *testing.T) {
	t.Parallel()
	occurrences := []scan.Occurrence{
		parsedOccurrence(t, "app.go", 1, `// @Application`, scan.TargetFunction, "main"),
		parsedOccurrence(t, "config.go", 2, `// @Configuration`, scan.TargetType, "Config"),
		parsedOccurrence(t, "controller.go", 3, `// @Controller(prefix="/users")`, scan.TargetType, "Controller"),
		parsedOccurrence(t, "controller.go", 4, `// @Controller`, scan.TargetType, "RootController"),
		parsedOccurrence(t, "controller.go", 5, `// @Get(path="/{id}")`, scan.TargetMethod, "GetUser"),
		parsedOccurrence(t, "controller.go", 6, `// @Get("/{id}")`, scan.TargetMethod, "GetUserCompact"),
		parsedOccurrence(t, "controller.go", 7, `// @Post(path="/")`, scan.TargetMethod, "CreateUser"),
		parsedOccurrence(t, "controller.go", 8, `// @Post("/")`, scan.TargetMethod, "CreateUserCompact"),
		parsedOccurrence(t, "service.go", 9, `// @Service`, scan.TargetType, "UserService"),
		parsedOccurrence(t, "app.go", 10, `// @management.Enable(expose=["health", "info"])`, scan.TargetFunction, "main"),
		parsedOccurrence(t, "app.go", 11, `// @observability.Logging`, scan.TargetFunction, "main"),
		parsedOccurrence(t, "controller.go", 12, `// @security.Authorize(authenticated=true, anyRoles=["admin"], allScopes=["users:read"])`, scan.TargetMethod, "Secure"),
	}
	if diagnostics := Occurrences(occurrences, builtin.Registry()); len(diagnostics) != 0 {
		t.Fatalf("Occurrences() diagnostics = %#v", diagnostics)
	}
}

func TestOccurrencesRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		comment  string
		target   scan.Target
		contains []string
	}{
		{name: "unknown argument", comment: `// @Controller(prefx="/users")`, target: scan.TargetType, contains: []string{`does not define argument "prefx"`, "available argument: prefix"}},
		{name: "wrong controller kind", comment: `// @Controller(prefix=3)`, target: scan.TargetType, contains: []string{`argument "prefix" requires string, got integer`}},
		{name: "named only", comment: `// @Controller("/users")`, target: scan.TargetType, contains: []string{`does not accept a positional argument`, `named argument "prefix"`}},
		{name: "missing required", comment: `// @Get`, target: scan.TargetMethod, contains: []string{`requires argument "path"`}},
		{name: "wrong route kind", comment: `// @Get(path=3)`, target: scan.TargetMethod, contains: []string{`argument "path" requires string, got integer`}},
		{name: "duplicate semantic assignment", comment: `// @Get("/{id}", path="/{other}")`, target: scan.TargetMethod, contains: []string{`assigns argument "path" more than once`}},
		{name: "marker arguments", comment: `// @Service(name="users")`, target: scan.TargetType, contains: []string{"does not accept arguments"}},
		{name: "multiple positional", comment: `// @Get("/one", "/two")`, target: scan.TargetMethod, contains: []string{"accepts at most one positional argument"}},
		{name: "wrong management list item", comment: `// @management.Enable(expose=["health", readiness])`, target: scan.TargetFunction, contains: []string{`argument "expose" list item 1 requires string, got identifier`}},
		{name: "wrong authorization boolean", comment: `// @security.Authorize(authenticated="true")`, target: scan.TargetMethod, contains: []string{`argument "authenticated" requires boolean, got string`}},
		{name: "wrong authorization list item", comment: `// @security.Authorize(allScopes=["users:read", scope])`, target: scan.TargetMethod, contains: []string{`argument "allScopes" list item 1 requires string, got identifier`}},
		{name: "authorization named only", comment: `// @security.Authorize(true)`, target: scan.TargetMethod, contains: []string{`does not accept positional arguments`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostics := Occurrences([]scan.Occurrence{parsedOccurrence(t, "invalid.go", 7, test.comment, test.target, "Declaration")}, builtin.Registry())
			if len(diagnostics) == 0 {
				t.Fatal("expected diagnostics")
			}
			joined := diagnosticsText(diagnostics)
			for _, expected := range test.contains {
				if !strings.Contains(joined, expected) {
					t.Fatalf("diagnostics = %q, missing %q", joined, expected)
				}
			}
		})
	}
}

func TestOccurrencesAccumulatesUnknownAndMissingArguments(t *testing.T) {
	t.Parallel()
	diagnostics := Occurrences([]scan.Occurrence{parsedOccurrence(t, "route.go", 3, `// @Get(paht="/")`, scan.TargetMethod, "Get")}, builtin.Registry())
	got := diagnosticsText(diagnostics)
	for _, expected := range []string{`does not define argument "paht"`, `requires argument "path"`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("diagnostics = %q, missing %q", got, expected)
		}
	}
}

func TestOccurrencesContinuesTargetAndExistenceValidation(t *testing.T) {
	t.Parallel()
	occurrences := []scan.Occurrence{
		parsedOccurrence(t, "invalid.go", 1, `// @Controller(prefix="/users")`, scan.TargetFunction, "NewController"),
		parsedOccurrence(t, "security.go", 2, `// @security.Authorize`, scan.TargetType, "Admin"),
	}
	diagnostics := Occurrences(occurrences, builtin.Registry())
	got := diagnosticsText(diagnostics)
	for _, expected := range []string{
		"@Controller cannot target function",
		"@security.Authorize cannot target type",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("diagnostics = %q, missing %q", got, expected)
		}
	}
}

func TestOccurrencesSortsDiagnosticsDeterministically(t *testing.T) {
	t.Parallel()
	input := []scan.Occurrence{
		parsedOccurrence(t, "z.go", 2, `// @Controller(prefx=3)`, scan.TargetFunction, "Z"),
		parsedOccurrence(t, "a.go", 9, `// @Get`, scan.TargetType, "A"),
		parsedOccurrence(t, "a.go", 3, `// @Post(path=3)`, scan.TargetType, "B"),
	}
	var first []string
	for run := range 10 {
		diagnostics := Occurrences(input, builtin.Registry())
		got := make([]string, len(diagnostics))
		for index, diagnostic := range diagnostics {
			got[index] = diagnostic.Error()
		}
		if run == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d diagnostics = %#v, want %#v", run, got, first)
		}
	}
	if len(first) != 6 {
		t.Fatalf("diagnostics = %#v, want 6 independent errors", first)
	}
}

func parsedOccurrence(t *testing.T, file string, line int, comment string, target scan.Target, declaration string) scan.Occurrence {
	t.Helper()
	parsed, ok, err := annotationparser.ParseComment(comment, token.Position{Filename: file, Line: line, Column: 1})
	if err != nil || !ok {
		t.Fatalf("ParseComment(%q) ok=%v error=%v", comment, ok, err)
	}
	return scan.Occurrence{Annotation: parsed, Target: target, Name: declaration, File: file}
}

func diagnosticsText(diagnostics []Diagnostic) string {
	messages := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		messages[index] = diagnostic.Error()
	}
	return strings.Join(messages, "\n")
}

func TestDiagnosticCodeAndMessage(t *testing.T) {
	t.Parallel()
	diagnostics := Occurrences([]scan.Occurrence{
		parsedOccurrence(
			t,
			"controller.go",
			7,
			`// @Controller(prefx="/users")`,
			scan.TargetType,
			"Controller",
		),
	}, builtin.Registry())
	if len(diagnostics) != 1 ||
		diagnostics[0].Code() != "unknown-argument" ||
		strings.HasPrefix(diagnostics[0].Message(), "controller.go:") ||
		!strings.Contains(diagnostics[0].Message(), `"prefx"`) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func FuzzOccurrences(f *testing.F) {
	f.Add("Get", "path", "/users/{id}")
	f.Add("Controller", "prefix", "/users")
	f.Add("Unknown", "value", "text")
	f.Fuzz(func(t *testing.T, name, argumentName, value string) {
		occurrences := []scan.Occurrence{{
			Annotation: annotation.Annotation{
				Name:     name,
				Position: token.Position{Filename: "fuzz.go", Line: 1, Column: 1},
				Arguments: []annotation.Argument{{
					Name: argumentName,
					Value: annotation.Value{
						Kind:   annotation.KindString,
						String: value,
					},
				}},
			},
			Target: scan.TargetFunction,
			Name:   "FuzzTarget",
			File:   "fuzz.go",
		}}
		first := Occurrences(occurrences, builtin.Registry())
		second := Occurrences(occurrences, builtin.Registry())
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("validation is nondeterministic: first=%#v second=%#v", first, second)
		}
		for _, diagnostic := range first {
			if diagnostic.Error() == "" {
				t.Fatal("empty diagnostic")
			}
		}
	})
}
