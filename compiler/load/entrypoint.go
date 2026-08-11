package load

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/spice-framework/toolchain/compiler/targetid"
)

const generatedApplicationAnalysisFilename = "zz_spice_analysis.go"

func addGeneratedApplicationEntrypointOverlays(
	options Options,
	patterns []string,
	overlay map[string][]byte,
) (map[string][]byte, []Diagnostic, error) {
	moduleRoot, modulePath, found, err := enclosingModule(options.Dir)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return overlay, nil, nil
	}
	sourceRoot, err := os.OpenRoot(moduleRoot)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"open generated application analysis root: %w",
			err,
		)
	}
	files, discoverErr := generatedEntrypointSourceFiles(
		sourceRoot,
		moduleRoot,
		options,
		patterns,
		overlay,
	)
	if discoverErr != nil {
		return nil, nil, errors.Join(discoverErr, sourceRoot.Close())
	}
	result := cloneOverlay(overlay)
	if result == nil {
		result = make(map[string][]byte)
	}
	var diagnostics []Diagnostic
	for _, filename := range files {
		content, present := overlay[filename]
		if !present {
			relative, relativeErr := filepath.Rel(moduleRoot, filename)
			if relativeErr != nil || !filepath.IsLocal(relative) {
				continue
			}
			content, err = sourceRoot.ReadFile(relative)
			if err != nil {
				return nil, nil, errors.Join(
					fmt.Errorf(
						"read generated application entrypoint %q: %w",
						filename,
						err,
					),
					sourceRoot.Close(),
				)
			}
		}
		analysis, sourceDiagnostics := analyzeGeneratedEntrypoint(
			moduleRoot,
			modulePath,
			filename,
			content,
		)
		diagnostics = append(diagnostics, sourceDiagnostics...)
		if analysis.packagePath == "" {
			continue
		}
		analysisFile := filepath.Join(
			moduleRoot,
			filepath.FromSlash(
				strings.TrimPrefix(
					analysis.packagePath,
					modulePath+"/",
				),
			),
			generatedApplicationAnalysisFilename,
		)
		result[analysisFile] = generatedApplicationAnalysisSource()
	}
	if closeErr := sourceRoot.Close(); closeErr != nil {
		return nil, nil, fmt.Errorf(
			"close generated application analysis root: %w",
			closeErr,
		)
	}
	sortDiagnostics(diagnostics)
	return result, diagnostics, nil
}

// GeneratedApplicationEntrypoint identifies one compiler-validated
// spice_generate application bridge selected by an exact Go build context.
// PackagePath is the handwritten package-main target; GeneratedPackagePath is
// the analysis-only package supplied through the loader overlay.
type GeneratedApplicationEntrypoint struct {
	PackagePath          string
	GeneratedPackagePath string
	Filename             string
}

// DiscoverGeneratedApplicationEntrypoints reports only entrypoints reachable
// through patterns under options' exact Go build context. It shares the same
// parser and body validation as Load's generated-package preparation.
func DiscoverGeneratedApplicationEntrypoints(
	options Options,
	patterns ...string,
) ([]GeneratedApplicationEntrypoint, []Diagnostic, error) {
	moduleRoot, modulePath, found, err := enclosingModule(options.Dir)
	if err != nil || !found {
		return nil, nil, err
	}
	root, err := os.OpenRoot(moduleRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("open generated application analysis root: %w", err)
	}
	files, discoverErr := generatedEntrypointSourceFiles(
		root,
		moduleRoot,
		options,
		patterns,
		options.Overlay,
	)
	if discoverErr != nil {
		return nil, nil, errors.Join(discoverErr, root.Close())
	}
	var result []GeneratedApplicationEntrypoint
	var diagnostics []Diagnostic
	for _, filename := range files {
		content, present := overlayFileContent(options.Overlay, filename)
		if !present {
			relative, relativeErr := filepath.Rel(moduleRoot, filename)
			if relativeErr != nil || !filepath.IsLocal(relative) {
				continue
			}
			content, err = root.ReadFile(relative)
			if err != nil {
				return nil, nil, errors.Join(
					fmt.Errorf("read generated application entrypoint %q: %w", filename, err),
					root.Close(),
				)
			}
		}
		analysis, sourceDiagnostics := analyzeGeneratedEntrypoint(
			moduleRoot,
			modulePath,
			filename,
			content,
		)
		diagnostics = append(diagnostics, sourceDiagnostics...)
		if analysis.packagePath == "" {
			continue
		}
		result = append(result, GeneratedApplicationEntrypoint{
			PackagePath:          analysis.entrypointPackage,
			GeneratedPackagePath: analysis.packagePath,
			Filename:             filepath.Clean(filename),
		})
	}
	if closeErr := root.Close(); closeErr != nil {
		return nil, nil, fmt.Errorf("close generated application analysis root: %w", closeErr)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PackagePath != result[j].PackagePath {
			return result[i].PackagePath < result[j].PackagePath
		}
		return result[i].Filename < result[j].Filename
	})
	sortDiagnostics(diagnostics)
	return result, diagnostics, nil
}

type generatedEntrypointAnalysis struct {
	entrypointPackage string
	packagePath       string
}

func analyzeGeneratedEntrypoint(
	moduleRoot string,
	modulePath string,
	filename string,
	content []byte,
) (generatedEntrypointAnalysis, []Diagnostic) {
	fileSet := token.NewFileSet()
	file, err := goparser.ParseFile(
		fileSet,
		filename,
		content,
		goparser.ParseComments,
	)
	if err != nil {
		return generatedEntrypointAnalysis{}, nil
	}
	mainFunctions := annotatedMainFunctions(file)
	if len(mainFunctions) == 0 {
		return generatedEntrypointAnalysis{}, nil
	}
	relativeDirectory, err := filepath.Rel(moduleRoot, filepath.Dir(filename))
	if err != nil ||
		(!filepath.IsLocal(relativeDirectory) && relativeDirectory != ".") {
		return generatedEntrypointAnalysis{}, nil
	}
	entrypointPackage := modulePath
	if relativeDirectory != "." {
		entrypointPackage = path.Join(
			modulePath,
			filepath.ToSlash(relativeDirectory),
		)
	}
	generatedPackage := path.Join(
		modulePath,
		"internal",
		"spicegen",
		targetid.Default(path.Base(entrypointPackage)),
	)
	alias, imported := generatedApplicationImport(file, generatedPackage)
	osAlias, osImported := generatedApplicationImport(file, "os")
	mainFunction := mainFunctions[0]
	position := fileSet.PositionFor(mainFunction.Name.Pos(), true)
	if !imported || !osImported {
		return generatedEntrypointAnalysis{}, []Diagnostic{{
			PackagePath: entrypointPackage,
			Position:    position.String(),
			Filename:    position.Filename,
			Line:        position.Line,
			Column:      position.Column,
			Kind:        "generated-entrypoint",
			Message: fmt.Sprintf(
				"@Application func main must import generated target package %q and call its Main function",
				generatedPackage,
			),
		}}
	}
	if !generatedApplicationMainCall(mainFunction, alias, osAlias) {
		return generatedEntrypointAnalysis{}, []Diagnostic{{
			PackagePath: entrypointPackage,
			Position:    position.String(),
			Filename:    position.Filename,
			Line:        position.Line,
			Column:      position.Column,
			Kind:        "generated-entrypoint",
			Message: fmt.Sprintf(
				"@Application func main must call os.Exit(%s.Main(os.Args[1:]))",
				alias,
			),
		}}
	}
	return generatedEntrypointAnalysis{
		entrypointPackage: entrypointPackage,
		packagePath:       generatedPackage,
	}, nil
}

func generatedApplicationImport(file *ast.File, packagePath string) (string, bool) {
	for _, spec := range file.Imports {
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath != packagePath {
			continue
		}
		alias := path.Base(packagePath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "" || alias == "." || alias == "_" {
			return "", false
		}
		return alias, true
	}
	return "", false
}

func generatedApplicationMainCall(
	function *ast.FuncDecl,
	alias string,
	osAlias string,
) bool {
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	statement, ok := function.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	exitCall, ok := statement.X.(*ast.CallExpr)
	if !ok ||
		!generatedSelector(exitCall.Fun, osAlias, "Exit") ||
		len(exitCall.Args) != 1 {
		return false
	}
	mainCall, ok := exitCall.Args[0].(*ast.CallExpr)
	return ok &&
		generatedSelector(mainCall.Fun, alias, "Main") &&
		len(mainCall.Args) == 1 &&
		generatedProcessArguments(mainCall.Args[0], osAlias)
}

func generatedSelector(expression ast.Expr, qualifier string, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != name {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == qualifier
}

func generatedProcessArguments(expression ast.Expr, osAlias string) bool {
	slice, ok := expression.(*ast.SliceExpr)
	if !ok || slice.Slice3 || slice.High != nil || slice.Max != nil {
		return false
	}
	low, ok := slice.Low.(*ast.BasicLit)
	return ok &&
		low.Kind == token.INT &&
		low.Value == "1" &&
		generatedSelector(slice.X, osAlias, "Args")
}

func generatedApplicationAnalysisSource() []byte {
	return []byte(
		"// Code generated by Spice analysis. DO NOT EDIT.\n\n" +
			"package spicegen\n\n" +
			"func Main([]string) int { return 0 }\n",
	)
}

// IsGeneratedApplicationEntrypoint reports whether symbol is the exact
// compiler-owned spice_generate bridge shape accepted by generated-package
// preparation. It does not accept ordinary application functions.
func IsGeneratedApplicationEntrypoint(program *Program, symbol Symbol) bool {
	if program == nil || symbol.Kind != SymbolFunction || symbol.Name != "main" ||
		symbol.Receiver != "" || filepath.Base(symbol.PhysicalPosition.Filename) == "main.go" {
		return false
	}
	function, ok := symbol.Node.(*ast.FuncDecl)
	if !ok {
		return false
	}
	for _, pkg := range program.PrimaryPackages() {
		if pkg.Path != symbol.PackagePath || pkg.Name != "main" {
			continue
		}
		for _, source := range pkg.Files {
			if filepath.Clean(source.PhysicalPath) != filepath.Clean(symbol.PhysicalPosition.Filename) ||
				source.Syntax == nil || !exactAnalysisBuildConstraint(source.Syntax) {
				continue
			}
			generatedPackage := path.Join(
				pkg.ModulePath,
				"internal",
				"spicegen",
				targetid.Default(path.Base(pkg.Path)),
			)
			alias, imported := generatedApplicationImport(source.Syntax, generatedPackage)
			osAlias, osImported := generatedApplicationImport(source.Syntax, "os")
			return imported && osImported &&
				generatedApplicationMainCall(function, alias, osAlias)
		}
	}
	return false
}

func exactAnalysisBuildConstraint(file *ast.File) bool {
	if file == nil {
		return false
	}
	for _, group := range file.Comments {
		if group == nil || group.End() > file.Package {
			continue
		}
		for _, comment := range group.List {
			if comment != nil && strings.TrimSpace(comment.Text) == "//go:build spice_generate" {
				return true
			}
		}
	}
	return false
}

func generatedEntrypointSourceFiles(
	root *os.Root,
	moduleRoot string,
	options Options,
	patterns []string,
	overlay map[string][]byte,
) ([]string, error) {
	directories, _ := compositionPatternDirectories(options.Dir, patterns)
	selectedDirectories := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		selectedDirectories[filepath.Clean(directory)] = struct{}{}
	}
	buildContext := compositionBuildContext(options)
	buildContext.OpenFile = func(filename string) (io.ReadCloser, error) {
		if content, found := overlayFileContent(overlay, filename); found {
			return io.NopCloser(bytes.NewReader(content)), nil
		}
		relative, err := filepath.Rel(moduleRoot, filepath.Clean(filename))
		if err != nil || !filepath.IsLocal(relative) {
			return nil, fs.ErrPermission
		}
		return root.Open(relative)
	}
	files := make(map[string]struct{})
	if err := walkGeneratedEntrypointSources(
		root,
		moduleRoot,
		selectedDirectories,
		buildContext,
		files,
	); err != nil {
		return nil, fmt.Errorf(
			"discover generated application entrypoints: %w",
			err,
		)
	}
	for filename := range overlay {
		filename = filepath.Clean(filename)
		matched, matchErr := buildContext.MatchFile(
			filepath.Dir(filename),
			filepath.Base(filename),
		)
		if matchErr == nil && matched && generatedEntrypointSource(filename) &&
			generatedEntrypointSelectedDirectory(selectedDirectories, filename) &&
			generatedEntrypointWithinRoot(moduleRoot, filename) &&
			!generatedEntrypointInsideNestedModule(moduleRoot, filename) {
			files[filename] = struct{}{}
		}
	}
	result := make([]string, 0, len(files))
	for filename := range files {
		result = append(result, filename)
	}
	sort.Strings(result)
	return result, nil
}

func walkGeneratedEntrypointSources(
	root *os.Root,
	moduleRoot string,
	selectedDirectories map[string]struct{},
	buildContext build.Context,
	files map[string]struct{},
) error {
	return fs.WalkDir(root.FS(), ".", func(
		relative string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filepath.ToSlash(relative) == "internal/spicegen" {
				return filepath.SkipDir
			}
			if relative != "." && generatedEntrypointExcludedDirectory(
				entry.Name(),
			) {
				return filepath.SkipDir
			}
			if relative != "." {
				if _, statErr := fs.Stat(
					root.FS(),
					path.Join(filepath.ToSlash(relative), "go.mod"),
				); statErr == nil {
					return filepath.SkipDir
				} else if !errors.Is(statErr, fs.ErrNotExist) {
					return statErr
				}
			}
			return nil
		}
		if generatedEntrypointSource(relative) {
			filename := filepath.Join(moduleRoot, filepath.FromSlash(relative))
			if !generatedEntrypointSelectedDirectory(selectedDirectories, filename) {
				return nil
			}
			matched, matchErr := buildContext.MatchFile(
				filepath.Dir(filename),
				filepath.Base(filename),
			)
			if matchErr != nil {
				return fmt.Errorf(
					"match generated application entrypoint %q: %w",
					filename,
					matchErr,
				)
			}
			if !matched {
				return nil
			}
			files[filename] = struct{}{}
		}
		return nil
	})
}

func generatedEntrypointSelectedDirectory(
	directories map[string]struct{},
	filename string,
) bool {
	_, found := directories[filepath.Clean(filepath.Dir(filename))]
	return found
}

func generatedEntrypointExcludedDirectory(name string) bool {
	switch name {
	case ".git", ".spice", "testdata", "vendor":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func generatedEntrypointSource(filename string) bool {
	base := filepath.Base(filename)
	return strings.EqualFold(filepath.Ext(base), ".go") &&
		!strings.HasSuffix(base, "_test.go") &&
		!strings.HasSuffix(base, "_spice_gen.go")
}

func generatedEntrypointInsideNestedModule(root string, filename string) bool {
	for directory := filepath.Dir(filename); directory != root; {
		module, err := os.OpenRoot(directory)
		if err == nil {
			_, readErr := module.ReadFile("go.mod")
			closeErr := module.Close()
			if readErr == nil && closeErr == nil {
				return true
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory || !generatedEntrypointWithinRoot(root, parent) {
			return false
		}
		directory = parent
	}
	return false
}

func generatedEntrypointWithinRoot(root string, filename string) bool {
	relative, err := filepath.Rel(root, filename)
	return err == nil &&
		(relative == "." || filepath.IsLocal(relative))
}

func enclosingModule(directory string) (string, string, bool, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", "", false, fmt.Errorf(
			"resolve generated application analysis directory: %w",
			err,
		)
	}
	for candidate := absolute; ; candidate = filepath.Dir(candidate) {
		root, openErr := os.OpenRoot(candidate)
		if openErr != nil {
			return "", "", false, fmt.Errorf(
				"open module candidate %q: %w",
				candidate,
				openErr,
			)
		}
		content, readErr := root.ReadFile("go.mod")
		closeErr := root.Close()
		switch {
		case readErr == nil && closeErr == nil:
			modulePath := modfile.ModulePath(content)
			if modulePath == "" {
				return "", "", false, fmt.Errorf(
					"module file %q has no module path",
					filepath.Join(candidate, "go.mod"),
				)
			}
			return candidate, modulePath, true, nil
		case readErr == nil:
			return "", "", false, fmt.Errorf(
				"close module candidate %q: %w",
				candidate,
				closeErr,
			)
		case !errors.Is(readErr, os.ErrNotExist):
			return "", "", false, fmt.Errorf(
				"read module candidate %q: %w",
				candidate,
				readErr,
			)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", "", false, nil
		}
	}
}
