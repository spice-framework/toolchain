package generate

import (
	"strings"
	"testing"
)

func TestRenderFailsClosedForCompiledCacheBoundary(t *testing.T) {
	root := writeModule(t, "example.com/cachegenerated", map[string]string{
		"app/application.go": `package app

import "context"

type Request struct {
	ID string ` + "`path:\"id\"`" + `
}

type Response struct {
	ID string ` + "`json:\"id\"`" + `
}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("/products/{id}")
// @cache.Cacheable(name="products.by-id")
func (*API) Product(context.Context, Request) (Response, error) {
	panic("cacheable route methods must not execute during analysis")
}

// @Application
func CacheGenerated(*API) {}
`,
	})
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"DefaultTarget() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
	_, diagnostics = Render(program, model, applicationTarget, target)
	if len(diagnostics) != 1 ||
		!strings.Contains(
			diagnostics[0].Message,
			"requires generated cache support",
		) {
		t.Fatalf(
			"Render() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
}
