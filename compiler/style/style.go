// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"

// Package style enforces optional source-organization profiles over the shared
// typed compiler program. Profiles constrain handwritten application shape;
// they never rewrite source or affect ordinary Go type checking.
//
// @NamedInterface("style")
package style

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

// Profile identifies one opt-in source-organization contract.
type Profile string

const (
	// ProfileNone preserves ordinary valid Go source organization.
	ProfileNone Profile = ""
	// ProfileJavaStructured makes named types and receiver methods the center of
	// handwritten application source.
	ProfileJavaStructured Profile = "java-structured"
)

// ValidateProfile rejects unknown profile names.
func ValidateProfile(profile Profile) error {
	switch profile {
	case ProfileNone, ProfileJavaStructured:
		return nil
	default:
		return fmt.Errorf(
			"unsupported Spice profile %q; expected %q",
			profile,
			ProfileJavaStructured,
		)
	}
}

// Diagnostic is one deterministic style-profile violation.
type Diagnostic struct {
	Position         token.Position
	PhysicalPosition token.Position
	SymbolID         string
	Kind             string
	Message          string
}

// Error renders a compiler-style diagnostic.
func (diagnostic Diagnostic) Error() string {
	position := diagnostic.Position
	if position.Filename == "" {
		position.Filename = "<unknown>"
	}
	if position.Line <= 0 {
		position.Line = 1
	}
	if position.Column <= 0 {
		position.Column = 1
	}
	return fmt.Sprintf(
		"%s:%d:%d: %s",
		position.Filename,
		position.Line,
		position.Column,
		diagnostic.Message,
	)
}

// Catalog is the immutable style validation result.
type Catalog struct {
	diagnostics []Diagnostic
}

// Diagnostics returns deterministic profile diagnostics.
func (catalog Catalog) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), catalog.diagnostics...)
}

// Build validates one optional profile against handwritten production source.
func Build(
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	profile Profile,
) Catalog {
	if profile == ProfileNone {
		return Catalog{}
	}
	if err := ValidateProfile(profile); err != nil {
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    "profile",
			Message: err.Error(),
		}}}
	}
	if program == nil {
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    "internal",
			Message: "style profile requires a loaded program",
		}}}
	}

	return buildJavaStructured(program, resolution, providers, nil, "", nil, true)
}

// BuildConfigured validates the typed phase using the shared schema-two policy.
func BuildConfigured(
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	configuration Configuration,
) Catalog {
	return BuildConfiguredAt(
		inferredWorkspaceRoot(program),
		program,
		resolution,
		providers,
		configuration,
	)
}

// BuildConfiguredAt validates schema two against one exact workspace root.
// Compiler-service callers must use this form so configured roots and file
// exceptions are never inferred from arbitrary absolute-path suffixes.
func BuildConfiguredAt(
	workspaceRoot string,
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	configuration Configuration,
) Catalog {
	return buildConfiguredAt(
		workspaceRoot,
		program,
		resolution,
		providers,
		configuration,
		nil,
		true,
	)
}

// BuildConfiguredSelectionAt validates schema two under one exact declared Go
// build selection. Existing callers retain strict filename matching without
// selection-derived suffix exceptions.
func BuildConfiguredSelectionAt(
	workspaceRoot string,
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	configuration Configuration,
	selection BuildSelection,
) Catalog {
	return buildConfiguredAt(
		workspaceRoot,
		program,
		resolution,
		providers,
		configuration,
		&selection,
		true,
	)
}

// BuildConfiguredSourceSelectionAt validates source-owned schema-two rules
// before one application composition scope exists. Provider- and
// application-semantic rules remain owned by the subsequent scoped build.
func BuildConfiguredSourceSelectionAt(
	workspaceRoot string,
	program *load.Program,
	resolution resolve.Result,
	configuration Configuration,
	selection BuildSelection,
) Catalog {
	return buildConfiguredAt(
		workspaceRoot,
		program,
		resolution,
		provider.Catalog{},
		configuration,
		&selection,
		false,
	)
}

func buildConfiguredAt(
	workspaceRoot string,
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	configuration Configuration,
	selection *BuildSelection,
	applicationSemantics bool,
) Catalog {
	if err := configuration.Validate(); err != nil {
		kind := "configuration.schema"
		var configurationErr ConfigurationError
		if errors.As(err, &configurationErr) {
			kind = strings.TrimPrefix(configurationErr.Code(), "spice.style.")
		}
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    kind,
			Message: err.Error(),
		}}}
	}
	if selection != nil && !configurationContainsSelection(configuration, *selection) {
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    "configuration.build-selection",
			Message: "style build selection must exactly match one declared schema-two selection",
		}}}
	}
	if program == nil {
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    "internal",
			Message: "style profile requires a loaded program",
		}}}
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	if workspaceRoot == "." || !filepath.IsAbs(workspaceRoot) {
		return Catalog{diagnostics: []Diagnostic{{
			Kind:    "configuration.source-selection",
			Message: "style profile requires an absolute workspace root",
		}}}
	}
	configuration = configuration.Clone()
	if diagnostics := configuredSourceOwnershipDiagnostics(
		program,
		workspaceRoot,
		configuration,
	); len(diagnostics) != 0 {
		sortDiagnostics(diagnostics)
		return Catalog{diagnostics: diagnostics}
	}
	return buildJavaStructured(
		program,
		resolution,
		providers,
		&configuration,
		workspaceRoot,
		selection,
		applicationSemantics,
	)
}

func buildJavaStructured(
	program *load.Program,
	resolution resolve.Result,
	providers provider.Catalog,
	configuration *Configuration,
	workspaceRoot string,
	selection *BuildSelection,
	applicationSemantics bool,
) Catalog {
	files := handwrittenFiles(
		program.PrimaryPackages(),
		configuration,
		workspaceRoot,
	)
	typesByFile, typeFiles := declaredTypes(program.PrimarySymbols(), files)
	annotatedFunctions := functionAnnotationKinds(resolution)
	packageNames := primaryPackageNames(program.PrimaryPackages())
	catalog := Catalog{}
	structureDiagnostics := fileStructureDiagnostics(files, configuration, selection)
	if configuration != nil {
		structureDiagnostics = applyPackageVariableExceptions(
			structureDiagnostics,
			program.PrimarySymbols(),
			files,
			configuration.PackageVariableExceptions,
		)
		structureDiagnostics = append(
			structureDiagnostics,
			configuredStructuralDiagnostics(program, files, *configuration)...,
		)
	}
	catalog.diagnostics = append(catalog.diagnostics, structureDiagnostics...)
	catalog.diagnostics = append(
		catalog.diagnostics,
		receiverLocationDiagnostics(
			program.PrimarySymbols(),
			files,
			typeFiles,
		)...,
	)
	if configuration == nil {
		catalog.diagnostics = append(
			catalog.diagnostics,
			freeFunctionDiagnostics(
				program.PrimarySymbols(),
				files,
				typesByFile,
				annotatedFunctions,
				packageNames,
			)...,
		)
	} else {
		catalog.diagnostics = append(
			catalog.diagnostics,
			configuredFreeFunctionDiagnostics(
				program,
				program.PrimarySymbols(),
				files,
				typesByFile,
				resolution,
				packageNames,
				*configuration,
			)...,
		)
	}
	if applicationSemantics {
		catalog.diagnostics = append(
			catalog.diagnostics,
			constructorDiagnostics(providers.Providers())...,
		)
		catalog.diagnostics = append(
			catalog.diagnostics,
			implicitInterfaceDiagnostics(
				program.PrimarySymbols(),
				providers.Providers(),
			)...,
		)
	}
	catalog.diagnostics = append(
		catalog.diagnostics,
		loggingDiagnostics(files)...,
	)
	if configuration != nil && applicationSemantics {
		catalog.diagnostics = append(
			catalog.diagnostics,
			configuredTypedDiagnostics(
				program,
				resolution,
				providers,
				files,
				*configuration,
			)...,
		)
		catalog.diagnostics = filterConfiguredDiagnostics(catalog.diagnostics, configuration.Rules)
	}
	sortDiagnostics(catalog.diagnostics)
	return catalog
}

func loggingDiagnostics(files map[string]sourceFile) []Diagnostic {
	var diagnostics []Diagnostic
	for _, filePath := range sortedFilePaths(files) {
		file := files[filePath]
		imports := importPaths(file.syntax)
		ast.Inspect(file.syntax, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if qualifier, ok := selector.X.(*ast.Ident); ok &&
				imports[qualifier.Name] == "log/slog" &&
				(selector.Sel.Name == "SetDefault" || selector.Sel.Name == "Default") {
				position, physical := positions(file, selector.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position: position, PhysicalPosition: physical,
					Kind:    "logging.global",
					Message: "profile java-structured forbids process-global slog state; inject *logging.Logger",
				})
			}
			if loggingCall(selector.Sel.Name) && containsRawErrorLogging(call.Args, imports) {
				position, physical := positions(file, selector.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position: position, PhysicalPosition: physical,
					Kind:    "logging.raw-error",
					Message: "profile java-structured forbids raw error text in logs; use logging.ErrorFields or an explicit logging.SafeError",
				})
			}
			return true
		})
	}
	return diagnostics
}

func importPaths(file *ast.File) map[string]string {
	result := make(map[string]string, len(file.Imports))
	for _, specification := range file.Imports {
		importPath, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(importPath)
		if specification.Name != nil {
			name = specification.Name.Name
		}
		result[name] = importPath
	}
	return result
}

func loggingCall(name string) bool {
	switch name {
	case "Log", "LogAttrs", "Trace", "Debug", "Info", "Warn", "Error":
		return true
	default:
		return false
	}
}

func containsRawErrorLogging(arguments []ast.Expr, imports map[string]string) bool {
	found := false
	for _, argument := range arguments {
		ast.Inspect(argument, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "Error" && len(call.Args) == 0 {
				found = true
				return false
			}
			qualifier, qualified := selector.X.(*ast.Ident)
			if qualified && imports[qualifier.Name] == "log/slog" &&
				selector.Sel.Name == "Any" && firstStringArgument(call.Args) == "error" {
				found = true
				return false
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func firstStringArgument(arguments []ast.Expr) string {
	if len(arguments) == 0 {
		return ""
	}
	literal, ok := arguments[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	return value
}

func filterConfiguredDiagnostics(diagnostics []Diagnostic, rules Rules) []Diagnostic {
	result := make([]Diagnostic, 0, len(diagnostics))
	for _, item := range diagnostics {
		level := RuleLevelError
		switch item.Kind {
		case "file.one-primary-type", "file.unrelated-declaration":
			level = rules.OnePrimaryTypePerFile
		case "file.name":
			level = rules.FileNameMatchesType
		case "file.method-owner":
			level = rules.MethodsInPrimaryFile
		case "function.package-level":
			level = rules.PackageFunctions
		case "constructor.explicit", "constructor.name", "constructor.location":
			level = rules.ExplicitConstructors
		case "package.mutable-global":
			level = rules.BanMutablePackageState
		case "function.init":
			level = rules.BanInit
		default:
		}
		if level != RuleLevelOff {
			result = append(result, item)
		}
	}
	return result
}

type sourceFile struct {
	packagePath string
	packageName string
	physical    string
	relative    string
	fileSet     *token.FileSet
	syntax      *ast.File
}

func handwrittenFiles(
	packages []load.Package,
	configuration *Configuration,
	workspaceRoot string,
) map[string]sourceFile {
	result := make(map[string]sourceFile)
	for _, pkg := range packages {
		if pkg.Raw == nil || pkg.Raw.Fset == nil {
			continue
		}
		for _, file := range pkg.Files {
			physical := cleanFile(file.PhysicalPath)
			if file.Syntax == nil ||
				strings.HasSuffix(physical, "_test.go") ||
				ast.IsGenerated(file.Syntax) {
				continue
			}
			relative := physical
			if configuration != nil {
				var found bool
				relative, found = workspaceRelativeFile(workspaceRoot, physical)
				if !found || pathUnderConfigurationRoot(relative, configuration.GeneratedRoots) {
					continue
				}
			}
			result[physical] = sourceFile{
				packagePath: pkg.Path,
				packageName: pkg.Name,
				physical:    physical,
				relative:    relative,
				fileSet:     pkg.Raw.Fset,
				syntax:      file.Syntax,
			}
		}
	}
	return result
}

func declaredTypes(
	symbols []load.Symbol,
	files map[string]sourceFile,
) (map[string][]string, map[string]string) {
	byFile := make(map[string][]string)
	byType := make(map[string]string)
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolType {
			continue
		}
		file := cleanFile(symbol.PhysicalPosition.Filename)
		if _, handwritten := files[file]; !handwritten {
			continue
		}
		byFile[file] = append(byFile[file], symbol.Name)
		byType[symbol.PackagePath+"\x00"+symbol.Name] = file
	}
	for file := range byFile {
		sort.Strings(byFile[file])
	}
	return byFile, byType
}

func fileStructureDiagnostics(
	files map[string]sourceFile,
	configuration *Configuration,
	selection *BuildSelection,
) []Diagnostic {
	paths := sortedFilePaths(files)
	var diagnostics []Diagnostic
	for _, path := range paths {
		file := files[path]
		var typeSpecs []*ast.TypeSpec
		for _, declaration := range file.syntax.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				if typed, ok := specification.(*ast.TypeSpec); ok {
					typeSpecs = append(typeSpecs, typed)
				}
			}
		}
		boundary := approvedBoundaryFile(path)
		if configuration != nil {
			boundary = configuredBoundaryFile(file.relative, configuration.AllowedBoundaryFiles)
		}
		if len(typeSpecs) == 0 && !boundary {
			position, physical := positions(file, file.syntax.Name.Pos())
			diagnostics = append(diagnostics, Diagnostic{
				Position:         position,
				PhysicalPosition: physical,
				Kind:             "file.one-primary-type",
				Message: fmt.Sprintf(
					"profile %s requires one primary named type in handwritten production file %s",
					ProfileJavaStructured,
					filepath.Base(path),
				),
			})
		}
		if len(typeSpecs) > 1 {
			position, physical := positions(file, typeSpecs[1].Name.Pos())
			diagnostics = append(diagnostics, Diagnostic{
				Position:         position,
				PhysicalPosition: physical,
				Kind:             "file.one-primary-type",
				Message: fmt.Sprintf(
					"profile %s requires one named type per handwritten production file; %s declares %d",
					ProfileJavaStructured,
					filepath.Base(path),
					len(typeSpecs),
				),
			})
		}
		for _, declaration := range file.syntax.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if ok && general.Tok == token.TYPE && len(general.Specs) > 1 {
				position, physical := positions(file, general.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position:         position,
					PhysicalPosition: physical,
					Kind:             "file.one-primary-type",
					Message:          "profile java-structured forbids grouped type declarations",
				})
			}
		}
		if len(typeSpecs) == 1 {
			expected := typeFileName(typeSpecs[0].Name.Name)
			if !selectedTypeFileName(file, expected, selection) {
				position, physical := positions(file, typeSpecs[0].Name.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position:         position,
					PhysicalPosition: physical,
					Kind:             "file.name",
					Message: fmt.Sprintf(
						"profile %s requires primary type %s to live in %s",
						ProfileJavaStructured,
						typeSpecs[0].Name.Name,
						expected,
					),
				})
			}
		}
		if strings.EqualFold(filepath.Base(path), "doc.go") {
			for _, declaration := range file.syntax.Decls {
				if general, ok := declaration.(*ast.GenDecl); ok &&
					general.Tok == token.IMPORT {
					continue
				}
				position, physical := positions(file, declaration.Pos())
				diagnostics = append(diagnostics, Diagnostic{
					Position:         position,
					PhysicalPosition: physical,
					Kind:             "file.unrelated-declaration",
					Message: fmt.Sprintf(
						"profile %s reserves doc.go for package documentation, annotation imports, and the package clause",
						ProfileJavaStructured,
					),
				})
			}
		}
		for _, declaration := range file.syntax.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, specification := range general.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					position, physical := positions(file, name.Pos())
					diagnostics = append(diagnostics, Diagnostic{
						Position:         position,
						PhysicalPosition: physical,
						Kind:             "package.mutable-global",
						Message: fmt.Sprintf(
							"profile %s forbids mutable package variable %s; move state onto a managed type",
							ProfileJavaStructured,
							name.Name,
						),
					})
				}
			}
		}
		for _, declaration := range file.syntax.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != "init" {
				continue
			}
			position, physical := positions(file, function.Name.Pos())
			diagnostics = append(diagnostics, Diagnostic{
				Position:         position,
				PhysicalPosition: physical,
				Kind:             "function.init",
				Message: fmt.Sprintf(
					"profile %s forbids init functions; make initialization an explicit constructor or lifecycle method",
					ProfileJavaStructured,
				),
			})
		}
	}
	return diagnostics
}

func selectedTypeFileName(
	file sourceFile,
	expected string,
	selection *BuildSelection,
) bool {
	actual := filepath.Base(file.physical)
	if actual == expected {
		return true
	}
	if selection == nil {
		return false
	}
	stem := strings.TrimSuffix(expected, ".go") + "_"
	if !strings.HasPrefix(actual, stem) || !strings.HasSuffix(actual, ".go") {
		return false
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(actual, stem), ".go")
	if suffix == selection.GOOS || suffix == selection.GOARCH ||
		suffix == selection.GOOS+"_"+selection.GOARCH {
		return true
	}
	if alias := implicitPlatformAlias(selection.GOOS); alias != "" &&
		(suffix == alias || suffix == alias+"_"+selection.GOARCH) {
		return true
	}
	if suffix == "unix" && unixBuildTarget(selection.GOOS) {
		return sourceRequiresUnixFamily(file.syntax, *selection)
	}
	for _, tag := range selection.Tags {
		if suffix == tag {
			return sourceRequiresPositiveTag(file.syntax, tag)
		}
	}
	return false
}

func implicitPlatformAlias(goos string) string {
	switch goos {
	case "android":
		return "linux"
	case "illumos":
		return "solaris"
	case "ios":
		return "darwin"
	default:
		return ""
	}
}

func unixBuildTarget(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd",
		"illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	default:
		return false
	}
}

func sourceBuildConstraint(file *ast.File) constraint.Expr {
	if file == nil {
		return nil
	}
	for _, group := range file.Comments {
		if group == nil || group.End() > file.Package {
			continue
		}
		for _, comment := range group.List {
			if comment == nil || !constraint.IsGoBuild(comment.Text) {
				continue
			}
			expression, err := constraint.Parse(comment.Text)
			if err == nil {
				return expression
			}
			return nil
		}
	}
	return nil
}

func sourceRequiresPositiveTag(file *ast.File, required string) bool {
	return expressionRequiresPositiveTag(sourceBuildConstraint(file), required)
}

func expressionRequiresPositiveTag(expression constraint.Expr, required string) bool {
	if expression == nil {
		return false
	}
	switch value := expression.(type) {
	case *constraint.TagExpr:
		return value.Tag == required
	case *constraint.AndExpr:
		return expressionRequiresPositiveTag(value.X, required) ||
			expressionRequiresPositiveTag(value.Y, required)
	case *constraint.OrExpr:
		return expressionRequiresPositiveTag(value.X, required) &&
			expressionRequiresPositiveTag(value.Y, required)
	case *constraint.NotExpr:
		return false
	default:
		return false
	}
}

func sourceRequiresUnixFamily(
	file *ast.File,
	selection BuildSelection,
) bool {
	expression := sourceBuildConstraint(file)
	if expression == nil || !knownUnixConstraintTags(expression, selection) {
		return false
	}
	if !expression.Eval(func(tag string) bool {
		return selectedBuildTag(selection, selection.GOOS, tag)
	}) {
		return false
	}
	for _, pair := range supportedBuildPairs() {
		goos, goarch, _ := strings.Cut(pair, "/")
		if unixBuildTarget(goos) {
			continue
		}
		for _, cgoEnabled := range []bool{false, true} {
			if expression.Eval(func(tag string) bool {
				candidate := selection
				candidate.GOARCH = goarch
				candidate.CGOEnabled = &cgoEnabled
				return selectedBuildTag(candidate, goos, tag)
			}) {
				return false
			}
		}
	}
	return true
}

func selectedBuildTag(selection BuildSelection, goos string, tag string) bool {
	if tag == goos || tag == selection.GOARCH || tag == "gc" ||
		tag == "cgo" && selection.CGOEnabled != nil && *selection.CGOEnabled ||
		releaseBuildTag(tag) ||
		tag == implicitPlatformAlias(goos) || tag == "unix" && unixBuildTarget(goos) {
		return true
	}
	for _, selected := range selection.Tags {
		if tag == selected {
			return true
		}
	}
	return false
}

func knownUnixConstraintTags(expression constraint.Expr, selection BuildSelection) bool {
	known := true
	expression.Eval(func(tag string) bool {
		if tag == "gc" || tag == "cgo" || tag == "unix" || releaseBuildTag(tag) ||
			unixBuildTarget(tag) || schemaBuildOperatingSystem(tag) ||
			schemaBuildArchitecture(tag) {
			return false
		}
		for _, selected := range selection.Tags {
			if tag == selected {
				return false
			}
		}
		known = false
		return false
	})
	return known
}

func releaseBuildTag(tag string) bool {
	minor, found := strings.CutPrefix(tag, "go1.")
	if !found || minor == "" || strings.Contains(minor, ".") {
		return false
	}
	value, err := strconv.Atoi(minor)
	return err == nil && value >= 1 && value <= 26
}

func schemaBuildOperatingSystems() []string {
	values := make(map[string]struct{})
	for _, pair := range supportedBuildPairs() {
		goos, _, _ := strings.Cut(pair, "/")
		values[goos] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func schemaBuildArchitecture(value string) bool {
	for _, pair := range supportedBuildPairs() {
		_, goarch, _ := strings.Cut(pair, "/")
		if value == goarch {
			return true
		}
	}
	return false
}

func schemaBuildOperatingSystem(value string) bool {
	for _, goos := range schemaBuildOperatingSystems() {
		if value == goos {
			return true
		}
	}
	return false
}

func configurationContainsSelection(
	configuration Configuration,
	selection BuildSelection,
) bool {
	for _, declared := range configuration.BuildSelections {
		if declared.Name == selection.Name && declared.GOOS == selection.GOOS &&
			declared.GOARCH == selection.GOARCH &&
			declared.CGOEnabled != nil && selection.CGOEnabled != nil &&
			*declared.CGOEnabled == *selection.CGOEnabled &&
			strings.Join(declared.SourceRoots, "\x00") == strings.Join(selection.SourceRoots, "\x00") &&
			strings.Join(declared.Tags, "\x00") == strings.Join(selection.Tags, "\x00") {
			return true
		}
	}
	return false
}

func receiverLocationDiagnostics(
	symbols []load.Symbol,
	files map[string]sourceFile,
	typeFiles map[string]string,
) []Diagnostic {
	var diagnostics []Diagnostic
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolMethod {
			continue
		}
		methodFile := cleanFile(symbol.PhysicalPosition.Filename)
		if _, handwritten := files[methodFile]; !handwritten {
			continue
		}
		typeFile := typeFiles[symbol.PackagePath+"\x00"+symbol.Receiver]
		if typeFile == "" || typeFile == methodFile {
			continue
		}
		diagnostics = append(diagnostics, symbolDiagnostic(
			symbol,
			"file.method-owner",
			fmt.Sprintf(
				"profile %s requires method %s to live with receiver type %s in %s",
				ProfileJavaStructured,
				symbol.DisplayLabel,
				symbol.Receiver,
				filepath.Base(typeFile),
			),
		))
	}
	return diagnostics
}

type functionAnnotationKind uint8

const (
	functionAnnotationNone functionAnnotationKind = iota
	functionAnnotationBean
	functionAnnotationTopic
)

func functionAnnotationKinds(
	resolution resolve.Result,
) map[string]functionAnnotationKind {
	result := make(map[string]functionAnnotationKind)
	for _, occurrence := range resolution.Occurrences {
		if occurrence.Target != annotation.TargetFunction {
			continue
		}
		switch {
		case occurrence.HasContribution(sdk.ContributionProvider):
			result[occurrence.SymbolID] = functionAnnotationBean
		case occurrence.HasContribution(sdk.ContributionEventTopic):
			result[occurrence.SymbolID] = functionAnnotationTopic
		}
	}
	return result
}

func freeFunctionDiagnostics(
	symbols []load.Symbol,
	files map[string]sourceFile,
	typesByFile map[string][]string,
	annotations map[string]functionAnnotationKind,
	packageNames map[string]string,
) []Diagnostic {
	var diagnostics []Diagnostic
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolFunction || symbol.Receiver != "" ||
			symbol.Name == "init" {
			continue
		}
		file := cleanFile(symbol.PhysicalPosition.Filename)
		if _, handwritten := files[file]; !handwritten {
			continue
		}
		if symbol.Name == "main" && packageNames[symbol.PackagePath] == "main" {
			continue
		}
		switch annotations[symbol.ID] {
		case functionAnnotationNone:
		case functionAnnotationBean:
			if approvedAnnotatedBoundary(file, "_bean.go", symbols) {
				continue
			}
			diagnostics = append(diagnostics, symbolDiagnostic(
				symbol,
				"function.package-level",
				fmt.Sprintf(
					"package-level @Bean provider %s must be the sole function in a dedicated *_bean.go boundary file",
					symbol.DisplayLabel,
				),
			))
			continue
		case functionAnnotationTopic:
			if approvedAnnotatedBoundary(file, "_topic.go", symbols) {
				continue
			}
			diagnostics = append(diagnostics, symbolDiagnostic(
				symbol,
				"function.package-level",
				fmt.Sprintf(
					"event topic %s must be the sole function in a dedicated *_topic.go boundary file under profile %s",
					symbol.DisplayLabel,
					ProfileJavaStructured,
				),
			))
			continue
		}
		if associatedFunction(symbol.Name, typesByFile[file]) {
			continue
		}
		diagnostics = append(diagnostics, symbolDiagnostic(
			symbol,
			"function.package-level",
			fmt.Sprintf(
				"package function %s is not type-associated under profile %s; use a receiver method or managed component",
				symbol.DisplayLabel,
				ProfileJavaStructured,
			),
		))
	}
	return diagnostics
}

func approvedBoundaryFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "doc.go" || base == "main.go" ||
		base == "package_constants.go" ||
		strings.HasSuffix(base, "_bean.go") ||
		strings.HasSuffix(base, "_topic.go")
}

func approvedAnnotatedBoundary(
	file string,
	suffix string,
	symbols []load.Symbol,
) bool {
	if !strings.HasSuffix(strings.ToLower(filepath.Base(file)), suffix) {
		return false
	}
	functions := 0
	for _, symbol := range symbols {
		if symbol.Kind == load.SymbolFunction && symbol.Receiver == "" &&
			cleanFile(symbol.PhysicalPosition.Filename) == file {
			functions++
		}
	}
	return functions == 1
}

func typeFileName(name string) string {
	var result strings.Builder
	runes := []rune(name)
	for index, current := range runes {
		if index > 0 && current >= 'A' && current <= 'Z' {
			previous := runes[index-1]
			nextLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			if previous >= 'a' && previous <= 'z' ||
				previous >= '0' && previous <= '9' ||
				previous >= 'A' && previous <= 'Z' && nextLower {
				result.WriteByte('_')
			}
		}
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		result.WriteRune(current)
	}
	result.WriteString(".go")
	return result.String()
}

func associatedFunction(name string, typeNames []string) bool {
	for _, typeName := range typeNames {
		if name == "New"+typeName ||
			name == "Parse"+typeName ||
			strings.HasPrefix(name, "New"+typeName+"From") &&
				len(name) > len("New")+len(typeName)+len("From") {
			return true
		}
	}
	return false
}

func constructorDiagnostics(providers []provider.Provider) []Diagnostic {
	var diagnostics []Diagnostic
	for _, item := range providers {
		if item.Source != provider.SourceStereotype {
			continue
		}
		if item.Construction == provider.ConstructionAllocate {
			diagnostics = append(diagnostics, symbolDiagnostic(
				item.Symbol,
				"constructor.explicit",
				fmt.Sprintf(
					"profile %s requires an explicit New%s constructor for managed type %s",
					ProfileJavaStructured,
					item.Symbol.Name,
					item.Symbol.DisplayLabel,
				),
			))
			continue
		}
		if item.Construction != provider.ConstructionFactory {
			continue
		}
		expected := "New" + item.Symbol.Name
		if item.Constructor.Name != expected {
			diagnostics = append(diagnostics, symbolDiagnostic(
				item.Constructor,
				"constructor.name",
				fmt.Sprintf(
					"profile %s requires constructor %s for managed type %s; generic or custom constructor names are forbidden",
					ProfileJavaStructured,
					expected,
					item.Symbol.DisplayLabel,
				),
			))
		}
		if cleanFile(item.Constructor.PhysicalPosition.Filename) !=
			cleanFile(item.Symbol.PhysicalPosition.Filename) {
			diagnostics = append(diagnostics, symbolDiagnostic(
				item.Constructor,
				"constructor.location",
				fmt.Sprintf(
					"profile %s requires constructor %s to live with managed type %s in %s",
					ProfileJavaStructured,
					item.Constructor.Name,
					item.Symbol.DisplayLabel,
					filepath.Base(item.Symbol.PhysicalPosition.Filename),
				),
			))
		}
	}
	return diagnostics
}

func implicitInterfaceDiagnostics(
	symbols []load.Symbol,
	providers []provider.Provider,
) []Diagnostic {
	interfaces := namedInterfaces(symbols)
	var diagnostics []Diagnostic
	for _, item := range providers {
		if item.Source != provider.SourceStereotype || item.Output == nil {
			continue
		}
		explicit := make(map[string]struct{}, len(item.Interfaces))
		for _, binding := range item.Interfaces {
			explicit[binding.TypeID] = struct{}{}
		}
		for _, candidate := range interfaces {
			if _, declared := explicit[candidate.typeID]; declared ||
				!types.Implements(item.Output, candidate.interfaceType) {
				continue
			}
			diagnostics = append(diagnostics, symbolDiagnostic(
				item.Symbol,
				"bean.interface-binding",
				fmt.Sprintf(
					"managed type %s satisfies application interface %s; profile %s requires an explicit @Implements(%s) relationship",
					item.OutputTypeID,
					candidate.typeID,
					ProfileJavaStructured,
					candidate.typeID,
				),
			))
		}
	}
	return diagnostics
}

type namedInterface struct {
	typeID        string
	interfaceType *types.Interface
}

func namedInterfaces(symbols []load.Symbol) []namedInterface {
	var result []namedInterface
	for _, symbol := range symbols {
		if symbol.Kind != load.SymbolType {
			continue
		}
		typeName, ok := symbol.Object.(*types.TypeName)
		if !ok || typeName.IsAlias() {
			continue
		}
		named, ok := types.Unalias(typeName.Type()).(*types.Named)
		if !ok || named.TypeParams() != nil && named.TypeParams().Len() != 0 {
			continue
		}
		interfaceType, ok := named.Underlying().(*types.Interface)
		if !ok {
			continue
		}
		interfaceType.Complete()
		result = append(result, namedInterface{
			typeID:        provider.TypeID(named),
			interfaceType: interfaceType,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].typeID < result[j].typeID
	})
	return result
}

func primaryPackageNames(packages []load.Package) map[string]string {
	result := make(map[string]string, len(packages))
	for _, pkg := range packages {
		result[pkg.Path] = pkg.Name
	}
	return result
}

func positions(
	file sourceFile,
	position token.Pos,
) (token.Position, token.Position) {
	return file.fileSet.PositionFor(position, true),
		file.fileSet.PositionFor(position, false)
}

func symbolDiagnostic(
	symbol load.Symbol,
	kind string,
	message string,
) Diagnostic {
	return Diagnostic{
		Position:         symbol.Position,
		PhysicalPosition: symbol.PhysicalPosition,
		SymbolID:         symbol.ID,
		Kind:             kind,
		Message:          message,
	}
}

func sortedFilePaths(files map[string]sourceFile) []string {
	result := make([]string, 0, len(files))
	for path := range files {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func cleanFile(value string) string {
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i].PhysicalPosition
		right := diagnostics[j].PhysicalPosition
		if left.Filename != right.Filename {
			return left.Filename < right.Filename
		}
		if left.Offset != right.Offset {
			return left.Offset < right.Offset
		}
		if diagnostics[i].Kind != diagnostics[j].Kind {
			return diagnostics[i].Kind < diagnostics[j].Kind
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
}
