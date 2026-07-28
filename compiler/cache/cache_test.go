package cache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/controller"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/internal/testannotation"
)

func TestCatalogCompilesCacheableReadRoutes(t *testing.T) {
	source := cacheSource(`
// @Get("/products/{id}")
// @cache.Cacheable(name="products.by-id")
func (*API) Product(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	panic("cacheable route methods must not execute during analysis")
}

// @Get("/inventory/{id}")
// @cache.Cacheable(name="inventory-by-id")
func (*API) Inventory(
	context.Context,
	InventoryRequest,
) (ProductResponse, error) {
	panic("cacheable route methods must not execute during analysis")
}
`)
	program, resolution, controllers := loadCatalogs(t, source)
	catalog := Build(program, resolution, controllers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	boundaries := catalog.Boundaries()
	if len(boundaries) != 2 ||
		boundaries[0].RouteID >= boundaries[1].RouteID {
		t.Fatalf("Boundaries() = %#v", boundaries)
	}
	product := boundaryByRoute(boundaries, "Product")
	if product == nil ||
		product.CacheName != "products.by-id" ||
		product.Module != "example.com/cacheapplication/api" ||
		product.KeyTypeID !=
			"example.com/cacheapplication/api.ProductRequest" ||
		product.ValueTypeID !=
			"example.com/cacheapplication/api.ProductResponse" {
		t.Fatalf("Product boundary = %#v", product)
	}
	inventory := boundaryByRoute(boundaries, "Inventory")
	if inventory == nil || inventory.CacheName != "inventory-by-id" {
		t.Fatalf("Inventory boundary = %#v", inventory)
	}
	boundaries[0].CacheName = "changed"
	if catalog.Boundaries()[0].CacheName == "changed" {
		t.Fatal("Boundaries() exposed catalog storage")
	}
}

func TestCatalogRejectsUnsafeCacheableRoutesDeterministically(t *testing.T) {
	source := cacheSource(`
// @Post("/post/{id}")
// @cache.Cacheable(name="post")
func (*API) Post(context.Context, ProductRequest) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/raw")
// @cache.Cacheable(name="raw")
func (*API) Raw(http.ResponseWriter, *http.Request) {}

// @Get("/no-content/{id}")
// @cache.Cacheable(name="no-content")
func (*API) NoContent(
	context.Context,
	ProductRequest,
) (web.NoContent, error) {
	return web.NoContent{}, nil
}

// @Get("/incomparable/{id}")
// @cache.Cacheable(name="incomparable")
func (*API) Incomparable(
	context.Context,
	IncomparableRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/transaction/{id}")
// @cache.Cacheable(name="transaction")
func (*API) Transaction(
	context.Context,
	data.Executor,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/protected/{id}")
// @security.Authorize(authenticated=true)
// @cache.Cacheable(name="protected")
func (*API) Protected(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/invalid-name/{id}")
// @cache.Cacheable(name="Not Valid")
func (*API) InvalidName(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/duplicate-one/{id}")
// @cache.Cacheable(name="duplicate")
func (*API) DuplicateOne(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/duplicate-two/{id}")
// @cache.Cacheable(name="duplicate")
func (*API) DuplicateTwo(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/environment-one/{id}")
// @cache.Cacheable(name="environment-one")
func (*API) EnvironmentOne(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @Get("/environment-two/{id}")
// @cache.Cacheable(name="environment.one")
func (*API) EnvironmentTwo(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// @cache.Cacheable(name="not-route")
func (*API) NotRoute(context.Context, ProductRequest) (ProductResponse, error) {
	return ProductResponse{}, nil
}
`)
	program, resolution, controllers := loadCatalogs(t, source)
	var baseline []string
	for run := range 4 {
		if run%2 == 1 {
			slices.Reverse(resolution.Occurrences)
		}
		diagnostics := Build(
			program,
			resolution,
			controllers,
		).Diagnostics()
		got := diagnosticStrings(diagnostics)
		if len(got) != 10 {
			t.Fatalf(
				"run %d diagnostic count = %d, want 10:\n%s",
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
		"must use @Get",
		"must use a typed request and response",
		"must return a response value",
		"is not comparable",
		"cannot also own a transaction boundary",
		"cannot use authorization",
		"must match",
		"is already owned by route",
		"same generated environment prefix",
		"must declare exactly one valid typed @Get route",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogRejectsRepeatedAnnotationAndInvalidInternalInput(t *testing.T) {
	source := cacheSource(`
// @Get("/products/{id}")
// @cache.Cacheable(name="products")
func (*API) Product(
	context.Context,
	ProductRequest,
) (ProductResponse, error) {
	return ProductResponse{}, nil
}
`)
	program, resolution, controllers := loadCatalogs(t, source)
	for _, occurrence := range resolution.Occurrences {
		if occurrence.Annotation.Name != Annotation {
			continue
		}
		repeated := occurrence
		repeated.PhysicalOffset++
		resolution.Occurrences = append(resolution.Occurrences, repeated)
		break
	}
	diagnostics := Build(program, resolution, controllers).Diagnostics()
	if len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Message, "is repeated") {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}

	if diagnostics := Build(
		nil,
		resolve.Result{},
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

func TestCacheNameFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		annotation annotation.Annotation
		want       string
	}{
		{
			name:       "missing",
			annotation: annotation.Annotation{Name: Annotation},
			want:       "requires exactly one",
		},
		{
			name: "positional",
			annotation: annotation.Annotation{
				Name: Annotation,
				Arguments: []annotation.Argument{{
					Value: annotation.Value{
						Kind:   annotation.KindString,
						String: "products",
					},
				}},
			},
			want: "requires exactly one",
		},
		{
			name: "wrong type",
			annotation: annotation.Annotation{
				Name: Annotation,
				Arguments: []annotation.Argument{{
					Name: "name",
					Value: annotation.Value{
						Kind:    annotation.KindInteger,
						Integer: 1,
					},
				}},
			},
			want: "requires exactly one",
		},
		{
			name: "unsafe",
			annotation: annotation.Annotation{
				Name: Annotation,
				Arguments: []annotation.Argument{{
					Name: "name",
					Value: annotation.Value{
						Kind:   annotation.KindString,
						String: "Products_By_ID",
					},
				}},
			},
			want: "must match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, problem := cacheName(test.annotation)
			if !strings.Contains(problem, test.want) {
				t.Fatalf("cacheName() problem = %q, want %q", problem, test.want)
			}
		})
	}
}

func boundaryByRoute(
	boundaries []Boundary,
	name string,
) *Boundary {
	for index := range boundaries {
		if boundaries[index].RouteName == name {
			return &boundaries[index]
		}
	}
	return nil
}

func cacheSource(routes string) string {
	return `// Package api owns cacheable HTTP reads.
//
// @Module
package api

import (
	"context"
	"net/http"

	"github.com/StevenBuglione/spice/data"
	"github.com/StevenBuglione/spice/web"
)

var (
	_ = http.MethodGet
	_ data.Executor
	_ web.NoContent
)

type ProductRequest struct {
	ID string ` + "`path:\"id\"`" + `
}

type InventoryRequest = ProductRequest

type IncomparableRequest struct {
	ID string ` + "`path:\"id\"`" + `
	private []string
}

type ProductResponse struct {
	ID string ` + "`json:\"id\"`" + `
}

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
	[]controller.Controller,
) {
	t.Helper()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/cacheapplication\n\ngo 1.26.0\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " +
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
	return program, resolution, controllerCatalog.Controllers()
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
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
