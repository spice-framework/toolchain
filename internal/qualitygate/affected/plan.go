// Package affected derives conservative repository verification work from the
// Go package graph and the current Git worktree.
package affected

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ModulePlan is the selected package work for one Go module.
type ModulePlan struct {
	Root     string
	Packages []string
}

// Plan is an immutable, deterministic affected-work selection.
type Plan struct {
	Base           string
	Changed        []string
	GoFiles        []string
	Modules        []ModulePlan
	Reasons        []string
	Full           bool
	SpringCoverage bool
}

// Empty reports whether the worktree contains no relevant change.
func (plan Plan) Empty() bool {
	return len(plan.Changed) == 0
}

// Config describes one repository selection.
type Config struct {
	RepositoryRoot string
	Base           string
	ModuleRoots    []string
}

// Build discovers changes and selects their complete reverse dependency
// closure. Any unavailable or ambiguous ownership widens selection rather than
// risking a false green result.
func Build(ctx context.Context, config Config) (Plan, error) {
	root, err := filepath.Abs(config.RepositoryRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve repository root: %w", err)
	}
	base, changed, err := changedFiles(ctx, root, config.Base)
	if err != nil {
		return Plan{}, err
	}
	relevant := relevantPaths(changed)
	if len(relevant) == 0 {
		return Plan{Base: base}, nil
	}
	graphs, err := loadGraphs(ctx, root, config.ModuleRoots, relevant)
	if err != nil {
		return Plan{}, err
	}
	plan := Select(root, base, relevant, graphs)
	return plan, nil
}

// Package describes the package facts required for affected selection.
type Package struct {
	ImportPath   string
	Directory    string
	ModuleRoot   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// Graph is the local package graph across all selected modules.
type Graph struct {
	Packages []Package
}

// Select computes a deterministic, conservative plan from normalized changes.
func Select(
	repositoryRoot string,
	base string,
	changed []string,
	graph Graph,
) Plan {
	relevant := relevantPaths(changed)
	plan := Plan{
		Base:    base,
		Changed: slices.Clone(relevant),
	}
	if len(relevant) == 0 {
		return plan
	}

	packages, reverse := indexGraph(graph)
	selected := selectChangedPackages(
		repositoryRoot,
		relevant,
		graph.Packages,
		&plan,
	)
	if plan.Full {
		for path := range packages {
			selected[path] = struct{}{}
		}
	}
	expandReverseClosure(selected, reverse)
	plan.Modules = modulePlans(selected, packages)
	slices.Sort(plan.GoFiles)
	plan.GoFiles = slices.Compact(plan.GoFiles)
	slices.Sort(plan.Reasons)
	plan.Reasons = slices.Compact(plan.Reasons)
	return plan
}

func relevantPaths(changed []string) []string {
	normalized := normalizePaths(changed)
	relevant := make([]string, 0, len(normalized))
	for _, name := range normalized {
		if !ignoredWorkspacePath(name) {
			relevant = append(relevant, name)
		}
	}
	return relevant
}

func indexGraph(
	graph Graph,
) (map[string]Package, map[string][]string) {
	packages := make(map[string]Package, len(graph.Packages))
	reverse := make(map[string][]string, len(graph.Packages))
	for _, item := range graph.Packages {
		packages[item.ImportPath] = item
		for _, dependency := range packageImports(item) {
			if dependency != item.ImportPath {
				reverse[dependency] = append(
					reverse[dependency],
					item.ImportPath,
				)
			}
		}
	}
	for dependency := range reverse {
		slices.Sort(reverse[dependency])
		reverse[dependency] = slices.Compact(reverse[dependency])
	}
	return packages, reverse
}

func selectChangedPackages(
	repositoryRoot string,
	changed []string,
	packages []Package,
	plan *Plan,
) map[string]struct{} {
	selected := make(map[string]struct{})
	for _, name := range changed {
		if !requiresPackageSelection(name, plan) {
			continue
		}
		owner, found := owningPackage(repositoryRoot, name, packages)
		if found {
			selected[owner.ImportPath] = struct{}{}
			continue
		}
		if pathBelongsToKnownModule(repositoryRoot, name, packages) {
			plan.Full = true
			plan.Reasons = append(
				plan.Reasons,
				name+" has no unambiguous package owner",
			)
		}
	}
	return selected
}

func requiresPackageSelection(name string, plan *Plan) bool {
	if isGlobalInput(name) {
		plan.Full = true
		plan.Reasons = append(
			plan.Reasons,
			name+" changes the complete build graph",
		)
		return false
	}
	if name == "docs/spring-coverage.md" {
		plan.SpringCoverage = true
	}
	if strings.HasSuffix(name, ".go") {
		plan.GoFiles = append(plan.GoFiles, name)
	}
	return !documentationOnly(name)
}

func expandReverseClosure(
	selected map[string]struct{},
	reverse map[string][]string,
) {
	queue := make([]string, 0, len(selected))
	for path := range selected {
		queue = append(queue, path)
	}
	slices.Sort(queue)
	for index := 0; index < len(queue); index++ {
		for _, dependent := range reverse[queue[index]] {
			if _, found := selected[dependent]; found {
				continue
			}
			selected[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}
}

func modulePlans(
	selected map[string]struct{},
	packages map[string]Package,
) []ModulePlan {
	byModule := make(map[string][]string)
	for path := range selected {
		item, found := packages[path]
		if found {
			byModule[item.ModuleRoot] = append(byModule[item.ModuleRoot], path)
		}
	}
	roots := make([]string, 0, len(byModule))
	for root := range byModule {
		roots = append(roots, root)
	}
	slices.Sort(roots)
	result := make([]ModulePlan, 0, len(roots))
	for _, root := range roots {
		paths := byModule[root]
		slices.Sort(paths)
		result = append(result, ModulePlan{
			Root:     root,
			Packages: slices.Compact(paths),
		})
	}
	return result
}

func packageImports(item Package) []string {
	result := make(
		[]string,
		0,
		len(item.Imports)+len(item.TestImports)+len(item.XTestImports),
	)
	result = append(result, item.Imports...)
	result = append(result, item.TestImports...)
	result = append(result, item.XTestImports...)
	slices.Sort(result)
	return slices.Compact(result)
}

func owningPackage(
	repositoryRoot string,
	name string,
	packages []Package,
) (Package, bool) {
	absolute := filepath.Join(repositoryRoot, filepath.FromSlash(name))
	absolute = filepath.Clean(absolute)
	var owner Package
	longest := -1
	for _, item := range packages {
		directory := filepath.Clean(item.Directory)
		relative, err := filepath.Rel(directory, absolute)
		if err != nil ||
			relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(directory) > longest {
			owner = item
			longest = len(directory)
		}
	}
	return owner, longest >= 0
}

func pathBelongsToKnownModule(
	repositoryRoot string,
	name string,
	packages []Package,
) bool {
	absolute := filepath.Clean(
		filepath.Join(repositoryRoot, filepath.FromSlash(name)),
	)
	for _, item := range packages {
		relative, err := filepath.Rel(filepath.Clean(item.ModuleRoot), absolute)
		if err == nil &&
			relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func normalizePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, name := range paths {
		name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
		name = path.Clean(name)
		name = strings.TrimPrefix(name, "./")
		if name == "" || name == "." {
			continue
		}
		result = append(result, name)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func ignoredWorkspacePath(name string) bool {
	for _, prefix := range []string{
		".git/",
		".tmp/",
		"bin/",
		"dist/",
		"out/",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isGlobalInput(name string) bool {
	base := path.Base(name)
	switch base {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	switch name {
	case "Makefile", ".golangci.yml":
		return true
	}
	for _, prefix := range []string{"vendor/", "tools/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return strings.Contains(name, "/vendor/")
}

func documentationOnly(name string) bool {
	if strings.HasPrefix(name, "docs/") ||
		strings.HasPrefix(name, "adrs/") ||
		strings.HasPrefix(name, "rfcs/") ||
		strings.HasPrefix(name, "research/") {
		return true
	}
	switch name {
	case "README.md", "ARCHITECTURE.md", "ROADMAP.md", "CONTRIBUTING.md",
		"GOVERNANCE.md", "SECURITY.md", "AGENTS.md", "LICENSE":
		return true
	default:
		return false
	}
}

func changedFiles(
	ctx context.Context,
	root string,
	requestedBase string,
) (string, []string, error) {
	base := strings.TrimSpace(requestedBase)
	if base == "" {
		output, err := gitOutput(ctx, root, "merge-base", "HEAD", "origin/main")
		if err != nil {
			return "", nil, fmt.Errorf(
				"resolve affected base; pass -base explicitly when origin/main is unavailable: %w",
				err,
			)
		}
		base = strings.TrimSpace(string(output))
	}
	if base == "" {
		return "", nil, errors.New("affected base revision is empty")
	}
	var paths []string
	for _, arguments := range [][]string{
		{"diff", "--name-only", "-z", "--diff-filter=ACDMRTUXB", base, "HEAD", "--"},
		{"diff", "--name-only", "-z", "--diff-filter=ACDMRTUXB", "HEAD", "--"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		output, err := gitOutput(ctx, root, arguments...)
		if err != nil {
			return "", nil, err
		}
		paths = append(paths, splitNUL(output)...)
	}
	return base, normalizePaths(paths), nil
}

func gitOutput(
	ctx context.Context,
	root string,
	arguments ...string,
) ([]byte, error) {
	// #nosec G204 -- Git receives a fixed repository-owned operation plus an
	// opaque revision argument directly; no command shell interprets it.
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = root
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	if exitErr, found := errors.AsType[*exec.ExitError](err); found {
		return nil, fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(arguments, " "),
			err,
			strings.TrimSpace(string(exitErr.Stderr)),
		)
	}
	return nil, fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
}

func splitNUL(content []byte) []string {
	parts := bytes.Split(content, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			result = append(result, string(part))
		}
	}
	return result
}

func loadGraphs(
	ctx context.Context,
	repositoryRoot string,
	configured []string,
	changed []string,
) (Graph, error) {
	roots := []string{repositoryRoot}
	roots = append(roots, configured...)
	for _, name := range changed {
		if ignoredWorkspacePath(name) {
			continue
		}
		if root, found := findModuleRoot(
			repositoryRoot,
			filepath.Join(repositoryRoot, filepath.FromSlash(name)),
		); found {
			roots = append(roots, root)
		}
	}
	for index := range roots {
		absolute, err := filepath.Abs(roots[index])
		if err != nil {
			return Graph{}, fmt.Errorf("resolve module root: %w", err)
		}
		roots[index] = filepath.Clean(absolute)
	}
	slices.Sort(roots)
	roots = slices.Compact(roots)

	var result Graph
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Graph{}, fmt.Errorf("inspect module %s: %w", root, err)
		}
		packages, err := loadModule(ctx, root)
		if err != nil {
			return Graph{}, err
		}
		result.Packages = append(result.Packages, packages...)
	}
	slices.SortFunc(result.Packages, func(left, right Package) int {
		return strings.Compare(left.ImportPath, right.ImportPath)
	})
	return result, nil
}

func findModuleRoot(
	repositoryRoot string,
	path string,
) (string, bool) {
	current := filepath.Clean(path)
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	} else if filepath.Ext(current) != "" {
		current = filepath.Dir(current)
	}
	repositoryRoot = filepath.Clean(repositoryRoot)
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, true
		}
		if current == repositoryRoot {
			return repositoryRoot, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		relative, err := filepath.Rel(repositoryRoot, parent)
		if err != nil ||
			relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", false
		}
		current = parent
	}
}

type listedPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Module       *struct {
		Dir string
	}
}

func loadModule(ctx context.Context, root string) ([]Package, error) {
	command := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf(
			"go list package graph in %s: %w: %s",
			root,
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	decoder := json.NewDecoder(&stdout)
	var result []Package
	for {
		var item listedPackage
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode package graph in %s: %w", root, err)
		}
		if item.ImportPath == "" || item.Dir == "" {
			continue
		}
		moduleRoot := root
		if item.Module != nil && item.Module.Dir != "" {
			moduleRoot = item.Module.Dir
		}
		result = append(result, Package{
			ImportPath:   item.ImportPath,
			Directory:    item.Dir,
			ModuleRoot:   filepath.Clean(moduleRoot),
			Imports:      slices.Clone(item.Imports),
			TestImports:  slices.Clone(item.TestImports),
			XTestImports: slices.Clone(item.XTestImports),
		})
	}
	return result, nil
}
