package controller

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func TestBuildCreatesTypedControllerRouteMetadata(t *testing.T) {
	source := `// Package api owns HTTP delivery.
//
// @Module
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/StevenBuglione/spice/web"
)

type UserID string
type Limit int16

type UserResponse struct {
	ID string ` + "`json:\"id\"`" + `
}

type CreateBody struct {
	Name string ` + "`json:\"name\"`" + `
}

type GetUserRequest struct {
	ID UserID ` + "`path:\"id\"`" + `
	Verbose bool ` + "`query:\"verbose\"`" + `
	Limit Limit ` + "`query:\"limit,required\"`" + `
	Timeout time.Duration ` + "`header:\"X-Timeout\"`" + `
	Ignored string ` + "`web:\"-\"`" + `
	private string
}

type CreateUserRequest struct {
	Parent UserID ` + "`path:\"parent\"`" + `
	Body CreateBody ` + "`body:\"\"`" + `
}

// @Controller(prefix="/users")
type Users struct{}

// @Bean
func NewUsers() *Users { return &Users{} }

// @Get(path="/{id}")
func (*Users) GetUser(context.Context, GetUserRequest) (UserResponse, error) {
	return UserResponse{}, nil
}

// @Post("/{parent}")
func (*Users) CreateUser(context.Context, CreateUserRequest) (web.NoContent, error) {
	return web.NoContent{}, nil
}

// @Get("/raw")
func (*Users) Raw(http.ResponseWriter, *http.Request) {}
`
	catalog := buildCatalog(t, source)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	controllers := catalog.Controllers()
	if len(controllers) != 1 {
		t.Fatalf("Controllers() = %#v", controllers)
	}
	controller := controllers[0]
	if controller.Name != "Users" || controller.Prefix != "/users" ||
		controller.Module != "example.com/webapp/api" || controller.ProviderID == "" {
		t.Fatalf("controller = %#v", controller)
	}
	routes := controller.Routes()
	if got, want := routeSummaries(routes), []string{
		"GET /users/raw Raw",
		"GET /users/{id} GetUser",
		"POST /users/{parent} CreateUser",
	}; !slices.Equal(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	if !routes[0].Raw || routes[0].Request != nil || routes[0].Response != nil {
		t.Fatalf("raw route = %#v", routes[0])
	}
	get := routes[1]
	if get.Raw || get.NoContent || get.RequestTypeID != "example.com/webapp/api.GetUserRequest" ||
		get.ResponseTypeID != "example.com/webapp/api.UserResponse" {
		t.Fatalf("GET route = %#v", get)
	}
	bindings := get.Bindings()
	if got, want := bindingSummaries(bindings), []string{
		"path:id:string:true",
		"query:verbose:boolean:false",
		"query:limit:integer:true",
		"header:X-Timeout:duration:false",
	}; !slices.Equal(got, want) {
		t.Fatalf("bindings = %v, want %v", got, want)
	}
	if !routes[2].NoContent || len(routes[2].Bindings()) != 2 ||
		routes[2].Bindings()[1].Location != Body {
		t.Fatalf("POST route = %#v", routes[2])
	}
	for _, route := range routes {
		if route.ProviderID != controller.ProviderID || route.Module != controller.Module ||
			route.Position.Filename == "" || route.PhysicalPosition.Filename == "" {
			t.Fatalf("route lacks ownership or position: %#v", route)
		}
	}

	routes[1].bindings[0].Name = "changed"
	bindings[0].Name = "also-changed"
	if got := catalog.Controllers()[0].Routes()[1].Bindings()[0].Name; got != "id" {
		t.Fatalf("catalog exposed mutable bindings: %q", got)
	}
}

func TestBuildReportsInvalidControllerAndRouteContracts(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "controller alias", body: "type Base struct{}\n// @Controller\ntype API = Base\n", want: "defined named struct"},
		{name: "controller non struct", body: "// @Controller\ntype API string\n", want: "struct underlying type"},
		{name: "controller generic", body: "// @Controller\ntype API[T any] struct{}\n", want: "must not declare type parameters"},
		{name: "controller private", body: "// @Controller\ntype api struct{}\n", want: "must be exported"},
		{name: "bad prefix", body: "// @Controller(prefix=\"users/\")\ntype API struct{}\n", want: "prefix"},
		{name: "empty controller", body: "// @Controller\ntype API struct{}\n", want: "declares no valid"},
		{name: "orphan", body: "type API struct{}\n// @Get(\"/\")\nfunc (*API) Get() {}\n", want: "not owned by an @Controller"},
		{name: "missing provider", body: typedRouteSource("", "Request struct{}", "/"), want: "has no provider"},
		{name: "private method", body: typedRouteSource("func NewAPI() *API { return &API{} }\n// @Bean\nfunc APIProvider() *API { return NewAPI() }", "Request struct{}", "/") + "\n", want: "must be exported"},
		{name: "bad path relative", body: validController("// @Get(\"relative\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }", "type Request struct{}\ntype Response struct{}"), want: "route paths must be absolute"},
		{name: "bad wildcard", body: validController("// @Get(\"/{id...}\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }", "type Request struct{}\ntype Response struct{}"), want: "simple {name}"},
		{name: "typed arity", body: validController("// @Get(\"/\")\nfunc (*API) Get() {}", ""), want: "typed route"},
		{name: "request pointer", body: validController("// @Get(\"/\")\nfunc (*API) Get(context.Context, *Request) (Response, error) { return Response{}, nil }", "type Request struct{}\ntype Response struct{}"), want: "exported named struct value"},
		{name: "request non struct", body: validController("// @Get(\"/\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }", "type Request string\ntype Response struct{}"), want: "exported named struct value"},
		{name: "missing field tag", body: validTypedRoute("type Request struct { Value string }\n"), want: "requires exactly one"},
		{name: "multiple tags", body: validTypedRoute("type Request struct { Value string `path:\"value\" query:\"value\"` }\n"), want: "requires exactly one"},
		{name: "tagged private", body: validTypedRoute("type Request struct { value string `query:\"value\"` }\n"), want: "is unexported"},
		{name: "embedded", body: validTypedRoute("type Embedded struct{}\ntype Request struct { Embedded }\n"), want: "is embedded"},
		{name: "conflicting ignore", body: validTypedRoute("type Request struct { Value string `web:\"-\" query:\"value\"` }\n"), want: "combines web"},
		{name: "bad body tag", body: postTypedRoute("type Request struct { Value string `body:\"value\"` }\n"), want: "body tag must be empty"},
		{name: "two bodies", body: postTypedRoute("type Request struct { One string `body:\"\"`; Two string `body:\"\"` }\n"), want: "only one body"},
		{name: "unsupported scalar", body: validTypedRoute("type Request struct { Value uint `query:\"value\"` }\n"), want: "not a supported request scalar"},
		{name: "invalid path tag", body: typedWildcardRoute("type Request struct { ID string `path:\"id,required\"` }\n"), want: "invalid path tag"},
		{name: "duplicate query", body: validTypedRoute("type Request struct { One string `query:\"value\"`; Two string `query:\"value\"` }\n"), want: "declared more than once"},
		{name: "missing wildcard binding", body: typedWildcardRoute("type Request struct{}\n"), want: "has no matching"},
		{name: "extra path binding", body: validTypedRoute("type Request struct { ID string `path:\"id\"` }\n"), want: "has no matching route wildcard"},
		{name: "get body", body: validTypedRoute("type Request struct { Value string `body:\"\"` }\n"), want: "must not bind a request body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := buildCatalog(t, "package api\n\nimport \"context\"\n\nvar _ = context.Background\n\n"+test.body)
			if !containsDiagnostic(catalog.Diagnostics(), test.want) {
				t.Fatalf("diagnostics = %v, want %q", diagnosticStrings(catalog.Diagnostics()), test.want)
			}
		})
	}
}

func TestBuildReportsDuplicateRoutePatterns(t *testing.T) {
	source := `package api

import "context"

type Request struct{}
type Response struct{}

// @Controller(prefix="/api")
type First struct{}

// @Bean
func NewFirst() *First { return &First{} }

// @Get("/items")
func (*First) Get(context.Context, Request) (Response, error) { return Response{}, nil }

// @Controller(prefix="/api")
type Second struct{}

// @Bean
func NewSecond() *Second { return &Second{} }

// @Get("/items")
func (*Second) Get(context.Context, Request) (Response, error) { return Response{}, nil }
`
	catalog := buildCatalog(t, source)
	if !containsDiagnostic(catalog.Diagnostics(), "route GET /api/items conflicts") {
		t.Fatalf("diagnostics = %v", diagnosticStrings(catalog.Diagnostics()))
	}
}

func TestBuildDefendsAgainstInvalidPipelineInput(t *testing.T) {
	if diagnostics := Build(nil, resolve.Result{}, provider.Catalog{}, modulith.Model{}).Diagnostics(); len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Error(), "loaded program") {
		t.Fatalf("nil diagnostics = %v", diagnostics)
	}
	program, resolution, providers, modules := loadPipeline(
		t,
		"package api\n\nimport \"context\"\n\n"+validController(
			"// @Get(\"/\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }",
			"type Request struct{}\ntype Response struct{}",
		),
	)
	resolution.Occurrences[0].SymbolID = "missing"
	if diagnostics := Build(program, resolution, providers, modules).Diagnostics(); !containsDiagnostic(diagnostics, "no stable typed symbol") {
		t.Fatalf("missing diagnostics = %v", diagnosticStrings(diagnostics))
	}

	diagnostic := Diagnostic{Message: "broken"}
	if diagnostic.Error() != "<unknown>:1:1: broken" {
		t.Fatalf("Diagnostic.Error() = %q", diagnostic.Error())
	}
}

func validController(route, declarations string) string {
	return declarations + `
// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

` + route + "\n"
}

func validTypedRoute(request string) string {
	return validController(
		"// @Get(\"/\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }",
		request+"\ntype Response struct{}",
	)
}

func postTypedRoute(request string) string {
	return validController(
		"// @Post(\"/\")\nfunc (*API) Post(context.Context, Request) (Response, error) { return Response{}, nil }",
		request+"\ntype Response struct{}",
	)
}

func typedWildcardRoute(request string) string {
	return validController(
		"// @Get(\"/{id}\")\nfunc (*API) Get(context.Context, Request) (Response, error) { return Response{}, nil }",
		request+"\ntype Response struct{}",
	)
}

func typedRouteSource(providerSource, requestDeclaration, path string) string {
	method := "Get"
	if providerSource == "" {
		providerSource = ""
	} else {
		method = "get"
	}
	return `type ` + requestDeclaration + `
type Response struct{}

// @Controller
type API struct{}

` + providerSource + `

// @Get("` + path + `")
func (*API) ` + method + `(context.Context, Request) (Response, error) { return Response{}, nil }
`
}

func buildCatalog(t *testing.T, source string) Catalog {
	t.Helper()
	program, resolution, providers, modules := loadPipeline(t, source)
	return Build(program, resolution, providers, modules)
}

func loadPipeline(
	t *testing.T,
	source string,
) (*load.Program, resolve.Result, provider.Catalog, modulith.Model) {
	t.Helper()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/webapp\n\ngo 1.26.0\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " + filepath.ToSlash(repositoryRoot(t)) + "\n",
		"api/api.go": source,
	})
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	modules := modulith.Build(program, resolution)
	if diagnostics := modules.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("modulith.Build() diagnostics = %v", diagnostics)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider.Build() diagnostics = %v", diagnostics)
	}
	return program, resolution, providers, modules
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

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func routeSummaries(routes []Route) []string {
	result := make([]string, len(routes))
	for index, route := range routes {
		result[index] = route.HTTPMethod + " " + route.Path + " " + route.Name
	}
	return result
}

func bindingSummaries(bindings []Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = string(binding.Location) + ":" + binding.Name + ":" +
			string(binding.Kind) + ":" + boolString(binding.Required)
	}
	return result
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
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
