// Package application assembles the immutable Spice application model from
// one resolved, type-aware compiler program.
package application

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/graph"
	"github.com/StevenBuglione/spice/compiler/lifecycle"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

const acceptedMarkerSignature = "func(applicationRoots...)"

// Stage identifies the compiler phase that produced a model diagnostic.
type Stage string

const (
	// StageResolution identifies invalid resolved annotation input.
	StageResolution Stage = "resolution"
	// StageProvider identifies provider catalog validation.
	StageProvider Stage = "provider"
	// StageGraph identifies provider graph validation.
	StageGraph Stage = "graph"
	// StageLifecycle identifies lifecycle hook validation.
	StageLifecycle Stage = "lifecycle"
	// StageApplication identifies application marker and root validation.
	StageApplication Stage = "application"
)

// Root identifies one exact provider type requested by an @Application marker.
type Root struct {
	Index            int
	Name             string
	Type             types.Type
	TypeID           string
	ProviderID       string
	Position         token.Position
	PhysicalPosition token.Position
}

// Target describes one validated @Application marker. The marker function is
// compile-time metadata only; Spice never invokes its body during analysis.
type Target struct {
	Symbol           load.Symbol
	SymbolID         string
	Name             string
	PackagePath      string
	Position         token.Position
	PhysicalPosition token.Position
	roots            []Root
}

// Roots returns the target's roots in function-parameter order.
func (t Target) Roots() []Root {
	return append([]Root(nil), t.roots...)
}

// Diagnostic is one deterministic source-positioned application-model failure.
type Diagnostic struct {
	Stage            Stage
	Position         token.Position
	PhysicalPosition token.Position
	SymbolID         string
	Kind             string
	Message          string
}

// Error renders a compiler-style diagnostic.
func (d Diagnostic) Error() string {
	position := d.Position
	if position.Filename == "" {
		position.Filename = "<unknown>"
	}
	if position.Line <= 0 {
		position.Line = 1
	}
	if position.Column <= 0 {
		position.Column = 1
	}
	return fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, d.Message)
}

// Model is the immutable-by-convention application IR assembled from one
// loaded Program and one resolved annotation result.
type Model struct {
	providers   []provider.Provider
	edges       []graph.Edge
	components  []lifecycle.Component
	targets     []Target
	diagnostics []Diagnostic
}

// Providers returns providers in deterministic dependency-first construction
// order. Cleanup capabilities remain attached to each provider record.
func (m Model) Providers() []provider.Provider {
	result := make([]provider.Provider, len(m.providers))
	for index := range m.providers {
		result[index] = cloneProvider(m.providers[index])
	}
	return result
}

// Edges returns exact-type provider graph edges in stable consumer and
// parameter order.
func (m Model) Edges() []graph.Edge {
	return append([]graph.Edge(nil), m.edges...)
}

// Components returns lifecycle components in provider construction order.
func (m Model) Components() []lifecycle.Component {
	result := make([]lifecycle.Component, len(m.components))
	for index := range m.components {
		result[index] = cloneComponent(m.components[index])
	}
	return result
}

// Targets returns validated application targets in stable symbol order.
func (m Model) Targets() []Target {
	result := make([]Target, len(m.targets))
	copy(result, m.targets)
	for index := range result {
		result[index].roots = append([]Root(nil), m.targets[index].roots...)
	}
	return result
}

// Diagnostics returns deterministic diagnostics. A model with diagnostics must
// not be used for generation.
func (m Model) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), m.diagnostics...)
}

// Build assembles provider, graph, lifecycle, and application-root metadata
// from the existing single-program pipeline. It never reloads source, reparses
// files, reflects on declarations, or executes application/provider bodies.
func Build(program *load.Program, resolution resolve.Result) Model {
	model := Model{}
	if program == nil {
		model.diagnostics = []Diagnostic{{
			Stage:   StageApplication,
			Kind:    "internal",
			Message: "application model requires a loaded program",
		}}
		return model
	}
	if len(resolution.Diagnostics) != 0 {
		model.diagnostics = resolutionDiagnostics(resolution.Diagnostics)
		return model
	}

	providerCatalog := provider.Build(program, resolution)
	if diagnostics := providerCatalog.Diagnostics(); len(diagnostics) != 0 {
		model.diagnostics = providerDiagnostics(diagnostics)
		return model
	}

	providerGraph := graph.Build(providerCatalog)
	if diagnostics := providerGraph.Diagnostics(); len(diagnostics) != 0 {
		model.diagnostics = graphDiagnostics(diagnostics)
		return model
	}

	lifecycleCatalog := lifecycle.Build(program, resolution, providerCatalog)
	if diagnostics := lifecycleCatalog.Diagnostics(); len(diagnostics) != 0 {
		model.diagnostics = lifecycleDiagnostics(diagnostics)
		return model
	}

	model.providers = providerGraph.ConstructionOrder()
	model.edges = providerGraph.Edges()
	model.components = orderedComponents(model.providers, lifecycleCatalog.Components())
	model.targets, model.diagnostics = applicationTargets(program, resolution, providerCatalog.Providers())
	return model
}

func applicationTargets(
	program *load.Program,
	resolution resolve.Result,
	providers []provider.Provider,
) ([]Target, []Diagnostic) {
	symbols := make(map[string]load.Symbol)
	for _, symbol := range program.Symbols() {
		symbols[symbol.ID] = symbol
	}
	fileSets := packageFileSets(program)
	seen := make(map[string]resolve.Occurrence)
	var targets []Target
	var diagnostics []Diagnostic
	for _, occurrence := range resolution.Occurrences {
		if occurrence.Annotation.Name != "Application" {
			continue
		}
		if previous, duplicate := seen[occurrence.SymbolID]; duplicate {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"duplicate-annotation",
				fmt.Sprintf(
					"@Application marker %q is declared more than once; first declaration is at %s",
					occurrence.Name,
					renderPosition(previous.DisplayPosition),
				),
			))
			continue
		}
		seen[occurrence.SymbolID] = occurrence

		symbol, ok := symbols[occurrence.SymbolID]
		if !ok {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"missing-symbol",
				fmt.Sprintf("@Application target %q has no stable typed symbol in the loaded program", occurrence.Name),
			))
			continue
		}
		target, markerDiagnostics := analyzeMarker(
			occurrence,
			symbol,
			fileSets[symbol.PackagePath],
			providers,
		)
		diagnostics = append(diagnostics, markerDiagnostics...)
		if len(markerDiagnostics) == 0 {
			targets = append(targets, target)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i].SymbolID < targets[j].SymbolID
	})
	sortDiagnostics(diagnostics)
	return targets, diagnostics
}

func analyzeMarker(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	fileSet *token.FileSet,
	providers []provider.Provider,
) (Target, []Diagnostic) {
	signature, diagnostic := markerSignature(occurrence, symbol)
	if diagnostic != nil {
		return Target{}, []Diagnostic{*diagnostic}
	}
	roots, diagnostics := applicationRoots(occurrence, symbol, signature, fileSet, providers)
	if len(diagnostics) != 0 {
		return Target{}, diagnostics
	}
	return Target{
		Symbol:           symbol,
		SymbolID:         symbol.ID,
		Name:             symbol.Name,
		PackagePath:      symbol.PackagePath,
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
		roots:            roots,
	}, nil
}

func markerSignature(occurrence resolve.Occurrence, symbol load.Symbol) (*types.Signature, *Diagnostic) {
	label := symbolLabel(symbol)
	if occurrence.Target != annotation.TargetFunction ||
		symbol.Kind != load.SymbolFunction ||
		symbol.Receiver != "" {
		diagnostic := symbolDiagnostic(
			occurrence,
			symbol,
			"invalid-target",
			fmt.Sprintf("@Application %s must target a package-level function", label),
		)
		return nil, &diagnostic
	}
	if len(occurrence.Annotation.Arguments) != 0 {
		diagnostic := symbolDiagnostic(
			occurrence,
			symbol,
			"arguments",
			fmt.Sprintf("@Application marker %s does not accept annotation arguments", label),
		)
		return nil, &diagnostic
	}
	signature := symbol.Signature
	if signature == nil {
		diagnostic := symbolDiagnostic(
			occurrence,
			symbol,
			"missing-signature",
			fmt.Sprintf("@Application marker %s has no typed function signature", label),
		)
		return nil, &diagnostic
	}
	if signature.TypeParams() != nil && signature.TypeParams().Len() != 0 {
		return nil, invalidSignatureDiagnostic(
			occurrence,
			symbol,
			"generic",
			"generic application markers are not supported",
		)
	}
	if signature.Variadic() {
		return nil, invalidSignatureDiagnostic(
			occurrence,
			symbol,
			"variadic",
			"application markers must be non-variadic",
		)
	}
	if signature.Results().Len() != 0 {
		return nil, invalidSignatureDiagnostic(
			occurrence,
			symbol,
			"results",
			fmt.Sprintf("application markers must return no results, got %d", signature.Results().Len()),
		)
	}
	return signature, nil
}

func applicationRoots(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	signature *types.Signature,
	fileSet *token.FileSet,
	providers []provider.Provider,
) ([]Root, []Diagnostic) {
	roots := make([]Root, 0, signature.Params().Len())
	var diagnostics []Diagnostic
	for index := 0; index < signature.Params().Len(); index++ {
		parameter := signature.Params().At(index)
		item := Root{
			Index:  index,
			Name:   parameter.Name(),
			Type:   parameter.Type(),
			TypeID: provider.TypeID(parameter.Type()),
		}
		if fileSet != nil && parameter.Pos().IsValid() {
			item.Position = fileSet.PositionFor(parameter.Pos(), true)
			item.PhysicalPosition = fileSet.PositionFor(parameter.Pos(), false)
		}
		match, ok := exactProvider(parameter.Type(), providers)
		if !ok {
			diagnostics = append(diagnostics, rootDiagnostic(
				occurrence,
				symbol,
				item,
				fmt.Sprintf(
					"@Application marker %s root %s requires exact provider type %s, but no @Bean provider produces that type",
					symbolLabel(symbol),
					parameterLabel(item),
					item.TypeID,
				),
			))
			continue
		}
		item.ProviderID = match.SymbolID
		roots = append(roots, item)
	}
	return roots, diagnostics
}

func exactProvider(required types.Type, providers []provider.Provider) (provider.Provider, bool) {
	for _, candidate := range providers {
		if types.Identical(required, candidate.Output) {
			return candidate, true
		}
	}
	return provider.Provider{}, false
}

func orderedComponents(
	providers []provider.Provider,
	components []lifecycle.Component,
) []lifecycle.Component {
	byProvider := make(map[string]lifecycle.Component, len(components))
	for _, component := range components {
		byProvider[component.Provider.SymbolID] = component
	}
	result := make([]lifecycle.Component, 0, len(components))
	for _, item := range providers {
		if component, ok := byProvider[item.SymbolID]; ok {
			result = append(result, cloneComponent(component))
		}
	}
	return result
}

func packageFileSets(program *load.Program) map[string]*token.FileSet {
	result := make(map[string]*token.FileSet)
	for _, pkg := range program.Packages() {
		if pkg.Raw != nil && pkg.Raw.Fset != nil {
			result[pkg.Path] = pkg.Raw.Fset
		}
	}
	return result
}

func invalidSignatureDiagnostic(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	kind string,
	reason string,
) *Diagnostic {
	diagnostic := symbolDiagnostic(
		occurrence,
		symbol,
		kind,
		fmt.Sprintf(
			"@Application marker %s has an unsupported signature: %s; accepted form is %s",
			symbolLabel(symbol),
			reason,
			acceptedMarkerSignature,
		),
	)
	return &diagnostic
}

func rootDiagnostic(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	root Root,
	message string,
) Diagnostic {
	diagnostic := symbolDiagnostic(occurrence, symbol, "missing-root-provider", message)
	if root.Position.Filename != "" {
		diagnostic.Position = root.Position
	}
	if root.PhysicalPosition.Filename != "" {
		diagnostic.PhysicalPosition = root.PhysicalPosition
	}
	return diagnostic
}

func occurrenceDiagnostic(occurrence resolve.Occurrence, kind, message string) Diagnostic {
	return Diagnostic{
		Stage:            StageApplication,
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
		SymbolID:         occurrence.SymbolID,
		Kind:             kind,
		Message:          message,
	}
}

func symbolDiagnostic(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	kind string,
	message string,
) Diagnostic {
	diagnostic := occurrenceDiagnostic(occurrence, kind, message)
	if diagnostic.Position.Filename == "" {
		diagnostic.Position = symbol.Position
	}
	if diagnostic.PhysicalPosition.Filename == "" {
		diagnostic.PhysicalPosition = symbol.PhysicalPosition
	}
	return diagnostic
}

func resolutionDiagnostics(diagnostics []resolve.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = Diagnostic{
			Stage: StageResolution,
			Position: token.Position{
				Filename: diagnostic.Position.Filename,
				Offset:   diagnostic.Position.Offset,
				Line:     diagnostic.Position.Line,
				Column:   diagnostic.Position.Column,
			},
			PhysicalPosition: token.Position{
				Filename: diagnostic.PhysicalFile,
				Offset:   diagnostic.PhysicalOffset,
			},
			Kind:    diagnostic.Kind,
			Message: diagnostic.Message,
		}
	}
	sortDiagnostics(result)
	return result
}

func providerDiagnostics(diagnostics []provider.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = Diagnostic{
			Stage:            StageProvider,
			Position:         diagnostic.Position,
			PhysicalPosition: diagnostic.PhysicalPosition,
			SymbolID:         diagnostic.ProviderID,
			Kind:             diagnostic.Kind,
			Message:          diagnostic.Message,
		}
	}
	sortDiagnostics(result)
	return result
}

func graphDiagnostics(diagnostics []graph.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = Diagnostic{
			Stage:            StageGraph,
			Position:         diagnostic.Position,
			PhysicalPosition: diagnostic.PhysicalPosition,
			SymbolID:         diagnostic.ProviderID,
			Kind:             diagnostic.Kind,
			Message:          diagnostic.Message,
		}
	}
	sortDiagnostics(result)
	return result
}

func lifecycleDiagnostics(diagnostics []lifecycle.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = Diagnostic{
			Stage:            StageLifecycle,
			Position:         diagnostic.Position,
			PhysicalPosition: diagnostic.PhysicalPosition,
			SymbolID:         diagnostic.MethodID,
			Kind:             diagnostic.Kind,
			Message:          diagnostic.Message,
		}
	}
	sortDiagnostics(result)
	return result
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.PhysicalPosition.Filename != right.PhysicalPosition.Filename {
			return left.PhysicalPosition.Filename < right.PhysicalPosition.Filename
		}
		if left.PhysicalPosition.Offset != right.PhysicalPosition.Offset {
			return left.PhysicalPosition.Offset < right.PhysicalPosition.Offset
		}
		if left.Stage != right.Stage {
			return left.Stage < right.Stage
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.SymbolID != right.SymbolID {
			return left.SymbolID < right.SymbolID
		}
		return left.Message < right.Message
	})
}

func cloneProvider(item provider.Provider) provider.Provider {
	item.Dependencies = append([]provider.Dependency(nil), item.Dependencies...)
	return item
}

func cloneComponent(component lifecycle.Component) lifecycle.Component {
	component.Provider = cloneProvider(component.Provider)
	if component.Start != nil {
		start := *component.Start
		component.Start = &start
	}
	if component.Stop != nil {
		stop := *component.Stop
		component.Stop = &stop
	}
	return component
}

func symbolLabel(symbol load.Symbol) string {
	if symbol.DisplayLabel != "" {
		return symbol.DisplayLabel
	}
	if symbol.ID != "" {
		return symbol.ID
	}
	return symbol.Name
}

func parameterLabel(root Root) string {
	if root.Name == "" {
		return fmt.Sprintf("parameter %d", root.Index)
	}
	return fmt.Sprintf("parameter %d %q", root.Index, root.Name)
}

func renderPosition(position token.Position) string {
	if position.Filename == "" {
		return "<unknown>:1:1"
	}
	line, column := position.Line, position.Column
	if line <= 0 {
		line = 1
	}
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", position.Filename, line, column)
}
