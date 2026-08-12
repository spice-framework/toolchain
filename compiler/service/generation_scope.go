package service

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spice-framework/toolchain/compiler/modulith"
)

// analyzeGenerationScope keeps ordinary generation target-local while giving
// its exact local module dependencies the same compiler-derived identity
// registry used by configured application scopes.
func (service *Service) analyzeGenerationScope(
	ctx context.Context,
	request normalizedRequest,
) (Result, error) {
	request.applicationScope = true
	initial, err := service.analyze(ctx, request)
	if err != nil {
		return Result{}, err
	}

	models := []modulith.Model{initial.moduleModel}
	known := generationModuleIdentities(models)
	pending := make(map[generationModuleCandidate]struct{})
	inventoried := make(map[generationModuleCandidate]struct{})
	enqueueGenerationModuleDependencies(initial.moduleModel, known, pending)
	inventories := make([]normalizedRequest, 0, len(pending))

	for len(pending) != 0 {
		if contextErr := ctx.Err(); contextErr != nil {
			return Result{}, contextErr
		}
		candidate := nextGenerationModuleCandidate(pending)
		delete(pending, candidate)
		if _, found := inventoried[candidate]; found {
			continue
		}
		inventoried[candidate] = struct{}{}
		if modulePath, found := known[candidate.id]; found {
			if modulePath != candidate.modulePath {
				// One import path cannot be admitted under a different Go module.
				continue
			}
		}

		inventoryRequest := generationModuleInventoryRequest(request, candidate.id)
		module, found := exactGenerationModuleInModels(
			models,
			candidate,
			request.root,
		)
		var inventory Result
		if !found {
			var analyzeErr error
			inventory, analyzeErr = service.analyze(ctx, inventoryRequest)
			if analyzeErr != nil {
				return Result{}, analyzeErr
			}
			module, found = exactGenerationModule(
				inventory.moduleModel,
				candidate,
				request.root,
			)
			if !found {
				// Missing, unannotated, and external packages remain unknown to the
				// final application model; their declaration diagnostic is preserved.
				continue
			}
		}
		interfacePatterns, discoverErr := generationNamedInterfacePatterns(
			ctx,
			request.root,
			module,
			request.overlay,
		)
		if discoverErr != nil {
			return Result{}, discoverErr
		}
		if len(interfacePatterns) != 0 {
			inventoryRequest.patterns = append(
				inventoryRequest.patterns,
				interfacePatterns...,
			)
			var analyzeErr error
			inventory, analyzeErr = service.analyze(ctx, inventoryRequest)
			if analyzeErr != nil {
				return Result{}, analyzeErr
			}
			module, found = exactGenerationModule(
				inventory.moduleModel,
				candidate,
				request.root,
			)
			if !found || len(inventory.moduleModel.Modules()) != 1 {
				continue
			}
		} else if len(inventory.moduleModel.Modules()) == 0 {
			// A known module without additional named-interface packages already
			// contributes its exact identity through the initial application model.
			continue
		}
		models = append(models, inventory.moduleModel)
		known[module.ID] = module.GoModulePath
		inventories = append(inventories, inventoryRequest)
		enqueueGenerationModuleDependencies(inventory.moduleModel, known, pending)
	}

	universe := modulith.NewUniverse(models...)
	for _, inventoryRequest := range inventories {
		if contextErr := ctx.Err(); contextErr != nil {
			return Result{}, contextErr
		}
		inventoryRequest.moduleUniverse = universe
		validated, analyzeErr := service.analyze(ctx, inventoryRequest)
		if analyzeErr != nil {
			return Result{}, analyzeErr
		}
		if len(validated.moduleModel.Diagnostics()) != 0 {
			return validated, nil
		}
	}
	if len(inventories) == 0 {
		return initial, nil
	}

	request.moduleUniverse = universe
	result, err := service.analyze(ctx, request)
	if err != nil {
		return Result{}, err
	}
	result.moduleGraph = mergeGenerationModuleGraphIdentities(
		result.moduleGraph,
		models,
	)
	return result, nil
}

func mergeGenerationModuleGraphIdentities(
	graph ModuleGraph,
	models []modulith.Model,
) ModuleGraph {
	result := cloneModuleGraph(graph)
	moduleIndexes := make(map[string]int, len(result.Modules))
	known := make(map[string]map[string]struct{}, len(result.Modules))
	for index, module := range result.Modules {
		moduleIndexes[module.ID] = index
		interfaces := make(map[string]struct{}, len(module.NamedInterfaces))
		for _, named := range module.NamedInterfaces {
			interfaces[named.Name+"\x00"+named.PackagePath] = struct{}{}
		}
		known[module.ID] = interfaces
	}
	for _, model := range models {
		for _, module := range model.Modules() {
			index, found := moduleIndexes[module.ID]
			if !found {
				continue
			}
			interfaces, found := known[module.ID]
			if !found {
				continue
			}
			for _, named := range module.NamedInterfaces() {
				key := named.Name + "\x00" + named.PackagePath
				if _, duplicate := interfaces[key]; duplicate {
					continue
				}
				interfaces[key] = struct{}{}
				result.Modules[index].NamedInterfaces = append(
					result.Modules[index].NamedInterfaces,
					NamedInterface{
						Name:        named.Name,
						PackagePath: named.PackagePath,
					},
				)
			}
		}
	}
	for index := range result.Modules {
		sort.SliceStable(result.Modules[index].NamedInterfaces, func(i, j int) bool {
			left := result.Modules[index].NamedInterfaces[i]
			right := result.Modules[index].NamedInterfaces[j]
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			return left.PackagePath < right.PackagePath
		})
	}
	return result
}

func exactGenerationModuleInModels(
	models []modulith.Model,
	candidate generationModuleCandidate,
	root string,
) (modulith.Module, bool) {
	for _, model := range models {
		if module, found := exactGenerationModule(model, candidate, root); found {
			return module, true
		}
	}
	return modulith.Module{}, false
}

type generationModuleCandidate struct {
	id         string
	modulePath string
}

const (
	generationInventoryMaximumFiles     = 100_000
	generationInventoryMaximumBytes     = 64 << 20
	generationInventoryMaximumFileBytes = 1 << 20
)

func generationModuleIdentities(models []modulith.Model) map[string]string {
	result := make(map[string]string)
	for _, model := range models {
		for _, module := range model.Modules() {
			result[module.ID] = module.GoModulePath
		}
	}
	return result
}

func enqueueGenerationModuleDependencies(
	model modulith.Model,
	known map[string]string,
	pending map[generationModuleCandidate]struct{},
) {
	for _, module := range model.Modules() {
		if module.GoModulePath == "" {
			continue
		}
		for _, dependency := range module.AllowedDependencies() {
			if !sameGenerationGoModule(module.GoModulePath, dependency.ModuleID) {
				continue
			}
			if knownPath, found := known[dependency.ModuleID]; found &&
				dependency.Interface == "" &&
				knownPath == module.GoModulePath {
				continue
			}
			pending[generationModuleCandidate{
				id:         dependency.ModuleID,
				modulePath: module.GoModulePath,
			}] = struct{}{}
		}
	}
}

func sameGenerationGoModule(modulePath, packagePath string) bool {
	return packagePath == modulePath ||
		strings.HasPrefix(packagePath, modulePath+"/")
}

func nextGenerationModuleCandidate(
	pending map[generationModuleCandidate]struct{},
) generationModuleCandidate {
	values := make([]generationModuleCandidate, 0, len(pending))
	for candidate := range pending {
		values = append(values, candidate)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].modulePath != values[j].modulePath {
			return values[i].modulePath < values[j].modulePath
		}
		return values[i].id < values[j].id
	})
	return values[0]
}

func generationModuleInventoryRequest(
	request normalizedRequest,
	packagePath string,
) normalizedRequest {
	request.target = ""
	request.patterns = []string{packagePath}
	request.mode = AnalysisValidate
	request.style = nil
	request.selection = nil
	request.styleInventory = false
	request.forceStyleInventory = false
	request.generatedEntrypoint = false
	request.applicationScope = false
	request.generationInventory = true
	request.moduleUniverse = modulith.Universe{}
	return request
}

func exactGenerationModule(
	model modulith.Model,
	candidate generationModuleCandidate,
	root string,
) (modulith.Module, bool) {
	for _, module := range model.Modules() {
		if module.ID == candidate.id &&
			module.GoModulePath == candidate.modulePath &&
			generationModuleWithinRoot(root, module) {
			return module, true
		}
	}
	return modulith.Module{}, false
}

func generationModuleWithinRoot(root string, module modulith.Module) bool {
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil || module.PhysicalPosition.Filename == "" {
		return false
	}
	filename := module.PhysicalPosition.Filename
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(root, filename)
	}
	resolvedFile, err := filepath.EvalSymlinks(filepath.Clean(filename))
	return err == nil && pathWithin(resolvedRoot, resolvedFile)
}

func generationNamedInterfacePatterns(
	ctx context.Context,
	root string,
	module modulith.Module,
	overlay map[string]Document,
) ([]string, error) {
	document := module.PhysicalPosition.Filename
	if !filepath.IsAbs(document) {
		document = filepath.Join(root, document)
	}
	moduleRoot := filepath.Dir(filepath.Clean(document))
	resolvedWorkspace, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil, err
	}
	resolvedModule, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil || !pathWithin(resolvedWorkspace, resolvedModule) {
		return nil, fs.ErrInvalid
	}
	moduleFiles, err := os.OpenRoot(moduleRoot)
	if err != nil {
		return nil, err
	}
	defer moduleFiles.Close() //nolint:errcheck // Read-only inventory close cannot alter validation.

	packages := make(map[string]struct{})
	seenFiles := make(map[string]struct{})
	files := 0
	bytesRead := int64(0)
	inspect := func(filename string, content []byte) error {
		filename = filepath.Clean(filename)
		if _, found := seenFiles[filename]; found {
			return nil
		}
		seenFiles[filename] = struct{}{}
		files++
		bytesRead += int64(len(content))
		if files > generationInventoryMaximumFiles ||
			bytesRead > generationInventoryMaximumBytes {
			return fmt.Errorf(
				"generation module identity inventory exceeds %d files or %d bytes",
				generationInventoryMaximumFiles,
				generationInventoryMaximumBytes,
			)
		}
		file, parseErr := parser.ParseFile(
			token.NewFileSet(),
			filename,
			content,
			parser.ParseComments|parser.PackageClauseOnly,
		)
		if parseErr != nil || ast.IsGenerated(file) || !hasNamedInterfaceComment(file) {
			return parseErr
		}
		relative, relErr := filepath.Rel(moduleRoot, filepath.Dir(filename))
		if relErr != nil {
			return relErr
		}
		if relative == "." || !filepath.IsLocal(relative) {
			return nil
		}
		packages[path.Join(module.ID, filepath.ToSlash(relative))] = struct{}{}
		return nil
	}
	err = filepath.WalkDir(moduleRoot, func(
		current string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if entry.IsDir() {
			eligible, eligibilityErr := generationInventoryPathEligible(
				moduleRoot,
				resolvedModule,
				current,
				true,
				0,
			)
			if eligibilityErr != nil {
				return eligibilityErr
			}
			if !eligible {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		document, overlaid := overlay[filepath.Clean(current)]
		effectiveBytes := info.Size()
		if overlaid {
			effectiveBytes = int64(len(document.Content))
		}
		eligible, eligibilityErr := generationInventoryPathEligible(
			moduleRoot,
			resolvedModule,
			current,
			false,
			effectiveBytes,
		)
		if eligibilityErr != nil {
			return eligibilityErr
		}
		if !eligible {
			return nil
		}
		content := document.Content
		if !overlaid {
			relativeFile, relErr := filepath.Rel(moduleRoot, current)
			if relErr != nil || !filepath.IsLocal(relativeFile) {
				return fs.ErrInvalid
			}
			var readErr error
			content, readErr = moduleFiles.ReadFile(relativeFile)
			if readErr != nil {
				return readErr
			}
		}
		return inspect(current, content)
	})
	if err != nil {
		return nil, err
	}
	overlayPaths := make([]string, 0, len(overlay))
	for filename := range overlay {
		overlayPaths = append(overlayPaths, filepath.Clean(filename))
	}
	sort.Strings(overlayPaths)
	for _, filename := range overlayPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		document := overlay[filename]
		eligible, eligibilityErr := generationInventoryPathEligible(
			moduleRoot,
			resolvedModule,
			filename,
			false,
			int64(len(document.Content)),
		)
		if eligibilityErr != nil {
			return nil, eligibilityErr
		}
		if !eligible {
			continue
		}
		if err := inspect(filename, document.Content); err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(packages))
	for packagePath := range packages {
		result = append(result, packagePath)
	}
	sort.Strings(result)
	return result, nil
}

func generationInventoryPathEligible(
	moduleRoot string,
	resolvedModule string,
	filename string,
	directory bool,
	fileBytes int64,
) (bool, error) {
	filename = filepath.Clean(filename)
	relative, err := filepath.Rel(moduleRoot, filename)
	if err != nil {
		return false, err
	}
	if relative != "." && !filepath.IsLocal(relative) {
		return false, nil
	}
	if relative == "." {
		return directory, nil
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	directoryComponents := components
	if !directory {
		directoryComponents = components[:len(components)-1]
		base := components[len(components)-1]
		if filepath.Ext(base) != ".go" || strings.HasSuffix(base, "_test.go") ||
			fileBytes > generationInventoryMaximumFileBytes {
			return false, nil
		}
	}

	current := moduleRoot
	previous := ""
	for _, component := range directoryComponents {
		if component == "vendor" || component == "testdata" ||
			strings.HasPrefix(component, ".") || strings.HasPrefix(component, "_") ||
			(previous == "internal" && component == "spicegen") {
			return false, nil
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return false, nil
			}
			return false, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		if _, statErr = os.Lstat(filepath.Join(current, "go.mod")); statErr == nil {
			return false, nil
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return false, statErr
		}
		previous = component
	}

	physicalDirectory := current
	resolvedDirectory, err := filepath.EvalSymlinks(physicalDirectory)
	if err != nil {
		return false, err
	}
	if !pathWithin(resolvedModule, resolvedDirectory) {
		return false, nil
	}
	if directory {
		return true, nil
	}
	if info, statErr := os.Lstat(filename); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return false, statErr
	}
	return true, nil
}

func hasNamedInterfaceComment(file *ast.File) bool {
	if file == nil {
		return false
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			value := strings.TrimSpace(comment.Text)
			value = strings.TrimSpace(strings.TrimPrefix(value, "//"))
			if strings.HasPrefix(value, "@NamedInterface(") {
				return true
			}
		}
	}
	return false
}
