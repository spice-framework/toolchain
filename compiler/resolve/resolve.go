package resolve

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	annotationparser "github.com/StevenBuglione/spice/compiler/parser"
)

// Occurrence associates one parsed annotation with the exact declaration from
// the supplied typed Program. DisplayPosition is developer-facing and may be
// adjusted by //line directives; PhysicalFile and PhysicalOffset remain the
// deterministic source identity.
type Occurrence struct {
	Annotation      annotation.Annotation
	Target          annotation.Target
	Name            string
	SymbolID        string
	PackagePath     string
	PhysicalFile    string
	PhysicalOffset  int
	DisplayPosition token.Position
}

// Diagnostic is one deterministic source-positioned resolution failure.
type Diagnostic struct {
	Position       token.Position
	PhysicalFile   string
	PhysicalOffset int
	Annotation     string
	Kind           string
	Message        string
	rendered       string
}

// Error formats a compiler-style diagnostic.
func (d Diagnostic) Error() string {
	if d.rendered != "" {
		return d.rendered
	}
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

// Result is the immutable-by-convention output of resolving annotations from
// one loaded Program.
type Result struct {
	Files       int
	Occurrences []Occurrence
	Diagnostics []Diagnostic
}

type symbolIndex struct {
	packages map[string]load.Symbol
	objects  map[types.Object]load.Symbol
}

type parsedDirective struct {
	annotation      annotation.Annotation
	physicalFile    string
	physicalOffset  int
	displayPosition token.Position
}

// Annotations resolves declaration documentation annotations against the exact
// AST and go/types universe already owned by program. It never reparses or
// reloads source.
func Annotations(program *load.Program) Result {
	if program == nil {
		return Result{Diagnostics: []Diagnostic{{Kind: "internal", Message: "typed annotation resolution requires a loaded program"}}}
	}

	index := buildSymbolIndex(program.Symbols())
	result := Result{}
	seenFiles := make(map[string]struct{})
	for _, pkg := range program.Packages() {
		resolvePackage(&result, pkg, index, seenFiles)
	}
	sortResult(&result)
	return result
}

func resolvePackage(result *Result, pkg load.Package, index symbolIndex, seenFiles map[string]struct{}) {
	if pkg.Raw == nil || pkg.Raw.Fset == nil || pkg.TypesInfo == nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Kind:    "internal",
			Message: fmt.Sprintf("package %q is missing syntax or type information required for annotation resolution", pkg.Path),
		})
		return
	}
	sourceFiles := make(map[string]struct{}, len(pkg.Raw.GoFiles))
	for _, path := range pkg.Raw.GoFiles {
		sourceFiles[filepath.Clean(path)] = struct{}{}
	}
	for _, source := range pkg.Files {
		physicalPath := filepath.Clean(source.PhysicalPath)
		if source.Syntax == nil {
			continue
		}
		if _, selected := sourceFiles[physicalPath]; !selected {
			continue
		}
		fileKey := pkg.Path + "\x00" + physicalPath
		if _, duplicate := seenFiles[fileKey]; duplicate {
			continue
		}
		seenFiles[fileKey] = struct{}{}
		result.Files++
		resolveFile(result, pkg, source.Syntax, index)
	}
}

func sortResult(result *Result) {
	sort.SliceStable(result.Occurrences, func(i, j int) bool {
		left, right := result.Occurrences[i], result.Occurrences[j]
		if left.PackagePath != right.PackagePath {
			return left.PackagePath < right.PackagePath
		}
		if left.PhysicalFile != right.PhysicalFile {
			return left.PhysicalFile < right.PhysicalFile
		}
		if left.PhysicalOffset != right.PhysicalOffset {
			return left.PhysicalOffset < right.PhysicalOffset
		}
		if left.Annotation.Name != right.Annotation.Name {
			return left.Annotation.Name < right.Annotation.Name
		}
		return left.SymbolID < right.SymbolID
	})
	sort.SliceStable(result.Diagnostics, func(i, j int) bool {
		left, right := result.Diagnostics[i], result.Diagnostics[j]
		if left.PhysicalFile != right.PhysicalFile {
			return left.PhysicalFile < right.PhysicalFile
		}
		if left.PhysicalOffset != right.PhysicalOffset {
			return left.PhysicalOffset < right.PhysicalOffset
		}
		if left.Annotation != right.Annotation {
			return left.Annotation < right.Annotation
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Error() < right.Error()
	})
}

func buildSymbolIndex(symbols []load.Symbol) symbolIndex {
	index := symbolIndex{
		packages: make(map[string]load.Symbol),
		objects:  make(map[types.Object]load.Symbol),
	}
	for _, symbol := range symbols {
		if symbol.Kind == load.SymbolPackage {
			index.packages[symbol.PackagePath] = symbol
		}
		if symbol.Object != nil {
			index.objects[symbol.Object] = symbol
		}
	}
	return index
}

func resolveFile(result *Result, pkg load.Package, file *ast.File, index symbolIndex) {
	if file.Doc != nil {
		directives, diagnostics := parseGroup(pkg, file.Doc)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		for _, directive := range directives {
			symbol, ok := index.packages[pkg.Path]
			if !ok {
				result.Diagnostics = append(result.Diagnostics, missingSymbolDiagnostic(directive, "package", pkg.Name))
				continue
			}
			result.Occurrences = append(result.Occurrences, occurrence(directive, symbol, annotation.TargetPackage))
		}
	}

	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			resolveFunction(result, pkg, node, index)
		case *ast.GenDecl:
			resolveGeneralDeclaration(result, pkg, node, index)
		}
	}
}

func resolveFunction(result *Result, pkg load.Package, declaration *ast.FuncDecl, index symbolIndex) {
	if declaration.Doc == nil {
		return
	}
	directives, diagnostics := parseGroup(pkg, declaration.Doc)
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	if len(directives) == 0 {
		return
	}

	target := annotation.TargetFunction
	if declaration.Recv != nil {
		target = annotation.TargetMethod
	}
	resolveIdentifierDirectives(result, pkg, declaration.Name, directives, target, index)
}

func resolveGeneralDeclaration(result *Result, pkg load.Package, declaration *ast.GenDecl, index symbolIndex) {
	if declaration.Tok != token.TYPE && declaration.Tok != token.VAR && declaration.Tok != token.CONST {
		return
	}

	if declaration.Doc != nil {
		directives, diagnostics := parseGroup(pkg, declaration.Doc)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		if len(directives) > 0 {
			if len(declaration.Specs) != 1 {
				for _, directive := range directives {
					result.Diagnostics = append(result.Diagnostics, ambiguityDiagnostic(
						directive,
						fmt.Sprintf("annotation @%s is ambiguous on a grouped declaration with %d specs; place it on one declaration or individual spec", directive.annotation.Name, len(declaration.Specs)),
					))
				}
			} else {
				resolveSpecDirectives(result, pkg, declaration.Specs[0], declaration.Tok, directives, index)
			}
		}
	}

	for _, specification := range declaration.Specs {
		group := specDocumentation(specification)
		if group == nil || group == declaration.Doc {
			continue
		}
		directives, diagnostics := parseGroup(pkg, group)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		resolveSpecDirectives(result, pkg, specification, declaration.Tok, directives, index)
	}
}

func resolveSpecDirectives(result *Result, pkg load.Package, specification ast.Spec, declarationToken token.Token, directives []parsedDirective, index symbolIndex) {
	if len(directives) == 0 {
		return
	}
	switch node := specification.(type) {
	case *ast.TypeSpec:
		resolveIdentifierDirectives(result, pkg, node.Name, directives, annotation.TargetType, index)
	case *ast.ValueSpec:
		target := annotation.TargetVariable
		if declarationToken == token.CONST {
			target = annotation.TargetConstant
		}
		if len(node.Names) != 1 {
			for _, directive := range directives {
				result.Diagnostics = append(result.Diagnostics, ambiguityDiagnostic(
					directive,
					fmt.Sprintf("annotation @%s is ambiguous on a value declaration with %d names; split the declaration or place metadata on a single-name declaration", directive.annotation.Name, len(node.Names)),
				))
			}
			return
		}
		resolveIdentifierDirectives(result, pkg, node.Names[0], directives, target, index)
	}
}

func resolveIdentifierDirectives(result *Result, pkg load.Package, identifier *ast.Ident, directives []parsedDirective, target annotation.Target, index symbolIndex) {
	name := "<declaration>"
	if identifier != nil {
		name = identifier.Name
	}
	if identifier == nil || identifier.Name == "_" {
		for _, directive := range directives {
			result.Diagnostics = append(result.Diagnostics, ambiguityDiagnostic(
				directive,
				fmt.Sprintf("annotation @%s cannot target blank identifier _; annotate a named declaration", directive.annotation.Name),
			))
		}
		return
	}

	object := pkg.TypesInfo.Defs[identifier]
	symbol, ok := index.objects[object]
	if object == nil || !ok {
		for _, directive := range directives {
			result.Diagnostics = append(result.Diagnostics, missingSymbolDiagnostic(directive, string(target), name))
		}
		return
	}

	for _, directive := range directives {
		result.Occurrences = append(result.Occurrences, occurrence(directive, symbol, target))
	}
}

func parseGroup(pkg load.Package, group *ast.CommentGroup) ([]parsedDirective, []Diagnostic) {
	if group == nil || pkg.Raw == nil || pkg.Raw.Fset == nil {
		return nil, nil
	}
	var directives []parsedDirective
	var diagnostics []Diagnostic
	for _, comment := range group.List {
		if comment == nil {
			continue
		}
		// The current annotation syntax is line-comment based. Block comments are
		// documentation, but not Spice directives.
		if !strings.HasPrefix(strings.TrimSpace(comment.Text), "//") {
			continue
		}
		display := pkg.Raw.Fset.PositionFor(comment.Pos(), true)
		physical := pkg.Raw.Fset.PositionFor(comment.Pos(), false)
		parsed, ok, err := annotationparser.ParseComment(comment.Text, display)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Position:       display,
				PhysicalFile:   filepath.Clean(physical.Filename),
				PhysicalOffset: physical.Offset,
				Kind:           "parse",
				Message:        err.Error(),
				rendered:       err.Error(),
			})
			continue
		}
		if !ok {
			continue
		}
		directives = append(directives, parsedDirective{
			annotation:      parsed,
			physicalFile:    filepath.Clean(physical.Filename),
			physicalOffset:  physical.Offset,
			displayPosition: display,
		})
	}
	return directives, diagnostics
}

func occurrence(directive parsedDirective, symbol load.Symbol, target annotation.Target) Occurrence {
	return Occurrence{
		Annotation:      directive.annotation,
		Target:          target,
		Name:            symbol.Name,
		SymbolID:        symbol.ID,
		PackagePath:     symbol.PackagePath,
		PhysicalFile:    directive.physicalFile,
		PhysicalOffset:  directive.physicalOffset,
		DisplayPosition: directive.displayPosition,
	}
}

func ambiguityDiagnostic(directive parsedDirective, message string) Diagnostic {
	return Diagnostic{
		Position:       directive.displayPosition,
		PhysicalFile:   directive.physicalFile,
		PhysicalOffset: directive.physicalOffset,
		Annotation:     directive.annotation.Name,
		Kind:           "ambiguity",
		Message:        message,
	}
}

func missingSymbolDiagnostic(directive parsedDirective, declarationKind, name string) Diagnostic {
	return Diagnostic{
		Position:       directive.displayPosition,
		PhysicalFile:   directive.physicalFile,
		PhysicalOffset: directive.physicalOffset,
		Annotation:     directive.annotation.Name,
		Kind:           "resolution",
		Message: fmt.Sprintf(
			"annotation @%s targets %s %q, but the loaded type information has no stable Spice symbol for it; annotate an addressable declaration selected by the active Go build",
			directive.annotation.Name,
			declarationKind,
			name,
		),
	}
}

func specDocumentation(specification ast.Spec) *ast.CommentGroup {
	switch node := specification.(type) {
	case *ast.TypeSpec:
		return node.Doc
	case *ast.ValueSpec:
		return node.Doc
	default:
		return nil
	}
}
