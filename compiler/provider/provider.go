package provider

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

const acceptedSignature = "func(dependencies...) T or func(dependencies...) (T, error)"

var errorType = types.Universe.Lookup("error").Type()

// Dependency describes one required provider input in declaration order.
// Type is live semantic data owned by the Program used to build the catalog;
// TypeID is the deterministic import-path-qualified representation.
type Dependency struct {
	Index            int
	Name             string
	Type             types.Type
	TypeID           string
	Position         token.Position
	PhysicalPosition token.Position
}

// Provider describes one validated package-level @Bean factory function.
type Provider struct {
	Symbol           load.Symbol
	SymbolID         string
	Name             string
	PackagePath      string
	Position         token.Position
	PhysicalPosition token.Position
	Output           types.Type
	OutputTypeID     string
	Dependencies     []Dependency
	ReturnsError     bool
}

// Diagnostic is one deterministic source-positioned catalog failure.
type Diagnostic struct {
	Position         token.Position
	PhysicalPosition token.Position
	ProviderID       string
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

// Catalog is the immutable-by-convention result of validating resolved @Bean
// annotations from one loaded Program.
type Catalog struct {
	providers   []Provider
	diagnostics []Diagnostic
}

// Providers returns a defensive copy of deterministic provider records.
func (c Catalog) Providers() []Provider {
	result := make([]Provider, len(c.providers))
	copy(result, c.providers)
	for i := range result {
		result[i].Dependencies = append([]Dependency(nil), result[i].Dependencies...)
	}
	return result
}

// Diagnostics returns a defensive copy of deterministic diagnostics.
func (c Catalog) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), c.diagnostics...)
}

// Build validates resolved @Bean annotations using the exact live symbols and
// types already owned by program. It never reloads, reparses, reflects on, or
// executes provider functions.
func Build(program *load.Program, resolution resolve.Result) Catalog {
	catalog := Catalog{}
	if program == nil {
		catalog.diagnostics = []Diagnostic{{Kind: "internal", Message: "provider catalog requires a loaded program"}}
		return catalog
	}

	symbols := make(map[string]load.Symbol)
	for _, symbol := range program.Symbols() {
		symbols[symbol.ID] = symbol
	}
	fileSets := make(map[string]*token.FileSet)
	for _, pkg := range program.Packages() {
		if pkg.Raw != nil && pkg.Raw.Fset != nil {
			fileSets[pkg.Path] = pkg.Raw.Fset
		}
	}

	for _, occurrence := range resolution.Occurrences {
		if occurrence.Annotation.Name != "Bean" {
			continue
		}
		symbol, ok := symbols[occurrence.SymbolID]
		if !ok {
			catalog.diagnostics = append(catalog.diagnostics, occurrenceDiagnostic(
				occurrence,
				"missing-symbol",
				fmt.Sprintf("@Bean target %q has no stable typed symbol in the loaded program", occurrence.Name),
			))
			continue
		}
		provider, diagnostic := analyzeProvider(occurrence, symbol, fileSets[symbol.PackagePath])
		if diagnostic != nil {
			catalog.diagnostics = append(catalog.diagnostics, *diagnostic)
			continue
		}
		catalog.providers = append(catalog.providers, provider)
	}

	sort.SliceStable(catalog.providers, func(i, j int) bool {
		return catalog.providers[i].SymbolID < catalog.providers[j].SymbolID
	})
	catalog.diagnostics = append(catalog.diagnostics, duplicateDiagnostics(catalog.providers)...)
	sortDiagnostics(catalog.diagnostics)
	return catalog
}

func analyzeProvider(occurrence resolve.Occurrence, symbol load.Symbol, fileSet *token.FileSet) (Provider, *Diagnostic) {
	fail := func(kind, message string) (Provider, *Diagnostic) {
		diagnostic := symbolDiagnostic(occurrence, symbol, kind, message)
		return Provider{}, &diagnostic
	}
	label := symbol.DisplayLabel
	if label == "" {
		label = symbol.ID
	}
	if occurrence.Target != annotation.TargetFunction || symbol.Kind != load.SymbolFunction || symbol.Receiver != "" {
		return fail("invalid-target", fmt.Sprintf("@Bean %s must target a package-level function; provider methods and other declarations are not supported", label))
	}
	if len(occurrence.Annotation.Arguments) != 0 {
		return fail("arguments", fmt.Sprintf("@Bean provider %s does not accept annotation arguments", label))
	}
	signature := symbol.Signature
	if signature == nil {
		return fail("missing-signature", fmt.Sprintf("@Bean provider %s has no typed function signature", label))
	}
	if signature.TypeParams() != nil && signature.TypeParams().Len() > 0 {
		return fail("generic", invalidSignatureMessage(label, "generic provider functions are not supported"))
	}
	if signature.Variadic() {
		return fail("variadic", invalidSignatureMessage(label, "variadic provider functions are not supported"))
	}

	results := signature.Results()
	var output types.Type
	returnsError := false
	switch results.Len() {
	case 0:
		return fail("zero-results", invalidSignatureMessage(label, "provider functions must return one provided value"))
	case 1:
		output = results.At(0).Type()
		if isError(output) {
			return fail("error-only", invalidSignatureMessage(label, "error cannot be the only result"))
		}
	case 2:
		first := results.At(0).Type()
		second := results.At(1).Type()
		if isError(first) {
			return fail("error-position", invalidSignatureMessage(label, "error must be the final and only additional result"))
		}
		if !isError(second) {
			return fail("multiple-values", invalidSignatureMessage(label, "provider functions may return only one provided value"))
		}
		output = first
		returnsError = true
	default:
		errorResults := 0
		for i := 0; i < results.Len(); i++ {
			if isError(results.At(i).Type()) {
				errorResults++
			}
		}
		reason := "provider functions may return only one provided value and one final error"
		if errorResults > 1 {
			reason = "provider functions may return at most one error result"
		}
		return fail("too-many-results", invalidSignatureMessage(label, reason))
	}

	dependencies := make([]Dependency, signature.Params().Len())
	for index := 0; index < signature.Params().Len(); index++ {
		parameter := signature.Params().At(index)
		dependency := Dependency{
			Index:  index,
			Name:   parameter.Name(),
			Type:   parameter.Type(),
			TypeID: TypeID(parameter.Type()),
		}
		if fileSet != nil && parameter.Pos().IsValid() {
			dependency.Position = fileSet.PositionFor(parameter.Pos(), true)
			dependency.PhysicalPosition = fileSet.PositionFor(parameter.Pos(), false)
		}
		dependencies[index] = dependency
	}

	return Provider{
		Symbol:           symbol,
		SymbolID:         symbol.ID,
		Name:             symbol.Name,
		PackagePath:      symbol.PackagePath,
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
		Output:           output,
		OutputTypeID:     TypeID(output),
		Dependencies:     dependencies,
		ReturnsError:     returnsError,
	}, nil
}

func invalidSignatureMessage(label, reason string) string {
	return fmt.Sprintf("@Bean provider %s has an unsupported signature: %s; accepted forms are %s", label, reason, acceptedSignature)
}

func isError(value types.Type) bool {
	return value != nil && types.Identical(value, errorType)
}

// TypeID returns a deterministic readable Go type string using package import
// paths rather than source aliases or filesystem paths.
func TypeID(value types.Type) string {
	if value == nil {
		return "<invalid>"
	}
	return types.TypeString(value, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}

func duplicateDiagnostics(providers []Provider) []Diagnostic {
	// OutputTypeID is a deterministic display/serialization form, but it is not
	// semantic identity: valid aliases may render differently from the type they
	// denote. Build groups from the live go/types universe first, then choose a
	// deterministic display name independently.
	var groups [][]int
	for index := range providers {
		placed := false
		for groupIndex := range groups {
			if types.Identical(providers[index].Output, providers[groups[groupIndex][0]].Output) {
				groups[groupIndex] = append(groups[groupIndex], index)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []int{index})
		}
	}

	var diagnostics []Diagnostic
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		displayTypeID := providers[group[0]].OutputTypeID
		conflicts := make([]string, len(group))
		for i, index := range group {
			provider := providers[index]
			if provider.OutputTypeID < displayTypeID {
				displayTypeID = provider.OutputTypeID
			}
			conflicts[i] = fmt.Sprintf("%s at %s", provider.Symbol.DisplayLabel, renderedPosition(provider.Position))
		}
		first := providers[group[0]]
		diagnostics = append(diagnostics, Diagnostic{
			Position:         first.Position,
			PhysicalPosition: first.PhysicalPosition,
			ProviderID:       first.SymbolID,
			Kind:             "duplicate-output",
			Message: fmt.Sprintf(
				"multiple @Bean providers produce exact type %s: %s; qualifiers and implicit interface bindings are not supported",
				displayTypeID,
				strings.Join(conflicts, ", "),
			),
		})
	}
	return diagnostics
}

func occurrenceDiagnostic(occurrence resolve.Occurrence, kind, message string) Diagnostic {
	return Diagnostic{
		Position: occurrence.DisplayPosition,
		PhysicalPosition: token.Position{
			Filename: occurrence.PhysicalFile,
			Offset:   occurrence.PhysicalOffset,
		},
		ProviderID: occurrence.SymbolID,
		Kind:       kind,
		Message:    message,
	}
}

func symbolDiagnostic(occurrence resolve.Occurrence, symbol load.Symbol, kind, message string) Diagnostic {
	diagnostic := occurrenceDiagnostic(occurrence, kind, message)
	if symbol.ID != "" {
		diagnostic.ProviderID = symbol.ID
	}
	if diagnostic.Position.Filename == "" {
		diagnostic.Position = symbol.Position
	}
	if diagnostic.PhysicalPosition.Filename == "" {
		diagnostic.PhysicalPosition = symbol.PhysicalPosition
	}
	return diagnostic
}

func renderedPosition(position token.Position) string {
	filename := position.Filename
	if filename == "" {
		filename = "<unknown>"
	}
	line := position.Line
	if line <= 0 {
		line = 1
	}
	column := position.Column
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", filename, line, column)
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
		if left.ProviderID != right.ProviderID {
			return left.ProviderID < right.ProviderID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Message < right.Message
	})
}
