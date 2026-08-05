package application

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCarriesCacheBoundariesInApplicationIR(t *testing.T) {
	root := writeCacheApplicationModule(t, `package app

import "context"

type Request struct {
	ID string `+"`path:\"id\"`"+`
}

type Response struct {
	ID string `+"`json:\"id\"`"+`
}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("/products/{id}")
// @cache.Cacheable(name="products.by-id")
func (*API) Product(context.Context, Request) (Response, error) {
	panic("cacheable route methods must never execute during analysis")
}

// @Application
func Application(*API) {}
`)
	program, resolution := loadAndResolve(t, root, "./...")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	caches := model.Caches()
	if len(caches) != 1 ||
		caches[0].CacheName != "products.by-id" ||
		caches[0].RouteName != "Product" ||
		caches[0].Module != "example.com/cacheir/app" ||
		caches[0].KeyTypeID != "example.com/cacheir/app.Request" ||
		caches[0].ValueTypeID != "example.com/cacheir/app.Response" {
		t.Fatalf("Caches() = %#v", caches)
	}
	caches[0].CacheName = "changed"
	if model.Caches()[0].CacheName == "changed" {
		t.Fatal("Caches() returned mutable model storage")
	}
}

func TestBuildReportsCacheDiagnosticsAtCacheStage(t *testing.T) {
	root := writeCacheApplicationModule(t, `package app

import "context"

type Request struct {
	ID string `+"`path:\"id\"`"+`
}

type Response struct{}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("/first/{id}")
// @cache.Cacheable(name="shared")
func (*API) First(context.Context, Request) (Response, error) {
	return Response{}, nil
}

// @Get("/second/{id}")
// @cache.Cacheable(name="shared")
func (*API) Second(context.Context, Request) (Response, error) {
	return Response{}, nil
}

// @Application
func Application(*API) {}
`)
	program, resolution := loadAndResolve(t, root, "./...")
	diagnostics := Build(program, resolution).Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != StageCache ||
		!strings.Contains(diagnostics[0].Message, "already owned") {
		t.Fatalf("Build() diagnostics = %#v", diagnostics)
	}
}

func writeCacheApplicationModule(t *testing.T, source string) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod": "module example.com/cacheir\n\ngo 1.26.0\n\n" +
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(applicationRepositoryRoot(t)) + "\n",
		"app/application.go": source,
	})
}
