// Package lifecycle validates explicit lifecycle-hook metadata against the
// provider catalog. It records typed metadata only and never executes hooks.
package lifecycle

import (
	"fmt"
	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"go/token"
	"go/types"
	"sort"
)

const acceptedHookSignature = "func(receiver)(context.Context) error"

type Kind string

const (
	Start Kind = "start"
	Stop  Kind = "stop"
)

var errorType = types.Universe.Lookup("error").Type()
var anyType = types.Universe.Lookup("any").Type()

type Hook struct {
	Kind             Kind
	Method           load.Symbol
	MethodID         string
	ProviderID       string
	Receiver         types.Type
	ReceiverTypeID   string
	Position         token.Position
	PhysicalPosition token.Position
}

type Component struct {
	Provider provider.Provider
	Start    *Hook
	Stop     *Hook
}

type Diagnostic struct {
	Position         token.Position
	PhysicalPosition token.Position
	MethodID         string
	ProviderID       string
	Kind             string
	Message          string
}

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

type Catalog struct {
	components  []Component
	diagnostics []Diagnostic
}

func (c Catalog) Components() []Component {
	result := make([]Component, len(c.components))
	copy(result, c.components)
	for index := range result {
		result[index].Provider.Dependencies = append([]provider.Dependency(nil), result[index].Provider.Dependencies...)
		if result[index].Start != nil {
			copyHook := *result[index].Start
			result[index].Start = &copyHook
		}
		if result[index].Stop != nil {
			copyHook := *result[index].Stop
			result[index].Stop = &copyHook
		}
	}
	return result
}

func (c Catalog) Diagnostics() []Diagnostic { return append([]Diagnostic(nil), c.diagnostics...) }

type buildStats struct{ identityChecks int }

func Build(program *load.Program, resolution resolve.Result, providers provider.Catalog) Catalog {
	catalog, _ := build(program, resolution, providers)
	return catalog
}
func build(program *load.Program, resolution resolve.Result, providers provider.Catalog) (Catalog, buildStats) {
	catalog := Catalog{}
	stats := buildStats{}
	if program == nil {
		catalog.diagnostics = []Diagnostic{{Kind: "internal", Message: "lifecycle catalog requires a loaded program"}}
		return catalog, stats
	}
	symbols := make(map[string]load.Symbol)
	for _, symbol := range program.Symbols() {
		symbols[symbol.ID] = symbol
	}
	providerIndex := make(map[string][]provider.Provider)
	for _, item := range providers.Providers() {
		key := semanticTypeKey(item.Output)
		providerIndex[key] = append(providerIndex[key], item)
	}
	contextType := canonicalContextType(program)
	components := make(map[string]*Component)
	methodKinds := make(map[string]map[Kind][]resolve.Occurrence)
	for _, occurrence := range resolution.Occurrences {
		kind, lifecycleOccurrence := occurrenceKind(occurrence)
		if !lifecycleOccurrence {
			continue
		}
		roles := methodKinds[occurrence.SymbolID]
		if roles == nil {
			roles = make(map[Kind][]resolve.Occurrence)
			methodKinds[occurrence.SymbolID] = roles
		}
		roles[kind] = append(roles[kind], occurrence)
	}
	methodIDs := make([]string, 0, len(methodKinds))
	for methodID := range methodKinds {
		methodIDs = append(methodIDs, methodID)
	}
	sort.Strings(methodIDs)
	for _, methodID := range methodIDs {
		roles := methodKinds[methodID]
		for _, occurrences := range roles {
			sort.SliceStable(occurrences, func(i, j int) bool {
				if occurrences[i].PhysicalFile != occurrences[j].PhysicalFile {
					return occurrences[i].PhysicalFile < occurrences[j].PhysicalFile
				}
				return occurrences[i].PhysicalOffset < occurrences[j].PhysicalOffset
			})
		}
		if len(roles) > 1 {
			occurrence := roles[Start][0]
			catalog.diagnostics = append(catalog.diagnostics, occurrenceDiagnostic(occurrence, methodID, "conflicting-annotations", fmt.Sprintf("method %q may carry only one lifecycle annotation; @OnStart and @OnStop cannot be combined", occurrence.Name)))
			continue
		}
		for _, kind := range []Kind{Start, Stop} {
			occurrences := roles[kind]
			if len(occurrences) == 0 {
				continue
			}
			occurrence := occurrences[0]
			if len(occurrences) > 1 {
				catalog.diagnostics = append(catalog.diagnostics, occurrenceDiagnostic(occurrence, methodID, "duplicate-annotation", fmt.Sprintf("method %q carries @On%s more than once; lifecycle annotations are not repeatable", occurrence.Name, kindTitle(kind))))
				continue
			}
			symbol, ok := symbols[methodID]
			if !ok {
				catalog.diagnostics = append(catalog.diagnostics, occurrenceDiagnostic(occurrence, methodID, "missing-symbol", fmt.Sprintf("@On%s target %q has no stable typed method symbol", kindTitle(kind), occurrence.Name)))
				continue
			}
			hook, owner, diagnostic := analyzeHook(occurrence, kind, symbol, contextType, providerIndex, &stats)
			if diagnostic != nil {
				catalog.diagnostics = append(catalog.diagnostics, *diagnostic)
				continue
			}
			component := components[owner.SymbolID]
			if component == nil {
				copyProvider := owner
				copyProvider.Dependencies = append([]provider.Dependency(nil), owner.Dependencies...)
				component = &Component{Provider: copyProvider}
				components[owner.SymbolID] = component
			}
			existing := component.Start
			if kind == Stop {
				existing = component.Stop
			}
			if existing != nil {
				catalog.diagnostics = append(catalog.diagnostics, hookDiagnostic(hook, "duplicate-"+string(kind), fmt.Sprintf("provider %s has multiple @On%s hooks: %s and %s", owner.Symbol.DisplayLabel, kindTitle(kind), existing.Method.DisplayLabel, hook.Method.DisplayLabel)))
				continue
			}
			copyHook := hook
			if kind == Start {
				component.Start = &copyHook
			} else {
				component.Stop = &copyHook
			}
		}
	}
	for _, component := range components {
		if component.Start == nil && component.Stop != nil {
			catalog.diagnostics = append(catalog.diagnostics, hookDiagnostic(*component.Stop, "stop-without-start", fmt.Sprintf("@OnStop method %s has no corresponding @OnStart hook for provider %s", component.Stop.Method.DisplayLabel, component.Provider.Symbol.DisplayLabel)))
		}
		catalog.components = append(catalog.components, *component)
	}
	sort.Slice(catalog.components, func(i, j int) bool {
		return catalog.components[i].Provider.SymbolID < catalog.components[j].Provider.SymbolID
	})
	sortDiagnostics(catalog.diagnostics)
	return catalog, stats
}
func analyzeHook(occurrence resolve.Occurrence, kind Kind, symbol load.Symbol, contextType types.Type, providerIndex map[string][]provider.Provider, stats *buildStats) (Hook, provider.Provider, *Diagnostic) {
	fail := func(failureKind, reason string) (Hook, provider.Provider, *Diagnostic) {
		diagnostic := symbolDiagnostic(occurrence, symbol, failureKind, fmt.Sprintf("@On%s method %s is invalid: %s; accepted form is %s", kindTitle(kind), methodLabel(symbol), reason, acceptedHookSignature))
		return Hook{}, provider.Provider{}, &diagnostic
	}
	if occurrence.Target != annotation.TargetMethod || symbol.Kind != load.SymbolMethod || symbol.Signature == nil || symbol.Signature.Recv() == nil {
		return fail("invalid-target", "lifecycle hooks must target ordinary methods")
	}
	if len(occurrence.Annotation.Arguments) != 0 {
		return fail("arguments", "lifecycle annotations do not accept arguments")
	}
	signature := symbol.Signature
	if (signature.RecvTypeParams() != nil && signature.RecvTypeParams().Len() > 0) || (signature.TypeParams() != nil && signature.TypeParams().Len() > 0) {
		return fail("generic-receiver", "receiver-generic lifecycle methods are not supported")
	}
	if signature.Variadic() {
		return fail("variadic", "lifecycle methods must be non-variadic")
	}
	if signature.Params().Len() != 1 {
		return fail("parameter-count", fmt.Sprintf("lifecycle methods require exactly one explicit context parameter, got %d", signature.Params().Len()))
	}
	if contextType == nil {
		return fail("context-type", "canonical context.Context identity could not be established safely from the loaded Go 1.23 type universe")
	}
	if !types.Identical(signature.Params().At(0).Type(), contextType) {
		return fail("context-type", fmt.Sprintf("parameter 0 must be the exact loaded context.Context type, got %s", provider.TypeID(signature.Params().At(0).Type())))
	}
	if signature.Results().Len() != 1 {
		return fail("result-count", fmt.Sprintf("lifecycle methods require exactly one error result, got %d", signature.Results().Len()))
	}
	if !types.Identical(signature.Results().At(0).Type(), errorType) {
		return fail("result-type", fmt.Sprintf("result 0 must be the exact predeclared error type, got %s", provider.TypeID(signature.Results().At(0).Type())))
	}
	receiver := signature.Recv().Type()
	candidates := providerIndex[semanticTypeKey(receiver)]
	var matches []provider.Provider
	for _, candidate := range candidates {
		stats.identityChecks++
		if types.Identical(receiver, candidate.Output) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return fail("receiver-provider", fmt.Sprintf("no @Bean provider produces exact receiver type %s; pointer/value convenience, assignability, interface implementation, and method promotion do not establish lifecycle ownership", provider.TypeID(receiver)))
	}
	if len(matches) != 1 {
		return fail("ambiguous-provider", fmt.Sprintf("receiver type %s is produced by %d providers; lifecycle ownership requires exactly one", provider.TypeID(receiver), len(matches)))
	}
	owner := matches[0]
	return Hook{
		Kind:             kind,
		Method:           symbol,
		MethodID:         symbol.ID,
		ProviderID:       owner.SymbolID,
		Receiver:         receiver,
		ReceiverTypeID:   provider.TypeID(receiver),
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
	}, owner, nil
}
func occurrenceKind(occurrence resolve.Occurrence) (Kind, bool) {
	switch occurrence.Annotation.Name {
	case "OnStart":
		return Start, true
	case "OnStop":
		return Stop, true
	default:
		return "", false
	}
}
func semanticTypeKey(value types.Type) string {
	if value == nil {
		return "<invalid>"
	}
	value = types.Unalias(value)
	switch typed := value.(type) {
	case *types.Pointer:
		return "*" + semanticTypeKey(typed.Elem())
	case *types.Named:
		origin := typed.Origin()
		object := origin.Obj()
		key := "named:"
		if object.Pkg() != nil {
			key += object.Pkg().Path() + "."
		}
		key += object.Name()
		if arguments := typed.TypeArgs(); arguments != nil && arguments.Len() > 0 {
			key += "["
			for index := 0; index < arguments.Len(); index++ {
				if index > 0 {
					key += ","
				}
				key += semanticTypeKey(arguments.At(index))
			}
			key += "]"
		}
		return key
	default:
		return provider.TypeID(value)
	}
}
func loadedNamedType(program *load.Program, packagePath, typeName string) *types.Named {
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
			} else if named, ok := object.Type().(*types.Named); !ok || named.Obj() != object {
				valid = false
			} else if found == nil {
				found = named
			} else if !types.Identical(found, named) {
				valid = false
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

func canonicalContextType(program *load.Program) types.Type {
	contextType := loadedNamedType(program, "context", "Context")
	timeType := loadedNamedType(program, "time", "Time")
	if contextType == nil || timeType == nil || !validContextDeclaration(contextType, timeType) {
		return nil
	}
	return contextType
}

func validContextDeclaration(contextType, timeType *types.Named) bool {
	contract, ok := contextType.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	contract.Complete()
	if contract.NumEmbeddeds() != 0 || contract.NumExplicitMethods() != 4 || contract.NumMethods() != 4 {
		return false
	}

	methods := make(map[string]*types.Signature, contract.NumMethods())
	for index := 0; index < contract.NumMethods(); index++ {
		method := contract.Method(index)
		if method.Pkg() != contextType.Obj().Pkg() {
			return false
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok || signature.Recv() == nil || !types.Identical(signature.Recv().Type(), contextType) ||
			signature.Variadic() ||
			(signature.TypeParams() != nil && signature.TypeParams().Len() != 0) ||
			(signature.RecvTypeParams() != nil && signature.RecvTypeParams().Len() != 0) {
			return false
		}
		if _, duplicate := methods[method.Name()]; duplicate {
			return false
		}
		methods[method.Name()] = signature
	}

	return validDeadlineMethod(methods["Deadline"], timeType) &&
		validDoneMethod(methods["Done"]) &&
		validErrMethod(methods["Err"]) &&
		validValueMethod(methods["Value"])
}

func validDeadlineMethod(signature *types.Signature, timeType types.Type) bool {
	return hasArity(signature, 0, 2) &&
		types.Identical(signature.Results().At(0).Type(), timeType) &&
		types.Identical(signature.Results().At(1).Type(), types.Typ[types.Bool])
}

func validDoneMethod(signature *types.Signature) bool {
	if !hasArity(signature, 0, 1) {
		return false
	}
	channel, ok := signature.Results().At(0).Type().(*types.Chan)
	if !ok || channel.Dir() != types.RecvOnly {
		return false
	}
	return types.Identical(channel.Elem(), types.NewStruct(nil, nil))
}

func validErrMethod(signature *types.Signature) bool {
	return hasArity(signature, 0, 1) && types.Identical(signature.Results().At(0).Type(), errorType)
}

func validValueMethod(signature *types.Signature) bool {
	return hasArity(signature, 1, 1) &&
		types.Identical(signature.Params().At(0).Type(), anyType) &&
		types.Identical(signature.Results().At(0).Type(), anyType)
}

func hasArity(signature *types.Signature, parameters, results int) bool {
	return signature != nil && signature.Params().Len() == parameters && signature.Results().Len() == results
}
func methodLabel(symbol load.Symbol) string {
	if symbol.DisplayLabel != "" {
		return symbol.DisplayLabel
	}
	if symbol.ID != "" {
		return symbol.ID
	}
	return symbol.Name
}
func kindTitle(kind Kind) string {
	if kind == Start {
		return "Start"
	}
	return "Stop"
}
func occurrenceDiagnostic(occurrence resolve.Occurrence, methodID, kind, message string) Diagnostic {
	return Diagnostic{
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: token.Position{Filename: occurrence.PhysicalFile, Offset: occurrence.PhysicalOffset},
		MethodID:         methodID,
		Kind:             kind,
		Message:          message,
	}
}
func symbolDiagnostic(occurrence resolve.Occurrence, symbol load.Symbol, kind, message string) Diagnostic {
	diagnostic := occurrenceDiagnostic(occurrence, symbol.ID, kind, message)
	if diagnostic.Position.Filename == "" {
		diagnostic.Position = symbol.Position
	}
	if diagnostic.PhysicalPosition.Filename == "" {
		diagnostic.PhysicalPosition = symbol.PhysicalPosition
	}
	return diagnostic
}
func hookDiagnostic(hook Hook, kind, message string) Diagnostic {
	return Diagnostic{
		Position:         hook.Position,
		PhysicalPosition: hook.PhysicalPosition,
		MethodID:         hook.MethodID,
		ProviderID:       hook.ProviderID,
		Kind:             kind,
		Message:          message,
	}
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
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.MethodID != right.MethodID {
			return left.MethodID < right.MethodID
		}
		return left.Message < right.Message
	})
}
