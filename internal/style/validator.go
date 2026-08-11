package style

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Validator owns one package's deterministic structural validation.
type Validator struct {
	pass          *analysis.Pass
	configuration Configuration
	workspaceRoot string
	files         []sourceFile
	types         map[string]namedType
}

// NewValidator constructs one package validator.
func NewValidator(
	pass *analysis.Pass,
	configuration Configuration,
	workspaceRoot string,
) *Validator {
	return &Validator{
		pass:          pass,
		configuration: configuration.Clone(),
		workspaceRoot: filepath.Clean(workspaceRoot),
		types:         make(map[string]namedType),
	}
}

// Validate reports every deterministic structural violation.
func (validator *Validator) Validate() {
	validator.collectFiles()
	validator.collectTypes()
	for index := range validator.files {
		file := &validator.files[index]
		validator.validateFile(file)
	}
	validator.validateMethods()
}

func (validator *Validator) collectFiles() {
	for _, syntax := range validator.pass.Files {
		position := validator.pass.Fset.PositionFor(syntax.Pos(), false)
		absolute := filepath.Clean(position.Filename)
		relative, err := filepath.Rel(validator.workspaceRoot, absolute)
		if err != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		if !validator.selected(relative) || validator.generated(syntax, relative) ||
			strings.HasSuffix(relative, "_test.go") {
			continue
		}
		file := sourceFile{
			path:      absolute,
			relative:  relative,
			lineCount: validator.lineCount(absolute),
			syntax:    syntax,
			fileSet:   validator.pass.Fset,
		}
		for _, declaration := range syntax.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				file.functions = append(file.functions, typed)
			case *ast.GenDecl:
				validator.collectGeneralDeclaration(&file, typed)
			default:
			}
		}
		validator.files = append(validator.files, file)
	}
	sort.Slice(validator.files, func(left, right int) bool {
		return validator.files[left].relative < validator.files[right].relative
	})
}

func (validator *Validator) collectGeneralDeclaration(file *sourceFile, declaration *ast.GenDecl) {
	for _, specification := range declaration.Specs {
		switch typed := specification.(type) {
		case *ast.TypeSpec:
			file.typeSpecs = append(file.typeSpecs, typed)
		case *ast.ValueSpec:
			if declaration.Tok == token.VAR {
				file.variables = append(file.variables, typed)
			}
			if declaration.Tok == token.CONST {
				file.constants = append(file.constants, typed)
			}
		}
	}
}

func (validator *Validator) collectTypes() {
	for _, file := range validator.files {
		for _, specification := range file.typeSpecs {
			object, ok := validator.pass.TypesInfo.Defs[specification.Name].(*types.TypeName)
			if !ok {
				continue
			}
			validator.types[specification.Name.Name] = namedType{
				name:       specification.Name.Name,
				file:       file,
				spec:       specification,
				typeObject: object,
			}
		}
	}
}

func (validator *Validator) validateFile(file *sourceFile) {
	boundary := validator.boundaryFile(file.relative)
	if validator.configuration.Rules.OnePrimaryTypePerFile != RuleLevelOff {
		switch {
		case len(file.typeSpecs) == 0 && !boundary:
			validator.report(file.syntax.Name, "spice.style.file.one-primary-type",
				"handwritten production file has no primary named type and is not an approved boundary file")
		case len(file.typeSpecs) > 1:
			validator.report(file.typeSpecs[1].Name, "spice.style.file.one-primary-type",
				"handwritten production file declares more than one primary named type")
		}
		for _, declaration := range file.syntax.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if ok && general.Tok == token.TYPE && len(general.Specs) > 1 {
				validator.report(general, "spice.style.file.one-primary-type",
					"grouped type declarations are forbidden")
			}
		}
	}
	if len(file.typeSpecs) == 1 && validator.configuration.Rules.FileNameMatchesType != RuleLevelOff {
		expected := initialismSnakeCase(file.typeSpecs[0].Name.Name) + ".go"
		if !typeFileNameMatches(filepath.Base(file.relative), expected) {
			validator.report(file.typeSpecs[0].Name, "spice.style.file.name",
				"filename must be "+expected+" or its exact supported build-specific form for primary type "+
					file.typeSpecs[0].Name.Name)
		}
	}
	if len(file.typeSpecs) == 1 && file.lineCount > validator.configuration.Rules.MaxTypeFileLines {
		validator.report(file.typeSpecs[0].Name, "spice.style.file.lines",
			"primary type file exceeds the configured line limit")
	}
	if validator.configuration.Rules.BanMutablePackageState != RuleLevelOff {
		for _, variable := range file.variables {
			for _, name := range variable.Names {
				if validator.packageVariable(file.relative, name) {
					continue
				}
				validator.report(name, "spice.style.package.mutable-global",
					"mutable package state is forbidden; move ownership to a constructed type")
			}
		}
	}
	for _, function := range file.functions {
		if function.Recv == nil {
			validator.validatePackageFunction(file, function)
		}
		validator.validateSignature(function.Type)
	}
	validator.validateStoredContexts(file)
}

func typeFileNameMatches(actual, expected string) bool {
	if actual == expected {
		return true
	}
	expectedBase := strings.TrimSuffix(expected, ".go")
	actualBase := strings.TrimSuffix(actual, ".go")
	suffix, found := strings.CutPrefix(actualBase, expectedBase+"_")
	return found && supportedBuildFileSuffix(suffix)
}

func supportedBuildFileSuffix(suffix string) bool {
	parts := strings.Split(suffix, "_")
	switch len(parts) {
	case 1:
		return supportedBuildOperatingSystem(parts[0]) || supportedBuildArchitecture(parts[0]) || parts[0] == "unix"
	case 2:
		return supportedBuildOperatingSystem(parts[0]) && supportedBuildArchitecture(parts[1])
	default:
		return false
	}
}

func supportedBuildOperatingSystem(value string) bool {
	switch value {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows":
		return true
	default:
		return false
	}
}

func supportedBuildArchitecture(value string) bool {
	switch value {
	case "386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
		"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
		return true
	default:
		return false
	}
}

func (validator *Validator) packageVariable(relative string, identifier *ast.Ident) bool {
	object, ok := validator.pass.TypesInfo.Defs[identifier].(*types.Var)
	if !ok {
		return false
	}
	actualType := types.TypeString(object.Type(), packageQualifier)
	for _, exception := range validator.configuration.PackageVariableExceptions {
		if globMatches(exception.Glob, relative) &&
			exception.Symbol == identifier.Name && exception.Type == actualType {
			return true
		}
	}
	return false
}

func (validator *Validator) validatePackageFunction(file *sourceFile, function *ast.FuncDecl) {
	if function.Name.Name == "init" && validator.configuration.Rules.BanInit != RuleLevelOff {
		validator.report(function.Name, "spice.style.function.init",
			"handwritten init functions are forbidden")
		return
	}
	if validator.configuration.Rules.PackageFunctions == RuleLevelOff {
		return
	}
	if function.Name.Name == "main" && file.syntax.Name.Name == "main" &&
		filepath.Base(file.relative) == "main.go" {
		return
	}
	if validator.boundaryFunction(file.relative, function.Name.Name) {
		return
	}
	if len(file.typeSpecs) == 1 && constructorOrParser(function.Name.Name, file.typeSpecs[0].Name.Name) {
		return
	}
	validator.report(function.Name, "spice.style.function.package-level",
		"package-level function is not an approved entrypoint, type constructor/parser, or exact boundary exception")
}

func (validator *Validator) validateSignature(function *ast.FuncType) {
	if function.Params != nil && len(function.Params.List) != 0 &&
		validator.configuration.Rules.ContextFirst != RuleLevelOff {
		for index, field := range function.Params.List {
			if validator.isContext(field.Type) && index != 0 {
				validator.report(field.Type, "spice.style.context.first", "context.Context must be the first parameter")
			}
		}
	}
	if function.Results == nil || len(function.Results.List) == 0 ||
		validator.configuration.Rules.ErrorLast == RuleLevelOff {
		return
	}
	for index, field := range function.Results.List {
		if validator.isError(field.Type) && index != len(function.Results.List)-1 {
			validator.report(field.Type, "spice.style.error.last", "error must be the final result")
		}
	}
}

func (validator *Validator) validateStoredContexts(file *sourceFile) {
	if validator.configuration.Rules.ContextFirst == RuleLevelOff {
		return
	}
	for _, specification := range file.typeSpecs {
		structure, ok := specification.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range structure.Fields.List {
			if validator.isContext(field.Type) {
				validator.report(field.Type, "spice.style.context.stored", "context.Context must not be stored in a struct")
			}
		}
	}
}

func (validator *Validator) validateMethods() {
	if validator.configuration.Rules.MethodsInPrimaryFile == RuleLevelOff {
		return
	}
	receiverNames := make(map[string]string)
	for _, file := range validator.files {
		for _, function := range file.functions {
			if function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiverType := receiverBaseName(function.Recv.List[0].Type)
			declaration, found := validator.types[receiverType]
			if !found {
				continue
			}
			if declaration.file.path != file.path {
				validator.report(function.Name, "spice.style.file.method-owner",
					"method must be declared in the same file as receiver type "+receiverType)
			}
			if len(function.Recv.List[0].Names) == 1 {
				name := function.Recv.List[0].Names[0]
				if previous := receiverNames[receiverType]; previous != "" && previous != name.Name {
					validator.report(name, "spice.style.receiver.name",
						"receiver name must be consistent with other methods of "+receiverType)
				} else {
					receiverNames[receiverType] = name.Name
				}
			}
		}
	}
}

func (validator *Validator) selected(relative string) bool {
	for _, root := range validator.configuration.SourceRoots {
		root = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
		if relative == root || strings.HasPrefix(relative, root+"/") {
			return true
		}
	}
	return false
}

func (validator *Validator) generated(syntax *ast.File, relative string) bool {
	if ast.IsGenerated(syntax) {
		return true
	}
	for _, root := range validator.configuration.GeneratedRoots {
		root = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
		if relative == root || strings.HasPrefix(relative, root+"/") {
			return true
		}
	}
	return false
}

func (validator *Validator) boundaryFile(relative string) bool {
	for _, pattern := range validator.configuration.AllowedBoundaryFiles {
		if globMatches(pattern, relative) {
			return true
		}
	}
	return false
}

func (validator *Validator) boundaryFunction(relative, symbol string) bool {
	for _, exception := range validator.configuration.PackageFunctionExceptions {
		if !globMatches(exception.Glob, relative) {
			continue
		}
		switch {
		case exception.Symbol != "" && exception.Symbol == symbol:
			return true
		case exception.SymbolPattern != "":
			matched, err := regexp.MatchString(exception.SymbolPattern, symbol)
			if err == nil && matched {
				return true
			}
		case exception.ContributionKind != "":
			// Contribution exceptions require the shared typed compiler result.
			// The standalone analysis.Pass cannot prove descriptor-backed
			// metadata, so it must fail closed instead of granting a filename
			// exemption.
			continue
		}
	}
	return false
}

func (validator *Validator) isContext(expression ast.Expr) bool {
	value := validator.pass.TypesInfo.TypeOf(expression)
	return value != nil && types.TypeString(value, packageQualifier) == "context.Context"
}

func (validator *Validator) isError(expression ast.Expr) bool {
	value := validator.pass.TypesInfo.TypeOf(expression)
	return value != nil && types.Identical(value, types.Universe.Lookup("error").Type())
}

func (validator *Validator) lineCount(path string) int {
	content, err := validator.pass.ReadFile(path)
	if err != nil || len(content) == 0 {
		return 0
	}
	return strings.Count(string(content), "\n") + 1
}

func (validator *Validator) report(node ast.Node, code, message string) {
	validator.pass.Report(analysis.Diagnostic{
		Pos:      node.Pos(),
		End:      node.End(),
		Category: code,
		Message:  code + ": " + message,
	})
}

func packageQualifier(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Name()
}

func receiverBaseName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverBaseName(typed.X)
	case *ast.IndexExpr:
		return receiverBaseName(typed.X)
	case *ast.IndexListExpr:
		return receiverBaseName(typed.X)
	default:
		return ""
	}
}

func constructorOrParser(function, primary string) bool {
	return function == "New"+primary ||
		strings.HasPrefix(function, "New"+primary+"From") && len(function) > len("New"+primary+"From") ||
		function == "Parse"+primary
}

func initialismSnakeCase(name string) string {
	var result strings.Builder
	runes := []rune(name)
	for index, current := range runes {
		if index > 0 && current >= 'A' && current <= 'Z' {
			previous := runes[index-1]
			nextLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' ||
				previous >= 'A' && previous <= 'Z' && nextLower {
				result.WriteByte('_')
			}
		}
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		result.WriteRune(current)
	}
	return result.String()
}

func globMatches(pattern, value string) bool {
	pattern = filepath.ToSlash(pattern)
	value = filepath.ToSlash(value)
	if strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
		for {
			matched, err := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(value))
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
	matched, err := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(value))
	return err == nil && matched
}
