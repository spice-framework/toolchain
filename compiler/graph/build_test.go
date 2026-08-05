package graph

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestGraphResolvesOnlyExplicitInterfaceBindings(t *testing.T) {
	pkg := types.NewPackage("example.com/interfaces", "interfaces")
	errorResult := types.NewTuple(
		types.NewVar(token.NoPos, nil, "", types.Universe.Lookup("error").Type()),
	)
	methodSignature := types.NewSignatureType(
		nil,
		nil,
		nil,
		types.NewTuple(),
		errorResult,
		false,
	)
	method := types.NewFunc(
		token.NoPos,
		pkg,
		"Process",
		methodSignature,
	)
	contract := types.NewInterfaceType([]*types.Func{method}, nil)
	contract.Complete()
	contractType := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Processor", nil),
		contract,
		nil,
	)
	implementation := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Stripe", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	implementation.AddMethod(types.NewFunc(
		token.NoPos,
		pkg,
		"Process",
		types.NewSignatureType(
			types.NewVar(
				token.NoPos,
				pkg,
				"",
				types.NewPointer(implementation),
			),
			nil,
			nil,
			types.NewTuple(),
			errorResult,
			false,
		),
	))
	consumer := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Checkout", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	catalog := provider.Add(
		provider.Catalog{},
		provider.Provider{
			Source:       provider.SourceStereotype,
			SymbolID:     "stripe",
			Name:         "stripe",
			Output:       types.NewPointer(implementation),
			OutputTypeID: "*example.com/interfaces.Stripe",
			Interfaces: []provider.InterfaceBinding{{
				Type:   contractType,
				TypeID: "example.com/interfaces.Processor",
			}},
		},
		provider.Provider{
			Source:       provider.SourceBean,
			SymbolID:     "checkout",
			Name:         "checkout",
			Output:       types.NewPointer(consumer),
			OutputTypeID: "*example.com/interfaces.Checkout",
			Dependencies: []provider.Dependency{{
				Index:  0,
				Name:   "processor",
				Type:   contractType,
				TypeID: "example.com/interfaces.Processor",
			}},
		},
	)
	result := Build(catalog)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnostics)
	}
	edges := result.Edges()
	if len(edges) != 1 ||
		edges[0].DependencyID != "stripe" ||
		edges[0].ConsumerID != "checkout" {
		t.Fatalf("Edges() = %#v", edges)
	}

	implicit := catalog.Providers()
	stripeIndex := -1
	for index := range implicit {
		if implicit[index].SymbolID == "stripe" {
			stripeIndex = index
			break
		}
	}
	if stripeIndex < 0 {
		t.Fatal("stripe provider missing")
	}
	implicit[stripeIndex].Interfaces = nil
	implicitCatalog := provider.Add(
		provider.Catalog{},
		implicit...,
	)
	missing := Build(implicitCatalog).Diagnostics()
	if len(missing) != 1 ||
		missing[0].Kind != "missing-dependency" {
		t.Fatalf("implicit diagnostics = %#v", missing)
	}

	duplicate := implicit[stripeIndex]
	duplicate.SymbolID = "square"
	duplicate.Name = "square"
	square := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Square", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	duplicate.Output = types.NewPointer(square)
	duplicate.OutputTypeID = "*example.com/interfaces.Square"
	duplicate.Interfaces = []provider.InterfaceBinding{{
		Type:   contractType,
		TypeID: "example.com/interfaces.Processor",
	}}
	implicit[stripeIndex].Interfaces = duplicate.Interfaces
	ambiguousCatalog := provider.Add(
		provider.Catalog{},
		append(implicit, duplicate)...,
	)
	ambiguous := Build(ambiguousCatalog).Diagnostics()
	if len(ambiguous) != 1 ||
		ambiguous[0].Kind != "ambiguous-dependency" ||
		!strings.Contains(ambiguous[0].Message, "square") ||
		!strings.Contains(ambiguous[0].Message, "stripe") {
		t.Fatalf("ambiguous diagnostics = %#v", ambiguous)
	}
}

func TestGraphValidDiamond(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/graphvalid\n\ngo 1.23.0\n",
		"shared/shared.go": `package shared

type Options struct{}
`,
		"app/providers.go": `package app

import "example.com/graphvalid/shared"

type Config struct{}
type Store struct{}
type Service struct{}
type Original struct{}
type Alias = Original
type AliasPointer = Original

// @Bean
func OptionsProvider() shared.Options { panic("must not execute") }

// @Bean
func ConfigProvider(shared.Options) Config { panic("must not execute") }

// @Bean
func StoreProvider(Config) *Store { panic("must not execute") }

// @Bean
func CacheProvider(Config) map[string]int { panic("must not execute") }

// @Bean
func ServiceProvider(*Store, map[string]int) Service { panic("must not execute") }

// @Bean
func StringsProvider() []string { panic("must not execute") }

// @Bean
func AliasProvider() Alias { panic("must not execute") }

// @Bean
func AliasConsumer(Original) struct{ Ready bool } { panic("must not execute") }

// @Bean
func OriginalPointerProvider() *Original { panic("must not execute") }

// @Bean
func AliasPointerConsumer(*AliasPointer) chan int { panic("must not execute") }
`,
	})
	result := buildModuleGraph(t, root)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	if len(result.Nodes()) != 10 || len(result.Edges()) != 7 || len(result.ConstructionOrder()) != 10 {
		t.Fatalf("nodes=%d edges=%d order=%d", len(result.Nodes()), len(result.Edges()), len(result.ConstructionOrder()))
	}
	assertBefore(t, result.ConstructionOrder(), "OptionsProvider", "ConfigProvider")
	assertBefore(t, result.ConstructionOrder(), "ConfigProvider", "StoreProvider")
	assertBefore(t, result.ConstructionOrder(), "ConfigProvider", "CacheProvider")
	assertBefore(t, result.ConstructionOrder(), "StoreProvider", "ServiceProvider")
	assertBefore(t, result.ConstructionOrder(), "CacheProvider", "ServiceProvider")
	assertBefore(t, result.ConstructionOrder(), "AliasProvider", "AliasConsumer")
	assertBefore(t, result.ConstructionOrder(), "OriginalPointerProvider", "AliasPointerConsumer")

	if result.Nodes()[0].Provider().SymbolID == "" || result.Edges()[0].Consumer().SymbolID == "" || result.Edges()[0].Dependency().SymbolID == "" {
		t.Fatal("graph did not retain provider records")
	}
	nodeRecord := result.Nodes()[0].Provider()
	nodeRecord.Name = "mutated"
	if nodeRecord.Name != "mutated" {
		t.Fatal("test mutation did not update copied provider record")
	}
	orderCopy := result.ConstructionOrder()
	orderCopy[0].Name = "mutated"
	if result.Nodes()[0].Provider().Name == "mutated" || result.ConstructionOrder()[0].Name == "mutated" {
		t.Fatal("graph accessors exposed mutable internal provider records")
	}
}

func TestGraphReportsMissingDependencies(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/missinggraph\n\ngo 1.23.0\n",
		"providers.go": `package missinggraph

type Repository interface{ Get() }
type Implementation struct{}
func (*Implementation) Get() {}
type NamedA int
type NamedB int
type Value struct{}
type Service struct{}
type Other struct{}

// @Bean
func ImplementationProvider() *Implementation { panic("must not execute") }

// @Bean
func NamedProvider() NamedA { panic("must not execute") }

// @Bean
func ValueProvider() Value { panic("must not execute") }

// @Bean
func ServiceProvider(repository Repository, named NamedB, pointer *Value, text string) Service { panic("must not execute") }

// @Bean
func OtherProvider(number int) Other { panic("must not execute") }
`,
	})
	result := buildModuleGraph(t, root)
	diagnostics := result.Diagnostics()
	if len(diagnostics) != 5 {
		t.Fatalf("Diagnostics() = %v, want 5", diagnosticStrings(diagnostics))
	}
	joined := strings.Join(diagnosticStrings(diagnostics), "\n")
	for _, expected := range []string{
		"Repository", `parameter 0 "repository"`, "NamedB", `parameter 1 "named"`,
		"*example.com/missinggraph.Value", `parameter 2 "pointer"`, "string", `parameter 3 "text"`,
		"int", `parameter 0 "number"`, "spice:symbol:v1|function|",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	if len(result.ConstructionOrder()) != 0 {
		t.Fatal("construction order exposed for missing dependencies")
	}
}

func TestGraphReportsCycles(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/cyclegraph\n\ngo 1.23.0\n",
		"providers.go": `package cyclegraph

type Self struct{}
type A struct{}
type B struct{}
type X struct{}
type Y struct{}
type Z struct{}
type Missing struct{}

// @Bean
func SelfProvider(Self) Self { panic("must not execute") }
// @Bean
func AProvider(B) A { panic("must not execute") }
// @Bean
func BProvider(A) B { panic("must not execute") }
// @Bean
func XProvider(Y) X { panic("must not execute") }
// @Bean
func YProvider(Z) Y { panic("must not execute") }
// @Bean
func ZProvider(X) Z { panic("must not execute") }
// @Bean
func MissingProvider(string) Missing { panic("must not execute") }
`,
	})
	result := buildModuleGraph(t, root)
	diagnostics := result.Diagnostics()
	if len(diagnostics) != 4 {
		t.Fatalf("Diagnostics() = %v, want missing plus three cycles", diagnosticStrings(diagnostics))
	}
	joined := strings.Join(diagnosticStrings(diagnostics), "\n")
	for _, expected := range []string{"missing-dependency", "SelfProvider", "AProvider", "BProvider", "XProvider", "YProvider", "ZProvider", " -> ", "component providers"} {
		if expected == "missing-dependency" {
			if diagnostics[0].Kind != expected && diagnostics[1].Kind != expected && diagnostics[2].Kind != expected && diagnostics[3].Kind != expected {
				t.Fatalf("diagnostics lack kind %q: %#v", expected, diagnostics)
			}
			continue
		}
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == "cycle" {
			path := strings.TrimPrefix(strings.Split(diagnostic.Message, ";")[0], "provider dependency cycle: ")
			parts := strings.Split(path, " -> ")
			if len(parts) < 2 || parts[0] != parts[len(parts)-1] {
				t.Fatalf("cycle path is not closed: %q", diagnostic.Message)
			}
		}
	}
	if len(result.ConstructionOrder()) != 0 {
		t.Fatal("construction order exposed for cyclic graph")
	}
}

func TestGraphDeterministic(t *testing.T) {
	filesA := map[string]string{
		"go.mod": "module example.com/deterministicgraph\n\ngo 1.23.0\n",
		"z.go": `package deterministicgraph

type A struct{}
type C struct{}
// @Bean
func AProvider() A { panic("must not execute") }
// @Bean
func CProvider(A) C { panic("must not execute") }
`,
		"a.go": `package deterministicgraph

type B struct{}
type D struct{}
// @Bean
func BProvider() B { panic("must not execute") }
// @Bean
func DProvider(A, B) D { panic("must not execute") }
`,
	}
	filesB := map[string]string{
		"go.mod": filesA["go.mod"],
		"one.go": filesA["a.go"],
		"two.go": filesA["z.go"],
	}
	first := graphSummary(buildModuleGraph(t, writeModule(t, filesA)))
	second := graphSummary(buildModuleGraph(t, writeModule(t, filesB)))
	if first != second {
		t.Fatalf("equivalent loads changed graph:\nfirst=%s\nsecond=%s", first, second)
	}
	for run := range 20 {
		if next := graphSummary(buildModuleGraph(t, writeModule(t, filesA))); next != first {
			t.Fatalf("run %d changed graph:\nfirst=%s\nnext=%s", run, first, next)
		}
	}
}

func TestGraphReadyTieBreaksByProviderID(t *testing.T) {
	result := buildModuleGraph(t, writeModule(t, map[string]string{
		"go.mod": "module example.com/readyorder\n\ngo 1.23.0\n",
		"providers.go": `package readyorder

type A struct{}
type B struct{}
type C struct{}
// @Bean
func LongProviderName() A { panic("must not execute") }
// @Bean
func Short() B { panic("must not execute") }
// @Bean
func CProvider() C { panic("must not execute") }
`,
	}))
	order := result.ConstructionOrder()
	if len(order) != 3 {
		t.Fatalf("order = %v", providerNames(order))
	}
	ids := []string{order[0].SymbolID, order[1].SymbolID, order[2].SymbolID}
	want := append([]string(nil), ids...)
	sort.Strings(want)
	if strings.Join(ids, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ready order IDs = %v, want %v", ids, want)
	}
}

func TestConstructionOrderFanOutUsesBoundedReadyComparisons(t *testing.T) {
	const consumerCount = 2048
	providers := make([]provider.Provider, consumerCount+1)
	providers[0].SymbolID = "spice:symbol:v1|function|4:root|0:|4:Root"
	adjacency := make([][]int, len(providers))
	for index := 1; index < len(providers); index++ {
		providers[index].SymbolID = fmt.Sprintf("spice:symbol:v1|function|8:consumer|0:|8:P%06d", index)
		adjacency[index] = []int{0}
	}

	order, stats := constructionOrderWithStats(adjacency, providers)
	if len(order) != len(providers) {
		t.Fatalf("order length = %d, want %d", len(order), len(providers))
	}
	if order[0].SymbolID != providers[0].SymbolID {
		t.Fatalf("first provider = %q, want root %q", order[0].SymbolID, providers[0].SymbolID)
	}
	for index := 2; index < len(order); index++ {
		if order[index-1].SymbolID >= order[index].SymbolID {
			t.Fatalf("ready providers are not ordered at %d: %q >= %q", index, order[index-1].SymbolID, order[index].SymbolID)
		}
	}

	// A binary priority queue uses O(N log N) comparisons across pushes and
	// pops. The deliberately generous bound is deterministic and still fails
	// the former implementation, which sorted the growing ready slice after
	// every fan-out insertion and therefore used O(N^2) comparisons.
	limit := 4 * len(providers) * bits.Len(uint(len(providers)))
	if stats.readyComparisons > limit {
		t.Fatalf("ready comparisons = %d, want <= %d for %d providers", stats.readyComparisons, limit, len(providers))
	}
}

func TestGraphLargeCatalog(t *testing.T) {
	const count = 180
	var source strings.Builder
	source.WriteString("package largegraph\n\n")
	for index := range count {
		fmt.Fprintf(&source, "type T%d struct{}\n", index)
		if index == 0 {
			fmt.Fprintf(&source, "// @Bean\nfunc Provider%d() T%d { panic(\"must not execute\") }\n", index, index)
		} else {
			fmt.Fprintf(&source, "// @Bean\nfunc Provider%d(T%d) T%d { panic(\"must not execute\") }\n", index, index-1, index)
		}
	}
	result := buildModuleGraph(t, writeModule(t, map[string]string{
		"go.mod":       "module example.com/largegraph\n\ngo 1.23.0\n",
		"providers.go": source.String(),
	}))
	if len(result.Diagnostics()) != 0 || len(result.Nodes()) != count || len(result.Edges()) != count-1 || len(result.ConstructionOrder()) != count {
		t.Fatalf("large graph diagnostics=%v nodes=%d edges=%d order=%d", diagnosticStrings(result.Diagnostics()), len(result.Nodes()), len(result.Edges()), len(result.ConstructionOrder()))
	}
}

func buildModuleGraph(t *testing.T, root string) Result {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolved := resolve.Annotations(program)
	if len(resolved.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolved.Diagnostics)
	}
	resolved, err = testannotation.AttachOfficial(resolved)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	catalog := provider.Build(program, resolved)
	if len(catalog.Diagnostics()) != 0 {
		t.Fatalf("provider.Build() diagnostics = %v", catalog.Diagnostics())
	}
	return buildQuiet(t, catalog)
}

func buildQuiet(t *testing.T, catalog provider.Catalog) Result {
	t.Helper()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		if closeErr := stdoutReader.Close(); closeErr != nil {
			t.Logf("close stdout reader after stderr pipe failure: %v", closeErr)
		}
		if closeErr := stdoutWriter.Close(); closeErr != nil {
			t.Logf("close stdout writer after stderr pipe failure: %v", closeErr)
		}
		t.Fatal(err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	result := Build(catalog)
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if closeErr := stdoutWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := stderrWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 || len(stderr) != 0 {
		t.Fatalf("graph library wrote stdout=%q stderr=%q", stdout, stderr)
	}
	return result
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, source := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Kind + ": " + diagnostic.Error()
	}
	return result
}

func graphSummary(result Result) string {
	var lines []string
	for _, node := range result.Nodes() {
		lines = append(lines, "node "+node.ProviderID)
	}
	for _, edge := range result.Edges() {
		lines = append(lines, fmt.Sprintf("edge %s[%d]->%s:%s", edge.ConsumerID, edge.ParameterIndex, edge.DependencyID, edge.RequiredTypeID))
	}
	for _, item := range result.ConstructionOrder() {
		lines = append(lines, "order "+item.SymbolID)
	}
	for _, diagnostic := range result.Diagnostics() {
		lines = append(lines, "diagnostic "+diagnostic.Kind+":"+diagnostic.ProviderID+":"+diagnostic.Message)
	}
	return strings.Join(lines, "\n")
}

func assertBefore(t *testing.T, order []provider.Provider, dependency, consumer string) {
	t.Helper()
	positions := make(map[string]int, len(order))
	for index := range order {
		item := order[index]
		positions[item.Name] = index
	}
	if positions[dependency] >= positions[consumer] {
		t.Fatalf("order=%v: %s must precede %s", providerNames(order), dependency, consumer)
	}
}

func providerNames(items []provider.Provider) []string {
	result := make([]string, len(items))
	for index := range items {
		result[index] = items[index].Name
	}
	return result
}

func TestGraphIgnoresProviderCleanupMetadata(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/spice-framework/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/providers.go": `package app

import life "github.com/spice-framework/spice/lifecycle"

type Config struct{}
type Store struct{}
type Service struct{}

// @Bean
func ConfigProvider() Config { panic("must not execute") }

// @Bean
func StoreProvider(Config) (Store, life.Cleanup) { panic("must not execute") }

// @Bean
func ServiceProvider(Store) (Service, life.Cleanup, error) { panic("must not execute") }
`,
	})

	result := buildModuleGraph(t, root)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	if len(result.Nodes()) != 3 || len(result.Edges()) != 2 || len(result.ConstructionOrder()) != 3 {
		t.Fatalf("nodes=%d edges=%d order=%d", len(result.Nodes()), len(result.Edges()), len(result.ConstructionOrder()))
	}
	assertBefore(t, result.ConstructionOrder(), "ConfigProvider", "StoreProvider")
	assertBefore(t, result.ConstructionOrder(), "StoreProvider", "ServiceProvider")

	cleanupProviders := 0
	for _, node := range result.Nodes() {
		item := node.Provider()
		if item.ReturnsCleanup {
			cleanupProviders++
		}
		if item.OutputTypeID == "github.com/spice-framework/spice/lifecycle.Cleanup" {
			t.Fatalf("cleanup metadata became a graph output node: %#v", item)
		}
	}
	if cleanupProviders != 2 {
		t.Fatalf("cleanup provider metadata count = %d, want 2", cleanupProviders)
	}
	for _, edge := range result.Edges() {
		if edge.RequiredTypeID == "github.com/spice-framework/spice/lifecycle.Cleanup" {
			t.Fatalf("cleanup metadata became a graph dependency edge: %#v", edge)
		}
	}
}
