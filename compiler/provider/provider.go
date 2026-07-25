// Package provider builds and validates the compile-time provider catalog.
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

const (
	acceptedSignature  = "func(dependencies...) T, func(dependencies...) (T, error), func(dependencies...) (T, lifecycle.Cleanup), or func(dependencies...) (T, lifecycle.Cleanup, error)"
	cleanupPackagePath = "github.com/StevenBuglione/spice/lifecycle"
	cleanupTypeName    = "Cleanup"
)

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
	ReturnsCleanup   bool
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
	cleanupType := canonicalCleanupType(program)
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
		provider, diagnostic := analyzeProvider(occurrence, symbol, fileSets[symbol.PackagePath], cleanupType)
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

type providerProblem struct {
	kind    string
	message string
}

type resultMetadata struct {
	output         types.Type
	returnsCleanup bool
	returnsError   bool
}

func analyzeProvider(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	fileSet *token.FileSet,
	cleanupType types.Type,
) (Provider, *Diagnostic) {
	label := symbol.DisplayLabel
	if label == "" {
		label = symbol.ID
	}
	signature, problem := providerSignature(occurrence, symbol, label)
	if problem != nil {
		diagnostic := symbolDiagnostic(occurrence, symbol, problem.kind, problem.message)
		return Provider{}, &diagnostic
	}
	results, problem := analyzeResults(signature.Results(), cleanupType, label)
	if problem != nil {
		diagnostic := symbolDiagnostic(occurrence, symbol, problem.kind, problem.message)
		return Provider{}, &diagnostic
	}

	dependencies := providerDependencies(signature, fileSet)
	return Provider{
		Symbol:           symbol,
		SymbolID:         symbol.ID,
		Name:             symbol.Name,
		PackagePath:      symbol.PackagePath,
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
		Output:           results.output,
		OutputTypeID:     TypeID(results.output),
		Dependencies:     dependencies,
		ReturnsCleanup:   results.returnsCleanup,
		ReturnsError:     results.returnsError,
	}, nil
}

func providerSignature(
	occurrence resolve.Occurrence,
	symbol load.Symbol,
	label string,
) (*types.Signature, *providerProblem) {
	if occurrence.Target != annotation.TargetFunction || symbol.Kind != load.SymbolFunction || symbol.Receiver != "" {
		return nil, &providerProblem{
			kind:    "invalid-target",
			message: fmt.Sprintf("@Bean %s must target a package-level function; provider methods and other declarations are not supported", label),
		}
	}
	if len(occurrence.Annotation.Arguments) != 0 {
		return nil, &providerProblem{
			kind:    "arguments",
			message: fmt.Sprintf("@Bean provider %s does not accept annotation arguments", label),
		}
	}
	signature := symbol.Signature
	if signature == nil {
		return nil, &providerProblem{
			kind:    "missing-signature",
			message: fmt.Sprintf("@Bean provider %s has no typed function signature", label),
		}
	}
	if signature.TypeParams() != nil && signature.TypeParams().Len() > 0 {
		return nil, signatureProblem("generic", label, "generic provider functions are not supported")
	}
	if signature.Variadic() {
		return nil, signatureProblem("variadic", label, "variadic provider functions are not supported")
	}
	return signature, nil
}

func analyzeResults(results *types.Tuple, cleanupType types.Type, label string) (resultMetadata, *providerProblem) {
	switch results.Len() {
	case 0:
		return resultMetadata{}, signatureProblem(
			"zero-results",
			label,
			"provider functions must return one provided value",
		)
	case 1:
		return analyzeOneResult(results, label)
	case 2:
		return analyzeTwoResults(results, cleanupType, label)
	case 3:
		return analyzeThreeResults(results, cleanupType, label)
	default:
		return analyzeExcessResults(results, cleanupType, label)
	}
}

func analyzeOneResult(results *types.Tuple, label string) (resultMetadata, *providerProblem) {
	output := results.At(0).Type()
	if isError(output) {
		return resultMetadata{}, signatureProblem("error-only", label, "error cannot be the only result")
	}
	return resultMetadata{output: output}, nil
}

func analyzeTwoResults(
	results *types.Tuple,
	cleanupType types.Type,
	label string,
) (resultMetadata, *providerProblem) {
	first := results.At(0).Type()
	second := results.At(1).Type()
	if isError(first) {
		return resultMetadata{}, signatureProblem(
			"error-position",
			label,
			"error must be the final and only additional result",
		)
	}
	switch {
	case isError(second):
		return resultMetadata{output: first, returnsError: true}, nil
	case isCleanup(second, cleanupType):
		return resultMetadata{output: first, returnsCleanup: true}, nil
	default:
		return resultMetadata{}, signatureProblem(
			"invalid-second-result",
			label,
			"provider functions may return only one provided value; the second result must be lifecycle.Cleanup or error",
		)
	}
}

func analyzeThreeResults(
	results *types.Tuple,
	cleanupType types.Type,
	label string,
) (resultMetadata, *providerProblem) {
	first := results.At(0).Type()
	second := results.At(1).Type()
	third := results.At(2).Type()
	errorResults, cleanupResults := metadataCounts([]types.Type{second, third}, cleanupType)
	if cleanupResults > 1 {
		return resultMetadata{}, signatureProblem(
			"too-many-results",
			label,
			"provider functions may return at most one lifecycle.Cleanup metadata result",
		)
	}
	if errorResults > 1 {
		return resultMetadata{}, signatureProblem(
			"too-many-results",
			label,
			"provider functions may return at most one error result",
		)
	}
	if isError(first) {
		return resultMetadata{}, signatureProblem(
			"error-position",
			label,
			"error must be the final and only additional result",
		)
	}
	if !isCleanup(second, cleanupType) {
		reason := "the second result must be lifecycle.Cleanup"
		if isError(second) {
			reason = "error must follow lifecycle.Cleanup as the final result"
		}
		return resultMetadata{}, signatureProblem("cleanup-position", label, reason)
	}
	if !isError(third) {
		reason := "the third result must be error"
		if isCleanup(third, cleanupType) {
			reason = "only one lifecycle.Cleanup metadata result is allowed and it must be second"
		}
		return resultMetadata{}, signatureProblem("error-position", label, reason)
	}
	return resultMetadata{output: first, returnsCleanup: true, returnsError: true}, nil
}

func analyzeExcessResults(
	results *types.Tuple,
	cleanupType types.Type,
	label string,
) (resultMetadata, *providerProblem) {
	metadata := make([]types.Type, 0, results.Len())
	for variable := range results.Variables() {
		metadata = append(metadata, variable.Type())
	}
	errorResults, cleanupResults := metadataCounts(metadata, cleanupType)
	reason := "provider functions may return only one provided value, one optional lifecycle.Cleanup, and one optional final error"
	if cleanupResults > 1 {
		reason = "provider functions may return at most one lifecycle.Cleanup result"
	} else if errorResults > 1 {
		reason = "provider functions may return at most one error result"
	}
	return resultMetadata{}, signatureProblem("too-many-results", label, reason)
}

func metadataCounts(metadata []types.Type, cleanupType types.Type) (errorResults, cleanupResults int) {
	for _, result := range metadata {
		if isError(result) {
			errorResults++
		}
		if isCleanup(result, cleanupType) {
			cleanupResults++
		}
	}
	return errorResults, cleanupResults
}

func providerDependencies(signature *types.Signature, fileSet *token.FileSet) []Dependency {
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
	return dependencies
}

func signatureProblem(kind, label, reason string) *providerProblem {
	return &providerProblem{kind: kind, message: invalidSignatureMessage(label, reason)}
}

func invalidSignatureMessage(label, reason string) string {
	return fmt.Sprintf("@Bean provider %s has an unsupported signature: %s; accepted forms are %s", label, reason, acceptedSignature)
}

func isError(value types.Type) bool {
	return value != nil && types.Identical(value, errorType)
}

func canonicalCleanupType(program *load.Program) types.Type {
	cleanup := loadedNamedType(program, cleanupPackagePath, cleanupTypeName)
	contextType := loadedNamedType(program, "context", "Context")
	if cleanup == nil || contextType == nil {
		return nil
	}
	signature, ok := cleanup.Underlying().(*types.Signature)
	if !ok || signature.Variadic() ||
		(signature.TypeParams() != nil && signature.TypeParams().Len() > 0) ||
		signature.Params().Len() != 1 || signature.Results().Len() != 1 {
		return nil
	}
	if !types.Identical(signature.Params().At(0).Type(), contextType) ||
		!isError(signature.Results().At(0).Type()) {
		return nil
	}
	return cleanup
}

func loadedNamedType(program *load.Program, packagePath, typeName string) *types.Named {
	if program == nil {
		return nil
	}
	seen := make(map[*types.Package]struct{})
	var found *types.Named
	valid := true
	var visit func(*types.Package)
	visit = func(pkg *types.Package) {
		if pkg == nil {
			return
		}
		if _, ok := seen[pkg]; ok {
			return
		}
		seen[pkg] = struct{}{}
		if pkg.Path() == packagePath {
			object, ok := pkg.Scope().Lookup(typeName).(*types.TypeName)
			if !ok || object.IsAlias() {
				valid = false
			} else {
				named, ok := object.Type().(*types.Named)
				switch {
				case !ok || named.Obj() != object:
					valid = false
				case found == nil:
					found = named
				case !types.Identical(found, named):
					valid = false
				}
			}
		}
		for _, imported := range pkg.Imports() {
			visit(imported)
		}
	}
	for _, pkg := range program.Packages() {
		visit(pkg.Types)
	}
	if !valid {
		return nil
	}
	return found
}

func isCleanup(value, cleanupType types.Type) bool {
	return value != nil && cleanupType != nil && types.Identical(value, cleanupType)
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
