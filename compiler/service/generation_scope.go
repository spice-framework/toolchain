package service

import (
	"context"
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
	enqueueGenerationModuleDependencies(initial.moduleModel, known, pending)
	inventories := make([]normalizedRequest, 0, len(pending))

	for len(pending) != 0 {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		candidate := nextGenerationModuleCandidate(pending)
		delete(pending, candidate)
		if modulePath, found := known[candidate.id]; found {
			if modulePath == candidate.modulePath {
				continue
			}
			// One import path cannot be admitted under a different Go module.
			continue
		}

		inventoryRequest := generationModuleInventoryRequest(request, candidate.id)
		inventory, analyzeErr := service.analyze(ctx, inventoryRequest)
		if analyzeErr != nil {
			return Result{}, analyzeErr
		}
		module, found := exactGenerationModule(
			inventory.moduleModel,
			candidate,
			request.root,
		)
		if !found {
			// Missing, unannotated, and external packages remain unknown to the
			// final application model; their declaration diagnostic is preserved.
			continue
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
		}
		models = append(models, inventory.moduleModel)
		known[module.ID] = module.GoModulePath
		inventories = append(inventories, inventoryRequest)
		enqueueGenerationModuleDependencies(inventory.moduleModel, known, pending)
	}

	universe := modulith.NewUniverse(models...)
	for _, inventoryRequest := range inventories {
		if err := ctx.Err(); err != nil {
			return Result{}, err
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
	return service.analyze(ctx, request)
}

type generationModuleCandidate struct {
	id         string
	modulePath string
}

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
	const (
		maximumFiles     = 100_000
		maximumBytes     = 64 << 20
		maximumFileBytes = 1 << 20
	)
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
		if files > maximumFiles || bytesRead > maximumBytes {
			return fmt.Errorf(
				"generation module identity inventory exceeds %d files or %d bytes",
				maximumFiles,
				maximumBytes,
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
			if current == moduleRoot {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 ||
				entry.Name() == "vendor" || entry.Name() == "testdata" ||
				strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") {
				return filepath.SkipDir
			}
			if generationGeneratedDirectory(moduleRoot, current) {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 ||
			filepath.Ext(entry.Name()) != ".go" ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() || info.Size() > maximumFileBytes {
			return nil
		}
		document, overlaid := overlay[filepath.Clean(current)]
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
	for filename, document := range overlay {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		filename = filepath.Clean(filename)
		relative, relErr := filepath.Rel(moduleRoot, filename)
		if relErr != nil || relative == "." || !filepath.IsLocal(relative) ||
			filepath.Ext(filename) != ".go" || strings.HasSuffix(filename, "_test.go") ||
			len(document.Content) > maximumFileBytes ||
			generationGeneratedDirectory(moduleRoot, filepath.Dir(filename)) {
			continue
		}
		resolvedDirectory, resolveErr := filepath.EvalSymlinks(filepath.Dir(filename))
		if resolveErr != nil || !pathWithin(resolvedModule, resolvedDirectory) {
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

func generationGeneratedDirectory(moduleRoot, directory string) bool {
	relative, err := filepath.Rel(moduleRoot, directory)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return false
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	for index := 0; index+1 < len(components); index++ {
		if components[index] == "internal" && components[index+1] == "spicegen" {
			return true
		}
	}
	return false
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
