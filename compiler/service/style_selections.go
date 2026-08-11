package service

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/spice-framework/toolchain/compiler/diagnostic"
	"github.com/spice-framework/toolchain/compiler/load"
	compilerstyle "github.com/spice-framework/toolchain/compiler/style"
)

func (service *Service) analyzeConfiguredSelections(
	ctx context.Context,
	request normalizedRequest,
) (Result, error) {
	configuration := request.style.Clone()
	results := make([]selectionResult, 0, len(configuration.BuildSelections))
	for index := range configuration.BuildSelections {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		selection := configuration.BuildSelections[index]
		selected := request
		selected.selection = &selection
		selected.patterns = selectionPatterns(selection)
		result, err := service.analyze(ctx, selected)
		if err != nil {
			return Result{}, fmt.Errorf(
				"analyze style build selection %q: %w",
				selection.Name,
				err,
			)
		}
		results = append(results, selectionResult{
			id:     selection.Name,
			result: result,
		})
	}
	if len(results) == 0 {
		return Result{}, nil
	}
	result := results[0].result
	result.diagnostics = mergeSelectionDiagnostics(results)
	unreachable, err := unreachableSourceDiagnostics(
		request.root,
		configuration,
		results,
	)
	if err != nil {
		return Result{}, err
	}
	result.diagnostics = diagnostic.Merge(result.diagnostics, unreachable)
	result.actions = actionsFromDiagnostics(result.diagnostics)
	return result, nil
}

type selectionResult struct {
	id     string
	result Result
}

func selectionPatterns(selection compilerstyle.BuildSelection) []string {
	patterns := make([]string, len(selection.SourceRoots))
	for index, root := range selection.SourceRoots {
		patterns[index] = "./" + strings.TrimSuffix(root, "/") + "/..."
	}
	return patterns
}

func exactStyleSelectionOptions(
	options load.Options,
	selection compilerstyle.BuildSelection,
) load.Options {
	result := cloneLoadOptions(options)
	if result.Env == nil {
		result.Env = os.Environ()
	}
	for _, name := range []string{"GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"} {
		result.Env = removeEnvironment(result.Env, name)
	}
	cgoEnabled := "0"
	if *selection.CGOEnabled {
		cgoEnabled = "1"
	}
	result.Env = append(
		result.Env,
		"GOFLAGS=",
		"GOOS="+selection.GOOS,
		"GOARCH="+selection.GOARCH,
		"CGO_ENABLED="+cgoEnabled,
	)
	result.BuildFlags = withoutTagFlags(result.BuildFlags)
	if len(selection.Tags) != 0 {
		result.BuildFlags = append(
			result.BuildFlags,
			"-tags="+strings.Join(selection.Tags, ","),
		)
	}
	return result
}

func removeEnvironment(environment []string, name string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func withoutTagFlags(flags []string) []string {
	result := make([]string, 0, len(flags))
	for index := 0; index < len(flags); index++ {
		switch {
		case flags[index] == "-tags":
			if index+1 < len(flags) {
				index++
			}
		case strings.HasPrefix(flags[index], "-tags="):
		default:
			result = append(result, flags[index])
		}
	}
	return result
}

func primaryProgramFiles(program *load.Program) []string {
	set := make(map[string]struct{})
	for _, pkg := range program.PrimaryPackages() {
		for _, file := range pkg.Files {
			if file.PhysicalPath != "" {
				set[filepath.Clean(file.PhysicalPath)] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for file := range set {
		result = append(result, file)
	}
	sort.Strings(result)
	return result
}

func mergeSelectionDiagnostics(results []selectionResult) diagnostic.Set {
	type aggregate struct {
		item    diagnostic.Diagnostic
		ids     []string
		related []diagnostic.RelatedInformation
	}
	order := make([]*aggregate, 0)
	byKey := make(map[selectionDiagnosticIdentity][]*aggregate)
	for _, selection := range results {
		for _, item := range selection.result.Diagnostics().Items() {
			key := selectionDiagnosticKey(item)
			var current *aggregate
			for _, candidate := range byKey[key] {
				if reflect.DeepEqual(candidate.item.Fixes, item.Fixes) {
					current = candidate
					break
				}
			}
			if current == nil {
				current = &aggregate{item: item}
				byKey[key] = append(byKey[key], current)
				order = append(order, current)
			}
			current.related = appendUniqueRelated(current.related, item.Related)
			if !slices.Contains(current.ids, selection.id) {
				current.ids = append(current.ids, selection.id)
			}
		}
	}
	items := make([]diagnostic.Diagnostic, 0, len(order))
	for _, current := range order {
		related := append(slices.Clone(current.related), diagnostic.RelatedInformation{
			Message:  "build selections: " + strings.Join(current.ids, ", "),
			Location: current.item.Location,
		})
		current.item = current.item.WithRelated(related...)
		items = append(items, current.item)
	}
	return diagnostic.NewSet(items...)
}

func appendUniqueRelated(
	destination []diagnostic.RelatedInformation,
	items []diagnostic.RelatedInformation,
) []diagnostic.RelatedInformation {
	for _, item := range items {
		found := slices.ContainsFunc(destination, func(existing diagnostic.RelatedInformation) bool {
			return reflect.DeepEqual(existing, item)
		})
		if !found {
			destination = append(destination, item)
		}
	}
	return destination
}

type selectionDiagnosticIdentity struct {
	code           string
	severity       diagnostic.Severity
	message        string
	uri            string
	path           string
	rangeStart     diagnostic.Position
	rangeEnd       diagnostic.Position
	displayPresent bool
	displayPath    string
	displayStart   diagnostic.Position
	displayEnd     diagnostic.Position
}

func selectionDiagnosticKey(item diagnostic.Diagnostic) selectionDiagnosticIdentity {
	identity := selectionDiagnosticIdentity{
		code:       item.Code,
		severity:   item.Severity,
		message:    item.Message,
		uri:        item.Location.URI,
		path:       item.Location.Path,
		rangeStart: item.Location.Range.Start,
		rangeEnd:   item.Location.Range.End,
	}
	if item.Location.Display != nil {
		identity.displayPresent = true
		identity.displayPath = item.Location.Display.Path
		identity.displayStart = item.Location.Display.Range.Start
		identity.displayEnd = item.Location.Display.Range.End
	}
	return identity
}

func unreachableSourceDiagnostics(
	root string,
	configuration compilerstyle.Configuration,
	results []selectionResult,
) (diagnostic.Set, error) {
	selected, inventoryDiagnostics, err := selectedHandwrittenSources(root, configuration)
	if err != nil {
		return diagnostic.Set{}, err
	}
	reached := make(map[string]struct{})
	for _, result := range results {
		for _, file := range result.result.loadedFiles {
			reached[filepath.Clean(file)] = struct{}{}
		}
	}
	ids := make([]string, len(results))
	for index := range results {
		ids[index] = results[index].id
	}
	for _, file := range selected {
		if _, found := reached[file]; found {
			continue
		}
		item := diagnostic.New(
			"spice.style.configuration.source-selection",
			diagnostic.SeverityError,
			"selected handwritten Go file is unreachable from every declared build selection",
			diagnostic.SourceLocation(root, file, file, 1, 1, 0),
		).WithRelated(diagnostic.RelatedInformation{
			Message:  "build selections: " + strings.Join(ids, ", "),
			Location: diagnostic.SourceLocation(root, file, file, 1, 1, 0),
		})
		inventoryDiagnostics = append(inventoryDiagnostics, item)
	}
	return diagnostic.NewSet(inventoryDiagnostics...), nil
}

func selectedHandwrittenSources(
	root string,
	configuration compilerstyle.Configuration,
) ([]string, []diagnostic.Diagnostic, error) {
	generated := make([]string, len(configuration.GeneratedRoots))
	for index, relative := range configuration.GeneratedRoots {
		generated[index] = filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	}
	set := make(map[string]struct{})
	var diagnostics []diagnostic.Diagnostic
	for _, relative := range configuration.SourceRoots {
		sourceRoot := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
		info, statErr := os.Stat(sourceRoot)
		if statErr != nil {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root cannot be inspected: "+statErr.Error()))
			continue
		}
		if !info.IsDir() {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root must be a directory"))
			continue
		}
		resolved, err := filepath.EvalSymlinks(sourceRoot)
		if err != nil {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root cannot be resolved: "+err.Error()))
			continue
		}
		if !pathWithin(root, resolved) {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root resolves outside the workspace"))
			continue
		}
		err = filepath.WalkDir(sourceRoot, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			file = filepath.Clean(file)
			if entry.IsDir() {
				if slices.ContainsFunc(generated, func(value string) bool { return file == value }) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(entry.Name()), ".go") ||
				strings.HasSuffix(strings.ToLower(entry.Name()), "_test.go") ||
				pathUnderAny(file, generated) || generatedGoFile(file) {
				return nil
			}
			set[file] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("inventory style source root %q: %w", relative, err)
		}
	}
	result := make([]string, 0, len(set))
	for file := range set {
		result = append(result, file)
	}
	sort.Strings(result)
	return result, diagnostics, nil
}

func sourceRootDiagnostic(root, path, message string) diagnostic.Diagnostic {
	return diagnostic.New(
		"spice.style.configuration.source-selection",
		diagnostic.SeverityError,
		message,
		diagnostic.SourceLocation(root, path, path, 1, 1, 0),
	)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func pathUnderAny(file string, roots []string) bool {
	for _, root := range roots {
		if file == root || pathWithin(root, file) {
			return true
		}
	}
	return false
}

func generatedGoFile(path string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.PackageClauseOnly)
	return err == nil && ast.IsGenerated(file)
}
