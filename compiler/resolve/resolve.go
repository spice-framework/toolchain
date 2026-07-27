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
	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/annotationimport"
	"github.com/StevenBuglione/spice/compiler/load"
	annotationparser "github.com/StevenBuglione/spice/compiler/parser"
)

// Occurrence associates one parsed annotation with the exact declaration from
// the supplied typed Program. DisplayPosition is developer-facing and may be
// adjusted by //line directives; PhysicalPosition remains the deterministic
// loaded source identity. PhysicalFile and PhysicalOffset are retained for
// compatibility.
type Occurrence struct {
	Annotation       annotation.Annotation
	Spelling         string
	Definition       annotation.DefinitionReference
	Target           annotation.Target
	Name             string
	SymbolID         string
	PackagePath      string
	PhysicalFile     string
	PhysicalOffset   int
	PhysicalPosition token.Position
	DisplayPosition  token.Position
	Contributions    []sdk.Contribution
}

// HasContribution reports whether a validated tool contribution is attached
// to this invocation.
func (occurrence Occurrence) HasContribution(
	kind sdk.ContributionKind,
) bool {
	for _, contribution := range occurrence.Contributions {
		if contribution.Kind == kind {
			return true
		}
	}
	return false
}

// Contribution returns a defensive typed contribution by kind.
func (occurrence Occurrence) Contribution(
	kind sdk.ContributionKind,
) (sdk.Contribution, bool) {
	for _, contribution := range occurrence.Contributions {
		if contribution.Kind == kind {
			return contribution.Clone(), true
		}
	}
	return sdk.Contribution{}, false
}

// UsesContribution preserves the legacy unimported spelling only while the
// built-in migration is in progress. Explicit imports must be authorized and
// contribute the requested semantic kind through their tool.
func (occurrence Occurrence) UsesContribution(
	kind sdk.ContributionKind,
	legacyName string,
) bool {
	if occurrence.HasContribution(kind) {
		return true
	}
	return occurrence.Definition == (annotation.DefinitionReference{}) &&
		occurrence.Annotation.Name == legacyName
}

// Diagnostic is one deterministic source-positioned resolution failure.
type Diagnostic struct {
	Position         token.Position
	PhysicalFile     string
	PhysicalOffset   int
	PhysicalPosition token.Position
	Annotation       string
	Kind             string
	Message          string
	rendered         string
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

// WithContributions returns a defensive result whose occurrence at index owns
// the supplied validated contributions.
func (result Result) WithContributions(
	index int,
	values []sdk.Contribution,
) (Result, error) {
	if index < 0 || index >= len(result.Occurrences) {
		return Result{}, fmt.Errorf(
			"annotation contribution occurrence index %d is out of range",
			index,
		)
	}
	cloned, err := cloneContributions(values)
	if err != nil {
		return Result{}, err
	}
	result.Occurrences = append([]Occurrence(nil), result.Occurrences...)
	result.Occurrences[index].Contributions = cloned
	return result, nil
}

func cloneContributions(
	values []sdk.Contribution,
) ([]sdk.Contribution, error) {
	result := make([]sdk.Contribution, len(values))
	seen := make(map[sdk.ContributionKind]struct{}, len(values))
	for index, value := range values {
		if err := value.Validate(); err != nil {
			return nil, fmt.Errorf(
				"annotation contribution %d is invalid: %w",
				index,
				err,
			)
		}
		if _, duplicate := seen[value.Kind]; duplicate {
			return nil, fmt.Errorf(
				"annotation invocation returned duplicate %q contributions",
				value.Kind,
			)
		}
		seen[value.Kind] = struct{}{}
		result[index] = value.Clone()
	}
	return result, nil
}

type symbolIndex struct {
	packages map[string]load.Symbol
	objects  map[types.Object]load.Symbol
}

type parsedDirective struct {
	annotation       annotation.Annotation
	spelling         string
	definition       annotation.DefinitionReference
	physicalFile     string
	physicalOffset   int
	physicalPosition token.Position
	displayPosition  token.Position
}

// DefinitionIndex maps an explicitly imported descriptor source to the
// canonical annotation name contributed by that descriptor.
type DefinitionIndex map[annotation.DefinitionReference]string

// Annotations resolves declaration documentation annotations against the exact
// AST and go/types universe already owned by program. It never reparses or
// reloads source.
func Annotations(program *load.Program) Result {
	return AnnotationsWithDefinitions(program, nil)
}

// AnnotationsWithDefinitions resolves explicit file-scoped imports against
// descriptors decoded from the same typed program. Files without import
// directives retain the pre-1.0 built-in spelling compatibility path.
func AnnotationsWithDefinitions(
	program *load.Program,
	definitions DefinitionIndex,
) Result {
	if program == nil {
		return Result{Diagnostics: []Diagnostic{{Kind: "internal", Message: "typed annotation resolution requires a loaded program"}}}
	}

	index := buildSymbolIndex(program.PrimarySymbols())
	result := Result{}
	seenFiles := make(map[string]struct{})
	for _, pkg := range program.PrimaryPackages() {
		resolvePackage(&result, pkg, index, seenFiles, definitions)
	}
	sortResult(&result)
	return result
}

func resolvePackage(
	result *Result,
	pkg load.Package,
	index symbolIndex,
	seenFiles map[string]struct{},
	definitions DefinitionIndex,
) {
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
		resolveFile(result, pkg, source.Syntax, index, definitions)
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

func resolveFile(
	result *Result,
	pkg load.Package,
	file *ast.File,
	index symbolIndex,
	definitions DefinitionIndex,
) {
	imports, importDiagnostics := resolveFileImports(pkg, file)
	result.Diagnostics = append(result.Diagnostics, importDiagnostics...)
	if file.Doc != nil {
		directives, diagnostics := parseGroup(pkg, file.Doc)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		directives, diagnostics = bindImports(directives, imports, definitions)
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
			resolveFunction(result, pkg, node, index, imports, definitions)
		case *ast.GenDecl:
			resolveGeneralDeclaration(
				result,
				pkg,
				node,
				index,
				imports,
				definitions,
			)
		}
	}
}

func resolveFunction(
	result *Result,
	pkg load.Package,
	declaration *ast.FuncDecl,
	index symbolIndex,
	imports fileImports,
	definitions DefinitionIndex,
) {
	if declaration.Doc == nil {
		return
	}
	directives, diagnostics := parseGroup(pkg, declaration.Doc)
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	directives, diagnostics = bindImports(directives, imports, definitions)
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

func resolveGeneralDeclaration(
	result *Result,
	pkg load.Package,
	declaration *ast.GenDecl,
	index symbolIndex,
	imports fileImports,
	definitions DefinitionIndex,
) {
	if declaration.Tok != token.TYPE && declaration.Tok != token.VAR && declaration.Tok != token.CONST {
		return
	}

	if declaration.Doc != nil {
		directives, diagnostics := parseGroup(pkg, declaration.Doc)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		directives, diagnostics = bindImports(directives, imports, definitions)
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
		directives, diagnostics = bindImports(directives, imports, definitions)
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
		_, importComment, importErr := annotationparser.ParseImportComment(
			comment.Text,
			display,
		)
		if importComment || importErr != nil {
			continue
		}
		parsed, ok, err := annotationparser.ParseComment(comment.Text, display)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Position:         display,
				PhysicalFile:     filepath.Clean(physical.Filename),
				PhysicalOffset:   physical.Offset,
				PhysicalPosition: physical,
				Kind:             "parse",
				Message:          err.Error(),
				rendered:         err.Error(),
			})
			continue
		}
		if !ok {
			continue
		}
		directives = append(directives, parsedDirective{
			annotation:       parsed,
			spelling:         parsed.Name,
			physicalFile:     filepath.Clean(physical.Filename),
			physicalOffset:   physical.Offset,
			physicalPosition: physical,
			displayPosition:  display,
		})
	}
	return directives, diagnostics
}

func occurrence(directive parsedDirective, symbol load.Symbol, target annotation.Target) Occurrence {
	return Occurrence{
		Annotation:       directive.annotation,
		Spelling:         directive.spelling,
		Definition:       directive.definition,
		Target:           target,
		Name:             symbol.Name,
		SymbolID:         symbol.ID,
		PackagePath:      symbol.PackagePath,
		PhysicalFile:     directive.physicalFile,
		PhysicalOffset:   directive.physicalOffset,
		PhysicalPosition: directive.physicalPosition,
		DisplayPosition:  directive.displayPosition,
	}
}

type fileImports struct {
	table    annotationimport.Table
	explicit bool
	invalid  bool
}

func resolveFileImports(
	pkg load.Package,
	file *ast.File,
) (fileImports, []Diagnostic) {
	if pkg.Raw == nil || pkg.Raw.Fset == nil {
		return fileImports{}, nil
	}
	var directives []annotation.ImportDirective
	var diagnostics []Diagnostic
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if comment == nil ||
				!strings.HasPrefix(strings.TrimSpace(comment.Text), "//") {
				continue
			}
			display := pkg.Raw.Fset.PositionFor(comment.Pos(), true)
			physical := pkg.Raw.Fset.PositionFor(comment.Pos(), false)
			directive, recognized, err := annotationparser.ParseImportComment(
				comment.Text,
				display,
			)
			if !recognized {
				continue
			}
			if err != nil {
				kind := "annotation-import-parse"
				if annotationparser.IsLegacyImportError(err) {
					kind = "annotation-import-legacy"
					offset := strings.Index(
						comment.Text,
						"@spice.import",
					)
					display = shiftedPosition(display, offset)
					physical = shiftedPosition(physical, offset)
				}
				diagnostics = append(diagnostics, sourceDiagnostic(
					display,
					physical,
					kind,
					err.Error(),
					err.Error(),
				))
				continue
			}
			directive.PhysicalPosition = physical
			directives = append(directives, directive)
		}
	}
	table, bindingDiagnostics := annotationimport.Resolve(directives)
	for _, item := range bindingDiagnostics {
		physical := physicalImportPosition(directives, item.Position)
		diagnostics = append(diagnostics, sourceDiagnostic(
			item.Position,
			physical,
			"annotation-import-binding",
			item.Message,
			"",
		))
	}
	return fileImports{
		table:    table,
		explicit: len(directives) != 0 || len(diagnostics) != 0,
		invalid:  len(diagnostics) != 0,
	}, diagnostics
}

func physicalImportPosition(
	directives []annotation.ImportDirective,
	display token.Position,
) token.Position {
	for _, directive := range directives {
		if directive.Position.Offset == display.Offset {
			return directive.PhysicalPosition
		}
	}
	return display
}

func bindImports(
	directives []parsedDirective,
	imports fileImports,
	definitions DefinitionIndex,
) ([]parsedDirective, []Diagnostic) {
	if imports.invalid {
		return nil, nil
	}
	if !imports.explicit {
		return directives, nil
	}
	resolved := make([]parsedDirective, 0, len(directives))
	var diagnostics []Diagnostic
	for _, directive := range directives {
		spelling := directive.annotation.Name
		reference, found := imports.table.Lookup(spelling)
		if !found {
			diagnostics = append(diagnostics, directiveDiagnostic(
				directive,
				"annotation-import",
				fmt.Sprintf(
					"annotation @%s is not imported in this file; add an explicit // @import declaration",
					spelling,
				),
			))
			continue
		}
		if !token.IsExported(reference.Symbol) {
			diagnostics = append(diagnostics, directiveDiagnostic(
				directive,
				"annotation-import",
				fmt.Sprintf(
					"annotation @%s selects unexported descriptor symbol %q",
					spelling,
					reference.Symbol,
				),
			))
			continue
		}
		canonical, found := definitions[reference]
		if !found {
			diagnostics = append(diagnostics, directiveDiagnostic(
				directive,
				"annotation-descriptor",
				fmt.Sprintf(
					"annotation @%s resolves to %s.%s, but that descriptor is unavailable in the typed program",
					spelling,
					reference.Package,
					reference.Symbol,
				),
			))
			continue
		}
		directive.annotation.Name = canonical
		directive.definition = reference
		resolved = append(resolved, directive)
	}
	return resolved, diagnostics
}

func shiftedPosition(position token.Position, offset int) token.Position {
	if offset <= 0 {
		return position
	}
	position.Column += offset
	position.Offset += offset
	return position
}

func directiveDiagnostic(
	directive parsedDirective,
	kind string,
	message string,
) Diagnostic {
	return Diagnostic{
		Position:         directive.displayPosition,
		PhysicalFile:     directive.physicalFile,
		PhysicalOffset:   directive.physicalOffset,
		PhysicalPosition: directive.physicalPosition,
		Annotation:       directive.annotation.Name,
		Kind:             kind,
		Message:          message,
	}
}

func sourceDiagnostic(
	display token.Position,
	physical token.Position,
	kind string,
	message string,
	rendered string,
) Diagnostic {
	return Diagnostic{
		Position:         display,
		PhysicalFile:     filepath.Clean(physical.Filename),
		PhysicalOffset:   physical.Offset,
		PhysicalPosition: physical,
		Kind:             kind,
		Message:          message,
		rendered:         rendered,
	}
}

func ambiguityDiagnostic(directive parsedDirective, message string) Diagnostic {
	return Diagnostic{
		Position:         directive.displayPosition,
		PhysicalFile:     directive.physicalFile,
		PhysicalOffset:   directive.physicalOffset,
		PhysicalPosition: directive.physicalPosition,
		Annotation:       directive.annotation.Name,
		Kind:             "ambiguity",
		Message:          message,
	}
}

func missingSymbolDiagnostic(directive parsedDirective, declarationKind, name string) Diagnostic {
	return Diagnostic{
		Position:         directive.displayPosition,
		PhysicalFile:     directive.physicalFile,
		PhysicalOffset:   directive.physicalOffset,
		PhysicalPosition: directive.physicalPosition,
		Annotation:       directive.annotation.Name,
		Kind:             "resolution",
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
