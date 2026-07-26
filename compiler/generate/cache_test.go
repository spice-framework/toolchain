package generate

import (
	"bytes"
	"testing"
)

func TestRenderGeneratesExecutableCacheableHTTPReads(t *testing.T) {
	root := cacheGenerationFixture(t, "products.by-id")
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"DefaultTarget() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}

	var firstSource, firstManifest []byte
	for iteration := range 10 {
		plan, renderDiagnostics := Render(
			program,
			model,
			applicationTarget,
			target,
		)
		if len(renderDiagnostics) != 0 {
			t.Fatalf(
				"Render() diagnostics = %v",
				generationDiagnosticStrings(renderDiagnostics),
			)
		}
		source := generatedGoContent(t, plan)
		manifest := plan.ManifestContent()
		if iteration == 0 {
			firstSource = source
			firstManifest = manifest
			writePlan(t, root, plan)
			continue
		}
		if !bytes.Equal(firstSource, source) ||
			!bytes.Equal(firstManifest, manifest) {
			t.Fatalf("cache generation changed bytes at iteration %d", iteration)
		}
	}

	for _, required := range []string{
		`spicecache "github.com/StevenBuglione/spice/cache"`,
		`Key:         "spice.cache.products.by-id.capacity"`,
		`Environment: "SPICE_CACHE_PRODUCTS_BY_ID_CAPACITY"`,
		`Default:     "256"`,
		`Key:         "spice.cache.products.by-id.ttl"`,
		`Environment: "SPICE_CACHE_PRODUCTS_BY_ID_TTL"`,
		`Default:     "5m"`,
		"CacheClock",
		"CacheObservers",
		`configurationSnapshot.Integer("spice.cache.products.by-id.capacity")`,
		`configurationSnapshot.Duration("spice.cache.products.by-id.ttl")`,
		"spicecache.NewMemory[api.Request, api.Response](",
		`ID:     "products.by-id"`,
		`Module: "example.com/cachegenerated/api"`,
		"options.CacheClock",
		"options.CacheObservers...",
		"generatedCache0.Get(httpRequest.Context(), requestValue)",
		"provider0.Product(httpRequest.Context(), requestValue)",
		"generatedCache0.Put(httpRequest.Context(), requestValue, responseValue, generatedCache0TTL)",
	} {
		if !bytes.Contains(firstSource, []byte(required)) {
			t.Fatalf("generated cache source missing %q:\n%s", required, firstSource)
		}
	}
	assertOrdered(
		t,
		string(firstSource),
		"api.NewAPI()",
		"spicecache.NewMemory[api.Request, api.Response](",
		"generatedCache0.Get(httpRequest.Context(), requestValue)",
		"provider0.Product(httpRequest.Context(), requestValue)",
		"generatedCache0.Put(httpRequest.Context(), requestValue, responseValue, generatedCache0TTL)",
	)

	writeTestFile(
		t,
		root,
		"internal/spicegen/cachegenerated/zz_spice_cache_test.go",
		generatedCacheTest,
	)
	runGoTest(t, root, "./internal/spicegen/cachegenerated")
}

func TestCacheMetadataChangesManifestInputHash(t *testing.T) {
	first := renderCacheFixture(
		t,
		cacheGenerationFixture(t, "products.by-id"),
	)
	second := renderCacheFixture(
		t,
		cacheGenerationFixture(t, "products-by-id"),
	)
	if first.Manifest().InputSHA256 == second.Manifest().InputSHA256 {
		t.Fatal("cache identity changed generated behavior without changing input hash")
	}
}

func TestRenderRejectsGeneratedCacheConfigurationCollisions(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{
			name:  "key",
			field: "Capacity int `spice:\"capacity\"`",
			want:  `framework-owned key "spice.cache.products.by-id.capacity"`,
		},
		{
			name: "environment",
			field: "Capacity int " +
				"`spice:\"custom-capacity,env=SPICE_CACHE_PRODUCTS_BY_ID_CAPACITY\"`",
			want: "framework-owned environment variable " +
				`"SPICE_CACHE_PRODUCTS_BY_ID_CAPACITY"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(
				t,
				"example.com/cachecollision",
				map[string]string{
					"app/application.go": `package app

import "context"

// @Configuration(prefix="spice.cache.products.by-id")
type Settings struct {
	` + test.field + `
}

type Request struct {
	ID string ` + "`path:\"id\"`" + `
}

type Response struct{}

// @Controller
type API struct{}

// @Bean
func NewAPI() *API { return &API{} }

// @Get("/products/{id}")
// @cache.Cacheable(name="products.by-id")
func (*API) Product(context.Context, Request) (Response, error) {
	return Response{}, nil
}

// @Application
func CacheCollision(*API, Settings) {}
`,
				},
			)
			program, model, applicationTarget := buildApplication(
				t,
				root,
				"./...",
			)
			target, diagnostics := DefaultTarget(program, applicationTarget)
			if len(diagnostics) != 0 {
				t.Fatalf(
					"DefaultTarget() diagnostics = %v",
					generationDiagnosticStrings(diagnostics),
				)
			}
			_, diagnostics = Render(
				program,
				model,
				applicationTarget,
				target,
			)
			if len(diagnostics) != 1 ||
				!containsGenerationDiagnostic(diagnostics, test.want) {
				t.Fatalf(
					"Render() diagnostics = %v",
					generationDiagnosticStrings(diagnostics),
				)
			}
		})
	}
}

func renderCacheFixture(t *testing.T, root string) Plan {
	t.Helper()
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"DefaultTarget() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
	plan, diagnostics := Render(program, model, applicationTarget, target)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"Render() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
	return plan
}

func cacheGenerationFixture(t *testing.T, cacheName string) string {
	t.Helper()
	return writeModule(t, "example.com/cachegenerated", map[string]string{
		"api/api.go": `// Package api owns cached product reads.
//
// @Module
package api

import (
	"context"
	"errors"
	"sync"

	"github.com/StevenBuglione/spice/lifecycle"
)

var state struct {
	sync.Mutex
	calls map[string]int
	trace []string
}

type Request struct {
	ID string ` + "`path:\"id\"`" + `
}

type Response struct {
	ID    string ` + "`json:\"id\"`" + `
	Calls int    ` + "`json:\"calls\"`" + `
}

// @Controller
type API struct{}

// @Bean
func NewAPI() (*API, lifecycle.Cleanup) {
	appendTrace("construct api")
	return &API{}, func(context.Context) error {
		appendTrace("cleanup api")
		return nil
	}
}

// @Get("/products/{id}")
// @cache.Cacheable(name="` + cacheName + `")
func (*API) Product(ctx context.Context, request Request) (Response, error) {
	if cause := context.Cause(ctx); cause != nil {
		return Response{}, cause
	}
	state.Lock()
	state.calls[request.ID]++
	calls := state.calls[request.ID]
	state.Unlock()
	if request.ID == "fail" {
		return Response{}, errors.New("product failed")
	}
	return Response{ID: request.ID, Calls: calls}, nil
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.calls = make(map[string]int)
	state.trace = nil
}

func Calls(id string) int {
	state.Lock()
	defer state.Unlock()
	return state.calls[id]
}

func Trace() []string {
	state.Lock()
	defer state.Unlock()
	return append([]string(nil), state.trace...)
}

func appendTrace(value string) {
	state.Lock()
	defer state.Unlock()
	state.trace = append(state.trace, value)
}
`,
		"bootstrap/application.go": `package bootstrap

import "example.com/cachegenerated/api"

// @Application
func CacheGenerated(*api.API) {
	panic("application marker bodies must never execute")
}
`,
	})
}

const generatedCacheTest = `package spicegen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/cachegenerated/api"
	spicecache "github.com/StevenBuglione/spice/cache"
	spiceconfig "github.com/StevenBuglione/spice/config"
)

func TestGeneratedCacheableRead(t *testing.T) {
	api.Reset()
	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{
			CacheObservers: []spicecache.Observer{nil},
		},
	)
	if err == nil || application != nil ||
		!strings.Contains(err.Error(), "observer 0 is nil") {
		t.Fatalf("nil observer construction = (%v, %v)", application, err)
	}
	assertCacheStrings(t, api.Trace(), []string{"construct api", "cleanup api"})

	for name, values := range map[string]map[string]string{
		"zero capacity": {
			"spice.cache.products.by-id.capacity": "0",
		},
		"negative ttl": {
			"spice.cache.products.by-id.ttl": "-1s",
		},
	} {
		t.Run(name, func(t *testing.T) {
			api.Reset()
			application, constructionErr := NewApplicationWithOptions(
				context.Background(),
				ApplicationOptions{
					Sources: []spiceconfig.Source{
						cacheSource(t, values),
					},
				},
			)
			if constructionErr == nil || application != nil {
				t.Fatalf("invalid configuration = (%v, %v)", application, constructionErr)
			}
			assertCacheStrings(
				t,
				api.Trace(),
				[]string{"construct api", "cleanup api"},
			)
		})
	}

	api.Reset()
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	var observations []spicecache.Observation
	application, err = NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{
			Sources: []spiceconfig.Source{
				cacheSource(t, map[string]string{
					"spice.cache.products.by-id.capacity": "2",
					"spice.cache.products.by-id.ttl":      "1m",
				}),
			},
			CacheClock: func() time.Time {
				return now
			},
			CacheObservers: []spicecache.Observer{
				func(_ context.Context, observation spicecache.Observation) {
					observations = append(observations, observation)
				},
			},
		},
	)
	if err != nil || application == nil {
		t.Fatalf("NewApplicationWithOptions() = (%v, %v)", application, err)
	}

	first := cachedProductRequest(t, application.Handler(), "one")
	second := cachedProductRequest(t, application.Handler(), "one")
	if first != (api.Response{ID: "one", Calls: 1}) ||
		second != first ||
		api.Calls("one") != 1 {
		t.Fatalf(
			"cached responses = (%#v, %#v), calls=%d",
			first,
			second,
			api.Calls("one"),
		)
	}
	now = now.Add(2 * time.Minute)
	expired := cachedProductRequest(t, application.Handler(), "one")
	if expired != (api.Response{ID: "one", Calls: 2}) ||
		api.Calls("one") != 2 {
		t.Fatalf("expired response = %#v, calls=%d", expired, api.Calls("one"))
	}

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/products/fail", nil)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("failed response status = %d", response.Code)
		}
	}
	if api.Calls("fail") != 2 {
		t.Fatalf("failed result was cached; calls=%d", api.Calls("fail"))
	}

	wantOperations := []spicecache.Operation{
		spicecache.OperationGet,
		spicecache.OperationPut,
		spicecache.OperationGet,
		spicecache.OperationGet,
		spicecache.OperationPut,
		spicecache.OperationGet,
		spicecache.OperationGet,
	}
	if len(observations) != len(wantOperations) {
		t.Fatalf("observations = %#v", observations)
	}
	for index, want := range wantOperations {
		if observations[index].Operation != want ||
			observations[index].Definition.ID != "products.by-id" ||
			observations[index].Definition.Module !=
				"example.com/cachegenerated/api" {
			t.Fatalf("observation %d = %#v", index, observations[index])
		}
	}
	if observations[0].Hit ||
		!observations[2].Hit ||
		observations[3].Hit ||
		observations[3].Removed != 1 {
		t.Fatalf("hit/expiration observations = %#v", observations[:4])
	}

	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertCacheStrings(
		t,
		api.Trace(),
		[]string{"construct api", "cleanup api"},
	)
}

func cachedProductRequest(
	t *testing.T,
	handler http.Handler,
	id string,
) api.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/products/"+id, nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response status = %d, body=%s", response.Code, response.Body)
	}
	var result api.Response
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cacheSource(
	t *testing.T,
	values map[string]string,
) spiceconfig.Source {
	t.Helper()
	source, err := spiceconfig.NewMapSource("test", values)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func assertCacheStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("values = %v, want %v", got, want)
		}
	}
}
`
