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

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/diagnostic"
	diagnosticadapt "github.com/spice-framework/toolchain/compiler/diagnostic/adapt"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/resolve"
	compilerstyle "github.com/spice-framework/toolchain/compiler/style"
)

func (service *Service) analyzeConfiguredSelections(
	ctx context.Context,
	request normalizedRequest,
) (Result, error) {
	configuration := request.style.Clone()
	states := make([]configuredSelectionState, 0, len(configuration.BuildSelections))
	for index := range configuration.BuildSelections {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		selection := configuration.BuildSelections[index]
		selected := request
		selected.selection = &selection
		selected.patterns = selectionPatterns(selection)
		selected.styleInventory = true
		entrypointOptions := service.analysisLoadOptions(selected)
		entrypointOptions.PrepareGeneratedApplicationEntrypoints = true
		entrypointOptions = withAnalysisBuildTag(entrypointOptions)
		entrypoints, entrypointDiagnostics, discoverErr := load.DiscoverGeneratedApplicationEntrypoints(
			entrypointOptions,
			selected.patterns...,
		)
		if discoverErr != nil {
			return Result{}, fmt.Errorf(
				"discover style build selection %q generated application entrypoints: %w",
				selection.Name,
				discoverErr,
			)
		}
		selected.forceStyleInventory = len(entrypoints) != 0 ||
			len(entrypointDiagnostics) != 0
		inventory, err := service.analyze(ctx, selected)
		if err != nil {
			return Result{}, fmt.Errorf(
				"analyze style build selection %q: %w",
				selection.Name,
				err,
			)
		}
		states = append(states, configuredSelectionState{
			selection:             selection,
			request:               selected,
			entrypoints:           entrypoints,
			entrypointDiagnostics: entrypointDiagnostics,
			inventory:             inventory,
		})
	}

	models := make([]modulith.Model, len(states))
	for index := range states {
		models[index] = states[index].inventory.moduleModel
	}
	moduleUniverse := modulith.NewUniverse(models...)
	results := make([]selectionResult, 0, len(configuration.BuildSelections)*2)
	var scopes []ApplicationScope
	for index := range states {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		state := states[index]
		selection := state.selection
		results = append(results, selectionResult{
			id:     selection.Name,
			result: state.inventory,
		})
		if !state.inventory.Diagnostics().Empty() {
			continue
		}
		if len(state.entrypointDiagnostics) != 0 {
			result := Result{workspaceRoot: request.root, sequence: request.sequence}
			result.diagnostics = versionDiagnostics(
				diagnosticadapt.Load(request.root, state.entrypointDiagnostics),
				request.overlay,
			)
			results = append(results, selectionResult{id: selection.Name, result: result})
			continue
		}

		targets := configuredApplicationTargets(
			state.inventory.applicationPackages,
			state.entrypoints,
		)
		if len(targets) == 0 {
			continue
		}

		selectionTargets := make([]selectionResult, 0, len(targets))
		for _, target := range targets {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			targetRequest := state.request
			targetRequest.selection = &selection
			targetRequest.styleInventory = false
			targetRequest.generatedEntrypoint = target.generated
			targetRequest.applicationScope = true
			targetRequest.moduleUniverse = moduleUniverse
			targetRequest.patterns = []string{target.packagePath}
			result, analyzeErr := service.analyze(ctx, targetRequest)
			if analyzeErr != nil {
				return Result{}, fmt.Errorf(
					"analyze style build selection %q application %q: %w",
					selection.Name,
					target.packagePath,
					analyzeErr,
				)
			}
			scope := ApplicationScope{
				BuildSelection:      selection.Name,
				PackagePath:         target.packagePath,
				GeneratedEntrypoint: target.generated,
			}
			scopes = append(scopes, scope)
			selectionTargets = append(selectionTargets, selectionResult{
				id:     selection.Name,
				scope:  target.packagePath,
				result: result,
			})
		}
		results = append(results, selectionTargets...)
		coverage := applicationSemanticCoverageDiagnostics(
			request.root,
			state.inventory,
			selectionTargets,
		)
		if !coverage.Empty() {
			results = append(results, selectionResult{
				id:     selection.Name,
				result: Result{diagnostics: coverage},
			})
		}
	}
	if len(results) == 0 {
		return Result{}, nil
	}
	result := mergeConfiguredResults(results)
	result.applicationScopes = scopes
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

type configuredSelectionState struct {
	selection             compilerstyle.BuildSelection
	request               normalizedRequest
	entrypoints           []load.GeneratedApplicationEntrypoint
	entrypointDiagnostics []load.Diagnostic
	inventory             Result
}

type selectionResult struct {
	id     string
	scope  string
	result Result
}

type configuredApplicationTarget struct {
	packagePath string
	generated   bool
}

func configuredApplicationTargets(
	applications []string,
	entrypoints []load.GeneratedApplicationEntrypoint,
) []configuredApplicationTarget {
	values := make(map[string]bool, len(applications)+len(entrypoints))
	for _, packagePath := range applications {
		if packagePath != "" {
			values[packagePath] = false
		}
	}
	for _, entrypoint := range entrypoints {
		if entrypoint.PackagePath != "" {
			values[entrypoint.PackagePath] = true
		}
	}
	result := make([]configuredApplicationTarget, 0, len(values))
	for packagePath, generated := range values {
		result = append(result, configuredApplicationTarget{
			packagePath: packagePath,
			generated:   generated,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].packagePath < result[j].packagePath
	})
	return result
}

func mergeConfiguredResults(results []selectionResult) Result {
	if len(results) == 0 {
		return Result{}
	}
	result := results[0].result
	for _, selected := range results {
		if selected.scope == "" || len(selected.result.loadedFiles) == 0 {
			continue
		}
		result.application = selected.result.application
		result.moduleModel = selected.result.moduleModel
		result.plan = selected.result.plan
		result.targetName = selected.result.targetName
		result.hasPlan = selected.result.hasPlan
		break
	}

	loaded := make(map[string]struct{})
	annotations := make(map[string]struct{})
	providers := make(map[string]struct{})
	edges := make(map[string]struct{})
	configurations := make(map[string]struct{})
	enums := make(map[string]struct{})
	autoConfigurations := make(map[string]struct{})
	modules := make(map[string]struct{})
	moduleEdges := make(map[string]struct{})
	unassignedPackages := make(map[string]struct{})
	result.loadedFiles = nil
	result.annotations = nil
	result.providerGraph = ProviderGraph{}
	result.configurations = nil
	result.enums = nil
	result.autoConfigs = nil
	result.moduleGraph = ModuleGraph{}
	for _, selected := range results {
		for _, file := range selected.result.loadedFiles {
			file = filepath.Clean(file)
			if _, found := loaded[file]; !found {
				loaded[file] = struct{}{}
				result.loadedFiles = append(result.loadedFiles, file)
			}
		}
		result.files += selected.result.files
		for _, item := range selected.result.annotations {
			key := item.SymbolID + "\x00" + item.Name + "\x00" + item.Location.Path +
				fmt.Sprint(item.Location.Range.Start)
			if _, found := annotations[key]; !found {
				annotations[key] = struct{}{}
				result.annotations = append(result.annotations, item)
			}
		}
		for _, item := range selected.result.providerGraph.Providers {
			if _, found := providers[item.ID]; !found {
				providers[item.ID] = struct{}{}
				result.providerGraph.Providers = append(result.providerGraph.Providers, item)
			}
		}
		for _, item := range selected.result.providerGraph.Edges {
			key := fmt.Sprintf("%#v", item)
			if _, found := edges[key]; !found {
				edges[key] = struct{}{}
				result.providerGraph.Edges = append(result.providerGraph.Edges, item)
			}
		}
		for _, item := range selected.result.configurations {
			if _, found := configurations[item.SymbolID]; !found {
				configurations[item.SymbolID] = struct{}{}
				result.configurations = append(result.configurations, item)
			}
		}
		for _, item := range selected.result.enums {
			if _, found := enums[item.SymbolID]; !found {
				enums[item.SymbolID] = struct{}{}
				result.enums = append(result.enums, item)
			}
		}
		for _, item := range selected.result.autoConfigs {
			key := item.PackagePath + "\x00" + item.Factory
			if _, found := autoConfigurations[key]; !found {
				autoConfigurations[key] = struct{}{}
				result.autoConfigs = append(result.autoConfigs, item)
			}
		}
		for _, item := range selected.result.moduleGraph.Modules {
			if _, found := modules[item.ID]; !found {
				modules[item.ID] = struct{}{}
				result.moduleGraph.Modules = append(result.moduleGraph.Modules, item)
			}
		}
		for _, item := range selected.result.moduleGraph.Edges {
			key := fmt.Sprintf("%#v", item)
			if _, found := moduleEdges[key]; !found {
				moduleEdges[key] = struct{}{}
				result.moduleGraph.Edges = append(result.moduleGraph.Edges, item)
			}
		}
		for _, item := range selected.result.moduleGraph.UnassignedPackages {
			if _, found := unassignedPackages[item]; !found {
				unassignedPackages[item] = struct{}{}
				result.moduleGraph.UnassignedPackages = append(
					result.moduleGraph.UnassignedPackages,
					item,
				)
			}
		}
	}
	result.files = len(result.loadedFiles)
	sort.Strings(result.loadedFiles)
	sort.Slice(result.annotations, func(i, j int) bool {
		return result.annotations[i].Location.Path+"\x00"+result.annotations[i].SymbolID <
			result.annotations[j].Location.Path+"\x00"+result.annotations[j].SymbolID
	})
	sort.Slice(result.providerGraph.Providers, func(i, j int) bool {
		return result.providerGraph.Providers[i].ID < result.providerGraph.Providers[j].ID
	})
	sort.Slice(result.providerGraph.Edges, func(i, j int) bool {
		return fmt.Sprintf("%#v", result.providerGraph.Edges[i]) <
			fmt.Sprintf("%#v", result.providerGraph.Edges[j])
	})
	sort.Slice(result.configurations, func(i, j int) bool {
		return result.configurations[i].SymbolID < result.configurations[j].SymbolID
	})
	sort.Slice(result.enums, func(i, j int) bool {
		return result.enums[i].SymbolID < result.enums[j].SymbolID
	})
	sort.Slice(result.autoConfigs, func(i, j int) bool {
		left := result.autoConfigs[i].PackagePath + "\x00" + result.autoConfigs[i].Factory
		right := result.autoConfigs[j].PackagePath + "\x00" + result.autoConfigs[j].Factory
		return left < right
	})
	sort.Slice(result.moduleGraph.Modules, func(i, j int) bool {
		return result.moduleGraph.Modules[i].ID < result.moduleGraph.Modules[j].ID
	})
	sort.Slice(result.moduleGraph.Edges, func(i, j int) bool {
		return fmt.Sprintf("%#v", result.moduleGraph.Edges[i]) <
			fmt.Sprintf("%#v", result.moduleGraph.Edges[j])
	})
	sort.Strings(result.moduleGraph.UnassignedPackages)
	return result
}

type semanticOccurrence struct {
	symbolID    string
	kind        sdk.ContributionKind
	packagePath string
	filename    string
	line        int
	column      int
	offset      int
}

func applicationPackagePaths(resolution resolve.Result) []string {
	values := make(map[string]struct{})
	for _, occurrence := range resolution.Occurrences {
		if occurrence.HasContribution(sdk.ContributionApplication) &&
			occurrence.PackagePath != "" {
			values[occurrence.PackagePath] = struct{}{}
		}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func applicationSemanticOccurrences(resolution resolve.Result) []semanticOccurrence {
	var result []semanticOccurrence
	for _, occurrence := range resolution.Occurrences {
		for _, contribution := range occurrence.Contributions {
			if !applicationSemanticContribution(contribution.Kind) {
				continue
			}
			result = append(result, semanticOccurrence{
				symbolID:    occurrence.SymbolID,
				kind:        contribution.Kind,
				packagePath: occurrence.PackagePath,
				filename:    occurrence.PhysicalPosition.Filename,
				line:        occurrence.PhysicalPosition.Line,
				column:      occurrence.PhysicalPosition.Column,
				offset:      occurrence.PhysicalPosition.Offset,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return semanticOccurrenceKey(result[i]) < semanticOccurrenceKey(result[j])
	})
	return result
}

func applicationSemanticContribution(kind sdk.ContributionKind) bool {
	switch kind {
	case sdk.ContributionApplication,
		sdk.ContributionModule,
		sdk.ContributionNamedInterface,
		sdk.ContributionGeneratedFile:
		return false
	case sdk.ContributionStereotype,
		sdk.ContributionInterface,
		sdk.ContributionProvider,
		sdk.ContributionBeanMetadata,
		sdk.ContributionConfiguration,
		sdk.ContributionEnum,
		sdk.ContributionController,
		sdk.ContributionRoute,
		sdk.ContributionLifecycle,
		sdk.ContributionBootstrap,
		sdk.ContributionSchedule,
		sdk.ContributionAsync,
		sdk.ContributionTransaction,
		sdk.ContributionEventTopic,
		sdk.ContributionEventListener,
		sdk.ContributionCache,
		sdk.ContributionAuthorization,
		sdk.ContributionRetry,
		sdk.ContributionObservation:
		return true
	}
	return true
}

func semanticOccurrenceKey(occurrence semanticOccurrence) string {
	return occurrence.symbolID + "\x00" + string(occurrence.kind) + "\x00" +
		filepath.Clean(occurrence.filename) + "\x00" + fmt.Sprint(occurrence.offset)
}

func applicationSemanticCoverageDiagnostics(
	root string,
	inventory Result,
	targets []selectionResult,
) diagnostic.Set {
	reached := make(map[string]struct{})
	for _, target := range targets {
		for _, occurrence := range target.result.semanticOccurrences {
			reached[semanticOccurrenceKey(occurrence)] = struct{}{}
		}
	}
	var diagnostics []diagnostic.Diagnostic
	for _, occurrence := range inventory.semanticOccurrences {
		if _, found := reached[semanticOccurrenceKey(occurrence)]; found {
			continue
		}
		location := diagnostic.SourceLocation(
			root,
			occurrence.filename,
			occurrence.filename,
			max(1, occurrence.line),
			max(1, occurrence.column),
			max(0, occurrence.offset),
		)
		diagnostics = append(diagnostics, diagnostic.New(
			"spice.style.configuration.application-selection",
			diagnostic.SeverityError,
			fmt.Sprintf(
				"%s contribution in package %q is unreachable from every application target in this build selection",
				occurrence.kind,
				occurrence.packagePath,
			),
			location,
		))
	}
	return diagnostic.NewSet(diagnostics...)
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
	for _, name := range []string{
		"CGO_ENABLED",
		"GO386",
		"GOAMD64",
		"GOARCH",
		"GOARM",
		"GOARM64",
		"GOAUTH",
		"GOENV",
		"GOEXPERIMENT",
		"GOFIPS140",
		"GOFLAGS",
		"GOMIPS",
		"GOMIPS64",
		"GOOS",
		"GOPPC64",
		"GOPROXY",
		"GORISCV64",
		"GOSUMDB",
		"GOTOOLCHAIN",
		"GOWASM",
	} {
		result.Env = removeEnvironment(result.Env, name)
	}
	cgoEnabled := "0"
	if *selection.CGOEnabled {
		cgoEnabled = "1"
	}
	result.Env = append(
		result.Env,
		"CGO_ENABLED="+cgoEnabled,
		"GOARCH="+selection.GOARCH,
		"GOAUTH=off",
		"GOENV=off",
		"GOEXPERIMENT=",
		"GOFIPS140=off",
		"GOFLAGS=",
		"GOOS="+selection.GOOS,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
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
		scopes  []string
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
			if selection.scope != "" && !slices.Contains(current.scopes, selection.scope) {
				current.scopes = append(current.scopes, selection.scope)
			}
		}
	}
	items := make([]diagnostic.Diagnostic, 0, len(order))
	for _, current := range order {
		sort.Strings(current.ids)
		sort.Strings(current.scopes)
		related := append(slices.Clone(current.related), diagnostic.RelatedInformation{
			Message:  "build selections: " + strings.Join(current.ids, ", "),
			Location: current.item.Location,
		})
		if len(current.scopes) != 0 {
			related = append(related, diagnostic.RelatedInformation{
				Message:  "application targets: " + strings.Join(current.scopes, ", "),
				Location: current.item.Location,
			})
		}
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
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve style workspace root: %w", err)
	}
	generated := make([]string, len(configuration.GeneratedRoots))
	for index, relative := range configuration.GeneratedRoots {
		generated[index] = filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	}
	set := make(map[string]struct{})
	var diagnostics []diagnostic.Diagnostic
	for _, relative := range configuration.SourceRoots {
		sourceRoot := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
		info, statErr := os.Lstat(sourceRoot)
		if statErr != nil {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root cannot be inspected: "+statErr.Error()))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			diagnostics = append(diagnostics, sourceRootDiagnostic(root, sourceRoot, "source root must not be a symbolic link"))
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
		if !pathWithin(resolvedRoot, resolved) {
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
