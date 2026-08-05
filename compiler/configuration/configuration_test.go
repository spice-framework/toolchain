package configuration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	runtimeconfig "github.com/spice-framework/spice/config"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestBuildCreatesTypedModuleOwnedConfigurationMetadata(t *testing.T) {
	source := `// Package app is a module.
//
// @Module
package app

import "time"

type Level string
type WorkerCount int8
type StringAlias = string

// @Configuration(prefix="server")
type Settings struct {
	Host StringAlias ` + "`spice:\"host,default=localhost\"`" + `
	Debug bool ` + "`spice:\"debug,default=true\"`" + `
	Workers WorkerCount ` + "`spice:\"workers,default=4,env=SERVER_WORKERS\"`" + `
	Timeout time.Duration ` + "`spice:\"timeout,default=5s\"`" + `
	Token Level ` + "`spice:\"token,required,secret,env=SERVER_TOKEN\"`" + `
	Ignored string ` + "`spice:\"-\"`" + `
	private string
}
`
	program, resolution, modules := loadFixture(t, source)
	catalog := Build(program, resolution, modules)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	configTypes := catalog.Types()
	if len(configTypes) != 1 {
		t.Fatalf("len(Types()) = %d, want 1", len(configTypes))
	}
	configType := configTypes[0]
	if configType.Name != "Settings" ||
		configType.Prefix != "server" ||
		configType.Module != "example.com/shop/app" ||
		configType.TypeID != "example.com/shop/app.Settings" ||
		configType.Type == nil {
		t.Fatalf("configuration type = %#v", configType)
	}
	fields := configType.Fields()
	if got, want := fieldNames(fields), []string{"Host", "Debug", "Workers", "Timeout", "Token"}; !slices.Equal(got, want) {
		t.Fatalf("field names = %v, want %v", got, want)
	}
	if got, want := fieldKeys(fields), []string{
		"server.host",
		"server.debug",
		"server.workers",
		"server.timeout",
		"server.token",
	}; !slices.Equal(got, want) {
		t.Fatalf("field keys = %v, want %v", got, want)
	}
	if fields[0].Kind != runtimeconfig.KindString || fields[1].Kind != runtimeconfig.KindBoolean ||
		fields[2].Kind != runtimeconfig.KindInteger || fields[3].Kind != runtimeconfig.KindDuration ||
		fields[4].Kind != runtimeconfig.KindString {
		t.Fatalf("field kinds = %#v", fields)
	}
	if fields[2].Environment != "SERVER_WORKERS" || fields[2].Default != "4" || !fields[2].HasDefault {
		t.Fatalf("Workers metadata = %#v", fields[2])
	}
	if fields[4].Environment != "SERVER_TOKEN" || !fields[4].Required || !fields[4].Secret ||
		fields[4].Module != "example.com/shop/app" {
		t.Fatalf("Token metadata = %#v", fields[4])
	}
	for _, field := range fields {
		if field.Position.Filename == "" || field.PhysicalPosition.Filename == "" || field.Type == nil || field.TypeID == "" {
			t.Fatalf("field lacks typed source metadata: %#v", field)
		}
	}

	configTypes[0].fields[0].Key = "changed"
	fields[0].Key = "also-changed"
	if got := catalog.Types()[0].Fields()[0].Key; got != "server.host" {
		t.Fatalf("catalog exposed mutable field storage: %q", got)
	}
}

func TestBuildReportsInvalidConfigurationDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "alias", body: "type Base struct{}\n\n// @Configuration\ntype Settings = Base\n", want: "aliases are not supported"},
		{name: "non struct", body: "// @Configuration\ntype Settings string\n", want: "must have a struct underlying type"},
		{name: "generic", body: "// @Configuration\ntype Settings[T any] struct{}\n", want: "must not declare type parameters"},
		{name: "private type", body: "// @Configuration\ntype settings struct{}\n", want: "must be exported"},
		{name: "missing tag", body: "// @Configuration\ntype Settings struct { Host string }\n", want: "requires a spice tag"},
		{name: "embedded", body: "type Base struct{}\n\n// @Configuration\ntype Settings struct { Base }\n", want: "is embedded"},
		{name: "tagged private", body: "// @Configuration\ntype Settings struct { private string `spice:\"private\"` }\n", want: "is unexported"},
		{name: "unsupported", body: "// @Configuration\ntype Settings struct { Ratio float64 `spice:\"ratio\"` }\n", want: "unsupported type"},
		{name: "private field type", body: "type level string\n\n// @Configuration\ntype Settings struct { Level level `spice:\"level\"` }\n", want: "uses unexported type"},
		{name: "bad prefix", body: "// @Configuration(prefix=\"Bad\")\ntype Settings struct { Host string `spice:\"host\"` }\n", want: "key \"Bad.host\""},
		{name: "bad key", body: "// @Configuration\ntype Settings struct { Host string `spice:\"Bad\"` }\n", want: "key \"Bad\""},
		{name: "empty key", body: "// @Configuration\ntype Settings struct { Host string `spice:\",required\"` }\n", want: "requires a property key"},
		{name: "empty option", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,\"` }\n", want: "contains an empty option"},
		{name: "unknown option", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,wat\"` }\n", want: "unknown option"},
		{name: "duplicate option", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,secret,secret\"` }\n", want: "repeats option"},
		{name: "required value", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,required=true\"` }\n", want: "does not accept a value"},
		{name: "secret value", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,secret=true\"` }\n", want: "does not accept a value"},
		{name: "default without value marker", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,default\"` }\n", want: "requires a value"},
		{name: "env without value", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,env\"` }\n", want: "requires a value"},
		{name: "empty env", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,env=\"` }\n", want: "requires a value"},
		{name: "invalid env", body: "// @Configuration\ntype Settings struct { Host string `spice:\"host,env=server_host\"` }\n", want: "environment variable"},
		{name: "invalid bool", body: "// @Configuration\ntype Settings struct { Debug bool `spice:\"debug,default=maybe\"` }\n", want: "default is invalid"},
		{name: "invalid duration", body: "import \"time\"\n\n// @Configuration\ntype Settings struct { Timeout time.Duration `spice:\"timeout,default=soon\"` }\n", want: "default is invalid"},
		{name: "int8 overflow", body: "// @Configuration\ntype Settings struct { Workers int8 `spice:\"workers,default=128\"` }\n", want: "outside the range of int8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, resolution, modules := loadFixture(t, "package app\n\n"+test.body)
			diagnostics := Build(program, resolution, modules).Diagnostics()
			if !containsDiagnostic(diagnostics, test.want) {
				t.Fatalf("diagnostics = %v, want text %q", diagnosticStrings(diagnostics), test.want)
			}
		})
	}
}

func TestBuildReportsDuplicateDeclarationsKeysAndEnvironmentVariables(t *testing.T) {
	source := `package app

// @Configuration
// @Configuration
type Repeated struct {
	Ignored string ` + "`spice:\"-\"`" + `
}

// @Configuration
type First struct {
	Host string ` + "`spice:\"server.host,env=SERVER_HOST\"`" + `
}

// @Configuration
type Second struct {
	Host string ` + "`spice:\"server.host\"`" + `
	Token string ` + "`spice:\"server.token,env=SERVER_HOST\"`" + `
}
`
	program, resolution, modules := loadFixture(t, source)
	diagnostics := Build(program, resolution, modules).Diagnostics()
	for _, want := range []string{
		"is declared more than once",
		`configuration property "server.host" duplicates`,
		`configuration environment variable "SERVER_HOST" duplicates`,
	} {
		if !containsDiagnostic(diagnostics, want) {
			t.Fatalf("diagnostics = %v, want text %q", diagnosticStrings(diagnostics), want)
		}
	}
}

func TestBuildDefendsAgainstInvalidPipelineInput(t *testing.T) {
	if diagnostics := Build(nil, resolve.Result{}, modulith.Model{}).Diagnostics(); len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Error(), "requires a loaded program") {
		t.Fatalf("nil diagnostics = %v", diagnostics)
	}

	program, resolution, modules := loadFixture(t, `package app

// @Configuration
type Settings struct {
	Host string `+"`spice:\"host\"`"+`
}
`)
	resolution.Occurrences[0].SymbolID = "missing"
	if diagnostics := Build(program, resolution, modules).Diagnostics(); !containsDiagnostic(diagnostics, "no stable typed symbol") {
		t.Fatalf("missing symbol diagnostics = %v", diagnosticStrings(diagnostics))
	}

	functionProgram, functionResolution, functionModules := loadFixture(t, `package app

// @Configuration
func Settings() {}
`)
	if diagnostics := Build(functionProgram, functionResolution, functionModules).Diagnostics(); !containsDiagnostic(diagnostics, "must target a named struct") {
		t.Fatalf("invalid target diagnostics = %v", diagnosticStrings(diagnostics))
	}
}

func TestConfigurationPrefixRejectsUnvalidatedArguments(t *testing.T) {
	program, resolution, modules := loadFixture(t, `package app

// @Configuration(foo="bar")
type Settings struct{}
`)
	if diagnostics := Build(program, resolution, modules).Diagnostics(); !containsDiagnostic(diagnostics, "accepts only the optional") {
		t.Fatalf("invalid argument diagnostics = %v", diagnosticStrings(diagnostics))
	}

	program, resolution, modules = loadFixture(t, `package app

// @Configuration(prefix="a", other="b")
type Settings struct{}
`)
	if diagnostics := Build(program, resolution, modules).Diagnostics(); !containsDiagnostic(diagnostics, "accepts only the optional") {
		t.Fatalf("excess argument diagnostics = %v", diagnosticStrings(diagnostics))
	}
}

func loadFixture(t *testing.T, source string) (*load.Program, resolve.Result, modulith.Model) {
	t.Helper()
	root := writeModule(t, map[string]string{
		"go.mod":     "module example.com/shop\n\ngo 1.26.0\n",
		"app/app.go": source,
	})
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./app")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	modules := modulith.Build(program, resolution)
	if diagnostics := modules.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("modulith.Build() diagnostics = %v", diagnostics)
	}
	return program, resolution, modules
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fieldNames(fields []Field) []string {
	result := make([]string, len(fields))
	for index, field := range fields {
		result[index] = field.Name
	}
	return result
}

func fieldKeys(fields []Field) []string {
	result := make([]string, len(fields))
	for index, field := range fields {
		result[index] = field.Key
	}
	return result
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}

func containsDiagnostic(diagnostics []Diagnostic, text string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, text) {
			return true
		}
	}
	return false
}
