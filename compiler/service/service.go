package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/application"
	compilerbootstrap "github.com/StevenBuglione/spice/compiler/bootstrap"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	diagnosticadapt "github.com/StevenBuglione/spice/compiler/diagnostic/adapt"
	"github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/scan"
	"github.com/StevenBuglione/spice/compiler/validate"
)

// Service owns bounded analysis state for independent workspaces.
type Service struct {
	config serviceConfig

	mu      sync.Mutex
	cache   map[[sha256.Size]byte]Result
	order   [][sha256.Size]byte
	latest  map[string]uint64
	running map[string]runningRequest
}

type runningRequest struct {
	sequence uint64
	cancel   context.CancelFunc
}

type serviceConfig struct {
	loader               Loader
	loadOptions          load.Options
	registry             annotation.Registry
	bootstrapDefinitions []compilerbootstrap.Definition
	providerEntrypoints  []provider.Entrypoint
	cacheNamespace       string
	maxCacheEntries      int
	maxOverlayFiles      int
	maxOverlayBytes      int
	cacheExtensions      bool
}

// New creates an isolated bounded compiler service.
func New(config Config) (*Service, error) {
	if config.Loader == nil {
		config.Loader = load.Load
	}
	if len(config.Registry.Definitions()) == 0 {
		config.Registry = builtin.Registry()
	}
	if config.MaxCacheEntries == 0 {
		config.MaxCacheEntries = defaultCacheEntries
	}
	if config.MaxCacheEntries < 0 {
		return nil, errors.New("compiler service cache size must not be negative")
	}
	if config.MaxOverlayFiles == 0 {
		config.MaxOverlayFiles = defaultOverlayFiles
	}
	if config.MaxOverlayFiles <= 0 {
		return nil, errors.New("compiler service overlay file limit must be positive")
	}
	if config.MaxOverlayBytes == 0 {
		config.MaxOverlayBytes = defaultOverlayBytes
	}
	if config.MaxOverlayBytes <= 0 {
		return nil, errors.New("compiler service overlay byte limit must be positive")
	}
	cacheExtensions := len(config.BootstrapDefinitions) == 0 &&
		len(config.ProviderEntrypoints) == 0
	if config.CacheNamespace != "" {
		cacheExtensions = true
	}
	return &Service{
		config: serviceConfig{
			loader:               config.Loader,
			loadOptions:          cloneLoadOptions(config.LoadOptions),
			registry:             config.Registry,
			bootstrapDefinitions: cloneBootstrapDefinitions(config.BootstrapDefinitions),
			providerEntrypoints:  slices.Clone(config.ProviderEntrypoints),
			cacheNamespace:       config.CacheNamespace,
			maxCacheEntries:      config.MaxCacheEntries,
			maxOverlayFiles:      config.MaxOverlayFiles,
			maxOverlayBytes:      config.MaxOverlayBytes,
			cacheExtensions:      cacheExtensions,
		},
		cache:   make(map[[sha256.Size]byte]Result),
		latest:  make(map[string]uint64),
		running: make(map[string]runningRequest),
	}, nil
}

type normalizedRequest struct {
	root     string
	target   string
	patterns []string
	overlay  map[string]Document
	content  string
	sequence uint64
}

// Analyze executes one read-only typed compiler analysis.
func (service *Service) Analyze(
	ctx context.Context,
	request Request,
) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("compiler service context must not be nil")
	}
	normalized, err := service.normalizeRequest(request)
	if err != nil {
		return Result{}, err
	}
	analysisCtx, cancel, err := service.begin(ctx, normalized)
	if err != nil {
		return Result{}, err
	}
	defer cancel()
	defer service.finish(normalized)

	key, cacheable, err := service.cacheKey(normalized)
	if err != nil {
		return Result{}, err
	}
	if cacheable {
		if cached, found := service.cached(key); found {
			if staleErr := service.rejectStale(normalized); staleErr != nil {
				return Result{}, staleErr
			}
			cached.sequence = normalized.sequence
			return cached, nil
		}
	}

	result, err := service.analyze(analysisCtx, normalized)
	if err != nil {
		if staleErr := service.rejectStale(normalized); staleErr != nil {
			return Result{}, staleErr
		}
		return Result{}, err
	}
	if err := service.rejectStale(normalized); err != nil {
		return Result{}, err
	}
	if cacheable {
		service.store(key, result)
	}
	return cloneResult(result), nil
}

func (service *Service) begin(
	ctx context.Context,
	request normalizedRequest,
) (context.Context, context.CancelFunc, error) {
	analysisCtx, cancel := context.WithCancel(ctx)
	if request.sequence == 0 {
		return analysisCtx, cancel, nil
	}
	workspaceKey := normalizedWorkspaceKey(request.root)
	service.mu.Lock()
	defer service.mu.Unlock()
	if request.sequence < service.latest[workspaceKey] {
		cancel()
		return nil, nil, ErrStaleAnalysis
	}
	service.latest[workspaceKey] = request.sequence
	if previous, found := service.running[workspaceKey]; found {
		previous.cancel()
	}
	service.running[workspaceKey] = runningRequest{
		sequence: request.sequence,
		cancel:   cancel,
	}
	return analysisCtx, cancel, nil
}

func (service *Service) finish(request normalizedRequest) {
	if request.sequence == 0 {
		return
	}
	workspaceKey := normalizedWorkspaceKey(request.root)
	service.mu.Lock()
	if running, found := service.running[workspaceKey]; found &&
		running.sequence == request.sequence {
		delete(service.running, workspaceKey)
	}
	service.mu.Unlock()
}

func (service *Service) rejectStale(request normalizedRequest) error {
	if request.sequence == 0 {
		return nil
	}
	service.mu.Lock()
	latest := service.latest[normalizedWorkspaceKey(request.root)]
	service.mu.Unlock()
	if request.sequence < latest {
		return ErrStaleAnalysis
	}
	return nil
}

func (service *Service) analyze(
	ctx context.Context,
	request normalizedRequest,
) (Result, error) {
	result := Result{
		workspaceRoot: request.root,
		sequence:      request.sequence,
		definitions:   summarizeDefinitions(service.config.registry),
	}
	options := service.analysisLoadOptions(request)
	program, loadErr := service.config.loader(
		ctx,
		options,
		request.patterns...,
	)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if loadErr != nil {
		if program != nil {
			result.diagnostics = versionDiagnostics(
				diagnosticadapt.Load(request.root, program.Diagnostics()),
				request.overlay,
			)
		} else {
			result.diagnostics = diagnosticadapt.Failure(
				"load",
				"operation",
				loadErr.Error(),
			)
		}
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}

	resolution := resolve.Annotations(program)
	result.annotations = summarizeAnnotations(request.root, resolution)
	if len(resolution.Diagnostics) != 0 {
		result.diagnostics = versionDiagnostics(
			diagnosticadapt.Resolution(
				request.root,
				resolution.Diagnostics,
			),
			request.overlay,
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}

	validationDiagnostics := validateOccurrences(
		resolution.Occurrences,
		service.config.registry,
	)
	if len(validationDiagnostics) != 0 {
		result.diagnostics = versionDiagnostics(
			diagnosticadapt.Validation(
				request.root,
				validationDiagnostics,
			),
			request.overlay,
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}

	buildOptions := application.BuildOptions{
		BootstrapDefinitions: cloneBootstrapDefinitions(
			service.config.bootstrapDefinitions,
		),
	}
	if len(service.config.providerEntrypoints) != 0 {
		catalog := provider.BuildEntrypoints(
			program,
			service.config.providerEntrypoints,
		)
		if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
			result.diagnostics = versionDiagnostics(
				diagnosticadapt.Provider(request.root, diagnostics),
				request.overlay,
			)
			result.actions = actionsFromDiagnostics(result.diagnostics)
			return result, nil
		}
		buildOptions.ProviderCatalogs = []provider.Catalog{catalog}
	}

	moduleModel := modulith.Build(program, resolution)
	result.moduleGraph = summarizeModuleGraph(moduleModel)
	model := application.BuildWithOptions(
		program,
		resolution,
		buildOptions,
	)
	result.application = model
	result.providerGraph = summarizeProviderGraph(model)
	result.configurations = summarizeConfigurations(model)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		result.diagnostics = versionDiagnostics(
			diagnosticadapt.Application(request.root, diagnostics),
			request.overlay,
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}

	target, err := selectTarget(model.Targets(), request.target)
	if err != nil {
		result.diagnostics = diagnosticadapt.Failure(
			"application",
			"target-selection",
			err.Error(),
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}
	generationTarget, generationDiagnostics := generate.DefaultTarget(
		program,
		target,
	)
	if len(generationDiagnostics) != 0 {
		result.diagnostics = versionDiagnostics(
			diagnosticadapt.Generation(
				request.root,
				generationDiagnostics,
			),
			request.overlay,
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}
	plan, generationDiagnostics := generate.Render(
		program,
		model,
		target,
		generationTarget,
	)
	if len(generationDiagnostics) != 0 {
		result.diagnostics = versionDiagnostics(
			diagnosticadapt.Generation(
				request.root,
				generationDiagnostics,
			),
			request.overlay,
		)
		result.actions = actionsFromDiagnostics(result.diagnostics)
		return result, nil
	}
	result.diagnostics = diagnostic.NewSet()
	result.plan = plan
	result.hasPlan = true
	return result, nil
}

func (service *Service) analysisLoadOptions(
	request normalizedRequest,
) load.Options {
	options := cloneLoadOptions(service.config.loadOptions)
	options.Dir = request.root
	options.Overlay = make(map[string][]byte, len(request.overlay))
	for filePath, document := range request.overlay {
		options.Overlay[filePath] = slices.Clone(document.Content)
	}
	options.AuxiliaryPackages = append(
		options.AuxiliaryPackages,
		entrypointPackages(service.config.providerEntrypoints)...,
	)
	options.AllowGeneratedMainBridge = true
	return withAnalysisBuildTag(options)
}

func (service *Service) normalizeRequest(
	request Request,
) (normalizedRequest, error) {
	if request.WorkspaceRoot == "" {
		return normalizedRequest{}, errors.New("compiler service workspace root must not be empty")
	}
	root, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return normalizedRequest{}, fmt.Errorf("resolve compiler service workspace root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return normalizedRequest{}, fmt.Errorf("inspect compiler service workspace root: %w", err)
	}
	if !info.IsDir() {
		return normalizedRequest{}, errors.New("compiler service workspace root must be a directory")
	}
	patterns := slices.Clone(request.Patterns)
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	overlay, err := normalizeOverlay(
		root,
		request.Overlay,
		service.config.maxOverlayFiles,
		service.config.maxOverlayBytes,
	)
	if err != nil {
		return normalizedRequest{}, err
	}
	return normalizedRequest{
		root:     filepath.Clean(root),
		target:   request.Target,
		patterns: patterns,
		overlay:  overlay,
		content:  request.ContentHash,
		sequence: request.Sequence,
	}, nil
}

func normalizeOverlay(
	root string,
	documents map[string]Document,
	maximumFiles int,
	maximumBytes int,
) (map[string]Document, error) {
	if len(documents) > maximumFiles {
		return nil, fmt.Errorf(
			"compiler service overlay has %d files; maximum is %d",
			len(documents),
			maximumFiles,
		)
	}
	result := make(map[string]Document, len(documents))
	total := 0
	for identity, document := range documents {
		filePath, err := overlayPath(root, identity)
		if err != nil {
			return nil, err
		}
		total += len(document.Content)
		if total > maximumBytes {
			return nil, fmt.Errorf(
				"compiler service overlay exceeds %d bytes",
				maximumBytes,
			)
		}
		document.Content = slices.Clone(document.Content)
		result[filePath] = document
	}
	return result, nil
}

func overlayPath(root, identity string) (string, error) {
	if identity == "" {
		return "", errors.New("compiler service overlay path must not be empty")
	}
	filePath, err := overlayIdentityPath(identity)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(root, filePath)
	}
	absolute, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve compiler service overlay %q: %w", identity, err)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || (relative != "." && !filepath.IsLocal(relative)) {
		return "", fmt.Errorf(
			"compiler service overlay %q must remain inside workspace",
			identity,
		)
	}
	return filepath.Clean(absolute), nil
}

func overlayIdentityPath(identity string) (string, error) {
	isURI := strings.HasPrefix(strings.ToLower(identity), "file:") ||
		strings.Contains(identity, "://")
	if !isURI {
		return identity, nil
	}
	parsed, err := url.Parse(identity)
	if err != nil {
		return "", fmt.Errorf(
			"parse compiler service overlay URI %q: %w",
			identity,
			err,
		)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf(
			"compiler service overlay URI %q must use file scheme",
			identity,
		)
	}
	filePath := parsed.Path
	if parsed.Host != "" && parsed.Host != "localhost" {
		if runtime.GOOS != "windows" ||
			len(parsed.Host) != 2 ||
			parsed.Host[1] != ':' {
			return "", fmt.Errorf(
				"compiler service overlay URI %q must not name a remote host",
				identity,
			)
		}
		filePath = "/" + parsed.Host + parsed.Path
	}
	if runtime.GOOS == "windows" &&
		len(filePath) >= 3 &&
		filePath[0] == '/' &&
		filePath[2] == ':' {
		filePath = filePath[1:]
	}
	return filepath.FromSlash(filePath), nil
}

func validateOccurrences(
	occurrences []resolve.Occurrence,
	registry annotation.Registry,
) []validate.Diagnostic {
	var diagnostics []validate.Diagnostic
	for _, occurrence := range occurrences {
		items := validate.Occurrences([]scan.Occurrence{{
			Annotation: occurrence.Annotation,
			Target:     occurrence.Target,
			Name:       occurrence.Name,
			File:       occurrence.PhysicalFile,
		}}, registry)
		for index := range items {
			physical := occurrence.PhysicalPosition
			if physical.Filename == "" {
				physical = token.Position{
					Filename: occurrence.PhysicalFile,
					Offset:   occurrence.PhysicalOffset,
				}
			}
			items[index].PhysicalPosition = physical
		}
		diagnostics = append(diagnostics, items...)
	}
	return diagnostics
}

func selectTarget(
	targets []application.Target,
	selector string,
) (application.Target, error) {
	if len(targets) == 0 {
		return application.Target{}, errors.New(
			"no @Application marker was found in the selected packages",
		)
	}
	if selector == "" {
		if len(targets) == 1 {
			return targets[0], nil
		}
		names := make([]string, len(targets))
		for index, target := range targets {
			names[index] = target.Name
		}
		return application.Target{}, fmt.Errorf(
			"multiple @Application targets were found (%s); select one",
			strings.Join(names, ", "),
		)
	}
	var matches []application.Target
	for _, target := range targets {
		if target.Name == selector ||
			target.PackagePath == selector ||
			target.SymbolID == selector ||
			strings.EqualFold(target.Name, selector) {
			matches = append(matches, target)
		}
	}
	if len(matches) == 0 {
		return application.Target{}, fmt.Errorf(
			"@Application target %q was not found",
			selector,
		)
	}
	if len(matches) > 1 {
		return application.Target{}, fmt.Errorf(
			"@Application target %q is ambiguous; select by stable symbol ID",
			selector,
		)
	}
	return matches[0], nil
}

func entrypointPackages(items []provider.Entrypoint) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.PackagePath)
	}
	sort.Strings(result)
	return slices.Compact(result)
}

func cloneLoadOptions(options load.Options) load.Options {
	options.Env = slices.Clone(options.Env)
	options.BuildFlags = slices.Clone(options.BuildFlags)
	options.AuxiliaryPackages = slices.Clone(options.AuxiliaryPackages)
	if options.Overlay != nil {
		overlay := make(map[string][]byte, len(options.Overlay))
		for filePath, content := range options.Overlay {
			overlay[filePath] = slices.Clone(content)
		}
		options.Overlay = overlay
	}
	return options
}

func cloneBootstrapDefinitions(
	items []compilerbootstrap.Definition,
) []compilerbootstrap.Definition {
	result := make([]compilerbootstrap.Definition, len(items))
	for index, item := range items {
		result[index] = item
		result[index].Options = slices.Clone(item.Options)
		for optionIndex := range result[index].Options {
			result[index].Options[optionIndex].ListItemKinds = slices.Clone(
				item.Options[optionIndex].ListItemKinds,
			)
			result[index].Options[optionIndex].AllowedStrings = slices.Clone(
				item.Options[optionIndex].AllowedStrings,
			)
		}
		result[index].Requirements = slices.Clone(item.Requirements)
		result[index].EntryPoints = slices.Clone(item.EntryPoints)
	}
	return result
}

func withAnalysisBuildTag(options load.Options) load.Options {
	result := cloneLoadOptions(options)
	result.BuildFlags = nil
	tags := map[string]struct{}{generate.AnalysisBuildTag: {}}
	addTagsFromFlags(tags, goFlags(options.Env))
	for index := 0; index < len(options.BuildFlags); index++ {
		flag := options.BuildFlags[index]
		switch {
		case flag == "-tags" && index+1 < len(options.BuildFlags):
			index++
			addTagValue(tags, options.BuildFlags[index])
		case strings.HasPrefix(flag, "-tags="):
			addTagValue(tags, strings.TrimPrefix(flag, "-tags="))
		default:
			result.BuildFlags = append(result.BuildFlags, flag)
		}
	}
	ordered := make([]string, 0, len(tags))
	for tag := range tags {
		ordered = append(ordered, tag)
	}
	sort.Strings(ordered)
	result.BuildFlags = append(
		result.BuildFlags,
		"-tags="+strings.Join(ordered, ","),
	)
	return result
}

func goFlags(environment []string) string {
	if environment == nil {
		return os.Getenv("GOFLAGS")
	}
	for _, value := range environment {
		name, flagValue, found := strings.Cut(value, "=")
		if found && strings.EqualFold(name, "GOFLAGS") {
			return flagValue
		}
	}
	return ""
}

func addTagsFromFlags(tags map[string]struct{}, flags string) {
	fields := strings.Fields(flags)
	for index := 0; index < len(fields); index++ {
		switch {
		case fields[index] == "-tags" && index+1 < len(fields):
			index++
			addTagValue(tags, fields[index])
		case strings.HasPrefix(fields[index], "-tags="):
			addTagValue(tags, strings.TrimPrefix(fields[index], "-tags="))
		}
	}
}

func addTagValue(tags map[string]struct{}, value string) {
	for tag := range strings.FieldsFuncSeq(value, func(character rune) bool {
		return character == ',' || character == ' '
	}) {
		if tag != "" {
			tags[tag] = struct{}{}
		}
	}
}

func normalizedWorkspaceKey(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		return strings.ToLower(root)
	}
	return root
}

func (service *Service) cacheKey(
	request normalizedRequest,
) ([sha256.Size]byte, bool, error) {
	if service.config.maxCacheEntries == 0 ||
		!service.config.cacheExtensions ||
		request.content == "" {
		return [sha256.Size]byte{}, false, nil
	}
	options := service.analysisLoadOptions(request)
	payload := struct {
		Root              string
		Target            string
		Patterns          []string
		Overlay           map[string]Document
		Environment       []string
		BuildFlags        []string
		AuxiliaryPackages []string
		Namespace         string
		Definitions       []annotation.Definition
		ContentHash       string
	}{
		Root:              normalizedWorkspaceKey(request.root),
		Target:            request.target,
		Patterns:          request.patterns,
		Overlay:           request.overlay,
		Environment:       options.Env,
		BuildFlags:        options.BuildFlags,
		AuxiliaryPackages: options.AuxiliaryPackages,
		Namespace:         service.config.cacheNamespace,
		Definitions:       service.config.registry.Definitions(),
		ContentHash:       request.content,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, false, fmt.Errorf(
			"hash compiler service request: %w",
			err,
		)
	}
	return sha256.Sum256(content), true, nil
}

func (service *Service) cached(
	key [sha256.Size]byte,
) (Result, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	result, found := service.cache[key]
	if !found {
		return Result{}, false
	}
	service.touch(key)
	return cloneResult(result), true
}

func (service *Service) store(key [sha256.Size]byte, result Result) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if _, found := service.cache[key]; found {
		service.cache[key] = cloneResult(result)
		service.touch(key)
		return
	}
	service.cache[key] = cloneResult(result)
	service.order = append(service.order, key)
	for len(service.order) > service.config.maxCacheEntries {
		oldest := service.order[0]
		service.order = service.order[1:]
		delete(service.cache, oldest)
	}
}

func (service *Service) touch(key [sha256.Size]byte) {
	for index, item := range service.order {
		if item == key {
			service.order = append(
				append(service.order[:index], service.order[index+1:]...),
				key,
			)
			return
		}
	}
}
