package transaction

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/controller"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

func TestCatalogCompilesTransactionalRoutes(t *testing.T) {
	source := transactionalSource(`
// @Post("/orders/{id}")
// @data.Transactional(readOnly=true, isolation="serializable")
func (*API) Place(context.Context, Queries, Request) (Response, error) {
	panic("transactional route methods must not execute during analysis")
}

// @Get("/orders/{id}")
// @data.Transactional
func (*API) Find(context.Context, data.Executor, Request) (Response, error) {
	panic("transactional route methods must not execute during analysis")
}
`)
	program, resolution, providers, controllers := loadCatalogs(t, source)
	catalog := Build(program, resolution, providers, controllers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	boundaries := catalog.Boundaries()
	if len(boundaries) != 2 ||
		boundaries[0].RouteID >= boundaries[1].RouteID {
		t.Fatalf("Boundaries() = %#v", boundaries)
	}
	place := boundaryByRoute(boundaries, "Place")
	if place == nil ||
		place.Isolation != sql.LevelSerializable ||
		!place.ReadOnly ||
		place.Module != "example.com/transactions/api" ||
		place.ManagerProviderID == "" {
		t.Fatalf("Place boundary = %#v", place)
	}
	find := boundaryByRoute(boundaries, "Find")
	if find == nil ||
		find.Isolation != sql.LevelDefault ||
		find.ReadOnly {
		t.Fatalf("Find boundary = %#v", find)
	}
	boundaries[0].Module = "changed"
	if catalog.Boundaries()[0].Module == "changed" {
		t.Fatal("Boundaries() exposed catalog storage")
	}
}

func TestCatalogRejectsInvalidTransactionalRoutesDeterministically(t *testing.T) {
	source := transactionalSource(`
// @Post("/normal/{id}")
// @data.Transactional
func (*API) Normal(context.Context, Request) (Response, error) {
	return Response{}, nil
}

// @Post("/unannotated/{id}")
func (*API) Unannotated(context.Context, data.Executor, Request) (Response, error) {
	return Response{}, nil
}

// @Post("/duplicate/{id}")
// @data.Transactional
// @data.Transactional
func (*API) Duplicate(context.Context, data.Executor, Request) (Response, error) {
	return Response{}, nil
}

// @data.Transactional
func (*API) NotRoute(context.Context, data.Executor, Request) (Response, error) {
	return Response{}, nil
}
`)
	program, resolution, providers, controllers := loadCatalogs(t, source)
	var baseline []string
	for run := range 4 {
		if run%2 == 1 {
			slices.Reverse(resolution.Occurrences)
		}
		diagnostics := Build(
			program,
			resolution,
			providers,
			controllers,
		).Diagnostics()
		got := diagnosticStrings(diagnostics)
		if len(got) != 4 {
			t.Fatalf(
				"run %d diagnostic count = %d, want 4:\n%s",
				run,
				len(got),
				strings.Join(got, "\n"),
			)
		}
		if run == 0 {
			baseline = got
		} else if !slices.Equal(got, baseline) {
			t.Fatalf(
				"run %d diagnostics changed:\nfirst=%v\nnext=%v",
				run,
				baseline,
				got,
			)
		}
	}
	joined := strings.Join(baseline, "\n")
	for _, expected := range []string{
		"must accept exact data.Executor as parameter 1",
		"accepts data.Executor but does not declare @data.Transactional",
		"is repeated",
		"must declare exactly one valid typed @Get or @Post route",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogRejectsMissingManagerAndInvalidOptions(t *testing.T) {
	tests := []struct {
		name       string
		providers  string
		annotation string
		want       string
	}{
		{
			name:       "missing manager",
			providers:  databaseProvider,
			annotation: "@data.Transactional",
			want:       "requires exactly one @Bean provider for *data.Manager",
		},
		{
			name:       "positional",
			providers:  transactionProviders,
			annotation: `@data.Transactional("serializable")`,
			want:       "accepts only named arguments",
		},
		{
			name:       "isolation type",
			providers:  transactionProviders,
			annotation: `@data.Transactional(isolation=true)`,
			want:       `argument "isolation" requires one of`,
		},
		{
			name:       "isolation value",
			providers:  transactionProviders,
			annotation: `@data.Transactional(isolation="eventual")`,
			want:       `argument "isolation" requires one of`,
		},
		{
			name:       "read only",
			providers:  transactionProviders,
			annotation: `@data.Transactional(readOnly="yes")`,
			want:       `argument "readOnly" requires boolean`,
		},
		{
			name:       "unknown",
			providers:  transactionProviders,
			annotation: `@data.Transactional(timeout="1s")`,
			want:       `does not define argument "timeout"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := transactionImports + test.providers + `
type Request struct{}
type Response struct{}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Post("/")
// ` + test.annotation + `
func (*API) Execute(context.Context, data.Executor, Request) (Response, error) {
	return Response{}, nil
}
`
			program, resolution, providers, controllers := loadCatalogs(
				t,
				source,
			)
			diagnostics := Build(
				program,
				resolution,
				providers,
				controllers,
			).Diagnostics()
			if len(diagnostics) != 1 ||
				!strings.Contains(diagnostics[0].Message, test.want) {
				t.Fatalf(
					"Diagnostics() = %v, want containing %q",
					diagnosticStrings(diagnostics),
					test.want,
				)
			}
		})
	}
	duplicateSource := transactionImports + transactionProviders + `
type Request struct{}
type Response struct{}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Post("/")
// @data.Transactional(readOnly=true)
func (*API) Execute(context.Context, data.Executor, Request) (Response, error) {
	return Response{}, nil
}
`
	program, resolution, providers, controllers := loadCatalogs(
		t,
		duplicateSource,
	)
	for index := range resolution.Occurrences {
		occurrence := &resolution.Occurrences[index]
		if occurrence.Annotation.Name == Annotation {
			occurrence.Annotation.Arguments = append(
				occurrence.Annotation.Arguments,
				occurrence.Annotation.Arguments[0],
			)
		}
	}
	diagnostics := Build(
		program,
		resolution,
		providers,
		controllers,
	).Diagnostics()
	if len(diagnostics) != 1 ||
		!strings.Contains(
			diagnostics[0].Message,
			`assigns argument "readOnly" more than once`,
		) {
		t.Fatalf("duplicate argument diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if diagnostics := Build(
		nil,
		resolve.Result{},
		provider.Catalog{},
		nil,
	).Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Kind != "internal" {
		t.Fatalf("Build(nil) diagnostics = %#v", diagnostics)
	}
	diagnostic := Diagnostic{Message: "broken"}
	if diagnostic.Error() != "<unknown>:1:1: broken" {
		t.Fatalf("Diagnostic.Error() = %q", diagnostic.Error())
	}
}

const transactionImports = `// Package api owns transaction boundaries.
//
// @Module
package api

import (
	"context"
	"database/sql"

	"github.com/spice-framework/spice/data"
)

`

const databaseProvider = `// @Bean
func Database() *sql.DB {
	panic("provider bodies must not execute during analysis")
}

`

const transactionProviders = databaseProvider + `// @Bean
func Transactions(database *sql.DB) (*data.Manager, error) {
	panic("provider bodies must not execute during analysis")
}

`

func transactionalSource(routes string) string {
	return transactionImports + transactionProviders + `
type Queries = data.Executor
type Request struct {
	ID string ` + "`path:\"id\"`" + `
}
type Response struct{}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API {
	panic("provider bodies must not execute during analysis")
}
` + routes
}

func loadCatalogs(
	t *testing.T,
	source string,
) (
	*load.Program,
	resolve.Result,
	provider.Catalog,
	[]controller.Controller,
) {
	t.Helper()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/transactions\n\ngo 1.26.0\n\n" +
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(repositoryRoot(t)) + "\n",
		"api/api.go": source,
	})
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./...",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	modules := modulith.Build(program, resolution)
	if diagnostics := modules.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("module diagnostics = %v", diagnostics)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider diagnostics = %v", diagnostics)
	}
	controllerCatalog := controller.Build(
		program,
		resolution,
		providers,
		modules,
	)
	if diagnostics := controllerCatalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("controller diagnostics = %v", diagnostics)
	}
	return program, resolution, providers, controllerCatalog.Controllers()
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
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(files[path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return testsupport.CoreDirectory(t)
}

func boundaryByRoute(boundaries []Boundary, name string) *Boundary {
	for index := range boundaries {
		if boundaries[index].RouteName == name {
			return &boundaries[index]
		}
	}
	return nil
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
