package load

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Options configures one isolated package-loading operation.
type Options struct {
	Dir        string
	Env        []string
	BuildFlags []string
	Overlay    map[string][]byte
	// Tests is reserved for a future test-package model. The bootstrap loader
	// rejects true because go/packages test variants can duplicate logical
	// package and symbol identities.
	Tests bool
}

// Load asks the standard Go package driver to load the requested root package
// patterns once, then builds deterministic package, symbol, and diagnostic
// records from that shared type universe.
func Load(ctx context.Context, options Options, patterns ...string) (*Program, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		diagnostic := Diagnostic{Kind: "list", Message: "no package patterns were provided"}
		program := &Program{diagnostics: []Diagnostic{diagnostic}}
		return program, &LoadError{Diagnostics: program.Diagnostics()}
	}
	if options.Tests {
		diagnostic := Diagnostic{
			Kind:    "configuration",
			Message: "test-variant loading is unsupported; load application packages with Tests disabled",
		}
		program := &Program{diagnostics: []Diagnostic{diagnostic}}
		return program, &LoadError{Diagnostics: program.Diagnostics()}
	}

	config := &packages.Config{
		Context:    ctx,
		Mode:       packages.LoadSyntax | packages.NeedModule,
		Dir:        options.Dir,
		Env:        append([]string(nil), options.Env...),
		BuildFlags: append([]string(nil), options.BuildFlags...),
		Overlay:    cloneOverlay(options.Overlay),
		Tests:      false,
	}

	roots, loadErr := packages.Load(config, patterns...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	program := &Program{}
	if loadErr != nil {
		program.diagnostics = append(program.diagnostics, Diagnostic{
			Kind:    "driver",
			Message: loadErr.Error(),
		})
	}

	for _, root := range roots {
		if root == nil {
			continue
		}
		record := packageRecord(root)
		program.packages = append(program.packages, record)
		program.symbols = append(program.symbols, packageSymbols(root)...)
		program.diagnostics = append(program.diagnostics, packageDiagnostics(root)...)
	}

	sort.SliceStable(program.packages, func(i, j int) bool {
		if program.packages[i].Path != program.packages[j].Path {
			return program.packages[i].Path < program.packages[j].Path
		}
		return program.packages[i].ID < program.packages[j].ID
	})
	sort.SliceStable(program.symbols, func(i, j int) bool {
		left, right := program.symbols[i], program.symbols[j]
		if left.PackagePath != right.PackagePath {
			return left.PackagePath < right.PackagePath
		}
		if leftRank, rightRank := symbolKindRank(left.Kind), symbolKindRank(right.Kind); leftRank != rightRank {
			return leftRank < rightRank
		}
		if left.Receiver != right.Receiver {
			return left.Receiver < right.Receiver
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.PhysicalPosition.Filename != right.PhysicalPosition.Filename {
			return left.PhysicalPosition.Filename < right.PhysicalPosition.Filename
		}
		return left.PhysicalPosition.Offset < right.PhysicalPosition.Offset
	})
	sortDiagnostics(program.diagnostics)

	if len(program.diagnostics) > 0 || loadErr != nil {
		return program, &LoadError{Diagnostics: program.Diagnostics()}
	}
	return program, nil
}

func cloneOverlay(overlay map[string][]byte) map[string][]byte {
	if len(overlay) == 0 {
		return nil
	}
	result := make(map[string][]byte, len(overlay))
	for path, content := range overlay {
		result[path] = append([]byte(nil), content...)
	}
	return result
}

func cleanSortedFiles(files []string) []string {
	result := append([]string(nil), files...)
	for i := range result {
		result[i] = filepath.Clean(result[i])
	}
	sort.Strings(result)
	return result
}

func fileSet(files []string) map[string]struct{} {
	result := make(map[string]struct{}, len(files))
	for _, file := range files {
		result[filepath.Clean(file)] = struct{}{}
	}
	return result
}

func sourcePackageName(root *packages.Package, sourceFiles map[string]struct{}) *ast.Ident {
	var selected *ast.Ident
	var selectedPosition token.Position
	for _, file := range root.Syntax {
		if file == nil || file.Name == nil || !sourcePosition(root, file.Name.Pos(), sourceFiles) {
			continue
		}
		position := root.Fset.PositionFor(file.Name.Pos(), true)
		if selected == nil || position.Filename < selectedPosition.Filename ||
			(position.Filename == selectedPosition.Filename && position.Offset < selectedPosition.Offset) {
			selected = file.Name
			selectedPosition = position
		}
	}
	return selected
}

func sourceObject(root *packages.Package, object types.Object, sourceFiles map[string]struct{}) bool {
	return object != nil && sourcePosition(root, object.Pos(), sourceFiles)
}

func sourcePosition(root *packages.Package, position token.Pos, sourceFiles map[string]struct{}) bool {
	if !position.IsValid() || root.Fset == nil {
		return false
	}
	physical := filepath.Clean(root.Fset.PositionFor(position, false).Filename)
	if _, ok := sourceFiles[physical]; ok {
		return true
	}
	display := filepath.Clean(root.Fset.PositionFor(position, true).Filename)
	_, ok := sourceFiles[display]
	return ok
}

func packageRecord(root *packages.Package) Package {
	files := sourceFileRecords(root)
	compiledGoFiles := make([]string, len(files))
	syntax := make([]*ast.File, len(files))
	for i, file := range files {
		compiledGoFiles[i] = file.PhysicalPath
		syntax[i] = file.Syntax
	}

	dir := ""
	if root.Dir != "" {
		dir = filepath.Clean(root.Dir)
	} else if sourceFiles := cleanSortedFiles(root.GoFiles); len(sourceFiles) > 0 {
		dir = filepath.Dir(sourceFiles[0])
	}
	modulePath := ""
	if root.Module != nil {
		modulePath = root.Module.Path
	}

	return Package{
		ID:              stableSymbolID(SymbolPackage, root.PkgPath, "", ""),
		Path:            root.PkgPath,
		Name:            root.Name,
		Dir:             dir,
		ModulePath:      modulePath,
		Files:           files,
		CompiledGoFiles: compiledGoFiles,
		IllTyped:        root.IllTyped || len(root.Errors) > 0,
		Types:           root.Types,
		TypesInfo:       root.TypesInfo,
		Syntax:          syntax,
		Raw:             root,
	}
}

func sourceFileRecords(root *packages.Package) []SourceFile {
	syntaxByPath := make(map[string][]*ast.File, len(root.Syntax))
	for _, file := range root.Syntax {
		if file == nil || root.Fset == nil {
			continue
		}
		path := filepath.Clean(root.Fset.PositionFor(file.Pos(), false).Filename)
		syntaxByPath[path] = append(syntaxByPath[path], file)
	}

	compiled := cleanSortedFiles(root.CompiledGoFiles)
	result := make([]SourceFile, 0, len(compiled)+len(syntaxByPath))
	for _, path := range compiled {
		var syntax *ast.File
		if queue := syntaxByPath[path]; len(queue) > 0 {
			syntax = queue[0]
			if len(queue) == 1 {
				delete(syntaxByPath, path)
			} else {
				syntaxByPath[path] = queue[1:]
			}
		}
		result = append(result, SourceFile{PhysicalPath: path, Syntax: syntax})
	}

	leftovers := make([]string, 0, len(syntaxByPath))
	for path := range syntaxByPath {
		leftovers = append(leftovers, path)
	}
	sort.Strings(leftovers)
	for _, path := range leftovers {
		for _, syntax := range syntaxByPath[path] {
			result = append(result, SourceFile{PhysicalPath: path, Syntax: syntax})
		}
	}
	return result
}

func packageDiagnostics(root *packages.Package) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(root.Errors))
	for _, packageError := range root.Errors {
		position, filename, line, column := normalizeDiagnosticPosition(packageError.Pos)
		diagnostics = append(diagnostics, Diagnostic{
			PackagePath: root.PkgPath,
			Position:    position,
			Filename:    filename,
			Line:        line,
			Column:      column,
			Kind:        errorKindName(packageError.Kind),
			Message:     packageError.Msg,
		})
	}
	return diagnostics
}

func normalizeDiagnosticPosition(raw string) (position, filename string, line, column int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", 0, 0
	}

	prefix, trailing, ok := splitTrailingNumber(raw)
	if !ok {
		filename = filepath.Clean(raw)
		return filename, filename, 0, 0
	}
	prefix2, preceding, hasPreceding := splitTrailingNumber(prefix)
	if hasPreceding {
		filename = filepath.Clean(prefix2)
		line, column = preceding, trailing
	} else {
		filename = filepath.Clean(prefix)
		line = trailing
	}

	position = filename
	if line > 0 {
		position += ":" + strconv.Itoa(line)
	}
	if column > 0 {
		position += ":" + strconv.Itoa(column)
	}
	return position, filename, line, column
}

func splitTrailingNumber(value string) (string, int, bool) {
	separator := strings.LastIndexByte(value, ':')
	if separator < 0 || separator == len(value)-1 {
		return value, 0, false
	}
	number, err := strconv.Atoi(value[separator+1:])
	if err != nil {
		return value, 0, false
	}
	return value[:separator], number, true
}

func errorKindName(kind packages.ErrorKind) string {
	switch kind {
	case packages.ListError:
		return "list"
	case packages.ParseError:
		return "parse"
	case packages.TypeError:
		return "type"
	default:
		return "unknown"
	}
}

func packageSymbols(root *packages.Package) []Symbol {
	if root.Fset == nil || root.TypesInfo == nil {
		return nil
	}

	sourceFiles := fileSet(root.GoFiles)
	symbols := make([]Symbol, 0)
	if name := sourcePackageName(root, sourceFiles); name != nil {
		symbols = append(symbols, Symbol{
			ID:               stableSymbolID(SymbolPackage, root.PkgPath, "", ""),
			DisplayLabel:     root.PkgPath,
			Kind:             SymbolPackage,
			Name:             root.Name,
			PackagePath:      root.PkgPath,
			Position:         root.Fset.PositionFor(name.Pos(), true),
			PhysicalPosition: root.Fset.PositionFor(name.Pos(), false),
			Node:             name,
		})
	}

	for _, file := range root.Syntax {
		if file == nil {
			continue
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					switch specification := specification.(type) {
					case *ast.TypeSpec:
						if specification.Name.Name == "_" {
							continue
						}
						if object := root.TypesInfo.Defs[specification.Name]; object != nil && sourceObject(root, object, sourceFiles) {
							symbols = append(symbols, objectSymbol(root, object, specification, SymbolType, ""))
						}
					case *ast.ValueSpec:
						for _, name := range specification.Names {
							if name.Name == "_" {
								continue
							}
							object := root.TypesInfo.Defs[name]
							if object == nil || !sourceObject(root, object, sourceFiles) {
								continue
							}
							kind := SymbolVariable
							if _, ok := object.(*types.Const); ok {
								kind = SymbolConstant
							}
							symbols = append(symbols, objectSymbol(root, object, name, kind, ""))
						}
					}
				}
			case *ast.FuncDecl:
				if declaration.Name.Name == "_" || (declaration.Recv == nil && declaration.Name.Name == "init") {
					continue
				}
				object, _ := root.TypesInfo.Defs[declaration.Name].(*types.Func)
				if object == nil || !sourceObject(root, object, sourceFiles) {
					continue
				}
				signature, _ := object.Type().(*types.Signature)
				if declaration.Recv == nil {
					symbol := objectSymbol(root, object, declaration, SymbolFunction, "")
					symbol.Signature = signature
					symbols = append(symbols, symbol)
					continue
				}
				receiver, err := normalizedReceiverName(signature)
				if err != nil {
					// Ill-formed receiver declarations are already reported by go/types.
					continue
				}
				symbol := objectSymbol(root, object, declaration, SymbolMethod, receiver)
				symbol.Signature = signature
				symbols = append(symbols, symbol)
			}
		}
	}
	return symbols
}

func objectSymbol(root *packages.Package, object types.Object, node ast.Node, kind SymbolKind, receiver string) Symbol {
	name := object.Name()
	return Symbol{
		ID:               stableSymbolID(kind, root.PkgPath, receiver, name),
		DisplayLabel:     symbolDisplayLabel(root.PkgPath, receiver, name),
		Kind:             kind,
		Name:             name,
		PackagePath:      root.PkgPath,
		Receiver:         receiver,
		Position:         root.Fset.PositionFor(object.Pos(), true),
		PhysicalPosition: root.Fset.PositionFor(object.Pos(), false),
		Object:           object,
		Node:             node,
	}
}

const stableSymbolIDPrefix = "spice:symbol:v1|"

// stableSymbolID serializes the structured logical identity without relying on
// any delimiter being absent from package paths or identifiers. Lengths count
// UTF-8 bytes because Go strings and serialized IDs are byte sequences.
func stableSymbolID(kind SymbolKind, packagePath, receiver, name string) string {
	var builder strings.Builder
	builder.Grow(len(stableSymbolIDPrefix) + len(kind) + len(packagePath) + len(receiver) + len(name) + 32)
	builder.WriteString(stableSymbolIDPrefix)
	builder.WriteString(string(kind))
	builder.WriteByte('|')
	appendStableSymbolField(&builder, packagePath)
	builder.WriteByte('|')
	appendStableSymbolField(&builder, receiver)
	builder.WriteByte('|')
	appendStableSymbolField(&builder, name)
	return builder.String()
}

func appendStableSymbolField(builder *strings.Builder, value string) {
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
}

func symbolDisplayLabel(packagePath, receiver, name string) string {
	if name == "" {
		return packagePath
	}
	if receiver == "" {
		return packagePath + "." + name
	}
	return packagePath + "." + receiver + "." + name
}

func symbolKindRank(kind SymbolKind) int {
	switch kind {
	case SymbolPackage:
		return 0
	case SymbolType:
		return 1
	case SymbolFunction:
		return 2
	case SymbolMethod:
		return 3
	case SymbolVariable:
		return 4
	case SymbolConstant:
		return 5
	default:
		return 6
	}
}

func normalizedReceiverName(signature *types.Signature) (string, error) {
	if signature == nil || signature.Recv() == nil {
		return "", fmt.Errorf("method signature has no receiver")
	}
	receiverType := signature.Recv().Type()
	if pointer, ok := receiverType.(*types.Pointer); ok {
		receiverType = pointer.Elem()
	}
	named, ok := receiverType.(*types.Named)
	if !ok {
		return "", fmt.Errorf("receiver %s is not a named type", types.TypeString(receiverType, nil))
	}
	origin := named.Origin()
	if origin == nil || origin.Obj() == nil {
		return "", fmt.Errorf("receiver %s has no defining origin", types.TypeString(receiverType, nil))
	}
	return origin.Obj().Name(), nil
}
