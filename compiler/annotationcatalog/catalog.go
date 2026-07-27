// Package annotationcatalog discovers statically declared annotation
// descriptors from the target application's existing offline Go module graph.
package annotationcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/StevenBuglione/spice/compiler/annotationhost"
)

const (
	sdkPackagePath         = "github.com/StevenBuglione/spice/annotation/sdk"
	maximumCatalogModules  = 512
	maximumCatalogFiles    = 100_000
	maximumCatalogBytes    = 64 << 20
	maximumDescriptorBytes = 1 << 20
	maximumGoListStderr    = 256 << 10
)

// Candidate is one lexically valid exported func() sdk.Definition declaration
// and its existing Go module provenance. Full static decoding still occurs
// through the typed compiler after the application imports the symbol.
type Candidate struct {
	Package            string
	Symbol             string
	CanonicalName      string
	Summary            string
	Tool               string
	Handler            string
	Protocol           string
	Module             string
	Version            string
	ReplacementModule  string
	ReplacementVersion string
	ReplacementDir     string
	LocalReplacement   bool
	ToolAuthorized     bool
	DescriptorPosition token.Position
}

type moduleRoot struct {
	path               string
	version            string
	directory          string
	replacementPath    string
	replacementVersion string
	replacementDir     string
	localReplacement   bool
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace"`
}

type scanBudget struct {
	files int
	bytes int64
}

// Discover scans only source already selected by go.mod, go.work, the module
// cache, local replacements, or vendor. It forces GOPROXY=off and never edits
// module files or launches annotation tools.
func Discover(
	ctx context.Context,
	root string,
	environment []string,
) ([]Candidate, error) {
	if ctx == nil {
		return nil, errors.New(
			"annotation catalog context must not be nil",
		)
	}
	module, err := annotationhost.ReadTargetModule(root)
	if err != nil {
		return nil, err
	}
	roots, err := catalogModuleRoots(ctx, module, environment)
	if err != nil {
		return nil, err
	}
	if len(roots) > maximumCatalogModules {
		return nil, fmt.Errorf(
			"annotation catalog module graph has %d modules; maximum is %d",
			len(roots),
			maximumCatalogModules,
		)
	}
	authorized := make(map[string]struct{}, len(module.Tools))
	for _, tool := range module.Tools {
		authorized[tool] = struct{}{}
	}
	budget := &scanBudget{}
	var result []Candidate
	for _, item := range roots {
		candidates, scanErr := scanModule(
			ctx,
			item,
			authorized,
			budget,
		)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, candidates...)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Package != result[right].Package {
			return result[left].Package < result[right].Package
		}
		return result[left].Symbol < result[right].Symbol
	})
	return compactCandidates(result), nil
}

func catalogModuleRoots(
	ctx context.Context,
	module annotationhost.TargetModule,
	environment []string,
) ([]moduleRoot, error) {
	main := moduleRoot{
		path:             module.Path,
		directory:        module.Root,
		localReplacement: true,
	}
	vendorRoot := filepath.Join(module.Root, "vendor")
	if _, err := os.Stat(filepath.Join(vendorRoot, "modules.txt")); err == nil {
		vendored, parseErr := vendorModuleRoots(vendorRoot)
		if parseErr != nil {
			return nil, parseErr
		}
		return append([]moduleRoot{main}, vendored...), nil
	}
	command := exec.CommandContext( // #nosec G204 -- executable and arguments are fixed.
		ctx,
		"go",
		"list",
		"-mod=readonly",
		"-m",
		"-json",
		"all",
	)
	command.Dir = module.Root
	command.Env = catalogEnvironment(environment)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > maximumGoListStderr {
			message = message[:maximumGoListStderr] + "..."
		}
		if message != "" {
			message = ": " + message
		}
		return nil, fmt.Errorf(
			"discover annotation catalog with offline go list: %w%s",
			err,
			message,
		)
	}
	decoder := json.NewDecoder(&stdout)
	var roots []moduleRoot
	for {
		var listed listedModule
		if err := decoder.Decode(&listed); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf(
				"decode offline Go module graph: %w",
				err,
			)
		}
		item := listedModuleRoot(listed)
		if item.directory != "" {
			roots = append(roots, item)
		}
	}
	return compactModuleRoots(roots), nil
}

func listedModuleRoot(listed listedModule) moduleRoot {
	result := moduleRoot{
		path:      listed.Path,
		version:   listed.Version,
		directory: filepath.Clean(listed.Dir),
	}
	if listed.Main {
		result.localReplacement = true
	}
	if listed.Replace != nil {
		result.replacementPath = listed.Replace.Path
		result.replacementVersion = listed.Replace.Version
		result.replacementDir = filepath.Clean(listed.Replace.Dir)
		result.localReplacement = listed.Replace.Version == ""
	}
	return result
}

func vendorModuleRoots(vendorRoot string) ([]moduleRoot, error) {
	root, err := os.OpenRoot(vendorRoot)
	if err != nil {
		return nil, fmt.Errorf("open vendor module catalog: %w", err)
	}
	content, readErr := root.ReadFile("modules.txt")
	closeErr := root.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read vendor module catalog: %w", err)
	}
	var roots []moduleRoot
	for line := range strings.SplitSeq(string(content), "\n") {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "# "))
		if len(fields) < 2 || fields[0] == "=>" {
			continue
		}
		roots = append(roots, moduleRoot{
			path:      fields[0],
			version:   fields[1],
			directory: filepath.Join(vendorRoot, filepath.FromSlash(fields[0])),
		})
	}
	return compactModuleRoots(roots), nil
}

func compactModuleRoots(values []moduleRoot) []moduleRoot {
	sort.Slice(values, func(left, right int) bool {
		if values[left].directory != values[right].directory {
			return values[left].directory < values[right].directory
		}
		return values[left].path < values[right].path
	})
	result := values[:0]
	for _, value := range values {
		if value.directory == "" ||
			len(result) != 0 &&
				samePath(result[len(result)-1].directory, value.directory) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func scanModule(
	ctx context.Context,
	module moduleRoot,
	authorized map[string]struct{},
	budget *scanBudget,
) ([]Candidate, error) {
	info, err := os.Stat(module.directory)
	if err != nil {
		return nil, fmt.Errorf(
			"inspect annotation catalog module %s@%s: %w",
			module.path,
			module.version,
			err,
		)
	}
	if !info.IsDir() {
		return nil, nil
	}
	root, err := os.OpenRoot(module.directory)
	if err != nil {
		return nil, fmt.Errorf(
			"open annotation catalog module %s@%s: %w",
			module.path,
			module.version,
			err,
		)
	}
	var result []Candidate
	err = filepath.WalkDir(
		module.directory,
		func(path string, entry fs.DirEntry, walkErr error) error {
			candidates, entryErr := scanCatalogEntry(
				ctx,
				module,
				root,
				path,
				entry,
				walkErr,
				authorized,
				budget,
			)
			if entryErr != nil {
				return entryErr
			}
			result = append(result, candidates...)
			return nil
		},
	)
	err = errors.Join(err, root.Close())
	if err != nil {
		return nil, fmt.Errorf(
			"scan annotation catalog module %s@%s: %w",
			module.path,
			module.version,
			err,
		)
	}
	return result, nil
}

func scanCatalogEntry(
	ctx context.Context,
	module moduleRoot,
	root *os.Root,
	path string,
	entry fs.DirEntry,
	walkErr error,
	authorized map[string]struct{},
	budget *scanBudget,
) ([]Candidate, error) {
	if walkErr != nil {
		return nil, walkErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if entry.IsDir() {
		return nil, scanCatalogDirectory(
			root,
			module.directory,
			path,
			entry.Name(),
		)
	}
	if !catalogGoFile(entry.Name()) {
		return nil, nil
	}
	budget.files++
	if budget.files > maximumCatalogFiles {
		return nil, fmt.Errorf(
			"annotation catalog exceeds %d Go files",
			maximumCatalogFiles,
		)
	}
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() ||
		info.Size() > maximumDescriptorBytes {
		return nil, nil
	}
	budget.bytes += info.Size()
	if budget.bytes > maximumCatalogBytes {
		return nil, fmt.Errorf(
			"annotation catalog exceeds %d source bytes",
			maximumCatalogBytes,
		)
	}
	relative, err := filepath.Rel(module.directory, path)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve annotation catalog source %q: %w",
			path,
			err,
		)
	}
	if relative != "." && !filepath.IsLocal(relative) {
		return nil, fmt.Errorf(
			"annotation catalog source %q escapes module %q",
			path,
			module.directory,
		)
	}
	content, err := root.ReadFile(relative)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(content, []byte(sdkPackagePath)) {
		return nil, nil
	}
	return descriptorCandidates(
		module,
		path,
		content,
		authorized,
	)
}

func scanCatalogDirectory(
	root *os.Root,
	moduleDirectory string,
	directory string,
	name string,
) error {
	if directory != moduleDirectory && excludedCatalogDirectory(name) {
		return filepath.SkipDir
	}
	nested, err := nestedCatalogModule(
		root,
		moduleDirectory,
		directory,
	)
	if err != nil {
		return err
	}
	if nested {
		return filepath.SkipDir
	}
	return nil
}

func nestedCatalogModule(
	root *os.Root,
	moduleDirectory string,
	directory string,
) (bool, error) {
	if samePath(moduleDirectory, directory) {
		return false, nil
	}
	relative, err := filepath.Rel(moduleDirectory, directory)
	if err != nil {
		return false, fmt.Errorf(
			"resolve nested annotation catalog module %q: %w",
			directory,
			err,
		)
	}
	if !filepath.IsLocal(relative) {
		return false, fmt.Errorf(
			"nested annotation catalog module %q escapes %q",
			directory,
			moduleDirectory,
		)
	}
	_, err = root.Stat(filepath.Join(relative, "go.mod"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf(
		"inspect nested annotation catalog module %q: %w",
		directory,
		err,
	)
}

func descriptorCandidates(
	module moduleRoot,
	file string,
	content []byte,
	authorized map[string]struct{},
) ([]Candidate, error) {
	fileSet := token.NewFileSet()
	source, err := parser.ParseFile(
		fileSet,
		file,
		content,
		parser.SkipObjectResolution,
	)
	if err != nil && source == nil {
		return nil, fmt.Errorf(
			"parse annotation descriptor candidate %q: %w",
			file,
			err,
		)
	}
	sdkAlias := descriptorSDKAlias(source)
	if sdkAlias == "" {
		return nil, nil
	}
	packagePath, err := catalogPackagePath(module, filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	var result []Candidate
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !descriptorDeclaration(function, sdkAlias) {
			continue
		}
		candidate := decodeCandidateLiteral(function)
		candidate.Package = packagePath
		candidate.Symbol = function.Name.Name
		candidate.Module = module.path
		candidate.Version = module.version
		candidate.ReplacementModule = module.replacementPath
		candidate.ReplacementVersion = module.replacementVersion
		candidate.ReplacementDir = module.replacementDir
		candidate.LocalReplacement = module.localReplacement
		candidate.DescriptorPosition = fileSet.Position(
			function.Name.Pos(),
		)
		_, candidate.ToolAuthorized = authorized[candidate.Tool]
		if candidate.CanonicalName == "" ||
			candidate.Tool == "" ||
			candidate.Handler == "" ||
			candidate.Protocol == "" {
			continue
		}
		result = append(result, candidate)
	}
	return result, nil
}

func descriptorSDKAlias(source *ast.File) string {
	for _, imported := range source.Imports {
		pathValue, err := strconv.Unquote(imported.Path.Value)
		if err != nil || pathValue != sdkPackagePath {
			continue
		}
		if imported.Name == nil {
			return "sdk"
		}
		if imported.Name.Name != "." && imported.Name.Name != "_" {
			return imported.Name.Name
		}
	}
	return ""
}

func descriptorDeclaration(
	function *ast.FuncDecl,
	sdkAlias string,
) bool {
	if !descriptorFunctionShape(function) ||
		!token.IsExported(function.Name.Name) {
		return false
	}
	result := function.Type.Results.List[0]
	if len(result.Names) != 0 {
		return false
	}
	selector, ok := result.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil {
		return false
	}
	qualifier, qualified := selector.X.(*ast.Ident)
	return qualified &&
		qualifier.Name == sdkAlias &&
		selector.Sel.Name == "Definition"
}

func descriptorFunctionShape(function *ast.FuncDecl) bool {
	if function == nil ||
		function.Name == nil ||
		function.Type == nil ||
		function.Recv != nil ||
		function.Type.TypeParams != nil ||
		function.Body == nil {
		return false
	}
	if function.Type.Params != nil &&
		function.Type.Params.NumFields() != 0 {
		return false
	}
	return function.Type.Results != nil &&
		function.Type.Results.NumFields() == 1
}

func decodeCandidateLiteral(function *ast.FuncDecl) Candidate {
	if function == nil || function.Body == nil ||
		len(function.Body.List) != 1 {
		return Candidate{}
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return Candidate{}
	}
	literal, ok := returned.Results[0].(*ast.CompositeLit)
	if !ok {
		return Candidate{}
	}
	implementation := compositeField(literal, "Implementation")
	return Candidate{
		CanonicalName: stringField(literal, "Name"),
		Summary:       stringField(literal, "Summary"),
		Tool:          stringField(implementation, "Tool"),
		Handler:       stringField(implementation, "Handler"),
		Protocol:      protocolField(implementation),
	}
}

func compositeField(
	literal *ast.CompositeLit,
	name string,
) *ast.CompositeLit {
	expression := keyedField(literal, name)
	result, ok := expression.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	return result
}

func stringField(literal *ast.CompositeLit, name string) string {
	basic, ok := keyedField(literal, name).(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(basic.Value)
	if err != nil {
		return ""
	}
	return value
}

func protocolField(literal *ast.CompositeLit) string {
	selector, ok := keyedField(literal, "Protocol").(*ast.SelectorExpr)
	if ok && selector.Sel != nil &&
		selector.Sel.Name == "ProtocolV1Alpha1" {
		return "spice.annotation/v1alpha1"
	}
	return ""
}

func keyedField(literal *ast.CompositeLit, name string) ast.Expr {
	if literal == nil {
		return nil
	}
	for _, element := range literal.Elts {
		keyed, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, named := keyed.Key.(*ast.Ident)
		if named && key.Name == name {
			return keyed.Value
		}
	}
	return nil
}

func catalogPackagePath(
	module moduleRoot,
	directory string,
) (string, error) {
	relative, err := filepath.Rel(module.directory, directory)
	if err != nil || relative != "." && !filepath.IsLocal(relative) {
		return "", fmt.Errorf(
			"annotation descriptor directory %q escapes module %q",
			directory,
			module.directory,
		)
	}
	if relative == "." {
		return module.path, nil
	}
	return strings.TrimSuffix(module.path, "/") + "/" +
		filepath.ToSlash(relative), nil
}

func excludedCatalogDirectory(name string) bool {
	switch name {
	case ".git", ".spice", "cmd", "internal", "testdata", "vendor":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func catalogGoFile(name string) bool {
	return strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go") &&
		name != "zz_spice_gen.go"
}

func compactCandidates(values []Candidate) []Candidate {
	result := values[:0]
	for _, value := range values {
		if len(result) != 0 &&
			result[len(result)-1].Package == value.Package &&
			result[len(result)-1].Symbol == value.Symbol {
			continue
		}
		result = append(result, value)
	}
	return result
}

func catalogEnvironment(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	result := replaceEnvironment(environment, "GOPROXY", "off")
	flags := strings.Fields(environmentValue(result, "GOFLAGS"))
	filtered := make([]string, 0, len(flags)+1)
	for index := 0; index < len(flags); index++ {
		if flags[index] == "-mod" {
			index++
			continue
		}
		if !strings.HasPrefix(flags[index], "-mod=") {
			filtered = append(filtered, flags[index])
		}
	}
	filtered = append(filtered, "-mod=readonly")
	return replaceEnvironment(
		result,
		"GOFLAGS",
		strings.Join(filtered, " "),
	)
}

func replaceEnvironment(
	environment []string,
	name string,
	value string,
) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, name+"="+value)
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func samePath(left, right string) bool {
	left, leftErr := filepath.Abs(left)
	right, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
