// Package generate renders deterministic generated application plans from one
// validated Spice application model. It performs no filesystem writes.
package generate

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/application"
	compilerasync "github.com/StevenBuglione/spice/compiler/async"
	compilercache "github.com/StevenBuglione/spice/compiler/cache"
	"github.com/StevenBuglione/spice/compiler/configuration"
	"github.com/StevenBuglione/spice/compiler/controller"
	compilerevent "github.com/StevenBuglione/spice/compiler/event"
	compilerlifecycle "github.com/StevenBuglione/spice/compiler/lifecycle"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	compilerschedule "github.com/StevenBuglione/spice/compiler/schedule"
	"github.com/StevenBuglione/spice/compiler/targetid"
	compilertransaction "github.com/StevenBuglione/spice/compiler/transaction"
	runtimeconfig "github.com/StevenBuglione/spice/config"
)

const (
	// SchemaVersion is the current generated ownership manifest schema.
	SchemaVersion = 5
	// GeneratorVersion is recorded in manifests to make generator compatibility
	// explicit during freshness checks.
	GeneratorVersion = "0.1.0-dev"
	// GoFormatLine is the supported Go formatter compatibility line.
	GoFormatLine = "1.26"
	// AnalysisBuildTag excludes committed generated source while Spice analyzes
	// source, allowing stale output to be regenerated safely.
	AnalysisBuildTag = "spice_generate"

	contractsFilename     = "spice_contracts_gen.go"
	configurationFilename = "spice_configuration_gen.go"
	providersFilename     = "spice_providers_gen.go"
	assemblyFilename      = "spice_assembly_gen.go"
	featuresFilename      = "spice_features_gen.go"
	httpFilename          = "spice_http_gen.go"
	lifecycleFilename     = "spice_lifecycle_gen.go"
	commandFilename       = "spice_command_gen.go"
	// bridgeFilename is retained for ownership migration tests. New plans never
	// emit a same-package bridge.
	bridgeFilename    = "zz_spice_bridge_gen.go"
	asyncPath         = "github.com/StevenBuglione/spice/async"
	beanPath          = "github.com/StevenBuglione/spice/bean"
	configPath        = "github.com/StevenBuglione/spice/config"
	cachePath         = "github.com/StevenBuglione/spice/cache"
	dataPath          = "github.com/StevenBuglione/spice/data"
	eventPath         = "github.com/StevenBuglione/spice/event"
	lifecyclePath     = "github.com/StevenBuglione/spice/lifecycle"
	managementPath    = "github.com/StevenBuglione/spice/management"
	observabilityPath = "github.com/StevenBuglione/spice/observability"
	schedulePath      = "github.com/StevenBuglione/spice/schedule"
	securityPath      = "github.com/StevenBuglione/spice/security"
	viewPath          = "github.com/StevenBuglione/spice/view"
	webPath           = "github.com/StevenBuglione/spice/web"

	shutdownConfigurationKey = "spice.shutdown-timeout"
	asyncConcurrencyKey      = "spice.async.max-concurrency"
)

var targetIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Layout identifies where generated Go is compiled.
type Layout string

const (
	// LayoutGeneratedPackage is the legacy importable
	// internal/spicegen/<target> package layout.
	LayoutGeneratedPackage Layout = "generated-package"
	// LayoutApplicationPackage emits the complete generated application in an
	// importable target-scoped package. The physical package-main source imports
	// that package explicitly; generation never writes beside handwritten code.
	LayoutApplicationPackage Layout = "application-package"
)

// Target identifies one module-scoped generated application output.
type Target struct {
	ID                    string
	Layout                Layout
	ModulePath            string
	ModuleRoot            string
	PackagePath           string
	EntrypointPackagePath string
	OutputDir             string
	BridgeDir             string
	ManifestPath          string
}

// TargetSummary is the safe, serializable subset of a generation target.
type TargetSummary struct {
	ID                    string `json:"id"`
	Layout                Layout `json:"layout"`
	ModulePath            string `json:"module"`
	PackagePath           string `json:"package"`
	EntrypointPackagePath string `json:"entrypoint_package,omitempty"`
	OutputDir             string `json:"output_dir"`
	BridgeDir             string `json:"bridge_dir,omitempty"`
	ManifestPath          string `json:"manifest_path"`
}

// FileRole identifies the purpose of one generated artifact.
type FileRole string

const (
	// FileRoleTargetOrchestrator is target-wide application coordination that
	// cannot truthfully belong to one source file. It is retained for guarded
	// migration from schema-4 monolithic output; new plans do not emit it.
	FileRoleTargetOrchestrator FileRole = "target-orchestrator"
	// FileRoleTargetContracts contains the stable public generated types.
	FileRoleTargetContracts FileRole = "target-contracts"
	// FileRoleTargetConfiguration contains generated configuration metadata.
	FileRoleTargetConfiguration FileRole = "target-configuration"
	// FileRoleTargetProviders contains dependency-ordered graph construction.
	FileRoleTargetProviders FileRole = "target-providers"
	// FileRoleTargetAssembly coordinates bounded generated construction phases.
	FileRoleTargetAssembly FileRole = "target-assembly"
	// FileRoleTargetFeatures contains lifecycle, schedule, and async wiring.
	FileRoleTargetFeatures FileRole = "target-features"
	// FileRoleTargetHTTP contains generated HTTP and management wiring.
	FileRoleTargetHTTP FileRole = "target-http"
	// FileRoleTargetHTTPRoute contains one source-linked generated route.
	FileRoleTargetHTTPRoute FileRole = "target-http-route"
	// FileRoleTargetLifecycle contains the public generated runtime methods.
	FileRoleTargetLifecycle FileRole = "target-lifecycle"
	// FileRoleTargetCommand contains the process-level generated command API.
	FileRoleTargetCommand FileRole = "target-command"
	// FileRoleCommandBridge is retained only to migrate previously owned
	// same-package bridges. New plans never emit this role.
	FileRoleCommandBridge FileRole = "command-bridge"
	// FileRoleSourceUnit is annotation-derived code owned by exactly one
	// handwritten source file in the mirrored generated tree.
	FileRoleSourceUnit FileRole = "source-unit"
	// FileRoleArtifact is a non-Go generated contract such as OpenAPI.
	FileRoleArtifact FileRole = "artifact"

	// FileRoleApplication identifies the generated construction entrypoint.
	FileRoleApplication = FileRoleTargetAssembly
	// FileRoleSourceShard is retained as a source-compatible alias while
	// callers migrate to the source-unit name.
	FileRoleSourceShard = FileRoleSourceUnit
)

// SourceOrigin identifies handwritten source that owns generated behavior.
// Paths are module-relative and never absolute.
type SourceOrigin struct {
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	Symbol string `json:"symbol,omitempty"`
}

// GeneratedRange identifies a half-open line/column range in generated output.
// Generated paths and coordinates remain physical so debuggers never pretend
// generated execution occurred in handwritten source.
type GeneratedRange struct {
	StartLine   int `json:"start_line"`
	StartColumn int `json:"start_column"`
	EndLine     int `json:"end_line"`
	EndColumn   int `json:"end_column"`
}

// SourceMapping connects one generated declaration or statement group to the
// handwritten contribution that caused it.
type SourceMapping struct {
	Kind          string         `json:"kind"`
	Contribution  string         `json:"contribution"`
	Source        SourceOrigin   `json:"source"`
	Generated     GeneratedRange `json:"generated"`
	RelatedSource []SourceOrigin `json:"related_sources,omitempty"`
}

// ManifestFile records one generated file owned by a target.
type ManifestFile struct {
	Path           string          `json:"path"`
	SHA256         string          `json:"sha256"`
	Role           FileRole        `json:"role"`
	PrimarySource  *SourceOrigin   `json:"primary_source,omitempty"`
	RelatedSources []SourceOrigin  `json:"related_sources,omitempty"`
	Mappings       []SourceMapping `json:"mappings,omitempty"`
}

// Manifest is the deterministic generated-file ownership record.
type Manifest struct {
	Schema           int            `json:"schema"`
	Target           TargetSummary  `json:"target"`
	GeneratorVersion string         `json:"generator_version"`
	GoFormatLine     string         `json:"go_format_line"`
	InputSHA256      string         `json:"input_sha256"`
	Files            []ManifestFile `json:"files"`
}

// File is one generated file in a render plan.
type File struct {
	Path           string
	Mode           fs.FileMode
	SHA256         string
	Role           FileRole
	PrimarySource  *SourceOrigin
	RelatedSources []SourceOrigin
	Mappings       []SourceMapping
	Sources        []SourceOrigin
	content        []byte
}

// Content returns a defensive copy of the generated bytes.
func (f File) Content() []byte {
	return append([]byte(nil), f.content...)
}

// Plan is a deterministic in-memory generation result.
type Plan struct {
	target          Target
	files           []File
	manifest        Manifest
	manifestContent []byte
}

// Target returns the plan's target.
func (p Plan) Target() Target {
	return p.target
}

// Files returns defensive copies sorted by module-relative path.
func (p Plan) Files() []File {
	result := make([]File, len(p.files))
	copy(result, p.files)
	for index := range result {
		result[index].content = append([]byte(nil), p.files[index].content...)
		result[index].PrimarySource = cloneSourceOrigin(
			p.files[index].PrimarySource,
		)
		result[index].RelatedSources = append(
			[]SourceOrigin(nil),
			p.files[index].RelatedSources...,
		)
		result[index].Mappings = cloneSourceMappings(
			p.files[index].Mappings,
		)
		result[index].Sources = append(
			[]SourceOrigin(nil),
			p.files[index].Sources...,
		)
	}
	return result
}

// Manifest returns a defensive copy of the ownership manifest.
func (p Plan) Manifest() Manifest {
	result := p.manifest
	result.Files = make([]ManifestFile, len(p.manifest.Files))
	for index, file := range p.manifest.Files {
		result.Files[index] = file
		result.Files[index].PrimarySource = cloneSourceOrigin(
			file.PrimarySource,
		)
		result.Files[index].RelatedSources = append(
			[]SourceOrigin(nil),
			file.RelatedSources...,
		)
		result.Files[index].Mappings = cloneSourceMappings(file.Mappings)
	}
	return result
}

func cloneSourceOrigin(origin *SourceOrigin) *SourceOrigin {
	if origin == nil {
		return nil
	}
	result := *origin
	return &result
}

func cloneSourceMappings(mappings []SourceMapping) []SourceMapping {
	if len(mappings) == 0 {
		return nil
	}
	result := make([]SourceMapping, len(mappings))
	copy(result, mappings)
	for index := range result {
		result[index].RelatedSource = append(
			[]SourceOrigin(nil),
			mappings[index].RelatedSource...,
		)
	}
	return result
}

// ManifestContent returns the exact canonical manifest bytes.
func (p Plan) ManifestContent() []byte {
	return append([]byte(nil), p.manifestContent...)
}

// Diagnostic is one deterministic generation failure.
type Diagnostic struct {
	Position         token.Position
	PhysicalPosition token.Position
	TargetID         string
	Kind             string
	Message          string
}

// Error renders a compiler-style diagnostic.
func (d Diagnostic) Error() string {
	position := d.Position
	if position.Filename == "" {
		position.Filename = "<generation>"
	}
	if position.Line <= 0 {
		position.Line = 1
	}
	if position.Column <= 0 {
		position.Column = 1
	}
	return fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, d.Message)
}

// DefaultTarget derives the standard module-scoped output for one application
// marker. All generated output lives below internal/spicegen; BridgeDir is
// retained as schema compatibility metadata for the handwritten entrypoint
// package. Every target uses .spice/<target>.manifest.json.
func DefaultTarget(program *load.Program, applicationTarget application.Target) (Target, []Diagnostic) {
	if program == nil {
		return Target{}, []Diagnostic{{
			Kind:    "invalid-program",
			Message: "derive generation target: loaded program is nil",
		}}
	}
	pkg, ok := packageByPath(program.Packages(), applicationTarget.PackagePath)
	if !ok || pkg.Raw == nil || pkg.Raw.Module == nil {
		return Target{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"module",
			fmt.Sprintf(
				"@Application target %s is not owned by a Go module; select a module-backed package",
				applicationTarget.SymbolID,
			),
		)}
	}
	id := defaultTargetID(applicationTarget.Name)
	layout := LayoutGeneratedPackage
	outputDir := path.Join("internal", "spicegen", id)
	packagePath := path.Join(pkg.Raw.Module.Path, outputDir)
	bridgeDir := ""
	entrypointPackagePath := ""
	if applicationTarget.AutomaticDiscovery() {
		layout = LayoutApplicationPackage
		relative, err := filepath.Rel(pkg.Raw.Module.Dir, pkg.Dir)
		if err != nil ||
			(!filepath.IsLocal(relative) && relative != ".") {
			return Target{}, []Diagnostic{targetDiagnostic(
				applicationTarget,
				"package-directory",
				fmt.Sprintf(
					"derive package-main output for %s inside module %s",
					applicationTarget.PackagePath,
					pkg.Raw.Module.Path,
				),
			)}
		}
		bridgeDir = filepath.ToSlash(relative)
		entrypointPackagePath = applicationTarget.PackagePath
	}
	target := Target{
		ID:                    id,
		Layout:                layout,
		ModulePath:            pkg.Raw.Module.Path,
		ModuleRoot:            pkg.Raw.Module.Dir,
		PackagePath:           packagePath,
		EntrypointPackagePath: entrypointPackagePath,
		OutputDir:             outputDir,
		BridgeDir:             bridgeDir,
		ManifestPath:          path.Join(".spice", id+".manifest.json"),
	}
	if diagnostics := validateTarget(target, applicationTarget); len(diagnostics) != 0 {
		return Target{}, diagnostics
	}
	return target, nil
}

func defaultTargetID(name string) string {
	return targetid.Default(name)
}

// Render creates canonical generated Go and ownership manifest bytes entirely
// in memory. It consumes the application model's existing order and edges and
// never reloads packages, rebuilds dependency resolution, or executes source.
func Render(
	program *load.Program,
	model application.Model,
	applicationTarget application.Target,
	target Target,
) (Plan, []Diagnostic) {
	if program == nil {
		return Plan{}, []Diagnostic{{
			Kind:    "invalid-program",
			Message: "render application: loaded program is nil",
		}}
	}
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"invalid-model",
			fmt.Sprintf("render application: model has %d diagnostic(s)", len(diagnostics)),
		)}
	}
	if diagnostics := validateTarget(target, applicationTarget); len(diagnostics) != 0 {
		return Plan{}, diagnostics
	}
	if !containsTarget(model.Targets(), applicationTarget.SymbolID) {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"unknown-target",
			fmt.Sprintf("application target %s is not present in the supplied model", applicationTarget.SymbolID),
		)}
	}
	if diagnostics := validateRenderable(program, model, applicationTarget, target); len(diagnostics) != 0 {
		return Plan{}, diagnostics
	}

	modelOrigins := modelSourceOrigins(
		program,
		model,
		applicationTarget,
		target,
	)
	targetFiles, renderErr := renderTargetFiles(
		model,
		applicationTarget,
		target,
		modelOrigins,
	)
	if renderErr != nil {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"render",
			fmt.Sprintf("render generated Go: %v", renderErr),
		)}
	}
	files := append([]File(nil), targetFiles...)
	sourceUnits, sourceUnitErr := renderSourceUnits(
		program,
		applicationTarget,
		model.Providers(),
		model.Configurations(),
		target,
	)
	if sourceUnitErr != nil {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"render-source-units",
			fmt.Sprintf("render generated source units: %v", sourceUnitErr),
		)}
	}
	files = append(files, sourceUnits...)
	if len(model.Controllers()) != 0 {
		openAPIContent, openAPIErr := renderOpenAPI(model, applicationTarget)
		if openAPIErr != nil {
			return Plan{}, []Diagnostic{targetDiagnostic(
				applicationTarget,
				"openapi",
				fmt.Sprintf("render OpenAPI document: %v", openAPIErr),
			)}
		}
		openAPIOrigins := controllerSourceOrigins(
			modelOrigins,
			model.Controllers(),
		)
		files = append(files, File{
			Path:           path.Join(target.OutputDir, "artifacts", openAPIFilename),
			Mode:           0o644,
			SHA256:         contentHash(openAPIContent),
			Role:           FileRoleArtifact,
			RelatedSources: openAPIOrigins,
			Sources:        openAPIOrigins,
			content:        openAPIContent,
		})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	inputHash, hashErr := modelHash(model, applicationTarget, target)
	if hashErr != nil {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"input-hash",
			fmt.Sprintf("hash application model: %v", hashErr),
		)}
	}
	manifest := Manifest{
		Schema:           SchemaVersion,
		Target:           summarizeTarget(target),
		GeneratorVersion: GeneratorVersion,
		GoFormatLine:     GoFormatLine,
		InputSHA256:      inputHash,
	}
	for _, file := range files {
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:           file.Path,
			SHA256:         file.SHA256,
			Role:           file.Role,
			PrimarySource:  cloneSourceOrigin(file.PrimarySource),
			RelatedSources: append([]SourceOrigin(nil), file.RelatedSources...),
			Mappings:       cloneSourceMappings(file.Mappings),
		})
	}
	manifestContent, manifestErr := json.MarshalIndent(manifest, "", "  ")
	if manifestErr != nil {
		return Plan{}, []Diagnostic{targetDiagnostic(
			applicationTarget,
			"manifest",
			fmt.Sprintf("encode generation manifest: %v", manifestErr),
		)}
	}
	manifestContent = append(manifestContent, '\n')
	return Plan{
		target:          target,
		files:           files,
		manifest:        manifest,
		manifestContent: manifestContent,
	}, nil
}

func renderTargetFiles(
	model application.Model,
	applicationTarget application.Target,
	target Target,
	modelOrigins []SourceOrigin,
) ([]File, error) {
	providers := model.Providers()
	configTypes := model.Configurations()
	controllers := model.Controllers()
	jobs := model.Jobs()
	asyncTasks := model.AsyncTasks()
	events := model.Events()
	transactions := model.Transactions()
	caches := model.Caches()
	features := commandFeaturesFor(applicationTarget, len(controllers) != 0)
	features.authorization = hasAuthorization(controllers)
	features.scheduling = len(jobs) != 0
	features.asynchronous = len(asyncTasks) != 0
	features.transactions = len(transactions) != 0
	features.events = len(events) != 0
	features.caching = len(caches) != 0
	features.requestScope = hasProviderScope(
		providers,
		sdk.BeanScopeRequest,
	)
	aliases := importAliases(
		providers,
		controllers,
		asyncTasks,
		events,
		caches,
		features,
	)
	providerAdapters, adapterErr := providerSourceAdapters(
		providers,
		target,
		aliases,
	)
	if adapterErr != nil {
		return nil, adapterErr
	}
	applicationAdapter, adapterErr := buildApplicationSourceAdapter(
		applicationTarget,
		target,
		aliases,
	)
	if adapterErr != nil {
		return nil, adapterErr
	}
	providerModules := providerModuleIDs(model, providers)
	dependencies, err := dependencyVariables(
		model,
		providers,
		aliases,
	)
	if err != nil {
		return nil, err
	}
	componentFields := generatedComponentFields(providers)
	applicationOrigins := sourceOriginsForSymbolFamilies(
		modelOrigins,
		applicationTarget.SymbolID,
	)
	providerOrigins := providerSourceOrigins(
		modelOrigins,
		providers,
		events,
	)
	configurationOrigins := configurationSourceOrigins(
		modelOrigins,
		configTypes,
	)
	featureOrigins := featureSourceOrigins(
		modelOrigins,
		model.Components(),
		jobs,
		asyncTasks,
	)
	controllerOrigins := controllerSourceOrigins(
		modelOrigins,
		controllers,
	)

	contracts := renderContractsTargetSource(
		applicationAdapter,
		componentFields,
		aliases,
		features,
		asyncTasks,
	)

	var configurationSource bytes.Buffer
	writeConfigurationAPI(
		&configurationSource,
		configTypes,
		caches,
		features.asynchronous,
	)

	localProviderVariables, dependencyProviderVariables := targetProviderVariables(providers)
	providerSource, providerErr := renderProvidersTargetSource(
		providers,
		configTypes,
		aliases,
		dependencies,
		providerModules,
		localProviderVariables,
		events,
		providerAdapters,
	)
	if providerErr != nil {
		return nil, providerErr
	}

	hasLifecycleFeatures := len(model.Components()) != 0 || len(jobs) != 0
	assembly := renderAssemblyTargetSource(
		target,
		features,
		componentFields,
		dependencyProviderVariables,
		hasLifecycleFeatures,
	)

	featureSource := renderFeaturesTargetSource(
		model,
		applicationTarget,
		features,
		asyncTasks,
		jobs,
		dependencyProviderVariables,
		providerModules,
		aliases,
	)

	httpSource, routeSpecifications, httpErr := renderHTTPTargetSources(
		model,
		applicationTarget,
		features,
		providers,
		controllers,
		transactions,
		caches,
		dependencyProviderVariables,
		aliases,
		modelOrigins,
	)
	if httpErr != nil {
		return nil, httpErr
	}

	var lifecycleSource bytes.Buffer
	writeLifecycleMethods(&lifecycleSource)
	writeComponentsMethod(&lifecycleSource)
	writeAsyncApplicationMethods(&lifecycleSource, asyncTasks, aliases)
	if features.hasMux {
		writeHandlerMethod(&lifecycleSource)
	}

	var commandSource bytes.Buffer
	writeCommandAPI(&commandSource, features)

	specifications := targetFileSpecifications(
		targetSourceBodies{
			contracts:     contracts,
			configuration: configurationSource.Bytes(),
			providers:     providerSource,
			assembly:      assembly,
			features:      featureSource,
			http:          httpSource,
			lifecycle:     lifecycleSource.Bytes(),
			command:       commandSource.Bytes(),
		},
		targetSourceOrigins{
			application:   applicationOrigins,
			providers:     providerOrigins,
			configuration: configurationOrigins,
			features:      featureOrigins,
			controllers:   controllerOrigins,
		},
		routeSpecifications,
	)

	return materializeTargetFiles(
		specifications,
		aliases,
		target,
		applicationTarget,
	)
}

type targetSourceBodies struct {
	contracts     []byte
	configuration []byte
	providers     []byte
	assembly      []byte
	features      []byte
	http          []byte
	lifecycle     []byte
	command       []byte
}

type targetSourceOrigins struct {
	application   []SourceOrigin
	providers     []SourceOrigin
	configuration []SourceOrigin
	features      []SourceOrigin
	controllers   []SourceOrigin
}

func targetFileSpecifications(
	bodies targetSourceBodies,
	origins targetSourceOrigins,
	routes []targetFileSpecification,
) []targetFileSpecification {
	specifications := []targetFileSpecification{
		{
			filename: contractsFilename,
			role:     FileRoleTargetContracts,
			body:     bodies.contracts,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.providers,
				origins.features,
			),
		},
		{
			filename: configurationFilename,
			role:     FileRoleTargetConfiguration,
			body:     bodies.configuration,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.configuration,
			),
		},
		{
			filename: providersFilename,
			role:     FileRoleTargetProviders,
			body:     bodies.providers,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.providers,
			),
		},
		{
			filename: assemblyFilename,
			role:     FileRoleTargetAssembly,
			body:     bodies.assembly,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.providers,
				origins.features,
				origins.controllers,
			),
		},
	}
	if len(bodies.features) != 0 {
		specifications = append(specifications, targetFileSpecification{
			filename: featuresFilename,
			role:     FileRoleTargetFeatures,
			body:     bodies.features,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.features,
			),
		})
	}
	if len(bodies.http) != 0 {
		specifications = append(specifications, targetFileSpecification{
			filename: httpFilename,
			role:     FileRoleTargetHTTP,
			body:     bodies.http,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.controllers,
			),
		})
		specifications = append(specifications, routes...)
	}
	return append(
		specifications,
		targetFileSpecification{
			filename: lifecycleFilename,
			role:     FileRoleTargetLifecycle,
			body:     bodies.lifecycle,
			relatedSources: mergeSourceOrigins(
				origins.application,
				origins.features,
			),
		},
		targetFileSpecification{
			filename:       commandFilename,
			role:           FileRoleTargetCommand,
			body:           bodies.command,
			relatedSources: origins.application,
		},
	)
}

func targetProviderVariables(
	providers []provider.Provider,
) (map[string]string, map[string]string) {
	local := make(map[string]string, len(providers))
	dependencies := make(map[string]string, len(providers))
	names := providerVariableNames(providers)
	for index, item := range providers {
		local[item.SymbolID] = names[index]
		dependencies[item.SymbolID] = "dependencies." + names[index]
	}
	return local, dependencies
}

func renderProvidersTargetSource(
	providers []provider.Provider,
	configTypes []configuration.Type,
	aliases map[string]string,
	dependencies map[string][]string,
	providerModules map[string]string,
	providerVariables map[string]string,
	events []compilerevent.Topic,
	adapters map[string]providerSourceAdapter,
) ([]byte, error) {
	var source bytes.Buffer
	writeApplicationDependenciesType(
		&source,
		providers,
		aliases,
		providerVariables,
	)
	source.WriteString("func constructApplicationDependencies(\n")
	source.WriteString("\tctx context.Context,\n")
	source.WriteString("\tapplication *Application,\n")
	source.WriteString("\toptions ApplicationOptions,\n")
	source.WriteString("\tconfigurationSnapshot spiceconfig.Snapshot,\n")
	source.WriteString(") (*applicationDependencies, error) {\n")
	source.WriteString("\t_ = ctx\n")
	source.WriteString("\t_ = application\n")
	source.WriteString("\t_ = options\n")
	source.WriteString("\t_ = configurationSnapshot\n")
	if err := writeProviders(
		&source,
		providers,
		configTypes,
		aliases,
		dependencies,
		providerModules,
		providerVariables,
		events,
		adapters,
	); err != nil {
		return nil, err
	}
	writeApplicationDependenciesReturn(
		&source,
		providers,
		providerVariables,
	)
	source.WriteString("}\n")
	return source.Bytes(), nil
}

func renderAssemblyTargetSource(
	target Target,
	features commandFeatures,
	componentFields []generatedComponentField,
	providerVariables map[string]string,
	hasLifecycleFeatures bool,
) []byte {
	var source bytes.Buffer
	source.WriteString("func NewApplication(ctx context.Context, observers ...spicelifecycle.Observer) (*Application, error) {\n")
	source.WriteString("\treturn NewApplicationWithOptions(ctx, ApplicationOptions{Observers: observers})\n")
	source.WriteString("}\n\n")
	source.WriteString("func NewApplicationWithOptions(ctx context.Context, options ApplicationOptions) (*Application, error) {\n")
	fmt.Fprintf(
		&source,
		"\tif ctx == nil {\n\t\treturn nil, fmt.Errorf(%s)\n\t}\n",
		strconv.Quote("construct application "+target.ID+": context is nil"),
	)
	source.WriteString("\tapplication := &Application{coordinator: spicelifecycle.NewCoordinator()}\n")
	writeBootstrapObservers(&source, features)
	writeAuthorizationSetup(&source, features)
	source.WriteString("\tfor index, observer := range observers {\n")
	source.WriteString("\t\tif err := application.coordinator.RegisterObserver(observer); err != nil {\n")
	source.WriteString("\t\t\treturn nil, fmt.Errorf(\"register lifecycle observer %d: %w\", index, err)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t}\n")
	writeConfigurationResolution(&source, target)
	source.WriteString("\tdependencies, err := constructApplicationDependencies(ctx, application, options, configurationSnapshot)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, err\n")
	source.WriteString("\t}\n")
	source.WriteString("\t_ = dependencies\n")
	writeComponentAssignments(
		&source,
		componentFields,
		providerVariables,
	)
	if features.asynchronous {
		source.WriteString("\tif _, err := configureGeneratedAsync(ctx, application, options, configurationSnapshot, dependencies); err != nil {\n")
		source.WriteString("\t\treturn nil, err\n")
		source.WriteString("\t}\n")
	}
	if hasLifecycleFeatures {
		source.WriteString("\tif _, err := configureGeneratedLifecycle(ctx, application, options, dependencies); err != nil {\n")
		source.WriteString("\t\treturn nil, err\n")
		source.WriteString("\t}\n")
	}
	if features.hasMux {
		writeHTTPSetupCall(&source, features)
	}
	source.WriteString("\treturn application, nil\n")
	source.WriteString("}\n")
	return source.Bytes()
}

func renderContractsTargetSource(
	applicationAdapter applicationSourceAdapter,
	componentFields []generatedComponentField,
	aliases map[string]string,
	features commandFeatures,
	asyncTasks []compilerasync.Task,
) []byte {
	var source bytes.Buffer
	writeGeneratedConstants(
		&source,
		applicationAdapter.alias+"."+applicationAdapter.identifier,
	)
	writeComponentsType(&source, componentFields, aliases)
	source.WriteString("type Application struct {\n")
	source.WriteString("\tcoordinator *spicelifecycle.Coordinator\n")
	source.WriteString("\thooks []spicelifecycle.Hook\n")
	source.WriteString("\tshutdownTimeout time.Duration\n")
	source.WriteString("\tcomponents Components\n")
	if features.asynchronous {
		source.WriteString("\tasyncExecutor *spiceasync.Executor\n")
		writeAsyncApplicationFields(&source, asyncTasks, aliases)
	}
	if features.hasMux {
		source.WriteString("\tmux *http.ServeMux\n")
		source.WriteString("\thandler http.Handler\n")
	}
	source.WriteString("}\n\n")
	writeApplicationOptions(&source, features)
	return source.Bytes()
}

func renderFeaturesTargetSource(
	model application.Model,
	applicationTarget application.Target,
	features commandFeatures,
	asyncTasks []compilerasync.Task,
	jobs []compilerschedule.Job,
	providerVariables map[string]string,
	providerModules map[string]string,
	aliases map[string]string,
) []byte {
	var source bytes.Buffer
	if features.asynchronous {
		source.WriteString("func configureGeneratedAsync(\n")
		source.WriteString("\tctx context.Context,\n")
		source.WriteString("\tapplication *Application,\n")
		source.WriteString("\toptions ApplicationOptions,\n")
		source.WriteString("\tconfigurationSnapshot spiceconfig.Snapshot,\n")
		source.WriteString("\tdependencies *applicationDependencies,\n")
		source.WriteString(") (*Application, error) {\n")
		source.WriteString("\t_ = dependencies\n")
		writeAsyncSetup(
			&source,
			asyncTasks,
			providerVariables,
			providerModules,
			applicationTarget.PackagePath,
			aliases,
		)
		source.WriteString("\treturn application, nil\n")
		source.WriteString("}\n\n")
	}
	if len(model.Components()) != 0 || len(jobs) != 0 {
		source.WriteString("func configureGeneratedLifecycle(\n")
		source.WriteString("\tctx context.Context,\n")
		source.WriteString("\tapplication *Application,\n")
		source.WriteString("\toptions ApplicationOptions,\n")
		source.WriteString("\tdependencies *applicationDependencies,\n")
		source.WriteString(") (*Application, error) {\n")
		source.WriteString("\t_ = ctx\n")
		source.WriteString("\t_ = options\n")
		source.WriteString("\t_ = dependencies\n")
		writeScheduleSetup(
			&source,
			jobs,
			providerVariables,
			providerModules,
		)
		writeHooks(
			&source,
			model,
			providerVariables,
			providerModules,
			applicationTarget.PackagePath,
		)
		source.WriteString("\treturn application, nil\n")
		source.WriteString("}\n")
	}
	return source.Bytes()
}

func renderHTTPTargetSources(
	model application.Model,
	applicationTarget application.Target,
	features commandFeatures,
	providers []provider.Provider,
	controllers []controller.Controller,
	transactions []compilertransaction.Boundary,
	caches []compilercache.Boundary,
	providerVariables map[string]string,
	aliases map[string]string,
	modelOrigins []SourceOrigin,
) ([]byte, []targetFileSpecification, error) {
	if !features.hasMux {
		return nil, nil, nil
	}
	var source bytes.Buffer
	writeHTTPSetupSignature(&source, features)
	if features.httpObservation {
		if err := writeFeatureHTTPObservers(
			&source,
			applicationTarget,
			providers,
			providerVariables,
		); err != nil {
			return nil, nil, err
		}
	}
	writeRouteMux(&source, providers, providerVariables)
	var routeSpecifications []targetFileSpecification
	if len(controllers) != 0 {
		var err error
		routeSpecifications, err = renderRouteSpecifications(
			controllers,
			transactions,
			caches,
			providerVariables,
			aliases,
			features,
			modelOrigins,
		)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range routeSpecifications {
			writeRouteSetupCall(
				&source,
				item.generatedIdentifier,
				features,
			)
		}
	}
	writeManagementSetup(
		&source,
		model,
		applicationTarget,
		features,
	)
	writeRequestScopeSetup(&source, features)
	source.WriteString("\treturn application, nil\n")
	source.WriteString("}\n")
	return source.Bytes(), routeSpecifications, nil
}

func materializeTargetFiles(
	specifications []targetFileSpecification,
	aliases map[string]string,
	target Target,
	applicationTarget application.Target,
) ([]File, error) {
	files := make([]File, 0, len(specifications))
	for _, specification := range specifications {
		content, err := renderTargetFile(specification.body, aliases)
		if err != nil {
			return nil, fmt.Errorf(
				"render %s for %s: %w",
				specification.filename,
				applicationTarget.SymbolID,
				err,
			)
		}
		relatedSources := specification.relatedSources
		var mappings []SourceMapping
		if specification.generatedIdentifier != "" &&
			len(specification.relatedSources) != 0 {
			line, column, found := generatedIdentifierPosition(
				content,
				specification.generatedIdentifier,
			)
			if !found {
				return nil, fmt.Errorf(
					"generated %s has no position for %s",
					specification.filename,
					specification.generatedIdentifier,
				)
			}
			mappings = append(mappings, SourceMapping{
				Kind:         specification.mappingKind,
				Contribution: specification.contribution,
				Source:       specification.relatedSources[0],
				Generated: generatedIdentifierRange(
					line,
					column,
					specification.generatedIdentifier,
				),
			})
		}
		files = append(files, File{
			Path:   path.Join(target.OutputDir, specification.filename),
			Mode:   0o644,
			SHA256: contentHash(content),
			Role:   specification.role,
			RelatedSources: append(
				[]SourceOrigin(nil),
				relatedSources...,
			),
			Mappings: mappings,
			Sources:  append([]SourceOrigin(nil), relatedSources...),
			content:  content,
		})
	}
	return files, nil
}

type targetFileSpecification struct {
	filename            string
	role                FileRole
	body                []byte
	relatedSources      []SourceOrigin
	mappingKind         string
	contribution        string
	generatedIdentifier string
}

func renderTargetFile(
	body []byte,
	aliases map[string]string,
) ([]byte, error) {
	used, err := selectorAliases(body)
	if err != nil {
		return nil, err
	}
	fileAliases := make(map[string]string)
	for importPath, alias := range aliases {
		if _, found := used[alias]; found {
			fileAliases[importPath] = alias
		}
	}
	var source bytes.Buffer
	source.WriteString("//go:build !" + AnalysisBuildTag + "\n\n")
	source.WriteString("// Code generated by Spice. DO NOT EDIT.\n\n")
	source.WriteString("package spicegen\n\n")
	writeImports(&source, fileAliases)
	source.Write(body)
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

func selectorAliases(body []byte) (map[string]struct{}, error) {
	source := append([]byte("package spicegen\n"), body...)
	parsed, err := parser.ParseFile(
		token.NewFileSet(),
		"generated.go",
		source,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("parse generated declarations: %w", err)
	}
	result := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok {
			result[identifier.Name] = struct{}{}
		}
		return true
	})
	return result, nil
}

func writeApplicationDependenciesType(
	source *bytes.Buffer,
	providers []provider.Provider,
	aliases map[string]string,
	providerVariables map[string]string,
) {
	source.WriteString("type applicationDependencies struct {\n")
	for _, item := range providers {
		output := renderedType(item.Output, aliases)
		if item.Scope != sdk.BeanScopeSingleton {
			output = "spicebean.Provider[" + output + "]"
		}
		fmt.Fprintf(
			source,
			"\t%s %s\n",
			providerVariables[item.SymbolID],
			output,
		)
	}
	source.WriteString("}\n\n")
}

func writeApplicationDependenciesReturn(
	source *bytes.Buffer,
	providers []provider.Provider,
	providerVariables map[string]string,
) {
	source.WriteString("\treturn &applicationDependencies{\n")
	for _, item := range providers {
		variable := providerVariables[item.SymbolID]
		fmt.Fprintf(source, "\t\t%s: %s,\n", variable, variable)
	}
	source.WriteString("\t}, nil\n")
}

func writeHTTPSetupCall(
	source *bytes.Buffer,
	features commandFeatures,
) {
	source.WriteString("\tif _, err := configureGeneratedHTTP(\n")
	source.WriteString("\t\tctx,\n")
	source.WriteString("\t\tapplication,\n")
	source.WriteString("\t\toptions,\n")
	source.WriteString("\t\tconfigurationSchema,\n")
	source.WriteString("\t\tconfigurationSnapshot,\n")
	source.WriteString("\t\tdependencies,\n")
	source.WriteString("\t\thttpObservers,\n")
	if features.metrics {
		source.WriteString("\t\tmanagementMetrics,\n")
	}
	if features.authorization {
		source.WriteString("\t\tauthorizer,\n")
	}
	source.WriteString("\t); err != nil {\n")
	source.WriteString("\t\treturn nil, err\n")
	source.WriteString("\t}\n")
}

func writeHTTPSetupSignature(
	source *bytes.Buffer,
	features commandFeatures,
) {
	source.WriteString("func configureGeneratedHTTP(\n")
	source.WriteString("\tctx context.Context,\n")
	source.WriteString("\tapplication *Application,\n")
	source.WriteString("\toptions ApplicationOptions,\n")
	source.WriteString("\tconfigurationSchema spiceconfig.Schema,\n")
	source.WriteString("\tconfigurationSnapshot spiceconfig.Snapshot,\n")
	source.WriteString("\tdependencies *applicationDependencies,\n")
	source.WriteString("\thttpObservers []spiceweb.HTTPObserver,\n")
	if features.metrics {
		source.WriteString("\tmanagementMetrics *spicemanagement.HTTPMetrics,\n")
	}
	if features.authorization {
		source.WriteString("\tauthorizer *spicesecurity.Authorizer,\n")
	}
	source.WriteString(") (*Application, error) {\n")
	source.WriteString("\t_ = configurationSchema\n")
	source.WriteString("\t_ = configurationSnapshot\n")
	source.WriteString("\t_ = dependencies\n")
	source.WriteString("\t_ = httpObservers\n")
}

func renderRouteSpecifications(
	controllers []controller.Controller,
	transactions []compilertransaction.Boundary,
	caches []compilercache.Boundary,
	providerVariables map[string]string,
	aliases map[string]string,
	features commandFeatures,
	applicationOrigins []SourceOrigin,
) ([]targetFileSpecification, error) {
	transactionIndex := make(
		map[string]compilertransaction.Boundary,
		len(transactions),
	)
	for _, boundary := range transactions {
		transactionIndex[boundary.RouteID] = boundary
	}
	cacheIndex := make(map[string]compilercache.Boundary, len(caches))
	for _, boundary := range caches {
		cacheIndex[boundary.RouteID] = boundary
	}

	var result []targetFileSpecification
	for _, item := range controllers {
		receiver := providerVariables[item.ProviderID]
		for _, route := range item.Routes() {
			functionName, filename := generatedRouteIdentity(route)
			var source bytes.Buffer
			writeRouteSetupSignature(
				&source,
				functionName,
				features,
			)
			var routeCaches []compilercache.Boundary
			if boundary, found := cacheIndex[route.SymbolID]; found {
				routeCaches = append(routeCaches, boundary)
			}
			cacheRuntimes := writeCacheSetup(
				&source,
				routeCaches,
				aliases,
			)
			middleware := "options.Middleware"
			if authorization, protected := route.Authorization(); protected {
				middleware = writeRouteAuthorization(
					&source,
					authorization,
					route.HTTPMethod+" "+route.Path,
					0,
				)
			}
			if err := writeControllerRoute(
				&source,
				route,
				transactionIndex,
				cacheRuntimes,
				providerVariables,
				receiver,
				middleware,
				aliases,
				0,
			); err != nil {
				return nil, err
			}
			source.WriteString("\treturn application, nil\n")
			source.WriteString("}\n")
			related := sourceOriginsForSymbol(
				applicationOrigins,
				route.SymbolID,
			)
			result = append(result, targetFileSpecification{
				filename:            filename,
				role:                FileRoleTargetHTTPRoute,
				body:                source.Bytes(),
				relatedSources:      related,
				mappingKind:         "http-route-wiring",
				contribution:        route.SymbolID,
				generatedIdentifier: functionName,
			})
		}
	}
	return result, nil
}

func generatedRouteIdentity(route controller.Route) (string, string) {
	digest := sha256.Sum256([]byte(route.SymbolID))
	suffix := hex.EncodeToString(digest[:6])
	functionSuffix := hex.EncodeToString(digest[:4])
	label := targetid.Default(
		path.Base(route.Symbol.PackagePath) + "_" +
			route.Symbol.Receiver + "_" +
			route.Name,
	)
	const maximumRouteLabelLength = 64
	if len(label) > maximumRouteLabelLength {
		label = strings.TrimSuffix(
			label[:maximumRouteLabelLength],
			"_",
		)
	}
	functionLabel := exportedGeneratedIdentifier(
		path.Base(route.Symbol.PackagePath),
		"Route",
	) + exportedGeneratedIdentifier(
		route.Symbol.Receiver,
		"Controller",
	) + exportedGeneratedIdentifier(route.Name, "Handler")
	return "registerGeneratedRoute" + functionLabel + "_" + functionSuffix,
		"spice_http_route_" + label + "_" + suffix + "_gen.go"
}

func sourceOriginsForSymbol(
	origins []SourceOrigin,
	symbolID string,
) []SourceOrigin {
	return sourceOriginsForSymbolFamilies(origins, symbolID)
}

func sourceOriginsForSymbolFamilies(
	origins []SourceOrigin,
	symbolIDs ...string,
) []SourceOrigin {
	var result []SourceOrigin
	for _, origin := range origins {
		for _, symbolID := range symbolIDs {
			if origin.Symbol == symbolID ||
				strings.HasPrefix(origin.Symbol, symbolID+".") ||
				strings.HasPrefix(origin.Symbol, symbolID+"#") {
				result = append(result, origin)
				break
			}
		}
	}
	return result
}

func mergeSourceOrigins(groups ...[]SourceOrigin) []SourceOrigin {
	var result []SourceOrigin
	for _, group := range groups {
		result = append(result, group...)
	}
	sortSourceOrigins(result)
	return slices.Compact(result)
}

func providerSourceOrigins(
	origins []SourceOrigin,
	providers []provider.Provider,
	events []compilerevent.Topic,
) []SourceOrigin {
	symbols := make([]string, 0, len(providers)+len(events))
	for _, item := range providers {
		symbols = append(symbols, item.SymbolID)
	}
	for _, topic := range events {
		symbols = append(symbols, topic.MarkerID)
		for _, listener := range topic.Listeners() {
			symbols = append(symbols, listener.MethodID)
		}
	}
	return sourceOriginsForSymbolFamilies(origins, symbols...)
}

func configurationSourceOrigins(
	origins []SourceOrigin,
	configTypes []configuration.Type,
) []SourceOrigin {
	symbols := make([]string, 0, len(configTypes))
	for _, item := range configTypes {
		symbols = append(symbols, item.SymbolID)
	}
	return sourceOriginsForSymbolFamilies(origins, symbols...)
}

func featureSourceOrigins(
	origins []SourceOrigin,
	components []compilerlifecycle.Component,
	jobs []compilerschedule.Job,
	tasks []compilerasync.Task,
) []SourceOrigin {
	var symbols []string
	for _, component := range components {
		if component.Start != nil {
			symbols = append(symbols, component.Start.MethodID)
		}
		if component.Stop != nil {
			symbols = append(symbols, component.Stop.MethodID)
		}
	}
	for _, job := range jobs {
		symbols = append(symbols, job.MethodID)
	}
	for _, task := range tasks {
		symbols = append(symbols, task.MethodID)
	}
	return sourceOriginsForSymbolFamilies(origins, symbols...)
}

func controllerSourceOrigins(
	origins []SourceOrigin,
	controllers []controller.Controller,
) []SourceOrigin {
	var symbols []string
	for _, item := range controllers {
		symbols = append(symbols, item.SymbolID)
		for _, route := range item.Routes() {
			symbols = append(symbols, route.SymbolID)
		}
	}
	return sourceOriginsForSymbolFamilies(origins, symbols...)
}

func writeRouteSetupSignature(
	source *bytes.Buffer,
	functionName string,
	features commandFeatures,
) {
	fmt.Fprintf(source, "func %s(\n", functionName)
	source.WriteString("\tctx context.Context,\n")
	source.WriteString("\tapplication *Application,\n")
	source.WriteString("\toptions ApplicationOptions,\n")
	source.WriteString("\tconfigurationSnapshot spiceconfig.Snapshot,\n")
	source.WriteString("\tdependencies *applicationDependencies,\n")
	source.WriteString("\thttpObservers []spiceweb.HTTPObserver,\n")
	source.WriteString("\trouteMux *http.ServeMux,\n")
	if features.authorization {
		source.WriteString("\tauthorizer *spicesecurity.Authorizer,\n")
	}
	source.WriteString(") (*Application, error) {\n")
	source.WriteString("\t_ = configurationSnapshot\n")
	source.WriteString("\t_ = dependencies\n")
	source.WriteString("\t_ = httpObservers\n")
	if features.authorization {
		source.WriteString("\t_ = authorizer\n")
	}
}

func writeRouteSetupCall(
	source *bytes.Buffer,
	functionName string,
	features commandFeatures,
) {
	fmt.Fprintf(source, "\tif _, err := %s(\n", functionName)
	source.WriteString("\t\tctx,\n")
	source.WriteString("\t\tapplication,\n")
	source.WriteString("\t\toptions,\n")
	source.WriteString("\t\tconfigurationSnapshot,\n")
	source.WriteString("\t\tdependencies,\n")
	source.WriteString("\t\thttpObservers,\n")
	source.WriteString("\t\trouteMux,\n")
	if features.authorization {
		source.WriteString("\t\tauthorizer,\n")
	}
	source.WriteString("\t); err != nil {\n")
	source.WriteString("\t\treturn nil, err\n")
	source.WriteString("\t}\n")
}

type providerSourceUnit struct {
	path        string
	packageName string
	origins     []SourceOrigin
	application *sourceUnitApplication
	providers   []provider.Provider
}

type sourceUnitApplication struct {
	target   application.Target
	targetID string
}

type applicationSourceAdapter struct {
	alias      string
	identifier string
}

type providerSourceAdapter struct {
	alias    string
	function string
}

func providerSourceAdapters(
	providers []provider.Provider,
	target Target,
	aliases map[string]string,
) (map[string]providerSourceAdapter, error) {
	providerPaths := make(map[string]string)
	var importPaths []string
	seenImports := make(map[string]struct{})
	for _, item := range providers {
		if !sourceUnitProvidesProvider(item) {
			continue
		}
		unitPath, _, _, err := sourceUnitLocation(
			item,
			target,
		)
		if err != nil {
			return nil, err
		}
		importPath := path.Join(
			target.ModulePath,
			path.Dir(unitPath),
		)
		providerPaths[item.SymbolID] = importPath
		if _, found := seenImports[importPath]; found {
			continue
		}
		seenImports[importPath] = struct{}{}
		importPaths = append(importPaths, importPath)
	}
	sort.Strings(importPaths)
	importAliases := make(map[string]string, len(importPaths))
	for index, importPath := range importPaths {
		preferred := "spice" + exportedGeneratedIdentifier(
			path.Base(importPath),
			"Source"+strconv.Itoa(index),
		)
		importAliases[importPath] = ensureSourceUnitImportAlias(
			aliases,
			importPath,
			preferred,
		)
	}
	result := make(map[string]providerSourceAdapter, len(providerPaths))
	for _, item := range providers {
		importPath := providerPaths[item.SymbolID]
		if importPath == "" {
			continue
		}
		result[item.SymbolID] = providerSourceAdapter{
			alias:    importAliases[importPath],
			function: generatedProviderFunction(item),
		}
	}
	return result, nil
}

func buildApplicationSourceAdapter(
	applicationTarget application.Target,
	target Target,
	aliases map[string]string,
) (applicationSourceAdapter, error) {
	unitPath, _, _, err := applicationSourceUnitLocation(
		applicationTarget,
		target,
	)
	if err != nil {
		return applicationSourceAdapter{}, err
	}
	importPath := path.Join(target.ModulePath, path.Dir(unitPath))
	return applicationSourceAdapter{
		alias: ensureSourceUnitImportAlias(
			aliases,
			importPath,
			"spiceentrypoint",
		),
		identifier: generatedApplicationTargetName(applicationTarget),
	}, nil
}

func renderSourceUnits(
	program *load.Program,
	applicationTarget application.Target,
	providers []provider.Provider,
	configTypes []configuration.Type,
	target Target,
) ([]File, error) {
	configByProvider := configurationProviderIndex(configTypes)
	units := make(map[string]providerSourceUnit)
	applicationPath, applicationPackage, applicationOrigin, err := applicationSourceUnitLocation(applicationTarget, target)
	if err != nil {
		return nil, err
	}
	units[applicationPath] = providerSourceUnit{
		path:        applicationPath,
		packageName: applicationPackage,
		origins:     []SourceOrigin{applicationOrigin},
		application: &sourceUnitApplication{
			target:   applicationTarget,
			targetID: target.ID,
		},
	}
	for _, item := range providers {
		if !sourceUnitProvider(item) {
			continue
		}
		shardPath, packageName, origin, err := sourceUnitLocation(
			item,
			target,
		)
		if err != nil {
			return nil, err
		}
		unit, found := units[shardPath]
		if !found {
			unit = providerSourceUnit{
				path:        shardPath,
				packageName: packageName,
			}
			units[shardPath] = unit
		} else if unit.packageName != packageName {
			return nil, fmt.Errorf(
				"generated provider source unit %s combines packages %s and %s",
				shardPath,
				unit.packageName,
				packageName,
			)
		}
		unit.providers = append(unit.providers, item)
		if !slices.Contains(unit.origins, origin) {
			unit.origins = append(unit.origins, origin)
		}
		units[shardPath] = unit
	}
	paths := make([]string, 0, len(units))
	for shardPath := range units {
		paths = append(paths, shardPath)
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, shardPath := range paths {
		unit := units[shardPath]
		sort.SliceStable(unit.providers, func(i, j int) bool {
			return unit.providers[i].SymbolID <
				unit.providers[j].SymbolID
		})
		sort.SliceStable(unit.origins, func(i, j int) bool {
			if unit.origins[i].Path != unit.origins[j].Path {
				return unit.origins[i].Path < unit.origins[j].Path
			}
			if unit.origins[i].Line != unit.origins[j].Line {
				return unit.origins[i].Line < unit.origins[j].Line
			}
			return unit.origins[i].Symbol < unit.origins[j].Symbol
		})
		content, mappings, err := renderProviderSourceUnit(
			program,
			unit,
			configByProvider,
		)
		if err != nil {
			return nil, err
		}
		files = append(files, File{
			Path:          unit.path,
			Mode:          0o644,
			SHA256:        contentHash(content),
			Role:          FileRoleSourceUnit,
			PrimarySource: firstSourceOrigin(unit.origins),
			Mappings:      mappings,
			Sources:       append([]SourceOrigin(nil), unit.origins...),
			content:       content,
		})
	}
	return files, nil
}

func sourceUnitProvider(item provider.Provider) bool {
	switch item.Source {
	case provider.SourceBean,
		provider.SourceStarter,
		provider.SourceStereotype,
		provider.SourceConfiguration:
		return true
	case provider.SourceEvent:
		return len(item.Interfaces) != 0
	}
	return len(item.Interfaces) != 0
}

func sourceUnitLocation(
	item provider.Provider,
	target Target,
) (string, string, SourceOrigin, error) {
	sourceFile := item.PhysicalPosition.Filename
	if sourceFile == "" {
		sourceFile = item.Position.Filename
	}
	return sourceUnitLocationAt(
		sourceFile,
		item.PackagePath,
		item.SymbolID,
		item.PhysicalPosition,
		item.Position,
		target,
	)
}

func applicationSourceUnitLocation(
	item application.Target,
	target Target,
) (string, string, SourceOrigin, error) {
	sourceFile := item.PhysicalPosition.Filename
	if sourceFile == "" {
		sourceFile = item.Position.Filename
	}
	return sourceUnitLocationAt(
		sourceFile,
		item.PackagePath,
		item.SymbolID,
		item.PhysicalPosition,
		item.Position,
		target,
	)
}

func sourceUnitLocationAt(
	sourceFile string,
	packagePath string,
	symbolID string,
	physicalPosition token.Position,
	position token.Position,
	target Target,
) (string, string, SourceOrigin, error) {
	if sourceFile == "" {
		return "", "", SourceOrigin{}, fmt.Errorf(
			"contribution %s has no physical source file",
			symbolID,
		)
	}
	relative, err := filepath.Rel(target.ModuleRoot, sourceFile)
	moduleRelative := err == nil && filepath.IsLocal(relative)
	packageName := "spicegen"
	var sourcePath string
	var shardDir string
	if moduleRelative {
		sourcePath = filepath.ToSlash(relative)
		sourceDirectory := path.Dir(sourcePath)
		if sourceDirectory == "." {
			sourceDirectory = "_root"
		}
		shardDir = path.Join(
			target.OutputDir,
			"sources",
			sourceDirectory,
		)
	} else {
		digest := sha256.Sum256([]byte(packagePath))
		shardDir = path.Join(
			target.OutputDir,
			"sources",
			"_external",
			hex.EncodeToString(digest[:6]),
		)
		sourcePath = packagePath + "/" + filepath.Base(sourceFile)
	}
	base := strings.TrimSuffix(
		filepath.Base(sourceFile),
		filepath.Ext(sourceFile),
	)
	if base == "" {
		return "", "", SourceOrigin{}, fmt.Errorf(
			"contribution %s source file has no base name",
			symbolID,
		)
	}
	origin := SourceOrigin{
		Path:   sourcePath,
		Line:   physicalPosition.Line,
		Column: physicalPosition.Column,
		Symbol: symbolID,
	}
	if origin.Line == 0 {
		origin.Line = position.Line
	}
	if origin.Column == 0 {
		origin.Column = position.Column
	}
	return path.Join(
		shardDir,
		base+"_spice_gen.go",
	), packageName, origin, nil
}

func renderProviderSourceUnit(
	program *load.Program,
	unit providerSourceUnit,
	configByProvider map[string]configuration.Type,
) ([]byte, []SourceMapping, error) {
	aliases, lifecycleAlias, fmtAlias, configAlias := sourceUnitImportAliases(
		program,
		unit,
		configByProvider,
	)
	var source bytes.Buffer
	source.WriteString("//go:build !" + AnalysisBuildTag + "\n\n")
	source.WriteString("// Code generated by Spice. DO NOT EDIT.\n")
	for _, origin := range unit.origins {
		fmt.Fprintf(
			&source,
			"// Source: %s:%d\n",
			origin.Path,
			origin.Line,
		)
	}
	source.WriteString("\n")
	fmt.Fprintf(&source, "package %s\n\n", unit.packageName)
	writeImports(&source, aliases)
	if unit.application != nil {
		writeSourceUnitApplication(&source, *unit.application)
	}
	if err := writeProviderSourceUnitDeclarations(
		&source,
		unit,
		configByProvider,
		aliases,
		lifecycleAlias,
		fmtAlias,
		configAlias,
	); err != nil {
		return nil, nil, err
	}
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf(
			"format provider source unit %s: %w",
			unit.path,
			err,
		)
	}
	mappings, err := providerSourceUnitMappings(unit, formatted)
	if err != nil {
		return nil, nil, err
	}
	return formatted, mappings, nil
}

func sourceUnitImportAliases(
	program *load.Program,
	unit providerSourceUnit,
	configByProvider map[string]configuration.Type,
) (map[string]string, string, string, string) {
	aliases := aliasesForTypes(
		sourceUnitTypes(unit, configByProvider),
		unit.packageName,
	)
	lifecycleAlias := ""
	if slices.ContainsFunc(
		unit.providers,
		sourceUnitConstructsProvider,
	) {
		lifecycleAlias = ensureSourceUnitImportAlias(
			aliases,
			lifecyclePath,
			"spicelifecycle",
		)
	}
	fmtAlias := ""
	if slices.ContainsFunc(
		unit.providers,
		func(item provider.Provider) bool {
			return sourceUnitNeedsFormatting(item, configByProvider)
		},
	) {
		fmtAlias = ensureSourceUnitImportAlias(aliases, "fmt", "fmt")
	}
	configAlias := ""
	if slices.ContainsFunc(
		unit.providers,
		func(item provider.Provider) bool {
			return item.Source == provider.SourceConfiguration
		},
	) {
		configAlias = ensureSourceUnitImportAlias(
			aliases,
			configPath,
			"spiceconfig",
		)
	}
	addSourceUnitConstructorAliases(program, unit.providers, aliases)
	return aliases, lifecycleAlias, fmtAlias, configAlias
}

func sourceUnitTypes(
	unit providerSourceUnit,
	configByProvider map[string]configuration.Type,
) []types.Type {
	var values []types.Type
	for _, item := range unit.providers {
		values = append(values, item.Output)
		for _, dependency := range item.Dependencies {
			values = append(values, dependency.Type)
		}
		for _, binding := range item.Interfaces {
			values = append(values, binding.Type)
		}
		if configType, found := configByProvider[item.SymbolID]; found {
			for _, field := range configType.Fields() {
				values = append(values, field.Type)
			}
		}
	}
	return values
}

func sourceUnitNeedsFormatting(
	item provider.Provider,
	configByProvider map[string]configuration.Type,
) bool {
	if sourceUnitConstructsProvider(item) && item.ReturnsError {
		return true
	}
	configType, found := configByProvider[item.SymbolID]
	return item.Source == provider.SourceConfiguration &&
		found && len(configType.Fields()) != 0
}

func addSourceUnitConstructorAliases(
	program *load.Program,
	providers []provider.Provider,
	aliases map[string]string,
) {
	for _, item := range providers {
		constructor := providerConstructor(item)
		if constructor.PackagePath == "" {
			continue
		}
		packageName := path.Base(constructor.PackagePath)
		if pkg, found := packageByPath(
			program.Packages(),
			constructor.PackagePath,
		); found {
			packageName = pkg.Name
		}
		ensureSourceUnitImportAlias(
			aliases,
			constructor.PackagePath,
			packageName,
		)
	}
}

func writeProviderSourceUnitDeclarations(
	source *bytes.Buffer,
	unit providerSourceUnit,
	configByProvider map[string]configuration.Type,
	aliases map[string]string,
	lifecycleAlias string,
	fmtAlias string,
	configAlias string,
) error {
	for _, item := range unit.providers {
		switch {
		case sourceUnitConstructsProvider(item):
			writeSourceUnitProvider(
				source,
				item,
				aliases,
				lifecycleAlias,
				fmtAlias,
			)
		case item.Source == provider.SourceConfiguration:
			configType, found := configByProvider[item.SymbolID]
			if !found {
				return fmt.Errorf(
					"configuration provider %s has no typed configuration metadata",
					item.SymbolID,
				)
			}
			writeSourceUnitConfigurationBinder(
				source,
				configType,
				aliases,
				configAlias,
				fmtAlias,
			)
		}
		writeSourceUnitInterfaceAssertions(source, item, aliases)
	}
	return nil
}

func writeSourceUnitApplication(
	source *bytes.Buffer,
	item sourceUnitApplication,
) {
	name := generatedApplicationTargetName(item.target)
	fmt.Fprintf(
		source,
		"// %s identifies the generated target selected by %s.\n",
		name,
		item.target.SymbolID,
	)
	fmt.Fprintf(
		source,
		"const %s = %s\n\n",
		name,
		strconv.Quote(item.targetID),
	)
}

func generatedApplicationTargetName(item application.Target) string {
	digest := sha256.Sum256([]byte(item.SymbolID))
	name := exportedGeneratedIdentifier(item.Name, "Application")
	return "ApplicationTarget" + name + "_" + hex.EncodeToString(digest[:4])
}

func writeSourceUnitInterfaceAssertions(
	source *bytes.Buffer,
	item provider.Provider,
	aliases map[string]string,
) {
	for _, binding := range item.Interfaces {
		fmt.Fprintf(
			source,
			"// %s verifies the explicit @Implements binding for %s.\n",
			generatedAssertionName(item, binding),
			item.SymbolID,
		)
		fmt.Fprintf(
			source,
			"var %s %s = *new(%s)\n\n",
			generatedAssertionName(item, binding),
			renderedTypeInPackage(binding.Type, aliases),
			renderedTypeInPackage(item.Output, aliases),
		)
	}
}

func providerSourceUnitMappings(
	unit providerSourceUnit,
	formatted []byte,
) ([]SourceMapping, error) {
	mappings := make([]SourceMapping, 0)
	if unit.application != nil {
		origin, found := sourceOriginForSymbol(
			unit.origins,
			unit.application.target.SymbolID,
		)
		if !found {
			return nil, fmt.Errorf(
				"application source unit %s has no source origin",
				unit.application.target.SymbolID,
			)
		}
		name := generatedApplicationTargetName(unit.application.target)
		line, column, found := generatedIdentifierPosition(formatted, name)
		if !found {
			return nil, fmt.Errorf(
				"application target %s has no generated position",
				name,
			)
		}
		mappings = append(mappings, SourceMapping{
			Kind:         "application-target",
			Contribution: unit.application.target.SymbolID,
			Source:       origin,
			Generated:    generatedIdentifierRange(line, column, name),
		})
	}
	for _, item := range unit.providers {
		origin, found := sourceOriginForSymbol(unit.origins, item.SymbolID)
		if !found {
			return nil, fmt.Errorf(
				"provider source unit %s has no source origin",
				item.SymbolID,
			)
		}
		if sourceUnitProvidesProvider(item) {
			mapping, err := providerSourceUnitMapping(
				item,
				origin,
				formatted,
			)
			if err != nil {
				return nil, err
			}
			mappings = append(mappings, mapping)
		}
		for _, binding := range item.Interfaces {
			mapping, err := interfaceSourceUnitMapping(
				item,
				binding,
				origin,
				formatted,
			)
			if err != nil {
				return nil, err
			}
			mappings = append(mappings, mapping)
		}
	}
	return mappings, nil
}

func providerSourceUnitMapping(
	item provider.Provider,
	origin SourceOrigin,
	formatted []byte,
) (SourceMapping, error) {
	name := generatedProviderFunction(item)
	line, column, found := generatedIdentifierPosition(formatted, name)
	if !found {
		return SourceMapping{}, fmt.Errorf(
			"provider constructor adapter %s has no generated position",
			name,
		)
	}
	return SourceMapping{
		Kind:         sourceUnitProviderMappingKind(item),
		Contribution: item.SymbolID,
		Source:       origin,
		Generated:    generatedIdentifierRange(line, column, name),
	}, nil
}

func interfaceSourceUnitMapping(
	item provider.Provider,
	binding provider.InterfaceBinding,
	origin SourceOrigin,
	formatted []byte,
) (SourceMapping, error) {
	name := generatedAssertionName(item, binding)
	line, column, found := generatedIdentifierPosition(formatted, name)
	if !found {
		return SourceMapping{}, fmt.Errorf(
			"interface assertion %s has no generated position",
			name,
		)
	}
	return SourceMapping{
		Kind:         "interface-assertion",
		Contribution: item.SymbolID + "#implements:" + binding.TypeID,
		Source:       origin,
		Generated:    generatedIdentifierRange(line, column, name),
	}, nil
}

func generatedIdentifierRange(
	line int,
	column int,
	name string,
) GeneratedRange {
	return GeneratedRange{
		StartLine:   line,
		StartColumn: column,
		EndLine:     line,
		EndColumn:   column + len(name),
	}
}

func sourceUnitConstructsProvider(item provider.Provider) bool {
	switch item.Source {
	case provider.SourceBean,
		provider.SourceStarter,
		provider.SourceStereotype:
		return true
	case provider.SourceConfiguration, provider.SourceEvent:
		return false
	}
	return false
}

func sourceUnitProvidesProvider(item provider.Provider) bool {
	return sourceUnitConstructsProvider(item) ||
		item.Source == provider.SourceConfiguration
}

func sourceUnitProviderMappingKind(item provider.Provider) string {
	if item.Source == provider.SourceConfiguration {
		return "configuration-binding"
	}
	return "provider-construction"
}

func ensureSourceUnitImportAlias(
	aliases map[string]string,
	importPath string,
	preferred string,
) string {
	if alias := aliases[importPath]; alias != "" {
		return alias
	}
	used := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		used[alias] = struct{}{}
	}
	alias := preferred
	for suffix := 2; ; suffix++ {
		if _, exists := used[alias]; !exists {
			break
		}
		alias = preferred + strconv.Itoa(suffix)
	}
	aliases[importPath] = alias
	return alias
}

func providerConstructor(item provider.Provider) load.Symbol {
	constructor := item.Constructor
	if constructor.PackagePath != "" && constructor.Name != "" {
		return constructor
	}
	return load.Symbol{
		PackagePath: item.PackagePath,
		Name:        item.Symbol.Name,
	}
}

func generatedProviderFunction(item provider.Provider) string {
	digest := sha256.Sum256([]byte(item.SymbolID))
	prefix := "Construct"
	if item.Source == provider.SourceConfiguration {
		prefix = "Bind"
	}
	name := exportedGeneratedIdentifier(
		semanticProviderName(item),
		"Provider",
	)
	return prefix + name + "_" + hex.EncodeToString(digest[:4])
}

func writeSourceUnitProvider(
	source *bytes.Buffer,
	item provider.Provider,
	aliases map[string]string,
	lifecycleAlias string,
	fmtAlias string,
) {
	functionName := generatedProviderFunction(item)
	fmt.Fprintf(
		source,
		"// %s performs the direct construction selected for bean %q.\n",
		functionName,
		item.Name,
	)
	fmt.Fprintf(source, "// Spice source identity: %s.\n", item.SymbolID)
	fmt.Fprintf(source, "func %s(", functionName)
	for index, dependency := range item.Dependencies {
		if index != 0 {
			source.WriteString(", ")
		}
		fmt.Fprintf(
			source,
			"dependency%d %s",
			index,
			renderedTypeInPackage(
				dependency.Type,
				aliases,
			),
		)
	}
	fmt.Fprintf(
		source,
		") (%s, %s.Cleanup, error) {\n",
		renderedTypeInPackage(item.Output, aliases),
		lifecycleAlias,
	)
	if item.Construction == provider.ConstructionAllocate {
		fmt.Fprintf(
			source,
			"\treturn new(%s.%s), nil, nil\n",
			aliases[item.PackagePath],
			item.Symbol.Name,
		)
		source.WriteString("}\n\n")
		return
	}
	constructor := providerConstructor(item)
	arguments := make([]string, len(item.Dependencies))
	for index := range arguments {
		arguments[index] = "dependency" + strconv.Itoa(index)
	}
	call := aliases[constructor.PackagePath] + "." +
		constructor.Name + "(" + strings.Join(arguments, ", ") + ")"
	switch {
	case item.ReturnsCleanup && item.ReturnsError:
		fmt.Fprintf(source, "\tvalue, cleanup, err := %s\n", call)
	case item.ReturnsCleanup:
		fmt.Fprintf(source, "\tvalue, cleanup := %s\n", call)
	case item.ReturnsError:
		fmt.Fprintf(source, "\tvalue, err := %s\n", call)
	default:
		fmt.Fprintf(source, "\tvalue := %s\n", call)
	}
	if item.ReturnsError {
		source.WriteString("\tif err != nil {\n")
		fmt.Fprintf(
			source,
			"\t\tvar zero %s\n",
			renderedTypeInPackage(item.Output, aliases),
		)
		fmt.Fprintf(
			source,
			"\t\treturn zero, nil, %s.Errorf(%s, err)\n",
			fmtAlias,
			strconv.Quote(
				"construct bean "+item.Name+
					" ("+item.OutputTypeID+
					", source "+item.SymbolID+"): %w",
			),
		)
		source.WriteString("\t}\n")
	}
	cleanup := "nil"
	if item.ReturnsCleanup {
		cleanup = "cleanup"
	}
	fmt.Fprintf(source, "\treturn value, %s, nil\n", cleanup)
	source.WriteString("}\n\n")
}

func generatedAssertionName(
	item provider.Provider,
	binding provider.InterfaceBinding,
) string {
	digest := sha256.Sum256(
		[]byte(item.SymbolID + "\x00" + binding.TypeID),
	)
	concrete := exportedGeneratedIdentifier(
		semanticProviderName(item),
		"Provider",
	)
	contract := exportedGeneratedIdentifier(
		generatedTypeName(binding.Type),
		"Interface",
	)
	return "spiceImplements" + concrete + "As" + contract +
		"_" + hex.EncodeToString(digest[:4])
}

func aliasesForTypes(
	values []types.Type,
	localPackageName string,
) map[string]string {
	names := make(map[string]string)
	aliases := make(map[string]string)
	for _, value := range values {
		addTypeImportName(names, aliases, value)
	}
	paths := make([]string, 0, len(names))
	for importPath := range names {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	used := map[string]struct{}{
		"spicegen":       {},
		localPackageName: {},
	}
	for _, importPath := range paths {
		base := names[importPath]
		alias := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[alias]; !exists {
				break
			}
			alias = base + strconv.Itoa(suffix)
		}
		used[alias] = struct{}{}
		aliases[importPath] = alias
	}
	return aliases
}

func renderedTypeInPackage(
	value types.Type,
	aliases map[string]string,
) string {
	return types.TypeString(value, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		if alias, ok := aliases[pkg.Path()]; ok {
			return alias
		}
		return pkg.Name()
	})
}

func applicationSourceOrigins(
	program *load.Program,
	target application.Target,
) []SourceOrigin {
	if program == nil || target.PhysicalPosition.Filename == "" {
		return nil
	}
	pkg, found := packageByPath(program.Packages(), target.PackagePath)
	if !found || pkg.Raw == nil || pkg.Raw.Module == nil {
		return nil
	}
	relative, err := filepath.Rel(
		pkg.Raw.Module.Dir,
		target.PhysicalPosition.Filename,
	)
	if err != nil || !filepath.IsLocal(relative) {
		return nil
	}
	origin := SourceOrigin{
		Path:   filepath.ToSlash(relative),
		Line:   target.PhysicalPosition.Line,
		Column: target.PhysicalPosition.Column,
		Symbol: target.SymbolID,
	}
	if origin.Line == 0 {
		origin.Line = target.Position.Line
	}
	if origin.Column == 0 {
		origin.Column = target.Position.Column
	}
	return []SourceOrigin{origin}
}

func modelSourceOrigins(
	program *load.Program,
	model application.Model,
	applicationTarget application.Target,
	target Target,
) []SourceOrigin {
	collector := sourceOriginCollector{
		target:  target,
		origins: applicationSourceOrigins(program, applicationTarget),
	}
	collector.addProviders(model.Providers())
	collector.addConfigurations(model.Configurations())
	collector.addControllers(model.Controllers())
	collector.addLifecycle(model.Components())
	collector.addSchedules(model.Jobs())
	collector.addAsync(model.AsyncTasks())
	collector.addEvents(model.Events())
	collector.addBoundaries(model.Transactions(), model.Caches())
	collector.addModules(model.Modules())
	sortSourceOrigins(collector.origins)
	return slices.Compact(collector.origins)
}

type sourceOriginCollector struct {
	target  Target
	origins []SourceOrigin
}

func (collector *sourceOriginCollector) add(
	position token.Position,
	display token.Position,
	symbolID string,
	packagePath string,
) {
	origin, ok := sourceOriginAt(
		position,
		display,
		symbolID,
		packagePath,
		collector.target,
	)
	if ok {
		collector.origins = append(collector.origins, origin)
	}
}

func (collector *sourceOriginCollector) addProviders(
	providers []provider.Provider,
) {
	for _, item := range providers {
		collector.add(
			item.PhysicalPosition,
			item.Position,
			item.SymbolID,
			item.PackagePath,
		)
	}
}

func (collector *sourceOriginCollector) addConfigurations(
	configTypes []configuration.Type,
) {
	for _, item := range configTypes {
		collector.add(
			item.PhysicalPosition,
			item.Position,
			item.SymbolID,
			item.PackagePath,
		)
		for _, field := range item.Fields() {
			collector.add(
				field.PhysicalPosition,
				field.Position,
				item.SymbolID+"."+field.Name,
				item.PackagePath,
			)
		}
	}
}

func (collector *sourceOriginCollector) addControllers(
	controllers []controller.Controller,
) {
	for _, item := range controllers {
		collector.add(
			item.PhysicalPosition,
			item.Position,
			item.SymbolID,
			item.PackagePath,
		)
		for _, route := range item.Routes() {
			collector.add(
				route.PhysicalPosition,
				route.Position,
				route.SymbolID,
				route.Symbol.PackagePath,
			)
			for _, binding := range route.Bindings() {
				collector.add(
					binding.PhysicalPosition,
					binding.Position,
					route.SymbolID+"."+binding.Field,
					route.Symbol.PackagePath,
				)
			}
			if authorization, ok := route.Authorization(); ok {
				collector.add(
					authorization.PhysicalPosition,
					authorization.Position,
					route.SymbolID+"#authorization",
					route.Symbol.PackagePath,
				)
			}
		}
	}
}

func (collector *sourceOriginCollector) addLifecycle(
	components []compilerlifecycle.Component,
) {
	for _, component := range components {
		for _, hook := range []*compilerlifecycle.Hook{
			component.Start,
			component.Stop,
		} {
			if hook == nil {
				continue
			}
			collector.add(
				hook.PhysicalPosition,
				hook.Position,
				hook.MethodID,
				hook.Method.PackagePath,
			)
		}
	}
}

func (collector *sourceOriginCollector) addSchedules(
	jobs []compilerschedule.Job,
) {
	for _, job := range jobs {
		collector.add(
			job.PhysicalPosition,
			job.Position,
			job.MethodID,
			job.Method.PackagePath,
		)
	}
}

func (collector *sourceOriginCollector) addAsync(
	tasks []compilerasync.Task,
) {
	for _, task := range tasks {
		collector.add(
			task.PhysicalPosition,
			task.Position,
			task.MethodID,
			task.Method.PackagePath,
		)
	}
}

func (collector *sourceOriginCollector) addEvents(
	topics []compilerevent.Topic,
) {
	for _, topic := range topics {
		collector.add(
			topic.PhysicalPosition,
			topic.Position,
			topic.MarkerID,
			topic.Marker.PackagePath,
		)
		for _, listener := range topic.Listeners() {
			collector.add(
				listener.PhysicalPosition,
				listener.Position,
				listener.MethodID,
				listener.Method.PackagePath,
			)
		}
	}
}

func (collector *sourceOriginCollector) addBoundaries(
	transactions []compilertransaction.Boundary,
	caches []compilercache.Boundary,
) {
	for _, boundary := range transactions {
		collector.add(
			boundary.PhysicalPosition,
			boundary.Position,
			boundary.RouteID+"#transaction",
			"",
		)
	}
	for _, boundary := range caches {
		collector.add(
			boundary.PhysicalPosition,
			boundary.Position,
			boundary.RouteID+"#cache",
			"",
		)
	}
}

func (collector *sourceOriginCollector) addModules(
	modules []modulith.Module,
) {
	for _, module := range modules {
		collector.add(
			module.PhysicalPosition,
			module.Position,
			module.ID+"#module",
			module.RootPackage,
		)
	}
}

func sourceOriginAt(
	physical token.Position,
	display token.Position,
	symbolID string,
	packagePath string,
	target Target,
) (SourceOrigin, bool) {
	sourceFile := physical.Filename
	if sourceFile == "" {
		sourceFile = display.Filename
	}
	if sourceFile == "" {
		return SourceOrigin{}, false
	}
	relative, err := filepath.Rel(target.ModuleRoot, sourceFile)
	var sourcePath string
	switch {
	case err == nil && filepath.IsLocal(relative):
		sourcePath = filepath.ToSlash(relative)
	case packagePath != "":
		sourcePath = packagePath + "/" + filepath.Base(sourceFile)
	default:
		return SourceOrigin{}, false
	}
	line := physical.Line
	if line == 0 {
		line = display.Line
	}
	column := physical.Column
	if column == 0 {
		column = display.Column
	}
	return SourceOrigin{
		Path:   sourcePath,
		Line:   line,
		Column: column,
		Symbol: symbolID,
	}, true
}

func sortSourceOrigins(origins []SourceOrigin) {
	sort.SliceStable(origins, func(left, right int) bool {
		if origins[left].Path != origins[right].Path {
			return origins[left].Path < origins[right].Path
		}
		if origins[left].Line != origins[right].Line {
			return origins[left].Line < origins[right].Line
		}
		if origins[left].Column != origins[right].Column {
			return origins[left].Column < origins[right].Column
		}
		return origins[left].Symbol < origins[right].Symbol
	})
}

func firstSourceOrigin(origins []SourceOrigin) *SourceOrigin {
	if len(origins) == 0 {
		return nil
	}
	result := origins[0]
	return &result
}

func sourceOriginForSymbol(
	origins []SourceOrigin,
	symbolID string,
) (SourceOrigin, bool) {
	for _, origin := range origins {
		if origin.Symbol == symbolID {
			return origin, true
		}
	}
	return SourceOrigin{}, false
}

func generatedIdentifierPosition(
	content []byte,
	identifier string,
) (int, int, bool) {
	offset := bytes.Index(content, []byte(identifier))
	if offset < 0 {
		return 0, 0, false
	}
	line := bytes.Count(content[:offset], []byte{'\n'}) + 1
	lineStart := bytes.LastIndexByte(content[:offset], '\n') + 1
	return line, offset - lineStart + 1, true
}

type generatedComponentField struct {
	providerID string
	beanName   string
	fieldName  string
	output     types.Type
}

func generatedComponentFields(
	providers []provider.Provider,
) []generatedComponentField {
	type candidate struct {
		provider provider.Provider
		index    int
		base     string
	}
	var candidates []candidate
	baseCounts := make(map[string]int)
	for index, item := range providers {
		if item.Scope != sdk.BeanScopeSingleton ||
			!publicComponentType(item.Output) {
			continue
		}
		base := componentFieldBase(item, index)
		baseCounts[base]++
		candidates = append(candidates, candidate{
			provider: item,
			index:    index,
			base:     base,
		})
	}

	used := make(map[string]int, len(candidates))
	fields := make([]generatedComponentField, 0, len(candidates))
	for _, candidate := range candidates {
		base := candidate.base
		if baseCounts[base] > 1 {
			prefix := exportedComponentFieldName(
				path.Base(candidate.provider.PackagePath),
				candidate.index,
			)
			base = prefix + base
		}
		used[base]++
		fieldName := base
		if used[base] > 1 {
			fieldName += strconv.Itoa(used[base])
		}
		fields = append(fields, generatedComponentField{
			providerID: candidate.provider.SymbolID,
			beanName:   candidate.provider.Name,
			fieldName:  fieldName,
			output:     candidate.provider.Output,
		})
	}
	return fields
}

func componentFieldBase(item provider.Provider, index int) string {
	base := exportedComponentFieldName(semanticProviderName(item), index)
	if item.ExplicitName ||
		!strings.HasPrefix(base, "New") ||
		len(base) == len("New") {
		return base
	}
	trimmed := base[len("New"):]
	if token.IsIdentifier(trimmed) {
		return trimmed
	}
	return base
}

func exportedComponentFieldName(name string, index int) string {
	if name == "" {
		return "Provider" + strconv.Itoa(index)
	}
	first := name[0]
	if first >= 'a' && first <= 'z' {
		first -= 'a' - 'A'
		name = string(first) + name[1:]
	}
	if !token.IsIdentifier(name) {
		return "Provider" + strconv.Itoa(index)
	}
	return name
}

func publicComponentType(value types.Type) bool {
	switch typed := value.(type) {
	case *types.Basic:
		return true
	case *types.Named:
		return publicNamedComponentType(typed.Obj(), typed.TypeArgs())
	case *types.Alias:
		return publicNamedComponentType(typed.Obj(), typed.TypeArgs())
	case *types.Pointer:
		return publicComponentType(typed.Elem())
	case *types.Slice:
		return publicComponentType(typed.Elem())
	case *types.Array:
		return publicComponentType(typed.Elem())
	case *types.Map:
		return publicComponentType(typed.Key()) &&
			publicComponentType(typed.Elem())
	case *types.Chan:
		return publicComponentType(typed.Elem())
	default:
		return false
	}
}

func publicNamedComponentType(
	object *types.TypeName,
	arguments *types.TypeList,
) bool {
	if object == nil ||
		object.Pkg() != nil && !object.Exported() {
		return false
	}
	return publicTypeArguments(arguments)
}

func publicTypeArguments(arguments *types.TypeList) bool {
	if arguments == nil {
		return true
	}
	for argument := range arguments.Types() {
		if !publicComponentType(argument) {
			return false
		}
	}
	return true
}

func writeComponentsType(
	source *bytes.Buffer,
	fields []generatedComponentField,
	aliases map[string]string,
) {
	source.WriteString("// Components is a typed snapshot of constructed singleton beans.\n")
	source.WriteString("// It performs no reflection or string-based lookup.\n")
	source.WriteString("type Components struct {\n")
	for _, field := range fields {
		fmt.Fprintf(
			source,
			"\t// %s is bean %q.\n",
			field.fieldName,
			field.beanName,
		)
		fmt.Fprintf(
			source,
			"\t%s %s\n",
			field.fieldName,
			renderedType(field.output, aliases),
		)
	}
	source.WriteString("}\n\n")
}

func writeComponentAssignments(
	source *bytes.Buffer,
	fields []generatedComponentField,
	providerVariables map[string]string,
) {
	if len(fields) == 0 {
		return
	}
	source.WriteString("\tapplication.components = Components{\n")
	for _, field := range fields {
		fmt.Fprintf(
			source,
			"\t\t%s: %s,\n",
			field.fieldName,
			providerVariables[field.providerID],
		)
	}
	source.WriteString("\t}\n")
}

func writeComponentsMethod(source *bytes.Buffer) {
	source.WriteString("// Components returns a typed snapshot of constructed singleton beans.\n")
	source.WriteString("func (application *Application) Components() Components {\n")
	source.WriteString("\tif application == nil {\n")
	source.WriteString("\t\treturn Components{}\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn application.components\n")
	source.WriteString("}\n\n")
}

func writeProviders(
	source *bytes.Buffer,
	providers []provider.Provider,
	configTypes []configuration.Type,
	aliases map[string]string,
	dependencies map[string][]string,
	providerModules map[string]string,
	providerVariables map[string]string,
	events []compilerevent.Topic,
	adapters map[string]providerSourceAdapter,
) error {
	configByProvider := configurationProviderIndex(configTypes)
	eventByProvider := eventProviderIndex(events)
	for _, item := range providers {
		variable := providerVariables[item.SymbolID]
		if item.Scope != sdk.BeanScopeSingleton {
			adapter, found := adapters[item.SymbolID]
			if !found {
				return fmt.Errorf(
					"scoped provider %s has no generated source adapter",
					item.SymbolID,
				)
			}
			writeScopedProviderAdapter(
				source,
				item,
				variable,
				aliases,
				dependencies[item.SymbolID],
				adapter,
			)
			continue
		}
		switch item.Source {
		case provider.SourceBean, provider.SourceStarter:
			adapter, found := adapters[item.SymbolID]
			if !found {
				return fmt.Errorf(
					"provider %s has no generated source adapter",
					item.SymbolID,
				)
			}
			writeProviderAdapterCall(
				source,
				item,
				variable,
				dependencies[item.SymbolID],
				providerModules[item.SymbolID],
				adapter,
			)
		case provider.SourceStereotype:
			adapter, found := adapters[item.SymbolID]
			if !found {
				return fmt.Errorf(
					"provider %s has no generated source adapter",
					item.SymbolID,
				)
			}
			writeProviderAdapterCall(
				source,
				item,
				variable,
				dependencies[item.SymbolID],
				providerModules[item.SymbolID],
				adapter,
			)
		case provider.SourceConfiguration:
			configType, ok := configByProvider[item.SymbolID]
			if !ok {
				return fmt.Errorf("configuration provider %s has no typed configuration metadata", item.SymbolID)
			}
			adapter, found := adapters[item.SymbolID]
			if !found {
				return fmt.Errorf(
					"configuration provider %s has no generated source adapter",
					item.SymbolID,
				)
			}
			writeConfigurationAdapterCall(
				source,
				item,
				configType,
				variable,
				adapter,
			)
		case provider.SourceEvent:
			topic, ok := eventByProvider[item.SymbolID]
			if !ok {
				return fmt.Errorf(
					"event provider %s has no typed event metadata",
					item.SymbolID,
				)
			}
			if err := writeEventProvider(
				source,
				topic,
				variable,
				aliases,
				providerVariables,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf("provider %s has unsupported source %q", item.SymbolID, item.Source)
		}
	}
	return nil
}

func writeScopedProviderAdapter(
	source *bytes.Buffer,
	item provider.Provider,
	variable string,
	aliases map[string]string,
	dependencies []string,
	adapter providerSourceAdapter,
) {
	factory := variable + "Factory"
	outputType := renderedType(item.Output, aliases)
	fmt.Fprintf(
		source,
		"\t%s := func(_ context.Context) (%s, spicelifecycle.Cleanup, error) {\n",
		factory,
		outputType,
	)
	fmt.Fprintf(
		source,
		"\t\treturn %s.%s(%s)\n",
		adapter.alias,
		adapter.function,
		strings.Join(dependencies, ", "),
	)
	source.WriteString("\t}\n")
	switch item.Scope {
	case sdk.BeanScopeSingleton:
		return
	case sdk.BeanScopePrototype:
		fmt.Fprintf(
			source,
			"\t%s := spicebean.NewProvider(%s)\n",
			variable,
			factory,
		)
	case sdk.BeanScopeRequest, sdk.BeanScopeSession:
		scopeKind := "spicebean.ScopeRequest"
		if item.Scope == sdk.BeanScopeSession {
			scopeKind = "spicebean.ScopeSession"
		}
		fmt.Fprintf(
			source,
			"\t%sScope := spicebean.NewScoped[%s](%s, %s)\n",
			variable,
			outputType,
			scopeKind,
			factory,
		)
		fmt.Fprintf(
			source,
			"\t%s := %sScope.Provider()\n",
			variable,
			variable,
		)
	}
	fmt.Fprintf(source, "\t_ = %s\n", variable)
}

func writeProviderAdapterCall(
	source *bytes.Buffer,
	item provider.Provider,
	variable string,
	dependencies []string,
	moduleID string,
	adapter providerSourceAdapter,
) {
	cleanup := variable + "Cleanup"
	fmt.Fprintf(
		source,
		"\t%s, %s, err := %s.%s(%s)\n",
		variable,
		cleanup,
		adapter.alias,
		adapter.function,
		strings.Join(dependencies, ", "),
	)
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
		strconv.Quote(
			"construct bean "+item.Name+
				" ("+item.OutputTypeID+
				", source "+item.SymbolID+"): %w",
		),
	)
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\tif %s != nil {\n", cleanup)
	fmt.Fprintf(
		source,
		"\t\tif err := application.coordinator.RegisterModuleCleanup(%s, %s, %s); err != nil {\n",
		strconv.Quote(moduleID),
		strconv.Quote(item.SymbolID),
		cleanup,
	)
	fmt.Fprintf(
		source,
		"\t\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
		strconv.Quote(
			"register cleanup for bean "+item.Name+
				" (source "+item.SymbolID+"): %w",
		),
	)
	source.WriteString("\t\t}\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\t_ = %s\n", variable)
}

func writeConfigurationAdapterCall(
	source *bytes.Buffer,
	item provider.Provider,
	configType configuration.Type,
	variable string,
	adapter providerSourceAdapter,
) {
	fmt.Fprintf(
		source,
		"\t%s, err := %s.%s(configurationSnapshot)\n",
		variable,
		adapter.alias,
		adapter.function,
	)
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
		strconv.Quote(
			"bind configuration "+configType.TypeID+
				" for bean "+item.Name+
				" (source "+item.SymbolID+"): %w",
		),
	)
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\t_ = %s\n", variable)
}

func eventProviderIndex(events []compilerevent.Topic) map[string]compilerevent.Topic {
	result := make(map[string]compilerevent.Topic, len(events))
	for _, topic := range events {
		result[topic.ProviderID] = topic
	}
	return result
}

func writeEventProvider(
	source *bytes.Buffer,
	topic compilerevent.Topic,
	variable string,
	aliases map[string]string,
	providerVariables map[string]string,
) error {
	payload := renderedType(topic.Payload, aliases)
	topicVariable := variable + "Topic"
	fmt.Fprintf(source, "\t%s, err := spiceevent.NewTopic(\n", topicVariable)
	source.WriteString("\t\tspiceevent.Definition{\n")
	fmt.Fprintf(source, "\t\t\tID: %s,\n", strconv.Quote(topic.MarkerID))
	fmt.Fprintf(source, "\t\t\tModule: %s,\n", strconv.Quote(topic.Module))
	source.WriteString("\t\t},\n")
	fmt.Fprintf(source, "\t\t[]spiceevent.Subscriber[%s]{\n", payload)
	for _, listener := range topic.Listeners() {
		receiver := providerVariables[listener.ProviderID]
		if receiver == "" {
			return fmt.Errorf(
				"event listener %s references unknown provider %s",
				listener.MethodID,
				listener.ProviderID,
			)
		}
		source.WriteString("\t\t\t{\n")
		fmt.Fprintf(source, "\t\t\t\tID: %s,\n", strconv.Quote(listener.MethodID))
		fmt.Fprintf(source, "\t\t\t\tModule: %s,\n", strconv.Quote(listener.Module))
		if listener.Order != 0 {
			fmt.Fprintf(source, "\t\t\t\tOrder: %d,\n", listener.Order)
		}
		fmt.Fprintf(
			source,
			"\t\t\t\tHandle: %s.%s,\n",
			receiver,
			listener.Method.Name,
		)
		source.WriteString("\t\t\t},\n")
	}
	source.WriteString("\t\t},\n")
	source.WriteString("\t\toptions.EventObservers...,\n")
	source.WriteString("\t)\n")
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
		strconv.Quote(
			"construct event topic "+topic.MarkerID+
				" ("+topic.PublisherTypeID+"): %w",
		),
	)
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tvar %s spiceevent.Publisher[%s] = %s\n",
		variable,
		payload,
		topicVariable,
	)
	fmt.Fprintf(source, "\t_ = %s\n", variable)
	return nil
}

type cacheRuntime struct {
	variable string
	ttl      string
}

func writeCacheSetup(
	source *bytes.Buffer,
	caches []compilercache.Boundary,
	aliases map[string]string,
) map[string]cacheRuntime {
	result := make(map[string]cacheRuntime, len(caches))
	for index, boundary := range caches {
		variable := "generatedCache" + strconv.Itoa(index)
		capacity := variable + "Capacity"
		ttl := variable + "TTL"
		fmt.Fprintf(
			source,
			"\t%s, err := configurationSnapshot.Integer(%s)\n",
			capacity,
			strconv.Quote(cacheCapacityKey(boundary.CacheName)),
		)
		source.WriteString("\tif err != nil {\n")
		fmt.Fprintf(
			source,
			"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
			strconv.Quote(
				"decode capacity for cache "+boundary.CacheName+": %w",
			),
		)
		source.WriteString("\t}\n")
		fmt.Fprintf(
			source,
			"\tif %s < 1 || uint64(%s) > uint64(^uint(0)>>1) {\n",
			capacity,
			capacity,
		)
		fmt.Fprintf(
			source,
			"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s))\n",
			strconv.Quote(
				"decode capacity for cache "+boundary.CacheName+
					": value must fit a positive int",
			),
		)
		source.WriteString("\t}\n")
		fmt.Fprintf(
			source,
			"\t%s, err := configurationSnapshot.Duration(%s)\n",
			ttl,
			strconv.Quote(cacheTTLKey(boundary.CacheName)),
		)
		source.WriteString("\tif err != nil {\n")
		fmt.Fprintf(
			source,
			"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
			strconv.Quote(
				"decode TTL for cache "+boundary.CacheName+": %w",
			),
		)
		source.WriteString("\t}\n")
		fmt.Fprintf(source, "\tif %s < 0 {\n", ttl)
		fmt.Fprintf(
			source,
			"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s))\n",
			strconv.Quote(
				"decode TTL for cache "+boundary.CacheName+
					": duration must not be negative",
			),
		)
		source.WriteString("\t}\n")
		fmt.Fprintf(
			source,
			"\t%s, err := spicecache.NewMemory[%s, %s](\n",
			variable,
			renderedType(boundary.Key, aliases),
			renderedType(boundary.Value, aliases),
		)
		source.WriteString("\t\tspicecache.Definition{\n")
		fmt.Fprintf(
			source,
			"\t\t\tID: %s,\n",
			strconv.Quote(boundary.CacheName),
		)
		fmt.Fprintf(
			source,
			"\t\t\tModule: %s,\n",
			strconv.Quote(boundary.Module),
		)
		source.WriteString("\t\t},\n")
		fmt.Fprintf(source, "\t\tint(%s),\n", capacity)
		source.WriteString("\t\toptions.CacheClock,\n")
		source.WriteString("\t\toptions.CacheObservers...,\n")
		source.WriteString("\t)\n")
		source.WriteString("\tif err != nil {\n")
		fmt.Fprintf(
			source,
			"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
			strconv.Quote(
				"construct generated cache "+boundary.CacheName+": %w",
			),
		)
		source.WriteString("\t}\n")
		result[boundary.RouteID] = cacheRuntime{
			variable: variable,
			ttl:      ttl,
		}
	}
	return result
}

func writeControllerRoute(
	source *bytes.Buffer,
	route controller.Route,
	transactionIndex map[string]compilertransaction.Boundary,
	caches map[string]cacheRuntime,
	providerVariables map[string]string,
	receiver string,
	middleware string,
	aliases map[string]string,
	routeIndex int,
) error {
	pattern := route.HTTPMethod + " " + route.Path
	observation := writeRouteObservation(source, route, pattern, routeIndex)
	if route.Raw {
		if _, transactional := transactionIndex[route.SymbolID]; transactional {
			return fmt.Errorf(
				"raw route %s cannot own a transaction boundary",
				route.SymbolID,
			)
		}
		fmt.Fprintf(
			source,
			"\tif routeErr := spiceweb.RegisterObserved(routeMux, %s, http.HandlerFunc(%s.%s), %s, %s...); routeErr != nil {\n",
			strconv.Quote(pattern),
			receiver,
			route.Name,
			observation,
			middleware,
		)
		writeRouteRegistrationError(source, pattern)
		return nil
	}
	boundary, transactional := transactionIndex[route.SymbolID]
	if route.ExecutorParameter != transactional {
		return fmt.Errorf(
			"typed route %s transaction metadata does not match its explicit executor parameter",
			route.SymbolID,
		)
	}
	if transactional &&
		providerVariables[boundary.ManagerProviderID] == "" {
		return fmt.Errorf(
			"transaction boundary %s has no manager provider variable",
			route.SymbolID,
		)
	}
	if route.View &&
		providerVariables[route.ViewRendererID] == "" {
		return fmt.Errorf(
			"view route %s has no renderer provider variable",
			route.SymbolID,
		)
	}
	if route.BindingResult {
		if _, cacheable := caches[route.SymbolID]; cacheable {
			return fmt.Errorf(
				"form route %s cannot be cacheable",
				route.SymbolID,
			)
		}
	}
	writeTypedRoute(
		source,
		route,
		transactionIndex,
		caches,
		providerVariables,
		receiver,
		pattern,
		observation,
		middleware,
		aliases,
	)
	return nil
}

func writeCacheableRouteCall(
	source *bytes.Buffer,
	route controller.Route,
	cache cacheRuntime,
	receiver string,
) {
	fmt.Fprintf(
		source,
		"\t\tresponseValue, cacheHit, routeErr := %s.Get(httpRequest.Context(), requestValue)\n",
		cache.variable,
	)
	source.WriteString("\t\tif routeErr == nil && !cacheHit {\n")
	fmt.Fprintf(
		source,
		"\t\t\tresponseValue, routeErr = %s.%s(httpRequest.Context(), requestValue)\n",
		receiver,
		route.Name,
	)
	source.WriteString("\t\t\tif routeErr == nil {\n")
	fmt.Fprintf(
		source,
		"\t\t\t\trouteErr = %s.Put(httpRequest.Context(), requestValue, responseValue, %s)\n",
		cache.variable,
		cache.ttl,
	)
	source.WriteString("\t\t\t}\n")
	source.WriteString("\t\t}\n")
}

func hasAuthorization(controllers []controller.Controller) bool {
	for _, item := range controllers {
		for _, route := range item.Routes() {
			if _, protected := route.Authorization(); protected {
				return true
			}
		}
	}
	return false
}

func writeRouteAuthorization(
	source *bytes.Buffer,
	authorization controller.Authorization,
	pattern string,
	index int,
) string {
	policy := "authorizationPolicy" + strconv.Itoa(index)
	policyErr := policy + "Err"
	fmt.Fprintf(
		source,
		"\t%s, %s := spicesecurity.NewPolicy(spicesecurity.PolicySpec{\n",
		policy,
		policyErr,
	)
	source.WriteString("\t\tDefinition: spicesecurity.Definition{\n")
	fmt.Fprintf(
		source,
		"\t\t\tID: %s,\n\t\t\tModule: %s,\n",
		strconv.Quote(authorization.PolicyID),
		strconv.Quote(authorization.Module),
	)
	source.WriteString("\t\t},\n")
	if authorization.Authenticated {
		source.WriteString("\t\tAuthenticated: true,\n")
	}
	writeAuthorizationNames(
		source,
		"AnyRoles",
		authorization.AnyRoles(),
	)
	writeAuthorizationNames(
		source,
		"AllRoles",
		authorization.AllRoles(),
	)
	writeAuthorizationNames(
		source,
		"AllScopes",
		authorization.AllScopes(),
	)
	source.WriteString("\t})\n")
	fmt.Fprintf(source, "\tif %s != nil {\n", policyErr)
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, %s))\n",
		strconv.Quote(
			"construct generated authorization policy for route "+
				pattern+": %w",
		),
		policyErr,
	)
	source.WriteString("\t}\n")
	guard := "authorizationGuard" + strconv.Itoa(index)
	guardErr := guard + "Err"
	fmt.Fprintf(
		source,
		"\t%s, %s := spicesecurity.Guard(authorizer, %s, options.AuthorizationWriteFailure)\n",
		guard,
		guardErr,
		policy,
	)
	fmt.Fprintf(source, "\tif %s != nil {\n", guardErr)
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, %s))\n",
		strconv.Quote(
			"construct generated authorization guard for route "+
				pattern+": %w",
		),
		guardErr,
	)
	source.WriteString("\t}\n")
	middleware := "routeMiddleware" + strconv.Itoa(index)
	fmt.Fprintf(
		source,
		"\t%s := append(append([]spiceweb.Middleware(nil), options.Middleware...), %s)\n",
		middleware,
		guard,
	)
	return middleware
}

func writeAuthorizationNames(
	source *bytes.Buffer,
	field string,
	values []string,
) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(source, "\t\t%s: []string{", field)
	for index, value := range values {
		if index != 0 {
			source.WriteString(", ")
		}
		source.WriteString(strconv.Quote(value))
	}
	source.WriteString("},\n")
}

func writeRouteObservation(
	source *bytes.Buffer,
	route controller.Route,
	pattern string,
	index int,
) string {
	observation := "routeObservation" + strconv.Itoa(index)
	observationErr := "routeObservationErr" + strconv.Itoa(index)
	fmt.Fprintf(
		source,
		"\t%s, %s := spiceweb.ObservationMiddleware(spiceweb.RouteMetadata{ID: %s, Module: %s, Method: %s, Pattern: %s}, httpObservers...)\n",
		observation,
		observationErr,
		strconv.Quote(route.SymbolID),
		strconv.Quote(route.Module),
		strconv.Quote(route.HTTPMethod),
		strconv.Quote(route.Path),
	)
	fmt.Fprintf(source, "\tif %s != nil {\n", observationErr)
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, %s))\n",
		strconv.Quote("configure generated route "+pattern+" observation: %w"),
		observationErr,
	)
	source.WriteString("\t}\n")
	return observation
}

func writeTypedRoute(
	source *bytes.Buffer,
	route controller.Route,
	transactions map[string]compilertransaction.Boundary,
	caches map[string]cacheRuntime,
	providerVariables map[string]string,
	receiver string,
	pattern string,
	observation string,
	middleware string,
	aliases map[string]string,
) {
	fmt.Fprintf(
		source,
		"\tif routeErr := spiceweb.RegisterObserved(routeMux, %s, http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {\n",
		strconv.Quote(pattern),
	)
	if route.View {
		fmt.Fprintf(
			source,
			"\t\twriteRouteError := func(routeError error) error { return %s.WriteError(writer, httpRequest, routeError, options.ErrorMapper) }\n",
			providerVariables[route.ViewRendererID],
		)
	} else {
		source.WriteString(
			"\t\twriteRouteError := func(routeError error) error { return spiceweb.WriteError(writer, httpRequest, routeError, options.ErrorMapper) }\n",
		)
	}
	writeRouteNegotiation(source, route)
	fmt.Fprintf(source, "\t\trequestValue := %s{}\n", renderedType(route.Request, aliases))
	if route.BindingResult {
		writeFormSetup(source, route)
	}
	for _, binding := range route.Bindings() {
		if binding.Location == controller.Form {
			writeFormBinding(source, binding, aliases)
			continue
		}
		writeRequestBinding(source, binding, aliases)
	}
	if route.ValidatorID != "" {
		if route.BindingResult {
			source.WriteString("\t\tif bindingResult.Valid() {\n")
			source.WriteString("\t\t\tif validationErr := spiceweb.Validate(httpRequest.Context(), requestValue.Validate); validationErr != nil {\n")
			writeBindingRejection(source, "validationErr", 4)
			source.WriteString("\t\t\t}\n")
			source.WriteString("\t\t}\n")
		} else {
			source.WriteString("\t\tif validationErr := spiceweb.Validate(httpRequest.Context(), requestValue.Validate); validationErr != nil {\n")
			writeGeneratedError(source, "validationErr", 3)
			source.WriteString("\t\t\treturn\n")
			source.WriteString("\t\t}\n")
		}
	}
	boundary, transactional := transactions[route.SymbolID]
	cache, cacheable := caches[route.SymbolID]
	switch {
	case transactional:
		writeTransactionalRouteCall(
			source,
			route,
			boundary,
			providerVariables[boundary.ManagerProviderID],
			receiver,
			aliases,
		)
	case cacheable:
		writeCacheableRouteCall(
			source,
			route,
			cache,
			receiver,
		)
	case route.NoContent:
		fmt.Fprintf(
			source,
			"\t\t_, routeErr := %s.%s(httpRequest.Context(), requestValue)\n",
			receiver,
			route.Name,
		)
	case route.BindingResult:
		fmt.Fprintf(
			source,
			"\t\tresponseValue, routeErr := %s.%s(httpRequest.Context(), requestValue, bindingResult)\n",
			receiver,
			route.Name,
		)
	default:
		fmt.Fprintf(
			source,
			"\t\tresponseValue, routeErr := %s.%s(httpRequest.Context(), requestValue)\n",
			receiver,
			route.Name,
		)
	}
	source.WriteString("\t\tif routeErr != nil {\n")
	writeGeneratedError(source, "routeErr", 3)
	source.WriteString("\t\t\treturn\n")
	source.WriteString("\t\t}\n")
	switch {
	case route.NoContent:
		source.WriteString("\t\t_ = spiceweb.WriteNoContent(writer)\n")
	case route.View:
		fmt.Fprintf(
			source,
			"\t\t_ = %s.Respond(httpRequest.Context(), writer, responseValue)\n",
			providerVariables[route.ViewRendererID],
		)
	default:
		source.WriteString("\t\t_ = spiceweb.WriteJSON(writer, http.StatusOK, responseValue)\n")
	}
	fmt.Fprintf(
		source,
		"\t}), %s, %s...); routeErr != nil {\n",
		observation,
		middleware,
	)
	writeRouteRegistrationError(source, pattern)
}

func writeRouteNegotiation(
	source *bytes.Buffer,
	route controller.Route,
) {
	if route.View {
		source.WriteString("\t\tif !spiceview.AcceptsHTML(httpRequest.Header.Get(\"Accept\")) {\n")
		source.WriteString("\t\t\tproblem := spiceweb.Problem{\n")
		source.WriteString("\t\t\t\tType: \"about:blank\",\n")
		source.WriteString("\t\t\t\tTitle: \"Not Acceptable\",\n")
		source.WriteString("\t\t\t\tStatus: http.StatusNotAcceptable,\n")
		source.WriteString("\t\t\t\tDetail: \"the endpoint produces text/html\",\n")
		source.WriteString("\t\t\t}\n")
		source.WriteString("\t\t\t_ = spiceweb.WriteProblem(writer, problem)\n")
		source.WriteString("\t\t\treturn\n")
		source.WriteString("\t\t}\n")
	} else if !route.NoContent {
		source.WriteString("\t\tif !spiceweb.AcceptsJSON(httpRequest.Header.Get(\"Accept\")) {\n")
		source.WriteString("\t\t\tproblem := spiceweb.Problem{\n")
		source.WriteString("\t\t\t\tType: \"about:blank\",\n")
		source.WriteString("\t\t\t\tTitle: \"Not Acceptable\",\n")
		source.WriteString("\t\t\t\tStatus: http.StatusNotAcceptable,\n")
		source.WriteString("\t\t\t\tDetail: \"the endpoint produces application/json\",\n")
		source.WriteString("\t\t\t}\n")
		source.WriteString("\t\t\t_ = spiceweb.WriteProblem(writer, problem)\n")
		source.WriteString("\t\t\treturn\n")
		source.WriteString("\t\t}\n")
	}
}

func writeTransactionalRouteCall(
	source *bytes.Buffer,
	route controller.Route,
	boundary compilertransaction.Boundary,
	manager string,
	receiver string,
	aliases map[string]string,
) {
	if !route.NoContent {
		fmt.Fprintf(
			source,
			"\t\tvar responseValue %s\n",
			renderedType(route.Response, aliases),
		)
	}
	fmt.Fprintf(
		source,
		"\t\trouteErr := %s.Within(httpRequest.Context(), spicedata.Definition{\n",
		manager,
	)
	fmt.Fprintf(source, "\t\t\tID: %s,\n", strconv.Quote(boundary.RouteID))
	fmt.Fprintf(source, "\t\t\tModule: %s,\n", strconv.Quote(boundary.Module))
	fmt.Fprintf(
		source,
		"\t\t\tIsolation: %s,\n",
		isolationLevelName(boundary.Isolation),
	)
	if boundary.ReadOnly {
		source.WriteString("\t\t\tReadOnly: true,\n")
	}
	source.WriteString("\t\t}, func(transactionContext context.Context, executor spicedata.Executor) error {\n")
	switch {
	case route.NoContent:
		fmt.Fprintf(
			source,
			"\t\t\t_, transactionErr := %s.%s(transactionContext, executor, requestValue)\n",
			receiver,
			route.Name,
		)
	case route.BindingResult:
		fmt.Fprintf(
			source,
			"\t\t\tvar transactionErr error\n\t\t\tresponseValue, transactionErr = %s.%s(transactionContext, executor, requestValue, bindingResult)\n",
			receiver,
			route.Name,
		)
	default:
		fmt.Fprintf(
			source,
			"\t\t\tvar transactionErr error\n\t\t\tresponseValue, transactionErr = %s.%s(transactionContext, executor, requestValue)\n",
			receiver,
			route.Name,
		)
	}
	source.WriteString("\t\t\treturn transactionErr\n")
	source.WriteString("\t\t})\n")
}

func writeFormSetup(source *bytes.Buffer, route controller.Route) {
	source.WriteString("\t\tbindingResult := spiceweb.BindingResult{}\n")
	source.WriteString("\t\tformValues, formErr := spiceweb.DecodeForm(httpRequest, options.MaxRequestBodyBytes)\n")
	source.WriteString("\t\tif formErr != nil {\n")
	writeBindingRejection(source, "formErr", 3)
	source.WriteString("\t\t} else if unknownFormErr := spiceweb.RejectUnknownForm(formValues, []string{")
	first := true
	for _, binding := range route.Bindings() {
		if binding.Location != controller.Form {
			continue
		}
		if !first {
			source.WriteString(", ")
		}
		first = false
		source.WriteString(strconv.Quote(binding.Name))
	}
	source.WriteString("}); unknownFormErr != nil {\n")
	writeBindingRejection(source, "unknownFormErr", 3)
	source.WriteString("\t\t}\n")
}

func writeFormBinding(
	source *bytes.Buffer,
	binding controller.Binding,
	aliases map[string]string,
) {
	index := strconv.Itoa(binding.Index)
	fmt.Fprintf(
		source,
		"\t\traw%s, present%s, bindErr%s := spiceweb.FormValue(formValues, %s, %t)\n",
		index,
		index,
		index,
		strconv.Quote(binding.Name),
		binding.Required,
	)
	fmt.Fprintf(source, "\t\tif bindErr%s != nil {\n", index)
	writeBindingRejection(source, "bindErr"+index, 3)
	source.WriteString("\t\t} else if present" + index + " {\n")
	writeFormScalarAssignment(source, binding, index, aliases)
	source.WriteString("\t\t}\n")
}

func writeFormScalarAssignment(
	source *bytes.Buffer,
	binding controller.Binding,
	index string,
	aliases map[string]string,
) {
	typeName := renderedType(binding.Type, aliases)
	if binding.Kind == controller.ScalarString {
		fmt.Fprintf(
			source,
			"\t\t\trequestValue.%s = %s(raw%s)\n",
			binding.Field,
			typeName,
			index,
		)
		return
	}
	accessor := "Boolean"
	extra := ""
	if binding.Kind == controller.ScalarInteger {
		accessor = "Integer"
		extra = ", " + strconv.Itoa(integerBitSize(binding.Type))
	}
	if binding.Kind == controller.ScalarDuration {
		accessor = "Duration"
	}
	fmt.Fprintf(
		source,
		"\t\t\tparsed%s, parseErr%s := spiceweb.%s(spiceweb.LocationForm, %s, raw%s%s)\n",
		index,
		index,
		accessor,
		strconv.Quote(binding.Name),
		index,
		extra,
	)
	fmt.Fprintf(source, "\t\t\tif parseErr%s != nil {\n", index)
	writeBindingRejection(source, "parseErr"+index, 4)
	source.WriteString("\t\t\t} else {\n")
	fmt.Fprintf(
		source,
		"\t\t\t\trequestValue.%s = %s(parsed%s)\n",
		binding.Field,
		typeName,
		index,
	)
	source.WriteString("\t\t\t}\n")
}

func writeBindingRejection(
	source *bytes.Buffer,
	variable string,
	tabs int,
) {
	indent := strings.Repeat("\t", tabs)
	fmt.Fprintf(
		source,
		"%supdatedBindingResult, rejectErr := bindingResult.RejectBinding(%s)\n",
		indent,
		variable,
	)
	fmt.Fprintf(source, "%sif rejectErr != nil {\n", indent)
	writeGeneratedError(source, "rejectErr", tabs+1)
	fmt.Fprintf(source, "%s\treturn\n", indent)
	fmt.Fprintf(source, "%s}\n", indent)
	fmt.Fprintf(source, "%sbindingResult = updatedBindingResult\n", indent)
}

func isolationLevelName(level sql.IsolationLevel) string {
	switch level {
	case sql.LevelDefault:
		return "sql.LevelDefault"
	case sql.LevelReadUncommitted:
		return "sql.LevelReadUncommitted"
	case sql.LevelReadCommitted:
		return "sql.LevelReadCommitted"
	case sql.LevelWriteCommitted:
		return "sql.LevelWriteCommitted"
	case sql.LevelRepeatableRead:
		return "sql.LevelRepeatableRead"
	case sql.LevelSnapshot:
		return "sql.LevelSnapshot"
	case sql.LevelSerializable:
		return "sql.LevelSerializable"
	case sql.LevelLinearizable:
		return "sql.LevelLinearizable"
	default:
		return strconv.Itoa(int(level))
	}
}

func writeRouteRegistrationError(source *bytes.Buffer, pattern string) {
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, routeErr))\n",
		strconv.Quote("register generated route "+pattern+": %w"),
	)
	source.WriteString("\t}\n")
}

func writeRequestBinding(source *bytes.Buffer, binding controller.Binding, aliases map[string]string) {
	if binding.Location == controller.Body {
		fmt.Fprintf(
			source,
			"\t\tif bindErr := spiceweb.DecodeJSON(httpRequest, &requestValue.%s, options.MaxRequestBodyBytes); bindErr != nil {\n",
			binding.Field,
		)
		writeGeneratedError(source, "bindErr", 3)
		source.WriteString("\t\t\treturn\n")
		source.WriteString("\t\t}\n")
		return
	}
	index := strconv.Itoa(binding.Index)
	values := bindingValues(binding)
	fmt.Fprintf(
		source,
		"\t\traw%s, present%s, bindErr%s := spiceweb.Parameter(%s, %s, %s, %t)\n",
		index,
		index,
		index,
		bindingLocation(binding.Location),
		strconv.Quote(binding.Name),
		values,
		binding.Required,
	)
	fmt.Fprintf(source, "\t\tif bindErr%s != nil {\n", index)
	writeGeneratedError(source, "bindErr"+index, 3)
	source.WriteString("\t\t\treturn\n")
	source.WriteString("\t\t}\n")
	fmt.Fprintf(source, "\t\tif present%s {\n", index)
	writeScalarAssignment(source, binding, index, aliases)
	source.WriteString("\t\t}\n")
}

func writeScalarAssignment(
	source *bytes.Buffer,
	binding controller.Binding,
	index string,
	aliases map[string]string,
) {
	typeName := renderedType(binding.Type, aliases)
	if binding.Kind == controller.ScalarString {
		fmt.Fprintf(source, "\t\t\trequestValue.%s = %s(raw%s)\n", binding.Field, typeName, index)
		return
	}
	accessor := "Boolean"
	extra := ""
	if binding.Kind == controller.ScalarInteger {
		accessor = "Integer"
		extra = ", " + strconv.Itoa(integerBitSize(binding.Type))
	}
	if binding.Kind == controller.ScalarDuration {
		accessor = "Duration"
	}
	fmt.Fprintf(
		source,
		"\t\t\tparsed%s, parseErr%s := spiceweb.%s(%s, %s, raw%s%s)\n",
		index,
		index,
		accessor,
		bindingLocation(binding.Location),
		strconv.Quote(binding.Name),
		index,
		extra,
	)
	fmt.Fprintf(source, "\t\t\tif parseErr%s != nil {\n", index)
	writeGeneratedError(source, "parseErr"+index, 4)
	source.WriteString("\t\t\t\treturn\n")
	source.WriteString("\t\t\t}\n")
	fmt.Fprintf(source, "\t\t\trequestValue.%s = %s(parsed%s)\n", binding.Field, typeName, index)
}

func writeGeneratedError(source *bytes.Buffer, variable string, tabs int) {
	indent := strings.Repeat("\t", tabs)
	fmt.Fprintf(
		source,
		"%s_ = writeRouteError(%s)\n",
		indent,
		variable,
	)
}

func bindingValues(binding controller.Binding) string {
	switch binding.Location {
	case controller.Path:
		return "[]string{httpRequest.PathValue(" + strconv.Quote(binding.Name) + ")}"
	case controller.Query:
		return "httpRequest.URL.Query()[" + strconv.Quote(binding.Name) + "]"
	case controller.Header:
		return "httpRequest.Header.Values(" + strconv.Quote(binding.Name) + ")"
	case controller.Body:
		return "nil"
	case controller.Form:
		return "formValues[" + strconv.Quote(binding.Name) + "]"
	}
	return "nil"
}

func bindingLocation(location controller.Location) string {
	switch location {
	case controller.Path:
		return "spiceweb.LocationPath"
	case controller.Query:
		return "spiceweb.LocationQuery"
	case controller.Header:
		return "spiceweb.LocationHeader"
	case controller.Body:
		return "spiceweb.LocationBody"
	case controller.Form:
		return "spiceweb.LocationForm"
	}
	return "spiceweb.LocationQuery"
}

func integerBitSize(value types.Type) int {
	basic, ok := types.Unalias(value).Underlying().(*types.Basic)
	if !ok {
		return 0
	}
	if basic.Kind() == types.Int8 {
		return 8
	}
	if basic.Kind() == types.Int16 {
		return 16
	}
	if basic.Kind() == types.Int32 {
		return 32
	}
	if basic.Kind() == types.Int64 {
		return 64
	}
	return 0
}

func pointerNamedType(value types.Type, packagePath, name string) bool {
	pointer, ok := types.Unalias(value).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == packagePath && named.Obj().Name() == name
}

func writeApplicationOptions(source *bytes.Buffer, features commandFeatures) {
	source.WriteString("type ApplicationOptions struct {\n")
	source.WriteString("\tProfiles []string\n")
	source.WriteString("\tSources []spiceconfig.Source\n")
	source.WriteString("\tAllowUnknownConfiguration bool\n")
	if features.hasMux {
		source.WriteString("\tErrorMapper spiceweb.ErrorMapper\n")
		source.WriteString("\tMaxRequestBodyBytes int64\n")
		source.WriteString("\tHTTPObservers []spiceweb.HTTPObserver\n")
		source.WriteString("\tMiddleware []spiceweb.Middleware\n")
		if features.requestScope {
			source.WriteString("\tScopeErrorHandler spicebean.ScopeErrorHandler\n")
		}
	}
	if features.authorization {
		source.WriteString("\tAuthorizationObservers []spicesecurity.Observer\n")
		source.WriteString("\tAuthorizationWriteFailure spicesecurity.WriteFailure\n")
	}
	if features.logging {
		source.WriteString("\tLogger *slog.Logger\n")
	}
	if features.scheduling {
		source.WriteString("\tScheduleContext context.Context\n")
		source.WriteString("\tScheduleWaiter spiceschedule.Waiter\n")
		source.WriteString("\tScheduleObservers []spiceschedule.Observer\n")
	}
	if features.asynchronous {
		source.WriteString("\tAsyncContext context.Context\n")
		source.WriteString("\tAsyncObservers []spiceasync.Observer\n")
	}
	if features.events {
		source.WriteString("\tEventObservers []spiceevent.Observer\n")
	}
	if features.caching {
		source.WriteString("\tCacheClock func() time.Time\n")
		source.WriteString("\tCacheObservers []spicecache.Observer\n")
	}
	source.WriteString("\tObservers []spicelifecycle.Observer\n")
	source.WriteString("}\n\n")
}

func writeRequestScopeSetup(
	source *bytes.Buffer,
	features commandFeatures,
) {
	if !features.hasMux || !features.requestScope {
		return
	}
	source.WriteString("\trequestScopedHandler, err := spicebean.RequestScopeMiddleware(application.handler, options.ScopeErrorHandler)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"configure request bean scope: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tapplication.handler = requestScopedHandler\n")
}

func hasProviderScope(
	providers []provider.Provider,
	scope sdk.BeanScope,
) bool {
	for _, item := range providers {
		if item.Scope == scope {
			return true
		}
	}
	return false
}

func writeConfigurationAPI(
	source *bytes.Buffer,
	configTypes []configuration.Type,
	caches []compilercache.Boundary,
	asynchronous bool,
) {
	source.WriteString("func ConfigurationSchema() (spiceconfig.Schema, error) {\n")
	source.WriteString("\treturn spiceconfig.NewSchema(\n")
	for _, configType := range configTypes {
		for _, field := range configType.Fields() {
			source.WriteString("\t\tspiceconfig.Property{\n")
			fmt.Fprintf(source, "\t\t\tKey: %s,\n", strconv.Quote(field.Key))
			fmt.Fprintf(source, "\t\t\tKind: %s,\n", configurationKindName(field.Kind))
			if field.Module != "" {
				fmt.Fprintf(source, "\t\t\tModule: %s,\n", strconv.Quote(field.Module))
			}
			if field.Environment != "" {
				fmt.Fprintf(source, "\t\t\tEnvironment: %s,\n", strconv.Quote(field.Environment))
			}
			if field.HasDefault {
				fmt.Fprintf(source, "\t\t\tDefault: %s,\n", strconv.Quote(field.Default))
				source.WriteString("\t\t\tHasDefault: true,\n")
			}
			if field.Required {
				source.WriteString("\t\t\tRequired: true,\n")
			}
			if field.Secret {
				source.WriteString("\t\t\tSecret: true,\n")
			}
			source.WriteString("\t\t},\n")
		}
	}
	for _, boundary := range caches {
		writeCacheConfigurationProperties(source, boundary)
	}
	if asynchronous {
		source.WriteString("\t\tspiceconfig.Property{\n")
		fmt.Fprintf(
			source,
			"\t\t\tKey: %s,\n",
			strconv.Quote(asyncConcurrencyKey),
		)
		source.WriteString("\t\t\tKind: spiceconfig.KindInteger,\n")
		source.WriteString("\t\t\tDescription: \"Maximum concurrent generated asynchronous tasks\",\n")
		source.WriteString("\t\t\tEnvironment: \"SPICE_ASYNC_MAX_CONCURRENCY\",\n")
		source.WriteString("\t\t\tDefault: \"16\",\n")
		source.WriteString("\t\t\tHasDefault: true,\n")
		source.WriteString("\t\t},\n")
	}
	source.WriteString("\t\tspiceconfig.Property{\n")
	fmt.Fprintf(source, "\t\t\tKey: %s,\n", strconv.Quote(shutdownConfigurationKey))
	source.WriteString("\t\t\tKind: spiceconfig.KindDuration,\n")
	source.WriteString("\t\t\tEnvironment: \"SPICE_SHUTDOWN_TIMEOUT\",\n")
	source.WriteString("\t\t\tDefault: \"10s\",\n")
	source.WriteString("\t\t\tHasDefault: true,\n")
	source.WriteString("\t\t},\n")
	source.WriteString("\t)\n")
	source.WriteString("}\n\n")
}

func writeCacheConfigurationProperties(
	source *bytes.Buffer,
	boundary compilercache.Boundary,
) {
	source.WriteString("\t\tspiceconfig.Property{\n")
	fmt.Fprintf(
		source,
		"\t\t\tKey: %s,\n",
		strconv.Quote(cacheCapacityKey(boundary.CacheName)),
	)
	source.WriteString("\t\t\tKind: spiceconfig.KindInteger,\n")
	fmt.Fprintf(
		source,
		"\t\t\tDescription: %s,\n",
		strconv.Quote("Maximum entries for cache "+boundary.CacheName),
	)
	fmt.Fprintf(
		source,
		"\t\t\tModule: %s,\n",
		strconv.Quote(boundary.Module),
	)
	fmt.Fprintf(
		source,
		"\t\t\tEnvironment: %s,\n",
		strconv.Quote(cacheEnvironment(boundary.CacheName, "CAPACITY")),
	)
	source.WriteString("\t\t\tDefault: \"256\",\n")
	source.WriteString("\t\t\tHasDefault: true,\n")
	source.WriteString("\t\t},\n")
	source.WriteString("\t\tspiceconfig.Property{\n")
	fmt.Fprintf(
		source,
		"\t\t\tKey: %s,\n",
		strconv.Quote(cacheTTLKey(boundary.CacheName)),
	)
	source.WriteString("\t\t\tKind: spiceconfig.KindDuration,\n")
	fmt.Fprintf(
		source,
		"\t\t\tDescription: %s,\n",
		strconv.Quote("Entry lifetime for cache "+boundary.CacheName),
	)
	fmt.Fprintf(
		source,
		"\t\t\tModule: %s,\n",
		strconv.Quote(boundary.Module),
	)
	fmt.Fprintf(
		source,
		"\t\t\tEnvironment: %s,\n",
		strconv.Quote(cacheEnvironment(boundary.CacheName, "TTL")),
	)
	source.WriteString("\t\t\tDefault: \"5m\",\n")
	source.WriteString("\t\t\tHasDefault: true,\n")
	source.WriteString("\t\t},\n")
}

func cacheCapacityKey(name string) string {
	return "spice.cache." + name + ".capacity"
}

func cacheTTLKey(name string) string {
	return "spice.cache." + name + ".ttl"
}

func cacheEnvironment(name, suffix string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	return "SPICE_CACHE_" +
		strings.ToUpper(replacer.Replace(name)) +
		"_" + suffix
}

func configurationKindName(kind runtimeconfig.Kind) string {
	switch kind {
	case runtimeconfig.KindString:
		return "spiceconfig.KindString"
	case runtimeconfig.KindBoolean:
		return "spiceconfig.KindBoolean"
	case runtimeconfig.KindInteger:
		return "spiceconfig.KindInteger"
	case runtimeconfig.KindDuration:
		return "spiceconfig.KindDuration"
	default:
		return strconv.Quote(string(kind))
	}
}

func writeConfigurationResolution(source *bytes.Buffer, target Target) {
	source.WriteString("\tconfigurationSchema, err := ConfigurationSchema()\n")
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, fmt.Errorf(%s, err)\n",
		strconv.Quote("construct configuration schema for application "+target.ID+": %w"),
	)
	source.WriteString("\t}\n")
	source.WriteString("\tconfigurationSnapshot, err := spiceconfig.Resolve(\n")
	source.WriteString("\t\tctx,\n")
	source.WriteString("\t\tconfigurationSchema,\n")
	source.WriteString("\t\tspiceconfig.Options{\n")
	source.WriteString("\t\t\tProfiles: options.Profiles,\n")
	source.WriteString("\t\t\tAllowUnknown: options.AllowUnknownConfiguration,\n")
	source.WriteString("\t\t},\n")
	source.WriteString("\t\toptions.Sources...,\n")
	source.WriteString("\t)\n")
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, fmt.Errorf(%s, err)\n",
		strconv.Quote("resolve configuration for application "+target.ID+": %w"),
	)
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tapplication.shutdownTimeout, err = configurationSnapshot.Duration(%s)\n",
		strconv.Quote(shutdownConfigurationKey),
	)
	source.WriteString("\tif err != nil {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s, err))\n",
		strconv.Quote("decode shutdown timeout for application "+target.ID+": %w"),
	)
	source.WriteString("\t}\n")
	source.WriteString("\tif application.shutdownTimeout <= 0 {\n")
	fmt.Fprintf(
		source,
		"\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(%s))\n",
		strconv.Quote("decode shutdown timeout for application "+target.ID+": duration must be positive"),
	)
	source.WriteString("\t}\n")
}

func writeSourceUnitConfigurationBinder(
	source *bytes.Buffer,
	configType configuration.Type,
	aliases map[string]string,
	configAlias string,
	fmtAlias string,
) {
	functionName := generatedConfigurationFunction(configType)
	outputType := renderedTypeInPackage(configType.Type, aliases)
	fmt.Fprintf(
		source,
		"// %s binds the validated configuration declared by %s.\n",
		functionName,
		configType.SymbolID,
	)
	fmt.Fprintf(
		source,
		"func %s(configurationSnapshot %s.Snapshot) (%s, error) {\n",
		functionName,
		configAlias,
		outputType,
	)
	fmt.Fprintf(source, "\tvalue := %s{}\n", outputType)
	for _, field := range configType.Fields() {
		source.WriteString("\tif _, configured := configurationSnapshot.Lookup(")
		source.WriteString(strconv.Quote(field.Key))
		source.WriteString("); configured {\n")
		fmt.Fprintf(
			source,
			"\t\trawValue, valueErr := configurationSnapshot.%s(%s)\n",
			configurationAccessor(field.Kind),
			strconv.Quote(field.Key),
		)
		source.WriteString("\t\tif valueErr != nil {\n")
		fmt.Fprintf(
			source,
			"\t\t\treturn %s{}, %s.Errorf(%s, valueErr)\n",
			outputType,
			fmtAlias,
			strconv.Quote(
				"decode configuration property "+field.Key+" for "+
					configType.TypeID+"."+field.Name+": %w",
			),
		)
		source.WriteString("\t\t}\n")
		fmt.Fprintf(
			source,
			"\t\tconvertedValue := %s(rawValue)\n",
			renderedType(field.Type, aliases),
		)
		if field.Kind == runtimeconfig.KindInteger {
			source.WriteString("\t\tif int64(convertedValue) != rawValue {\n")
			fmt.Fprintf(
				source,
				"\t\t\treturn %s{}, %s.Errorf(%s)\n",
				outputType,
				fmtAlias,
				strconv.Quote(
					"decode configuration property "+field.Key+" for "+
						configType.TypeID+"."+field.Name+": value is outside "+field.TypeID,
				),
			)
			source.WriteString("\t\t}\n")
		}
		fmt.Fprintf(source, "\t\tvalue.%s = convertedValue\n", field.Name)
		source.WriteString("\t}\n")
	}
	source.WriteString("\treturn value, nil\n")
	source.WriteString("}\n\n")
}

func generatedConfigurationFunction(configType configuration.Type) string {
	digest := sha256.Sum256([]byte(configType.SymbolID))
	name := exportedGeneratedIdentifier(configType.Name, "Configuration")
	return "Bind" + name + "_" + hex.EncodeToString(digest[:4])
}

func configurationAccessor(kind runtimeconfig.Kind) string {
	switch kind {
	case runtimeconfig.KindString:
		return "RequiredString"
	case runtimeconfig.KindBoolean:
		return "Boolean"
	case runtimeconfig.KindInteger:
		return "Integer"
	case runtimeconfig.KindDuration:
		return "Duration"
	default:
		return "RequiredString"
	}
}

func configurationProviderIndex(configTypes []configuration.Type) map[string]configuration.Type {
	result := make(map[string]configuration.Type, len(configTypes))
	for _, configType := range configTypes {
		result[configType.SymbolID] = configType
	}
	return result
}

func renderedType(value types.Type, aliases map[string]string) string {
	return types.TypeString(value, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		if alias, ok := aliases[pkg.Path()]; ok {
			return alias
		}
		return pkg.Name()
	})
}

func writeImports(source *bytes.Buffer, aliases map[string]string) {
	if len(aliases) == 0 {
		return
	}
	var standardPaths, applicationPaths []string
	for importPath := range aliases {
		if isStandardImport(importPath) {
			standardPaths = append(standardPaths, importPath)
		} else {
			applicationPaths = append(applicationPaths, importPath)
		}
	}
	sort.Strings(standardPaths)
	sort.Strings(applicationPaths)
	source.WriteString("import (\n")
	writeImportGroup(source, aliases, standardPaths)
	if len(standardPaths) != 0 && len(applicationPaths) != 0 {
		source.WriteByte('\n')
	}
	writeImportGroup(source, aliases, applicationPaths)
	source.WriteString(")\n\n")
}

func isStandardImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

func writeImportGroup(source *bytes.Buffer, aliases map[string]string, paths []string) {
	for _, importPath := range paths {
		fmt.Fprintf(source, "\t%s %s\n", aliases[importPath], strconv.Quote(importPath))
	}
}

func writeAsyncApplicationFields(
	source *bytes.Buffer,
	tasks []compilerasync.Task,
	aliases map[string]string,
) {
	for _, task := range tasks {
		fmt.Fprintf(
			source,
			"\t%s func(%s) error\n",
			asyncFieldName(task),
			strings.Join(asyncParameterTypes(task, aliases), ", "),
		)
	}
}

func writeAsyncSetup(
	source *bytes.Buffer,
	tasks []compilerasync.Task,
	providerVariables map[string]string,
	providerModules map[string]string,
	applicationModule string,
	aliases map[string]string,
) {
	if len(tasks) == 0 {
		return
	}
	source.WriteString("\tasyncConcurrency, err := configurationSnapshot.Integer(")
	source.WriteString(strconv.Quote(asyncConcurrencyKey))
	source.WriteString(")\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode generated async concurrency: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tif asyncConcurrency < 1 || uint64(asyncConcurrency) > uint64(^uint(0)>>1) {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode generated async concurrency: value must fit a positive int\"))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tasyncContext := options.AsyncContext\n")
	source.WriteString("\tif asyncContext == nil {\n")
	source.WriteString("\t\tasyncContext = context.WithoutCancel(ctx)\n")
	source.WriteString("\t}\n")
	source.WriteString("\tgeneratedAsyncExecutor, err := spiceasync.NewExecutor(\n")
	source.WriteString("\t\tasyncContext,\n")
	source.WriteString("\t\tint(asyncConcurrency),\n")
	source.WriteString("\t\toptions.AsyncObservers...,\n")
	source.WriteString("\t)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"construct generated async executor: %w\", err))\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tif err := application.coordinator.RegisterModuleCleanup(%s, \"spice.async\", generatedAsyncExecutor.Shutdown); err != nil {\n",
		strconv.Quote(applicationModule),
	)
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"register generated async cleanup: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tapplication.asyncExecutor = generatedAsyncExecutor\n")
	for _, task := range tasks {
		writeAsyncSubmitClosure(
			source,
			task,
			providerVariables[task.ProviderID],
			asyncTaskModule(task, providerModules),
			aliases,
		)
	}
}

func writeAsyncSubmitClosure(
	source *bytes.Buffer,
	task compilerasync.Task,
	providerVariable string,
	module string,
	aliases map[string]string,
) {
	field := asyncFieldName(task)
	declarations := asyncParameterDeclarations(task, aliases)
	arguments := asyncArgumentNames(task)
	fmt.Fprintf(
		source,
		"\tapplication.%s = func(%s) error {\n",
		field,
		strings.Join(declarations, ", "),
	)
	source.WriteString("\t\treturn generatedAsyncExecutor.Submit(\n")
	source.WriteString("\t\t\tadmission,\n")
	source.WriteString("\t\t\tspiceasync.Definition{\n")
	fmt.Fprintf(source, "\t\t\t\tID: %s,\n", strconv.Quote(task.MethodID))
	fmt.Fprintf(source, "\t\t\t\tModule: %s,\n", strconv.Quote(module))
	source.WriteString("\t\t\t},\n")
	source.WriteString("\t\t\tfunc(taskContext context.Context) error {\n")
	fmt.Fprintf(
		source,
		"\t\t\t\treturn %s.%s(taskContext%s)\n",
		providerVariable,
		task.Method.Name,
		asyncInvocationArguments(arguments),
	)
	source.WriteString("\t\t\t},\n")
	source.WriteString("\t\t)\n")
	source.WriteString("\t}\n")
}

func writeAsyncApplicationMethods(
	source *bytes.Buffer,
	tasks []compilerasync.Task,
	aliases map[string]string,
) {
	if len(tasks) != 0 {
		source.WriteString("\n")
	}
	for _, task := range tasks {
		declarations := asyncParameterDeclarations(task, aliases)
		arguments := asyncArgumentNames(task)
		fmt.Fprintf(
			source,
			"func (application *Application) %s(%s) error {\n",
			task.SubmitMethod,
			strings.Join(declarations, ", "),
		)
		fmt.Fprintf(
			source,
			"\tif application == nil || application.%s == nil {\n",
			asyncFieldName(task),
		)
		fmt.Fprintf(
			source,
			"\t\treturn fmt.Errorf(%s)\n",
			strconv.Quote(
				"submit asynchronous task "+task.MethodID+
					": application is nil",
			),
		)
		source.WriteString("\t}\n")
		source.WriteString("\tif state := application.State(); state != spicelifecycle.StateReady {\n")
		fmt.Fprintf(
			source,
			"\t\treturn fmt.Errorf(%s, state)\n",
			strconv.Quote(
				"submit asynchronous task "+task.MethodID+
					": application state %s is not ready",
			),
		)
		source.WriteString("\t}\n")
		fmt.Fprintf(
			source,
			"\treturn application.%s(%s)\n",
			asyncFieldName(task),
			strings.Join(arguments, ", "),
		)
		source.WriteString("}\n\n")
	}
	if len(tasks) == 0 {
		return
	}
	source.WriteString("func (application *Application) AsyncSnapshot() spiceasync.Snapshot {\n")
	source.WriteString("\tif application == nil || application.asyncExecutor == nil {\n")
	source.WriteString("\t\treturn spiceasync.Snapshot{Closed: true}\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn application.asyncExecutor.Snapshot()\n")
	source.WriteString("}\n\n")
}

func asyncParameterTypes(
	task compilerasync.Task,
	aliases map[string]string,
) []string {
	result := []string{"context.Context"}
	for _, parameter := range task.Parameters() {
		result = append(result, renderedType(parameter.Type, aliases))
	}
	return result
}

func asyncParameterDeclarations(
	task compilerasync.Task,
	aliases map[string]string,
) []string {
	result := []string{"admission context.Context"}
	for _, parameter := range task.Parameters() {
		result = append(
			result,
			fmt.Sprintf(
				"argument%d %s",
				parameter.Index,
				renderedType(parameter.Type, aliases),
			),
		)
	}
	return result
}

func asyncArgumentNames(task compilerasync.Task) []string {
	result := []string{"admission"}
	for _, parameter := range task.Parameters() {
		result = append(result, "argument"+strconv.Itoa(parameter.Index))
	}
	return result
}

func asyncInvocationArguments(arguments []string) string {
	if len(arguments) < 2 {
		return ""
	}
	return ", " + strings.Join(arguments[1:], ", ")
}

func asyncFieldName(task compilerasync.Task) string {
	return "submit" + strings.TrimPrefix(task.SubmitMethod, "Submit")
}

func asyncTaskModule(
	task compilerasync.Task,
	providerModules map[string]string,
) string {
	if module := providerModules[task.ProviderID]; module != "" {
		return module
	}
	return task.Method.PackagePath
}

func writeHooks(
	source *bytes.Buffer,
	model application.Model,
	providerVariables map[string]string,
	providerModules map[string]string,
	applicationModule string,
) {
	components := model.Components()
	jobs := model.Jobs()
	if len(components) == 0 && len(jobs) == 0 {
		return
	}
	source.WriteString("\tapplication.hooks = []spicelifecycle.Hook{\n")
	for _, component := range components {
		variable := providerVariables[component.Provider.SymbolID]
		source.WriteString("\t\t{\n")
		fmt.Fprintf(source, "\t\t\tID: %s,\n", strconv.Quote(component.Provider.SymbolID))
		fmt.Fprintf(
			source,
			"\t\t\tModule: %s,\n",
			strconv.Quote(providerModules[component.Provider.SymbolID]),
		)
		fmt.Fprintf(source, "\t\t\tStart: %s.%s,\n", variable, component.Start.Method.Name)
		if component.Stop != nil {
			fmt.Fprintf(source, "\t\t\tStop: %s.%s,\n", variable, component.Stop.Method.Name)
		}
		source.WriteString("\t\t},\n")
	}
	if len(jobs) != 0 {
		source.WriteString("\t\t{\n")
		source.WriteString("\t\t\tID: \"spice.schedule\",\n")
		fmt.Fprintf(
			source,
			"\t\t\tModule: %s,\n",
			strconv.Quote(applicationModule),
		)
		source.WriteString("\t\t\tStart: generatedScheduler.Start,\n")
		source.WriteString("\t\t\tStop: generatedScheduler.Shutdown,\n")
		source.WriteString("\t\t},\n")
	}
	source.WriteString("\t}\n")
}

func writeScheduleSetup(
	source *bytes.Buffer,
	jobs []compilerschedule.Job,
	providerVariables map[string]string,
	providerModules map[string]string,
) {
	if len(jobs) == 0 {
		return
	}
	source.WriteString("\tscheduleContext := options.ScheduleContext\n")
	source.WriteString("\tif scheduleContext == nil {\n")
	source.WriteString("\t\tscheduleContext = context.WithoutCancel(ctx)\n")
	source.WriteString("\t}\n")
	source.WriteString("\tgeneratedScheduler, err := spiceschedule.New(\n")
	source.WriteString("\t\tscheduleContext,\n")
	source.WriteString("\t\t[]spiceschedule.Job{\n")
	for _, job := range jobs {
		module := providerModules[job.ProviderID]
		if module == "" {
			module = job.Method.PackagePath
		}
		source.WriteString("\t\t\t{\n")
		source.WriteString("\t\t\t\tDefinition: spiceschedule.Definition{\n")
		fmt.Fprintf(
			source,
			"\t\t\t\t\tID: %s,\n",
			strconv.Quote(job.MethodID),
		)
		fmt.Fprintf(
			source,
			"\t\t\t\t\tModule: %s,\n",
			strconv.Quote(module),
		)
		source.WriteString("\t\t\t\t},\n")
		if job.InitialDelay != 0 {
			fmt.Fprintf(
				source,
				"\t\t\t\tInitialDelay: %d,\n",
				job.InitialDelay,
			)
		}
		fmt.Fprintf(
			source,
			"\t\t\t\tDelay: %d,\n",
			job.Delay,
		)
		if job.ContinueOnError {
			source.WriteString("\t\t\t\tContinueOnError: true,\n")
		}
		fmt.Fprintf(
			source,
			"\t\t\t\tRun: %s.%s,\n",
			providerVariables[job.ProviderID],
			job.Method.Name,
		)
		source.WriteString("\t\t\t},\n")
	}
	source.WriteString("\t\t},\n")
	source.WriteString("\t\toptions.ScheduleWaiter,\n")
	source.WriteString("\t\toptions.ScheduleObservers...,\n")
	source.WriteString("\t)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"construct generated scheduler: %w\", err))\n")
	source.WriteString("\t}\n")
}

func writeLifecycleMethods(source *bytes.Buffer) {
	source.WriteString(`func (application *Application) State() spicelifecycle.State {
	if application == nil || application.coordinator == nil {
		return spicelifecycle.StateInvalid
	}
	return application.coordinator.State()
}

func (application *Application) Start(ctx context.Context) error {
	if application == nil || application.coordinator == nil {
		return fmt.Errorf("start application: application is nil")
	}
	return application.coordinator.Start(ctx, application.hooks)
}

func (application *Application) Stop(ctx context.Context) error {
	if application == nil || application.coordinator == nil {
		return fmt.Errorf("stop application: application is nil")
	}
	return application.coordinator.Stop(ctx)
}

func (application *Application) ShutdownTimeout() time.Duration {
	if application == nil {
		return 0
	}
	return application.shutdownTimeout
}

func (application *Application) RegisterObserver(observer spicelifecycle.Observer) error {
	if application == nil || application.coordinator == nil {
		return fmt.Errorf("register lifecycle observer: application is nil")
	}
	return application.coordinator.RegisterObserver(observer)
}

func (application *Application) Run(ctx context.Context, shutdown spicelifecycle.ContextFactory) error {
	if application == nil || application.coordinator == nil {
		return fmt.Errorf("run application: application is nil")
	}
	return application.coordinator.Run(ctx, application.hooks, shutdown)
}
`)
}

func writeHandlerMethod(source *bytes.Buffer) {
	source.WriteString(`
func (application *Application) Handler() http.Handler {
	if application == nil {
		return nil
	}
	return application.handler
}
`)
}

func providerModuleIDs(
	model application.Model,
	providers []provider.Provider,
) map[string]string {
	packageModules := make(map[string]string)
	for _, module := range model.Modules() {
		for _, pkg := range module.Packages() {
			packageModules[pkg.Path] = module.ID
		}
	}
	result := make(map[string]string, len(providers))
	for _, item := range providers {
		result[item.SymbolID] = packageModules[item.PackagePath]
	}
	return result
}

func importAliases(
	providers []provider.Provider,
	controllers []controller.Controller,
	asyncTasks []compilerasync.Task,
	events []compilerevent.Topic,
	caches []compilercache.Boundary,
	features commandFeatures,
) map[string]string {
	aliases := map[string]string{
		"context":     "context",
		"flag":        "flag",
		"fmt":         "fmt",
		"io":          "io",
		"log/slog":    "slog",
		"os":          "os",
		"os/signal":   "signal",
		"syscall":     "syscall",
		"time":        "time",
		configPath:    "spiceconfig",
		lifecyclePath: "spicelifecycle",
	}
	if features.hasMux {
		aliases["net/http"] = "http"
		aliases[webPath] = "spiceweb"
	}
	addViewImportAlias(aliases, controllers)
	if features.management {
		aliases[managementPath] = "spicemanagement"
	}
	if features.logging {
		aliases[observabilityPath] = "spiceobservability"
	}
	if features.authorization {
		aliases[securityPath] = "spicesecurity"
	}
	if features.scheduling {
		aliases[schedulePath] = "spiceschedule"
	}
	if features.asynchronous {
		aliases[asyncPath] = "spiceasync"
	}
	if features.transactions {
		aliases["database/sql"] = "sql"
		aliases[dataPath] = "spicedata"
	}
	if features.events {
		aliases[eventPath] = "spiceevent"
	}
	if features.caching {
		aliases[cachePath] = "spicecache"
	}
	if usesBeanHandles(providers) {
		aliases[beanPath] = "spicebean"
	}
	used := map[string]struct{}{
		"Application":               {},
		"ApplicationOptions":        {},
		"CommandOptions":            {},
		"ConfigurationSchema":       {},
		"ExitFailure":               {},
		"ExitSuccess":               {},
		"ExitUsage":                 {},
		"Main":                      {},
		"NewApplication":            {},
		"NewApplicationWithOptions": {},
		"RunCommand":                {},
		"TargetID":                  {},
		"context":                   {},
		"flag":                      {},
		"fmt":                       {},
		"http":                      {},
		"io":                        {},
		"os":                        {},
		"signal":                    {},
		"slog":                      {},
		"sql":                       {},
		"spiceconfig":               {},
		"spiceasync":                {},
		"spicecache":                {},
		"spicebean":                 {},
		"spicedata":                 {},
		"spiceevent":                {},
		"spicelifecycle":            {},
		"spicemanagement":           {},
		"spiceobservability":        {},
		"spiceschedule":             {},
		"spicesecurity":             {},
		"spiceview":                 {},
		"spiceweb":                  {},
		"syscall":                   {},
		"time":                      {},
	}
	names := importNames(
		providers,
		controllers,
		asyncTasks,
		events,
		caches,
		aliases,
	)
	paths := make([]string, 0, len(names))
	for importPath := range names {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	for _, importPath := range paths {
		base := names[importPath]
		alias := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[alias]; !exists {
				break
			}
			alias = base + strconv.Itoa(suffix)
		}
		used[alias] = struct{}{}
		aliases[importPath] = alias
	}
	return aliases
}

func addViewImportAlias(
	aliases map[string]string,
	controllers []controller.Controller,
) {
	if hasViewRoutes(controllers) {
		aliases[viewPath] = "spiceview"
	}
}

func hasViewRoutes(controllers []controller.Controller) bool {
	for _, item := range controllers {
		for _, route := range item.Routes() {
			if route.View {
				return true
			}
		}
	}
	return false
}

func importNames(
	providers []provider.Provider,
	controllers []controller.Controller,
	asyncTasks []compilerasync.Task,
	events []compilerevent.Topic,
	caches []compilercache.Boundary,
	aliases map[string]string,
) map[string]string {
	names := make(map[string]string)
	addProviderImportNames(names, aliases, providers)
	for _, item := range controllers {
		for _, route := range item.Routes() {
			for _, binding := range route.Bindings() {
				addTypeImportName(names, aliases, binding.Type)
			}
		}
	}
	for _, task := range asyncTasks {
		for _, parameter := range task.Parameters() {
			addTypeImportName(names, aliases, parameter.Type)
		}
	}
	for _, topic := range events {
		addTypeImportName(names, aliases, topic.Payload)
	}
	for _, boundary := range caches {
		addTypeImportName(names, aliases, boundary.Key)
		addTypeImportName(names, aliases, boundary.Value)
	}
	return names
}

func addProviderImportNames(
	names map[string]string,
	aliases map[string]string,
	providers []provider.Provider,
) {
	for _, item := range providers {
		addTypeImportName(names, aliases, item.Output)
		for _, dependency := range item.Dependencies {
			if dependency.Kind == provider.DependencySingle {
				continue
			}
			addTypeImportName(
				names,
				aliases,
				dependency.Type,
			)
		}
	}
}

func addTypeImportName(names, aliases map[string]string, value types.Type) {
	switch typed := value.(type) {
	case *types.Named:
		addTypeObjectImportName(names, aliases, typed.Obj())
		addTypeArgumentImportNames(names, aliases, typed.TypeArgs())
	case *types.Alias:
		addTypeObjectImportName(names, aliases, typed.Obj())
		addTypeArgumentImportNames(names, aliases, typed.TypeArgs())
	case *types.Pointer:
		addTypeImportName(names, aliases, typed.Elem())
	case *types.Slice:
		addTypeImportName(names, aliases, typed.Elem())
	case *types.Array:
		addTypeImportName(names, aliases, typed.Elem())
	case *types.Map:
		addTypeImportName(names, aliases, typed.Key())
		addTypeImportName(names, aliases, typed.Elem())
	case *types.Chan:
		addTypeImportName(names, aliases, typed.Elem())
	}
}

func addTypeArgumentImportNames(
	names, aliases map[string]string,
	arguments *types.TypeList,
) {
	if arguments == nil {
		return
	}
	for argument := range arguments.Types() {
		addTypeImportName(names, aliases, argument)
	}
}

func addTypeObjectImportName(
	names, aliases map[string]string,
	object *types.TypeName,
) {
	if object == nil || object.Pkg() == nil {
		return
	}
	importPath := object.Pkg().Path()
	if _, fixed := aliases[importPath]; fixed {
		return
	}
	names[importPath] = object.Pkg().Name()
}

func dependencyVariables(
	model application.Model,
	providers []provider.Provider,
	aliases map[string]string,
) (map[string][]string, error) {
	edges := make(map[string][]graphEdge)
	for _, edge := range model.Edges() {
		key := dependencyKey(edge.ConsumerID, edge.ParameterIndex)
		edges[key] = append(edges[key], graphEdge{
			providerID: edge.DependencyID,
			index:      edge.CollectionIndex,
			kind:       edge.DependencyKind,
		})
	}
	variables, _ := targetProviderVariables(providers)
	result := make(map[string][]string, len(providers))
	for _, item := range providers {
		inputs := make([]string, len(item.Dependencies))
		for _, dependency := range item.Dependencies {
			selected := edges[dependencyKey(
				item.SymbolID,
				dependency.Index,
			)]
			if len(selected) == 0 &&
				dependency.Kind != provider.DependencySlice &&
				dependency.Kind != provider.DependencyMap &&
				dependency.Kind != provider.DependencyOptional {
				return nil, fmt.Errorf(
					"provider %s parameter %d has no validated graph edge",
					item.SymbolID,
					dependency.Index,
				)
			}
			sort.SliceStable(selected, func(i, j int) bool {
				return selected[i].index < selected[j].index
			})
			selectedVariables := make([]string, len(selected))
			selectedProviders := make([]provider.Provider, len(selected))
			for selectedIndex, edge := range selected {
				variable, ok := variables[edge.providerID]
				if !ok {
					return nil, fmt.Errorf(
						"provider %s parameter %d references unknown provider %s",
						item.SymbolID,
						dependency.Index,
						edge.providerID,
					)
				}
				selectedVariables[selectedIndex] = variable
				selectedProviders[selectedIndex] = providerByID(providers, edge.providerID)
			}
			effective := dependency
			if len(selected) != 0 &&
				selected[0].kind == provider.DependencySingle {
				effective.Kind = provider.DependencySingle
				effective.Element = nil
				effective.ElementTypeID = ""
			}
			inputs[dependency.Index] = dependencyExpression(
				effective,
				selectedVariables,
				selectedProviders,
				aliases,
			)
		}
		result[item.SymbolID] = inputs
	}
	return result, nil
}

type graphEdge struct {
	providerID string
	index      int
	kind       provider.DependencyKind
}

func dependencyExpression(
	dependency provider.Dependency,
	variables []string,
	selected []provider.Provider,
	aliases map[string]string,
) string {
	elementType := renderedType(dependency.MatchType(), aliases)
	switch dependency.Kind {
	case provider.DependencySingle:
		if len(variables) == 0 {
			return ""
		}
		return variables[0]
	case provider.DependencySlice:
		return renderedType(dependency.Type, aliases) +
			"{" + strings.Join(variables, ", ") + "}"
	case provider.DependencyMap:
		entries := make([]string, len(variables))
		for index, variable := range variables {
			entries[index] = strconv.Quote(selected[index].Name) +
				": " + variable
		}
		return renderedType(dependency.Type, aliases) +
			"{" + strings.Join(entries, ", ") + "}"
	case provider.DependencyOptional:
		if len(variables) == 0 {
			return "spicebean.None[" + elementType + "]()"
		}
		return "spicebean.Some[" + elementType + "](" +
			variables[0] + ")"
	case provider.DependencyLazy:
		return "spicebean.NewLazy(func(context.Context) (" +
			elementType + ", error) { return " +
			variables[0] + ", nil })"
	case provider.DependencyProvider:
		if len(selected) != 0 &&
			selected[0].Scope != sdk.BeanScopeSingleton {
			return variables[0]
		}
		return "spicebean.NewProvider(func(context.Context) (" +
			elementType +
			", spicelifecycle.Cleanup, error) { return " +
			variables[0] + ", nil, nil })"
	}
	return ""
}

func providerByID(
	providers []provider.Provider,
	symbolID string,
) provider.Provider {
	for _, item := range providers {
		if item.SymbolID == symbolID {
			return item
		}
	}
	return provider.Provider{}
}

func usesBeanHandles(providers []provider.Provider) bool {
	for _, item := range providers {
		if item.Scope != sdk.BeanScopeSingleton {
			return true
		}
		for _, dependency := range item.Dependencies {
			switch dependency.Kind {
			case provider.DependencyOptional,
				provider.DependencyLazy,
				provider.DependencyProvider:
				return true
			case provider.DependencySingle,
				provider.DependencySlice,
				provider.DependencyMap:
			}
		}
	}
	return false
}

func validateTarget(target Target, applicationTarget application.Target) []Diagnostic {
	var diagnostics []Diagnostic
	add := func(kind, message string) {
		diagnostics = append(diagnostics, targetDiagnostic(applicationTarget, kind, message))
	}
	if !targetIDPattern.MatchString(target.ID) {
		add("target-id", fmt.Sprintf("generation target ID %q must match %s", target.ID, targetIDPattern))
	}
	if target.ModulePath == "" {
		add("module-path", "generation target module path is required")
	}
	if target.ModuleRoot == "" {
		add("module-root", "generation target module root is required")
	}
	if target.PackagePath == "" {
		add("package-path", "generated package import path is required")
	}
	if target.OutputDir == "" {
		add("output-dir", "generated output directory is required")
	}
	diagnostics = append(
		diagnostics,
		validateTargetLayout(target, applicationTarget)...,
	)
	if target.ManifestPath == "" {
		add("manifest-path", "generated manifest path is required")
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

func validateTargetLayout(
	target Target,
	applicationTarget application.Target,
) []Diagnostic {
	add := func(kind, message string) Diagnostic {
		return targetDiagnostic(applicationTarget, kind, message)
	}
	switch target.Layout {
	case LayoutApplicationPackage:
		var diagnostics []Diagnostic
		if target.BridgeDir == "" {
			diagnostics = append(diagnostics, add(
				"bridge-dir",
				"application-package entrypoint directory is required",
			))
		}
		if target.EntrypointPackagePath == "" {
			diagnostics = append(diagnostics, add(
				"entrypoint-package",
				"application-package entrypoint import path is required",
			))
		}
		return diagnostics
	case LayoutGeneratedPackage:
		var diagnostics []Diagnostic
		if target.BridgeDir != "" {
			diagnostics = append(diagnostics, add(
				"bridge-dir",
				"generated-package target must not declare an entrypoint directory",
			))
		}
		if target.EntrypointPackagePath != "" {
			diagnostics = append(diagnostics, add(
				"entrypoint-package",
				"generated-package target must not declare an entrypoint import path",
			))
		}
		return diagnostics
	default:
		return []Diagnostic{add(
			"layout",
			fmt.Sprintf(
				"generation target layout %q is unsupported",
				target.Layout,
			),
		)}
	}
}

func validateRenderable(
	program *load.Program,
	model application.Model,
	applicationTarget application.Target,
	target Target,
) []Diagnostic {
	var diagnostics []Diagnostic
	packages := program.Packages()
	diagnostics = append(
		diagnostics,
		generatedImportCycleDiagnostics(
			packages,
			applicationTarget,
			target,
		)...,
	)
	diagnostics = append(
		diagnostics,
		providerVisibilityDiagnostics(
			packages,
			model.Providers(),
			applicationTarget,
			target,
		)...,
	)
	diagnostics = append(
		diagnostics,
		lifecycleVisibilityDiagnostics(
			model.Components(),
			applicationTarget,
		)...,
	)
	diagnostics = append(
		diagnostics,
		scheduledMethodDiagnostics(model, applicationTarget)...,
	)
	diagnostics = append(
		diagnostics,
		reservedConfigurationDiagnostics(model, applicationTarget)...,
	)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func generatedImportCycleDiagnostics(
	packages []load.Package,
	applicationTarget application.Target,
	target Target,
) []Diagnostic {
	if target.Layout != LayoutGeneratedPackage {
		return nil
	}
	sourcePackage, ok := packageByPath(
		packages,
		applicationTarget.PackagePath,
	)
	if !ok || sourcePackage.Types == nil {
		return nil
	}
	var diagnostics []Diagnostic
	for _, imported := range sourcePackage.Types.Imports() {
		if imported.Path() != target.PackagePath {
			continue
		}
		diagnostics = append(diagnostics, targetDiagnostic(
			applicationTarget,
			"import-cycle",
			fmt.Sprintf(
				"application package %s imports generated package %s; generated output must be imported only by an outer command package",
				sourcePackage.Path,
				target.PackagePath,
			),
		))
	}
	return diagnostics
}

func providerVisibilityDiagnostics(
	packages []load.Package,
	providers []provider.Provider,
	applicationTarget application.Target,
	target Target,
) []Diagnostic {
	packageNames := make(map[string]string, len(packages))
	for _, pkg := range packages {
		packageNames[pkg.Path] = pkg.Name
	}
	var diagnostics []Diagnostic
	for _, item := range providers {
		switch {
		case item.PackagePath == target.PackagePath:
			diagnostics = append(diagnostics, providerRenderDiagnostic(
				applicationTarget,
				item,
				"self-import",
				fmt.Sprintf("provider %s is declared in generated package %s", item.SymbolID, target.PackagePath),
			))
		case packageNames[item.PackagePath] == "main":
			diagnostics = append(diagnostics, providerRenderDiagnostic(
				applicationTarget,
				item,
				"main-package",
				fmt.Sprintf(
					"provider %s is declared in package main, which generated package %s cannot import; move providers into an importable package",
					item.SymbolID,
					target.PackagePath,
				),
			))
		case !providerConstructionExported(item):
			diagnostics = append(diagnostics, providerRenderDiagnostic(
				applicationTarget,
				item,
				"unexported-provider",
				fmt.Sprintf(
					"provider %s construction is unexported; target-scoped generated packages require exported @Bean functions or exported stereotype types and constructors",
					item.SymbolID,
				),
			))
		}
	}
	return diagnostics
}

func providerConstructionExported(item provider.Provider) bool {
	if item.Construction == provider.ConstructionAllocate {
		return token.IsExported(item.Symbol.Name)
	}
	if item.Constructor.Name != "" {
		return token.IsExported(item.Constructor.Name)
	}
	return token.IsExported(item.Name)
}

func lifecycleVisibilityDiagnostics(
	components []compilerlifecycle.Component,
	applicationTarget application.Target,
) []Diagnostic {
	var diagnostics []Diagnostic
	for _, component := range components {
		methods := []load.Symbol{component.Start.Method}
		if component.Stop != nil {
			methods = append(methods, component.Stop.Method)
		}
		for _, method := range methods {
			if token.IsExported(method.Name) {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Position:         method.Position,
				PhysicalPosition: method.PhysicalPosition,
				TargetID:         applicationTarget.SymbolID,
				Kind:             "unexported-hook",
				Message: fmt.Sprintf(
					"lifecycle method %s is unexported; target-scoped generated packages require exported hook methods",
					method.ID,
				),
			})
		}
	}
	return diagnostics
}

func scheduledMethodDiagnostics(
	model application.Model,
	applicationTarget application.Target,
) []Diagnostic {
	var diagnostics []Diagnostic
	for _, job := range model.Jobs() {
		if token.IsExported(job.Method.Name) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Position:         job.Position,
			PhysicalPosition: job.PhysicalPosition,
			TargetID:         applicationTarget.SymbolID,
			Kind:             "unexported-scheduled-method",
			Message: fmt.Sprintf(
				"scheduled method %s is unexported; target-scoped generated packages require exported scheduled methods",
				job.MethodID,
			),
		})
	}
	return diagnostics
}

func reservedConfigurationDiagnostics(
	model application.Model,
	applicationTarget application.Target,
) []Diagnostic {
	var diagnostics []Diagnostic
	reservedKeys := map[string]struct{}{
		shutdownConfigurationKey: {},
	}
	reservedEnvironments := map[string]struct{}{
		"SPICE_SHUTDOWN_TIMEOUT": {},
	}
	if len(model.AsyncTasks()) != 0 {
		reservedKeys[asyncConcurrencyKey] = struct{}{}
		reservedEnvironments["SPICE_ASYNC_MAX_CONCURRENCY"] = struct{}{}
	}
	for _, boundary := range model.Caches() {
		reservedKeys[cacheCapacityKey(boundary.CacheName)] = struct{}{}
		reservedKeys[cacheTTLKey(boundary.CacheName)] = struct{}{}
		reservedEnvironments[cacheEnvironment(
			boundary.CacheName,
			"CAPACITY",
		)] = struct{}{}
		reservedEnvironments[cacheEnvironment(
			boundary.CacheName,
			"TTL",
		)] = struct{}{}
	}
	for _, configType := range model.Configurations() {
		for _, field := range configType.Fields() {
			if _, reserved := reservedKeys[field.Key]; reserved {
				diagnostics = append(diagnostics, Diagnostic{
					Position:         field.Position,
					PhysicalPosition: field.PhysicalPosition,
					TargetID:         applicationTarget.SymbolID,
					Kind:             "reserved-configuration",
					Message: fmt.Sprintf(
						"configuration field %s.%s uses framework-owned key %q; choose a different prefix or field key",
						configType.TypeID,
						field.Name,
						field.Key,
					),
				})
				continue
			}
			if _, reserved := reservedEnvironments[field.Environment]; !reserved {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Position:         field.Position,
				PhysicalPosition: field.PhysicalPosition,
				TargetID:         applicationTarget.SymbolID,
				Kind:             "reserved-configuration",
				Message: fmt.Sprintf(
					"configuration field %s.%s uses framework-owned environment variable %q; choose a different environment mapping",
					configType.TypeID,
					field.Name,
					field.Environment,
				),
			})
		}
	}
	return diagnostics
}

type modelHashScheduleJob struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Module          string `json:"module"`
	InitialDelay    int64  `json:"initial_delay"`
	Delay           int64  `json:"delay"`
	ContinueOnError bool   `json:"continue_on_error"`
}

type modelHashAsyncParameter struct {
	Index int    `json:"index"`
	Type  string `json:"type"`
}

type modelHashAsyncTask struct {
	ID           string                    `json:"id"`
	Provider     string                    `json:"provider"`
	Module       string                    `json:"module"`
	SubmitMethod string                    `json:"submit_method"`
	Parameters   []modelHashAsyncParameter `json:"parameters"`
}

type modelHashEventListener struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Module   string `json:"module"`
	Order    int    `json:"order"`
}

type modelHashEventTopic struct {
	ID        string                   `json:"id"`
	Provider  string                   `json:"provider"`
	Module    string                   `json:"module"`
	Publisher string                   `json:"publisher"`
	Payload   string                   `json:"payload"`
	Listeners []modelHashEventListener `json:"listeners"`
}

type modelHashCache struct {
	Route  string `json:"route"`
	Name   string `json:"name"`
	Module string `json:"module"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

type modelHashDependency struct {
	Index      int                     `json:"index"`
	Type       string                  `json:"type"`
	Kind       provider.DependencyKind `json:"kind,omitempty"`
	Element    string                  `json:"element,omitempty"`
	Qualifiers []string                `json:"qualifiers,omitempty"`
}

type modelHashProvider struct {
	ID            string                `json:"id"`
	Source        provider.Source       `json:"source"`
	SourceID      string                `json:"source_id,omitempty"`
	SourceVersion string                `json:"source_version,omitempty"`
	Module        string                `json:"module,omitempty"`
	Output        string                `json:"output"`
	Construction  provider.Construction `json:"construction,omitempty"`
	Constructor   string                `json:"constructor,omitempty"`
	Interfaces    []string              `json:"interfaces,omitempty"`
	Name          string                `json:"name"`
	ExplicitName  bool                  `json:"explicit_name,omitempty"`
	Aliases       []string              `json:"aliases,omitempty"`
	Qualifiers    []string              `json:"qualifiers,omitempty"`
	Primary       bool                  `json:"primary,omitempty"`
	Fallback      bool                  `json:"fallback,omitempty"`
	Order         int64                 `json:"order,omitempty"`
	Scope         sdk.BeanScope         `json:"scope"`
	Cleanup       bool                  `json:"cleanup"`
	Error         bool                  `json:"error"`
	Inputs        []modelHashDependency `json:"inputs"`
}

type modelHashEdge struct {
	Consumer   string                  `json:"consumer"`
	Parameter  int                     `json:"parameter"`
	Provider   string                  `json:"provider"`
	Kind       provider.DependencyKind `json:"kind,omitempty"`
	Collection int                     `json:"collection,omitempty"`
}

type modelHashComponent struct {
	Provider string `json:"provider"`
	Start    string `json:"start"`
	Stop     string `json:"stop,omitempty"`
}

type modelHashRoot struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
}

type modelHashConfigurationField struct {
	Index       int                `json:"index"`
	Name        string             `json:"name"`
	Type        string             `json:"type"`
	Key         string             `json:"key"`
	Kind        runtimeconfig.Kind `json:"kind"`
	Module      string             `json:"module,omitempty"`
	Environment string             `json:"environment,omitempty"`
	Default     string             `json:"default,omitempty"`
	HasDefault  bool               `json:"has_default,omitempty"`
	Required    bool               `json:"required,omitempty"`
	Secret      bool               `json:"secret,omitempty"`
}

type modelHashConfiguration struct {
	ID     string                        `json:"id"`
	Type   string                        `json:"type"`
	Prefix string                        `json:"prefix,omitempty"`
	Module string                        `json:"module,omitempty"`
	Fields []modelHashConfigurationField `json:"fields"`
}

type modelHashBinding struct {
	Index    int                   `json:"index"`
	Field    string                `json:"field"`
	Name     string                `json:"name,omitempty"`
	Location controller.Location   `json:"location"`
	Required bool                  `json:"required"`
	Kind     controller.ScalarKind `json:"kind,omitempty"`
	Type     string                `json:"type"`
}

type modelHashAuthorization struct {
	PolicyID      string   `json:"policy_id"`
	Module        string   `json:"module"`
	Authenticated bool     `json:"authenticated,omitempty"`
	AnyRoles      []string `json:"any_roles,omitempty"`
	AllRoles      []string `json:"all_roles,omitempty"`
	AllScopes     []string `json:"all_scopes,omitempty"`
}

type modelHashRoute struct {
	ID                string                  `json:"id"`
	Method            string                  `json:"method"`
	Path              string                  `json:"path"`
	Provider          string                  `json:"provider"`
	Request           string                  `json:"request,omitempty"`
	Response          string                  `json:"response,omitempty"`
	Validator         string                  `json:"validator,omitempty"`
	Raw               bool                    `json:"raw,omitempty"`
	NoContent         bool                    `json:"no_content,omitempty"`
	ExecutorParameter bool                    `json:"executor_parameter,omitempty"`
	Bindings          []modelHashBinding      `json:"bindings"`
	Authorization     *modelHashAuthorization `json:"authorization,omitempty"`
}

type modelHashController struct {
	ID       string           `json:"id"`
	Module   string           `json:"module,omitempty"`
	Provider string           `json:"provider"`
	Prefix   string           `json:"prefix,omitempty"`
	Routes   []modelHashRoute `json:"routes"`
}

type modelHashTransaction struct {
	Route     string             `json:"route"`
	Manager   string             `json:"manager"`
	Module    string             `json:"module"`
	Isolation sql.IsolationLevel `json:"isolation"`
	ReadOnly  bool               `json:"read_only,omitempty"`
}

type modelHashNamedInterface struct {
	Name        string `json:"name"`
	PackagePath string `json:"package"`
}

type modelHashModule struct {
	ID                  string                    `json:"id"`
	RootPackage         string                    `json:"root_package"`
	Packages            []string                  `json:"packages"`
	NamedInterfaces     []modelHashNamedInterface `json:"named_interfaces"`
	AllowedDependencies []string                  `json:"allowed_dependencies"`
}

type modelHashModuleEdge struct {
	FromModule  string `json:"from_module"`
	ToModule    string `json:"to_module"`
	API         string `json:"api"`
	FromPackage string `json:"from_package"`
	ToPackage   string `json:"to_package"`
}

type modelHashInput struct {
	Schema         int                         `json:"schema"`
	Target         TargetSummary               `json:"target"`
	Symbol         string                      `json:"symbol"`
	Providers      []modelHashProvider         `json:"providers"`
	Configurations []modelHashConfiguration    `json:"configurations"`
	Controllers    []modelHashController       `json:"controllers"`
	Transactions   []modelHashTransaction      `json:"transactions,omitempty"`
	Edges          []modelHashEdge             `json:"edges"`
	Components     []modelHashComponent        `json:"components"`
	Jobs           []modelHashScheduleJob      `json:"jobs"`
	AsyncTasks     []modelHashAsyncTask        `json:"async_tasks,omitempty"`
	Events         []modelHashEventTopic       `json:"events,omitempty"`
	Caches         []modelHashCache            `json:"caches,omitempty"`
	Roots          []modelHashRoot             `json:"roots"`
	Bootstrap      []modelHashBootstrapFeature `json:"bootstrap"`
	Modules        []modelHashModule           `json:"modules,omitempty"`
	ModuleEdges    []modelHashModuleEdge       `json:"module_edges,omitempty"`
	Unassigned     []string                    `json:"unassigned_packages,omitempty"`
}

func modelHash(
	model application.Model,
	applicationTarget application.Target,
	target Target,
) (string, error) {
	value := modelHashInput{
		Schema:    SchemaVersion,
		Target:    summarizeTarget(target),
		Symbol:    applicationTarget.SymbolID,
		Bootstrap: bootstrapHashInput(applicationTarget),
	}
	providers := model.Providers()
	providerModules := providerModuleIDs(model, providers)
	for _, item := range providers {
		inputs := make([]modelHashDependency, len(item.Dependencies))
		for index, dependency := range item.Dependencies {
			inputs[index] = modelHashDependency{
				Index:   dependency.Index,
				Type:    dependency.TypeID,
				Kind:    dependency.Kind,
				Element: dependency.ElementTypeID,
				Qualifiers: append(
					[]string(nil),
					dependency.Qualifiers...,
				),
			}
		}
		value.Providers = append(value.Providers, modelHashProvider{
			ID:            item.SymbolID,
			Source:        item.Source,
			SourceID:      item.SourceID,
			SourceVersion: item.SourceVersion,
			Module:        providerModules[item.SymbolID],
			Output:        item.OutputTypeID,
			Construction:  item.Construction,
			Constructor:   item.Constructor.ID,
			Name:          item.Name,
			ExplicitName:  item.ExplicitName,
			Aliases:       append([]string(nil), item.Aliases...),
			Qualifiers:    append([]string(nil), item.Qualifiers...),
			Primary:       item.Primary,
			Fallback:      item.Fallback,
			Order:         item.Order,
			Scope:         item.Scope,
			Cleanup:       item.ReturnsCleanup,
			Error:         item.ReturnsError,
			Inputs:        inputs,
		})
		for _, binding := range item.Interfaces {
			value.Providers[len(value.Providers)-1].Interfaces = append(
				value.Providers[len(value.Providers)-1].Interfaces,
				binding.TypeID,
			)
		}
	}
	for _, configType := range model.Configurations() {
		item := modelHashConfiguration{
			ID:     configType.SymbolID,
			Type:   configType.TypeID,
			Prefix: configType.Prefix,
			Module: configType.Module,
		}
		for _, field := range configType.Fields() {
			item.Fields = append(item.Fields, modelHashConfigurationField{
				Index:       field.Index,
				Name:        field.Name,
				Type:        field.TypeID,
				Key:         field.Key,
				Kind:        field.Kind,
				Module:      field.Module,
				Environment: field.Environment,
				Default:     field.Default,
				HasDefault:  field.HasDefault,
				Required:    field.Required,
				Secret:      field.Secret,
			})
		}
		value.Configurations = append(value.Configurations, item)
	}
	for _, item := range model.Controllers() {
		inputItem := modelHashController{
			ID:       item.SymbolID,
			Module:   item.Module,
			Provider: item.ProviderID,
			Prefix:   item.Prefix,
		}
		for _, route := range item.Routes() {
			routeInput := modelHashRoute{
				ID:                route.SymbolID,
				Method:            route.HTTPMethod,
				Path:              route.Path,
				Provider:          route.ProviderID,
				Request:           route.RequestTypeID,
				Response:          route.ResponseTypeID,
				Validator:         route.ValidatorID,
				Raw:               route.Raw,
				NoContent:         route.NoContent,
				ExecutorParameter: route.ExecutorParameter,
			}
			if authorization, protected := route.Authorization(); protected {
				routeInput.Authorization = &modelHashAuthorization{
					PolicyID:      authorization.PolicyID,
					Module:        authorization.Module,
					Authenticated: authorization.Authenticated,
					AnyRoles:      authorization.AnyRoles(),
					AllRoles:      authorization.AllRoles(),
					AllScopes:     authorization.AllScopes(),
				}
			}
			for _, binding := range route.Bindings() {
				routeInput.Bindings = append(routeInput.Bindings, modelHashBinding{
					Index:    binding.Index,
					Field:    binding.Field,
					Name:     binding.Name,
					Location: binding.Location,
					Required: binding.Required,
					Kind:     binding.Kind,
					Type:     binding.TypeID,
				})
			}
			inputItem.Routes = append(inputItem.Routes, routeInput)
		}
		value.Controllers = append(value.Controllers, inputItem)
	}
	value.Transactions = modelHashTransactions(model.Transactions())
	for _, edge := range model.Edges() {
		value.Edges = append(value.Edges, modelHashEdge{
			Consumer:   edge.ConsumerID,
			Parameter:  edge.ParameterIndex,
			Provider:   edge.DependencyID,
			Kind:       edge.DependencyKind,
			Collection: edge.CollectionIndex,
		})
	}
	for _, component := range model.Components() {
		item := modelHashComponent{
			Provider: component.Provider.SymbolID,
			Start:    component.Start.MethodID,
		}
		if component.Stop != nil {
			item.Stop = component.Stop.MethodID
		}
		value.Components = append(value.Components, item)
	}
	value.Jobs = modelHashScheduleJobs(model.Jobs(), providerModules)
	value.AsyncTasks = modelHashAsyncTasks(
		model.AsyncTasks(),
		providerModules,
	)
	value.Events = modelHashEvents(model.Events())
	value.Caches = modelHashCaches(model.Caches())
	addModelHashModules(&value, model, applicationTarget)
	for _, root := range applicationTarget.Roots() {
		value.Roots = append(value.Roots, modelHashRoot{
			Index:    root.Index,
			Type:     root.TypeID,
			Provider: root.ProviderID,
		})
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return contentHash(encoded), nil
}

func addModelHashModules(
	value *modelHashInput,
	model application.Model,
	applicationTarget application.Target,
) {
	if !commandFeaturesFor(
		applicationTarget,
		len(model.Controllers()) != 0,
	).modules {
		return
	}
	value.Modules = modelHashModules(model)
	value.ModuleEdges = modelHashModuleEdges(model)
	for _, item := range model.UnassignedPackages() {
		value.Unassigned = append(value.Unassigned, item.Path)
	}
}

func modelHashModules(model application.Model) []modelHashModule {
	modules := model.Modules()
	result := make([]modelHashModule, len(modules))
	for index, module := range modules {
		item := modelHashModule{
			ID:          module.ID,
			RootPackage: module.RootPackage,
		}
		for _, pkg := range module.Packages() {
			item.Packages = append(item.Packages, pkg.Path)
		}
		for _, namedInterface := range module.NamedInterfaces() {
			item.NamedInterfaces = append(
				item.NamedInterfaces,
				modelHashNamedInterface{
					Name:        namedInterface.Name,
					PackagePath: namedInterface.PackagePath,
				},
			)
		}
		for _, dependency := range module.AllowedDependencies() {
			item.AllowedDependencies = append(
				item.AllowedDependencies,
				dependency.String(),
			)
		}
		result[index] = item
	}
	return result
}

func modelHashModuleEdges(model application.Model) []modelHashModuleEdge {
	edges := model.ModuleEdges()
	result := make([]modelHashModuleEdge, len(edges))
	for index, edge := range edges {
		api := "default"
		if edge.API != "" {
			api = edge.API
		}
		result[index] = modelHashModuleEdge{
			FromModule:  edge.FromModule,
			ToModule:    edge.ToModule,
			API:         api,
			FromPackage: edge.FromPackage,
			ToPackage:   edge.ToPackage,
		}
	}
	return result
}

func modelHashEvents(topics []compilerevent.Topic) []modelHashEventTopic {
	result := make([]modelHashEventTopic, len(topics))
	for index, topic := range topics {
		item := modelHashEventTopic{
			ID:        topic.MarkerID,
			Provider:  topic.ProviderID,
			Module:    topic.Module,
			Publisher: topic.PublisherTypeID,
			Payload:   topic.PayloadTypeID,
		}
		for _, listener := range topic.Listeners() {
			item.Listeners = append(item.Listeners, modelHashEventListener{
				ID:       listener.MethodID,
				Provider: listener.ProviderID,
				Module:   listener.Module,
				Order:    listener.Order,
			})
		}
		result[index] = item
	}
	return result
}

func modelHashCaches(boundaries []compilercache.Boundary) []modelHashCache {
	result := make([]modelHashCache, len(boundaries))
	for index, boundary := range boundaries {
		result[index] = modelHashCache{
			Route:  boundary.RouteID,
			Name:   boundary.CacheName,
			Module: boundary.Module,
			Key:    boundary.KeyTypeID,
			Value:  boundary.ValueTypeID,
		}
	}
	return result
}

func modelHashTransactions(
	boundaries []compilertransaction.Boundary,
) []modelHashTransaction {
	result := make([]modelHashTransaction, len(boundaries))
	for index, boundary := range boundaries {
		result[index] = modelHashTransaction{
			Route:     boundary.RouteID,
			Manager:   boundary.ManagerProviderID,
			Module:    boundary.Module,
			Isolation: boundary.Isolation,
			ReadOnly:  boundary.ReadOnly,
		}
	}
	return result
}

func modelHashScheduleJobs(
	jobs []compilerschedule.Job,
	providerModules map[string]string,
) []modelHashScheduleJob {
	result := make([]modelHashScheduleJob, len(jobs))
	for index, job := range jobs {
		module := providerModules[job.ProviderID]
		if module == "" {
			module = job.Method.PackagePath
		}
		result[index] = modelHashScheduleJob{
			ID:              job.MethodID,
			Provider:        job.ProviderID,
			Module:          module,
			InitialDelay:    int64(job.InitialDelay),
			Delay:           int64(job.Delay),
			ContinueOnError: job.ContinueOnError,
		}
	}
	return result
}

func modelHashAsyncTasks(
	tasks []compilerasync.Task,
	providerModules map[string]string,
) []modelHashAsyncTask {
	result := make([]modelHashAsyncTask, len(tasks))
	for index, task := range tasks {
		item := modelHashAsyncTask{
			ID:           task.MethodID,
			Provider:     task.ProviderID,
			Module:       asyncTaskModule(task, providerModules),
			SubmitMethod: task.SubmitMethod,
		}
		for _, parameter := range task.Parameters() {
			item.Parameters = append(
				item.Parameters,
				modelHashAsyncParameter{
					Index: parameter.Index,
					Type:  parameter.TypeID,
				},
			)
		}
		result[index] = item
	}
	return result
}

func contentHash(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func summarizeTarget(target Target) TargetSummary {
	return TargetSummary{
		ID:                    target.ID,
		Layout:                target.Layout,
		ModulePath:            target.ModulePath,
		PackagePath:           target.PackagePath,
		EntrypointPackagePath: target.EntrypointPackagePath,
		OutputDir:             target.OutputDir,
		BridgeDir:             target.BridgeDir,
		ManifestPath:          target.ManifestPath,
	}
}

func packageByPath(packages []load.Package, packagePath string) (load.Package, bool) {
	for _, pkg := range packages {
		if pkg.Path == packagePath {
			return pkg, true
		}
	}
	return load.Package{}, false
}

func containsTarget(targets []application.Target, symbolID string) bool {
	for _, target := range targets {
		if target.SymbolID == symbolID {
			return true
		}
	}
	return false
}

func providerVariableNames(providers []provider.Provider) []string {
	result := make([]string, len(providers))
	baseCounts := make(map[string]int, len(providers))
	for index, item := range providers {
		baseCounts[providerVariableBase(item, index)]++
	}
	used := make(map[string]int, len(providers))
	for index, item := range providers {
		base := providerVariableBase(item, index)
		if baseCounts[base] > 1 {
			packageName := localGeneratedIdentifier(
				path.Base(item.PackagePath),
				"pkg",
			)
			base = packageName + exportedGeneratedIdentifier(base, "Bean")
		}
		used[base]++
		result[index] = base
		if used[base] > 1 {
			result[index] += strconv.Itoa(used[base])
		}
	}
	return result
}

func providerVariableBase(item provider.Provider, index int) string {
	return localGeneratedIdentifier(
		semanticProviderName(item),
		"provider"+strconv.Itoa(index),
	)
}

func semanticProviderName(item provider.Provider) string {
	name := item.Name
	if name == "" {
		name = item.Symbol.Name
	}
	if !item.ExplicitName && (name == "New" || name == "new") {
		if outputName := generatedTypeName(item.Output); outputName != "Interface" {
			return outputName
		}
	}
	if !item.ExplicitName &&
		(strings.HasPrefix(name, "New") ||
			strings.HasPrefix(name, "new")) &&
		len(name) > len("New") {
		trimmed := name[len("New"):]
		if token.IsIdentifier(trimmed) {
			name = trimmed
		}
	}
	return name
}

func localGeneratedIdentifier(name, fallback string) string {
	name = normalizeGeneratedIdentifier(name, fallback)
	name = lowerGeneratedInitialism(name)
	if !token.IsIdentifier(name) {
		return fallback
	}
	if generatedLocalReserved(name) {
		return name + "Bean"
	}
	return name
}

func lowerGeneratedInitialism(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return name
	}
	end := 1
	for end < len(runes) && unicode.IsUpper(runes[end]) {
		if end+1 < len(runes) && unicode.IsLower(runes[end+1]) {
			break
		}
		end++
	}
	for index := range end {
		runes[index] = unicode.ToLower(runes[index])
	}
	return string(runes)
}

func exportedGeneratedIdentifier(name, fallback string) string {
	name = normalizeGeneratedIdentifier(name, fallback)
	first, size := utf8.DecodeRuneInString(name)
	name = string(unicode.ToUpper(first)) + name[size:]
	if !token.IsIdentifier(name) || !token.IsExported(name) {
		return fallback
	}
	return name
}

func normalizeGeneratedIdentifier(name, fallback string) string {
	if token.IsIdentifier(name) {
		return name
	}
	var builder strings.Builder
	upperNext := false
	for _, character := range name {
		if unicode.IsLetter(character) ||
			character == '_' ||
			builder.Len() != 0 && unicode.IsDigit(character) {
			if upperNext {
				character = unicode.ToUpper(character)
				upperNext = false
			}
			builder.WriteRune(character)
			continue
		}
		upperNext = builder.Len() != 0
	}
	if normalized := builder.String(); token.IsIdentifier(normalized) {
		return normalized
	}
	return fallback
}

func generatedLocalReserved(name string) bool {
	switch name {
	case "application",
		"append",
		"authorizer",
		"cap",
		"clear",
		"close",
		"complex",
		"configurationSchema",
		"configurationSnapshot",
		"copy",
		"ctx",
		"delete",
		"dependencies",
		"error",
		"err",
		"false",
		"httpObservers",
		"imag",
		"len",
		"logger",
		"make",
		"managementMetrics",
		"max",
		"min",
		"new",
		"nil",
		"observers",
		"options",
		"panic",
		"print",
		"println",
		"real",
		"recover",
		"true":
		return true
	}
	return false
}

func generatedTypeName(value types.Type) string {
	switch typed := types.Unalias(value).(type) {
	case *types.Named:
		if typed.Obj() != nil {
			return typed.Obj().Name()
		}
	case *types.Pointer:
		return generatedTypeName(typed.Elem())
	}
	return "Interface"
}

func dependencyKey(providerID string, parameter int) string {
	return providerID + "\x00" + strconv.Itoa(parameter)
}

func providerRenderDiagnostic(
	applicationTarget application.Target,
	item provider.Provider,
	kind string,
	message string,
) Diagnostic {
	return Diagnostic{
		Position:         item.Position,
		PhysicalPosition: item.PhysicalPosition,
		TargetID:         applicationTarget.SymbolID,
		Kind:             kind,
		Message:          message,
	}
}

func targetDiagnostic(target application.Target, kind, message string) Diagnostic {
	return Diagnostic{
		Position:         target.Position,
		PhysicalPosition: target.PhysicalPosition,
		TargetID:         target.SymbolID,
		Kind:             kind,
		Message:          message,
	}
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.PhysicalPosition.Filename != right.PhysicalPosition.Filename {
			return left.PhysicalPosition.Filename < right.PhysicalPosition.Filename
		}
		if left.PhysicalPosition.Offset != right.PhysicalPosition.Offset {
			return left.PhysicalPosition.Offset < right.PhysicalPosition.Offset
		}
		if left.TargetID != right.TargetID {
			return left.TargetID < right.TargetID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Message < right.Message
	})
}
