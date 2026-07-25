package parser

import (
	"go/token"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
)

func TestParseComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		annotation string
		arguments  int
	}{
		{name: "compact", input: `//@Controller(prefix="/users")`, annotation: "Controller", arguments: 1},
		{name: "formatted", input: `// @Get(path="/{id}")`, annotation: "Get", arguments: 1},
		{name: "qualified", input: `// @security.Authorize(roles=["admin", "operator"])`, annotation: "security.Authorize", arguments: 1},
		{name: "marker", input: `// @Service`, annotation: "Service", arguments: 0},
		{name: "positional", input: `// @Profile("production")`, annotation: "Profile", arguments: 1},
		{name: "typed values", input: `// @Retry(max=3, enabled=true, strategy=exponential)`, annotation: "Retry", arguments: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := ParseComment(test.input, token.Position{Filename: "test.go", Line: 10, Column: 1})
			if err != nil {
				t.Fatalf("ParseComment() error = %v", err)
			}
			if !ok {
				t.Fatal("ParseComment() did not recognize annotation")
			}
			if got.Name != test.annotation {
				t.Fatalf("Name = %q, want %q", got.Name, test.annotation)
			}
			if len(got.Arguments) != test.arguments {
				t.Fatalf("len(Arguments) = %d, want %d", len(got.Arguments), test.arguments)
			}
		})
	}
}

func TestParseCommentTypedValues(t *testing.T) {
	t.Parallel()

	got, ok, err := ParseComment(`// @Example(text="hello", count=-2, enabled=false, roles=["admin", user])`, token.Position{Filename: "test.go", Line: 1, Column: 1})
	if err != nil {
		t.Fatalf("ParseComment() error = %v", err)
	}
	if !ok {
		t.Fatal("annotation was not recognized")
	}

	wantKinds := []annotation.Kind{annotation.KindString, annotation.KindInteger, annotation.KindBoolean, annotation.KindList}
	for index, want := range wantKinds {
		if got.Arguments[index].Value.Kind != want {
			t.Fatalf("argument %d kind = %q, want %q", index, got.Arguments[index].Value.Kind, want)
		}
	}
}

func TestParseCommentIgnoresOrdinaryComment(t *testing.T) {
	t.Parallel()

	_, ok, err := ParseComment("// ordinary comment", token.Position{})
	if err != nil {
		t.Fatalf("ParseComment() error = %v", err)
	}
	if ok {
		t.Fatal("ordinary comment was recognized as annotation")
	}
}

func TestParseCommentRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`// @`,
		`// @Get(path=)`,
		`// @Get(path="/", path="/duplicate")`,
		`// @Get(path="/", "late positional")`,
		`// @Get(path=["a",])`,
		`// @Get(path="/") trailing`,
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			_, ok, err := ParseComment(input, token.Position{Filename: "invalid.go", Line: 7, Column: 1})
			if !ok {
				t.Fatal("invalid annotation was not recognized")
			}
			if err == nil {
				t.Fatal("invalid annotation did not return an error")
			}
		})
	}
}

func FuzzParseComment(f *testing.F) {
	for _, seed := range []string{
		"",
		"// ordinary comment",
		"// @Service",
		`// @Get(path="/{id}")`,
		`// @Retry(max=3, enabled=true, roles=["admin", user])`,
		`// @Get(path=["a",])`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		parsed, recognized, err := ParseComment(input, token.Position{Filename: "fuzz.go", Line: 1, Column: 1})
		if err == nil && recognized && parsed.Name == "" {
			t.Fatal("recognized annotation has an empty name")
		}
		if !recognized && err != nil {
			t.Fatalf("unrecognized comment returned error: %v", err)
		}
	})
}
