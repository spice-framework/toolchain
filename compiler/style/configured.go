package style

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/controller"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

type functionContributions map[sdk.ContributionKind]struct{}

func inferredWorkspaceRoot(program *load.Program) string {
	if program == nil {
		return ""
	}
	for _, pkg := range program.PrimaryPackages() {
		if pkg.Dir == "" || pkg.ModulePath == "" ||
			(pkg.Path != pkg.ModulePath && !strings.HasPrefix(pkg.Path, pkg.ModulePath+"/")) {
			continue
		}
		root := filepath.Clean(pkg.Dir)
		relative := strings.TrimPrefix(strings.TrimPrefix(pkg.Path, pkg.ModulePath), "/")
		if relative == "" {
			return root
		}
		for range strings.Split(relative, "/") {
			root = filepath.Dir(root)
		}
		return root
	}
	return ""
}

func configuredSourceOwnershipDiagnostics(
	program *load.Program,
	workspaceRoot string,
	configuration Configuration,
) []Diagnostic {
	var diagnostics []Diagnostic
	for _, pkg := range program.PrimaryPackages() {
		for _, file := range pkg.Files {
			relative, inside := workspaceRelativeFile(workspaceRoot, file.PhysicalPath)
			if inside && pathUnderConfigurationRoot(relative, configuration.SourceRoots) {
				continue
			}
			position := token.Position{Filename: file.PhysicalPath, Line: 1, Column: 1}
			physical := position
			if pkg.Raw != nil && pkg.Raw.Fset != nil && file.Syntax != nil {
				position = pkg.Raw.Fset.PositionFor(file.Syntax.Name.Pos(), true)
				physical = pkg.Raw.Fset.PositionFor(file.Syntax.Name.Pos(), false)
			}
			diagnostics = append(diagnostics, Diagnostic{
				Position: position, PhysicalPosition: physical,
				Kind:    "configuration.source-selection",
				Message: "loaded primary file is outside the exact configured source roots",
			})
		}
	}
	return diagnostics
}

func workspaceRelativeFile(workspaceRoot, file string) (string, bool) {
	if workspaceRoot == "" || file == "" {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(workspaceRoot), filepath.Clean(file))
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func pathUnderConfigurationRoot(file string, roots []string) bool {
	for _, root := range roots {
		if file == root || strings.HasPrefix(file, root+"/") {
			return true
		}
	}
	return false
}

func configuredFreeFunctionDiagnostics(
	symbols []load.Symbol,
	files map[string]sourceFile,
	typesByFile map[string][]string,
	resolution resolve.Result,
	packageNames map[string]string,
	configuration Configuration,
) []Diagnostic {
	contributions := functionContributionIndex(resolution)
	counts := contributionCounts(symbols, contributions)
	var diagnostics []Diagnostic
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolFunction || symbol.Receiver != "" || symbol.Name == "init" {
			continue
		}
		file := cleanFile(symbol.PhysicalPosition.Filename)
		source, handwritten := files[file]
		if !handwritten {
			continue
		}
		if configuredFunctionException(
			symbol,
			source.relative,
			file,
			contributions[symbol.ID],
			counts,
			packageNames,
			configuration.PackageFunctionExceptions,
		) {
			continue
		}
		if configuredAssociatedFunction(symbol, typesByFile[file]) {
			continue
		}
		diagnostics = append(diagnostics, symbolDiagnostic(
			symbol,
			"function.package-level",
			fmt.Sprintf(
				"package function %s is not a type-associated constructor/parser or exact configured boundary under profile %s",
				symbol.DisplayLabel,
				ProfileJavaStructured,
			),
		))
	}
	return diagnostics
}

func configuredAssociatedFunction(symbol load.Symbol, typeNames []string) bool {
	if symbol.Signature == nil || symbol.Signature.Results() == nil ||
		symbol.Signature.Results().Len() == 0 {
		return false
	}
	result := types.Unalias(symbol.Signature.Results().At(0).Type())
	if pointer, ok := result.(*types.Pointer); ok {
		result = types.Unalias(pointer.Elem())
	}
	named, ok := result.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil ||
		named.Obj().Pkg().Path() != symbol.PackagePath {
		return false
	}
	name := named.Obj().Name()
	if !slices.Contains(typeNames, name) {
		return false
	}
	return symbol.Name == "New"+name || symbol.Name == "Parse"+name ||
		strings.HasPrefix(symbol.Name, "New"+name+"From") &&
			len(symbol.Name) > len("New")+len(name)+len("From")
}

func configuredStructuralDiagnostics(
	program *load.Program,
	files map[string]sourceFile,
	configuration Configuration,
) []Diagnostic {
	var diagnostics []Diagnostic
	packageByFile := make(map[string]load.Package)
	for _, pkg := range program.PrimaryPackages() {
		for _, source := range pkg.Files {
			packageByFile[cleanFile(source.PhysicalPath)] = pkg
		}
	}
	for _, filePath := range sortedFilePaths(files) {
		file := files[filePath]
		pkg := packageByFile[filePath]
		if tokenFile := file.fileSet.File(file.syntax.Pos()); tokenFile != nil &&
			tokenFile.LineCount() > configuration.Rules.MaxTypeFileLines {
			position, physical := positions(file, file.syntax.Name.Pos())
			diagnostics = append(diagnostics, Diagnostic{
				Position: position, PhysicalPosition: physical,
				Kind: "file.lines",
				Message: fmt.Sprintf(
					"handwritten production file has %d lines; configured maximum is %d",
					tokenFile.LineCount(),
					configuration.Rules.MaxTypeFileLines,
				),
			})
		}
		for _, declaration := range file.syntax.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if configuration.Rules.ContextFirst != RuleLevelOff {
				diagnostics = append(
					diagnostics,
					contextSignatureDiagnostics(file, pkg.TypesInfo, function)...,
				)
			}
			if configuration.Rules.ErrorLast != RuleLevelOff {
				diagnostics = append(
					diagnostics,
					errorResultDiagnostics(file, pkg.TypesInfo, function)...,
				)
			}
		}
		if configuration.Rules.ContextFirst != RuleLevelOff {
			diagnostics = append(
				diagnostics,
				storedContextDiagnostics(file, pkg.TypesInfo)...,
			)
		}
	}
	if configuration.Rules.MethodsInPrimaryFile != RuleLevelOff {
		diagnostics = append(
			diagnostics,
			receiverNameDiagnostics(program.PrimarySymbols(), files)...,
		)
	}
	return diagnostics
}

func contextSignatureDiagnostics(
	file sourceFile,
	information *types.Info,
	function *ast.FuncDecl,
) []Diagnostic {
	if information == nil || function.Type.Params == nil {
		return nil
	}
	var diagnostics []Diagnostic
	parameterIndex := 0
	for _, field := range function.Type.Params.List {
		count := max(1, len(field.Names))
		if !isExactContext(information.TypeOf(field.Type)) ||
			parameterIndex == 0 && count == 1 {
			parameterIndex += count
			continue
		}
		position, physical := positions(file, field.Type.Pos())
		diagnostics = append(diagnostics, Diagnostic{
			Position: position, PhysicalPosition: physical,
			Kind:    "context.first",
			Message: "context.Context must be the first parameter",
		})
		parameterIndex += count
	}
	return diagnostics
}

func errorResultDiagnostics(
	file sourceFile,
	information *types.Info,
	function *ast.FuncDecl,
) []Diagnostic {
	if information == nil || function.Type.Results == nil {
		return nil
	}
	var diagnostics []Diagnostic
	resultCount := 0
	for _, field := range function.Type.Results.List {
		resultCount += max(1, len(field.Names))
	}
	resultIndex := 0
	for _, field := range function.Type.Results.List {
		count := max(1, len(field.Names))
		if (resultIndex+count == resultCount && count == 1) ||
			!types.Identical(information.TypeOf(field.Type), types.Universe.Lookup("error").Type()) {
			resultIndex += count
			continue
		}
		position, physical := positions(file, field.Type.Pos())
		diagnostics = append(diagnostics, Diagnostic{
			Position: position, PhysicalPosition: physical,
			Kind:    "error.last",
			Message: "error must be the final result",
		})
		resultIndex += count
	}
	return diagnostics
}

func storedContextDiagnostics(
	file sourceFile,
	information *types.Info,
) []Diagnostic {
	if information == nil {
		return nil
	}
	var diagnostics []Diagnostic
	for _, declaration := range file.syntax.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpecification.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				if !isExactContext(information.TypeOf(field.Type)) {
					continue
				}
				position, physical := positions(file, field.Type.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position: position, PhysicalPosition: physical,
					Kind:    "context.stored",
					Message: "context.Context must not be stored in a struct",
				})
			}
		}
	}
	return diagnostics
}

func isExactContext(value types.Type) bool {
	if value == nil {
		return false
	}
	named, ok := types.Unalias(value).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "context" && named.Obj().Name() == "Context"
}

func receiverNameDiagnostics(
	symbols []load.Symbol,
	files map[string]sourceFile,
) []Diagnostic {
	seen := make(map[string]string)
	var diagnostics []Diagnostic
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolMethod {
			continue
		}
		file, handwritten := files[cleanFile(symbol.PhysicalPosition.Filename)]
		if !handwritten {
			continue
		}
		declaration, ok := symbol.Node.(*ast.FuncDecl)
		if !ok || declaration.Recv == nil || len(declaration.Recv.List) != 1 ||
			len(declaration.Recv.List[0].Names) != 1 {
			continue
		}
		name := declaration.Recv.List[0].Names[0]
		key := symbol.PackagePath + "\x00" + symbol.Receiver
		if previous := seen[key]; previous != "" && previous != name.Name {
			position, physical := positions(file, name.Pos())
			diagnostics = append(diagnostics, Diagnostic{
				Position: position, PhysicalPosition: physical,
				SymbolID: symbol.ID,
				Kind:     "receiver.name",
				Message: fmt.Sprintf(
					"receiver name %s must match established name %s for %s",
					name.Name,
					previous,
					symbol.Receiver,
				),
			})
			continue
		}
		seen[key] = name.Name
	}
	return diagnostics
}

func functionContributionIndex(resolution resolve.Result) map[string]functionContributions {
	result := make(map[string]functionContributions)
	for _, occurrence := range resolution.Occurrences {
		if occurrence.SymbolID == "" {
			continue
		}
		for _, kind := range []sdk.ContributionKind{
			sdk.ContributionApplication,
			sdk.ContributionEventTopic,
			sdk.ContributionProvider,
		} {
			if occurrence.HasContribution(kind) {
				if result[occurrence.SymbolID] == nil {
					result[occurrence.SymbolID] = make(functionContributions)
				}
				result[occurrence.SymbolID][kind] = struct{}{}
			}
		}
	}
	return result
}

func contributionCounts(
	symbols []load.Symbol,
	contributions map[string]functionContributions,
) map[string]int {
	result := make(map[string]int)
	for _, symbol := range symbols {
		file := cleanFile(symbol.PhysicalPosition.Filename)
		for kind := range contributions[symbol.ID] {
			result[file+"\x00"+string(kind)]++
		}
	}
	return result
}

func configuredFunctionException(
	symbol load.Symbol,
	relative string,
	physical string,
	contributions functionContributions,
	counts map[string]int,
	packageNames map[string]string,
	exceptions []PackageFunctionException,
) bool {
	for _, exception := range exceptions {
		if !globMatches(exception.Glob, relative) {
			continue
		}
		switch {
		case exception.Symbol != "":
			if exception.Symbol != symbol.Name {
				continue
			}
			if symbol.Name == "main" &&
				(packageNames[symbol.PackagePath] != "main" ||
					!strings.EqualFold(filepath.Base(physical), "main.go")) {
				continue
			}
			return true
		case exception.SymbolPattern != "":
			matched, err := regexpMatch(exception.SymbolPattern, symbol.Name)
			if err == nil && matched {
				return true
			}
		case exception.ContributionKind != "":
			kind := sdk.ContributionKind(exception.ContributionKind)
			if _, found := contributions[kind]; !found {
				continue
			}
			if exception.Maximum > 0 && counts[physical+"\x00"+string(kind)] > exception.Maximum {
				continue
			}
			if kind == sdk.ContributionApplication && !emptyProofMarker(symbol) {
				continue
			}
			return true
		}
	}
	return false
}

func regexpMatch(pattern, value string) (bool, error) {
	return regexp.MatchString(pattern, value)
}

func configuredBoundaryFile(file string, patterns []string) bool {
	for _, pattern := range patterns {
		if globMatches(pattern, file) {
			return true
		}
	}
	return false
}

func applyPackageVariableExceptions(
	diagnostics []Diagnostic,
	symbols []load.Symbol,
	files map[string]sourceFile,
	exceptions []PackageVariableException,
) []Diagnostic {
	allowed := make(map[string]struct{})
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolVariable {
			continue
		}
		variable, ok := symbol.Object.(*types.Var)
		if !ok {
			continue
		}
		file := cleanFile(symbol.PhysicalPosition.Filename)
		source, handwritten := files[file]
		if !handwritten {
			continue
		}
		for _, exception := range exceptions {
			if exception.Symbol == symbol.Name &&
				exception.Type == types.TypeString(variable.Type(), packagePathQualifier) &&
				globMatches(exception.Glob, source.relative) {
				allowed[physicalDiagnosticKey(symbol.PhysicalPosition)] = struct{}{}
				break
			}
		}
	}
	result := make([]Diagnostic, 0, len(diagnostics))
	for _, item := range diagnostics {
		if item.Kind == "package.mutable-global" {
			if _, found := allowed[physicalDiagnosticKey(item.PhysicalPosition)]; found {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func physicalDiagnosticKey(position token.Position) string {
	return cleanFile(position.Filename) + "\x00" + strconv.Itoa(position.Offset)
}

func packagePathQualifier(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func globMatches(pattern, value string) bool {
	pattern = filepath.ToSlash(pattern)
	value = filepath.ToSlash(value)
	if strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
		for {
			matched, err := filepath.Match(
				filepath.FromSlash(pattern),
				filepath.FromSlash(value),
			)
			if err == nil && matched {
				return true
			}
			separator := strings.IndexByte(value, '/')
			if separator < 0 {
				return false
			}
			value = value[separator+1:]
		}
	}
	matched, err := filepath.Match(
		filepath.FromSlash(pattern),
		filepath.FromSlash(value),
	)
	return err == nil && matched
}

func emptyProofMarker(symbol load.Symbol) bool {
	declaration, ok := symbol.Node.(*ast.FuncDecl)
	return ok && declaration.Body != nil && len(declaration.Body.List) == 0
}

func configuredTypedDiagnostics(
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	files map[string]sourceFile,
	configuration Configuration,
) []Diagnostic {
	var diagnostics []Diagnostic
	if configuration.Rules.ExplicitManagedScopes != RuleLevelOff {
		diagnostics = append(diagnostics, managedScopeDiagnostics(resolution, providers)...)
	}
	if configuration.Rules.PrivateManagedFields != RuleLevelOff {
		diagnostics = append(diagnostics, managedFieldDiagnostics(program, providers)...)
	}
	modules := modulith.Build(program, resolution)
	if configuration.Rules.ModuleOwnership != RuleLevelOff {
		diagnostics = append(diagnostics, moduleOwnershipDiagnostics(program, modules, files)...)
	}
	if configuration.Rules.RouteClassification != RuleLevelOff {
		diagnostics = append(
			diagnostics,
			routeClassificationDiagnostics(
				controller.Build(program, resolution, providers, modules),
				configuration.PublicRoutes,
			)...,
		)
	}
	return diagnostics
}

func managedScopeDiagnostics(
	resolution resolve.Result,
	providers provider.Catalog,
) []Diagnostic {
	explicit := make(map[string]struct{})
	for _, occurrence := range resolution.Occurrences {
		contribution, found := occurrence.Contribution(sdk.ContributionBeanMetadata)
		if found && contribution.BeanMetadata.Scope != "" {
			explicit[occurrence.SymbolID] = struct{}{}
		}
	}
	var diagnostics []Diagnostic
	for _, item := range providers.Providers() {
		if !applicationManagedProvider(item) {
			continue
		}
		if _, found := explicit[item.SymbolID]; found {
			continue
		}
		diagnostics = append(diagnostics, symbolDiagnostic(
			item.Symbol,
			"bean.scope",
			fmt.Sprintf(
				"managed bean %s must declare an explicit @Singleton, @Prototype, @RequestScope, or @SessionScope",
				item.Symbol.DisplayLabel,
			),
		))
	}
	return diagnostics
}

func applicationManagedProvider(item provider.Provider) bool {
	return (item.Source == provider.SourceBean || item.Source == provider.SourceStereotype) &&
		item.Role != "configuration"
}

func managedFieldDiagnostics(
	program *load.Program,
	providers provider.Catalog,
) []Diagnostic {
	fileSets := make(map[string]*token.FileSet)
	for _, pkg := range program.PrimaryPackages() {
		if pkg.Raw != nil {
			fileSets[pkg.Path] = pkg.Raw.Fset
		}
	}
	var diagnostics []Diagnostic
	for _, item := range providers.Providers() {
		if item.Source != provider.SourceStereotype {
			continue
		}
		underlying := types.Unalias(item.Output)
		if pointer, ok := underlying.(*types.Pointer); ok {
			underlying = types.Unalias(pointer.Elem())
		}
		named, ok := underlying.(*types.Named)
		if !ok {
			continue
		}
		structure, ok := named.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for index := 0; index < structure.NumFields(); index++ {
			field := structure.Field(index)
			if !field.Exported() {
				continue
			}
			position := item.Position
			physical := item.PhysicalPosition
			if fileSet := fileSets[item.PackagePath]; fileSet != nil {
				position = fileSet.PositionFor(field.Pos(), true)
				physical = fileSet.PositionFor(field.Pos(), false)
			}
			diagnostics = append(diagnostics, Diagnostic{
				Position: position, PhysicalPosition: physical,
				SymbolID: item.SymbolID,
				Kind:     "bean.fields-private",
				Message: fmt.Sprintf(
					"managed component %s exposes field %s; managed fields must be private",
					item.Symbol.DisplayLabel,
					field.Name(),
				),
			})
		}
	}
	return diagnostics
}

func moduleOwnershipDiagnostics(
	program *load.Program,
	modules modulith.Model,
	files map[string]sourceFile,
) []Diagnostic {
	handwrittenPackages := make(map[string]struct{})
	for _, file := range files {
		handwrittenPackages[file.packagePath] = struct{}{}
	}
	var diagnostics []Diagnostic
	for _, item := range modules.Diagnostics() {
		diagnostics = append(diagnostics, Diagnostic{
			Position: item.Position, PhysicalPosition: item.PhysicalPosition,
			Kind: configuredModuleDiagnosticKind(item), Message: item.Message,
		})
	}
	for _, pkg := range program.PrimaryPackages() {
		if _, handwritten := handwrittenPackages[pkg.Path]; !handwritten {
			continue
		}
		if _, owned := modules.Owner(pkg.Path); owned {
			continue
		}
		position, physical := packageDeclarationPosition(pkg)
		diagnostics = append(diagnostics, Diagnostic{
			Position: position, PhysicalPosition: physical,
			Kind: "package.module",
			Message: fmt.Sprintf(
				"application package %s must be owned by exactly one @Module root",
				pkg.Path,
			),
		})
	}
	return diagnostics
}

func configuredModuleDiagnosticKind(item modulith.Diagnostic) string {
	switch item.Kind {
	case "duplicate-module", "missing-package":
		return "package.module"
	case "invalid-target":
		if strings.HasPrefix(item.Message, "@Module") {
			return "package.module"
		}
	}
	return "module.dependency"
}

func packageDeclarationPosition(pkg load.Package) (token.Position, token.Position) {
	if pkg.Raw == nil || pkg.Raw.Fset == nil {
		return token.Position{}, token.Position{}
	}
	var positions [][2]token.Position
	for _, syntax := range pkg.Syntax {
		if syntax == nil || syntax.Name == nil {
			continue
		}
		positions = append(positions, [2]token.Position{
			pkg.Raw.Fset.PositionFor(syntax.Name.Pos(), true),
			pkg.Raw.Fset.PositionFor(syntax.Name.Pos(), false),
		})
	}
	sort.SliceStable(positions, func(left, right int) bool {
		return positions[left][1].Filename < positions[right][1].Filename
	})
	if len(positions) == 0 {
		return token.Position{}, token.Position{}
	}
	return positions[0][0], positions[0][1]
}

func routeClassificationDiagnostics(
	catalog controller.Catalog,
	public []PublicRoute,
) []Diagnostic {
	exceptions := make(map[string]struct{}, len(public))
	for _, item := range public {
		exceptions[item.Package+"\x00"+item.Receiver+"\x00"+item.Method] = struct{}{}
	}
	var diagnostics []Diagnostic
	for _, owner := range catalog.Controllers() {
		for _, route := range owner.Routes() {
			if _, protected := route.Authorization(); protected {
				continue
			}
			key := owner.PackagePath + "\x00" + owner.Name + "\x00" + route.Name
			if _, allowed := exceptions[key]; allowed {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Position: route.Position, PhysicalPosition: route.PhysicalPosition,
				SymbolID: route.SymbolID,
				Kind:     "route.classification",
				Message: fmt.Sprintf(
					"route %s.%s is neither protected by typed authorization nor listed as an exact reviewed public route",
					owner.Name,
					route.Name,
				),
			})
		}
	}
	return diagnostics
}
