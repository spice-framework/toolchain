// Package modulith discovers and validates Spice application-module metadata
// from the shared typed compiler program.
package modulith

import (
	"fmt"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

const dependencySeparator = "::"

var interfaceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Package identifies one selected Go package and its module assignment.
type Package struct {
	Path         string
	Name         string
	GoModulePath string
	Root         bool
}

// NamedInterface exposes one otherwise-internal module package under a stable
// interface name.
type NamedInterface struct {
	Name             string
	PackagePath      string
	Position         token.Position
	PhysicalPosition token.Position
}

// Dependency identifies one explicitly allowed target module API. An empty
// Interface selects the target module's root-package default API.
type Dependency struct {
	ModuleID         string
	Interface        string
	Position         token.Position
	PhysicalPosition token.Position
}

// String renders the portable dependency identity accepted by @Module.
func (d Dependency) String() string {
	if d.Interface == "" {
		return d.ModuleID
	}
	return d.ModuleID + dependencySeparator + d.Interface
}

// Module is one package-rooted application module. ID is always the root
// package's full Go import path.
type Module struct {
	ID               string
	RootPackage      string
	GoModulePath     string
	Position         token.Position
	PhysicalPosition token.Position
	packages         []Package
	namedInterfaces  []NamedInterface
	allowed          []Dependency
}

// Packages returns owned packages sorted by import path. The root package is
// marked Root and forms the module's default API.
func (m Module) Packages() []Package {
	return append([]Package(nil), m.packages...)
}

// NamedInterfaces returns explicit APIs sorted by name and package path.
func (m Module) NamedInterfaces() []NamedInterface {
	return append([]NamedInterface(nil), m.namedInterfaces...)
}

// AllowedDependencies returns exact allowed module APIs in stable order.
func (m Module) AllowedDependencies() []Dependency {
	return append([]Dependency(nil), m.allowed...)
}

// Diagnostic is one deterministic, source-positioned module-model failure.
type Diagnostic struct {
	Position         token.Position
	PhysicalPosition token.Position
	ModuleID         string
	PackagePath      string
	Kind             string
	Message          string
}

// Error renders a compiler-style diagnostic.
func (d Diagnostic) Error() string {
	position := d.Position
	if position.Filename == "" {
		position.Filename = "<unknown>"
	}
	if position.Line <= 0 {
		position.Line = 1
	}
	if position.Column <= 0 {
		position.Column = 1
	}
	return fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, d.Message)
}

// Model is the immutable-by-convention module discovery result.
type Model struct {
	modules      []Module
	unassigned   []Package
	diagnostics  []Diagnostic
	packageOwner map[string]int
}

// Modules returns modules sorted by full import-path identity.
func (m Model) Modules() []Module {
	result := make([]Module, len(m.modules))
	for index := range m.modules {
		result[index] = cloneModule(m.modules[index])
	}
	return result
}

// UnassignedPackages returns selected packages in a Go module containing at
// least one @Module root but not owned by any root.
func (m Model) UnassignedPackages() []Package {
	return append([]Package(nil), m.unassigned...)
}

// Diagnostics returns deterministic semantic discovery failures.
func (m Model) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), m.diagnostics...)
}

// Owner returns the module owning packagePath. Descendants belong to the
// longest matching root, so nested module roots are deterministic.
func (m Model) Owner(packagePath string) (Module, bool) {
	index, ok := m.packageOwner[packagePath]
	if !ok || index < 0 || index >= len(m.modules) {
		return Module{}, false
	}
	return cloneModule(m.modules[index]), true
}

// Build discovers module roots, package ownership, named interfaces, allowed
// dependency identities, and unassigned packages without reloading source.
func Build(program *load.Program, resolution resolve.Result) Model {
	if program == nil {
		return Model{diagnostics: []Diagnostic{{
			Kind:    "internal",
			Message: "module discovery requires a loaded program",
		}}}
	}
	if len(resolution.Diagnostics) != 0 {
		return Model{diagnostics: []Diagnostic{{
			Kind:    "resolution",
			Message: "module discovery requires annotation resolution without diagnostics",
		}}}
	}

	packages := packageIndex(program.Packages())
	model := Model{packageOwner: make(map[string]int)}
	model.modules, model.diagnostics = discoverModules(resolution.Occurrences, packages)
	assignPackages(&model, packages)
	discoverNamedInterfaces(&model, resolution.Occurrences, packages)
	validateDependencies(&model)
	sortModel(&model)
	rebuildPackageOwners(&model)
	return model
}

func packageIndex(packages []load.Package) map[string]load.Package {
	result := make(map[string]load.Package, len(packages))
	for _, pkg := range packages {
		result[pkg.Path] = pkg
	}
	return result
}

func discoverModules(
	occurrences []resolve.Occurrence,
	packages map[string]load.Package,
) ([]Module, []Diagnostic) {
	seen := make(map[string]resolve.Occurrence)
	var modules []Module
	var diagnostics []Diagnostic
	for _, occurrence := range occurrences {
		if occurrence.Annotation.Name != "Module" {
			continue
		}
		if occurrence.Target != annotation.TargetPackage {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"invalid-target",
				"@Module must appear in package documentation",
			))
			continue
		}
		if previous, duplicate := seen[occurrence.PackagePath]; duplicate {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"duplicate-module",
				fmt.Sprintf(
					"package %s declares @Module more than once; first declaration is at %s",
					occurrence.PackagePath,
					renderPosition(previous.DisplayPosition),
				),
			))
			continue
		}
		seen[occurrence.PackagePath] = occurrence
		pkg, ok := packages[occurrence.PackagePath]
		if !ok {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"missing-package",
				fmt.Sprintf("@Module package %s is not in the loaded program", occurrence.PackagePath),
			))
			continue
		}
		allowed, allowedDiagnostics := parseDependencies(occurrence)
		diagnostics = append(diagnostics, allowedDiagnostics...)
		modules = append(modules, Module{
			ID:               pkg.Path,
			RootPackage:      pkg.Path,
			GoModulePath:     pkg.ModulePath,
			Position:         occurrence.DisplayPosition,
			PhysicalPosition: physicalPosition(occurrence),
			allowed:          allowed,
		})
	}
	sort.SliceStable(modules, func(i, j int) bool {
		return modules[i].ID < modules[j].ID
	})
	return modules, diagnostics
}

func parseDependencies(occurrence resolve.Occurrence) ([]Dependency, []Diagnostic) {
	var value annotation.Value
	found := false
	for _, argument := range occurrence.Annotation.Arguments {
		if argument.Name != "allowedDependencies" {
			continue
		}
		if found {
			return nil, []Diagnostic{occurrenceDiagnostic(
				occurrence,
				"duplicate-argument",
				"@Module assigns allowedDependencies more than once",
			)}
		}
		found = true
		value = argument.Value
	}
	if !found {
		return nil, nil
	}
	if value.Kind != annotation.KindList {
		return nil, []Diagnostic{occurrenceDiagnostic(
			occurrence,
			"dependency-kind",
			"@Module allowedDependencies must be a list of strings",
		)}
	}

	seen := make(map[string]struct{})
	var dependencies []Dependency
	var diagnostics []Diagnostic
	for index, item := range value.List {
		if item.Kind != annotation.KindString {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"dependency-kind",
				fmt.Sprintf("@Module allowedDependencies item %d must be a string", index),
			))
			continue
		}
		dependency, err := parseDependency(item.String, occurrence)
		if err != nil {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"dependency-identity",
				fmt.Sprintf("@Module allowed dependency %q is invalid: %v", item.String, err),
			))
			continue
		}
		if _, duplicate := seen[dependency.String()]; duplicate {
			diagnostics = append(diagnostics, occurrenceDiagnostic(
				occurrence,
				"duplicate-dependency",
				fmt.Sprintf("@Module allowed dependency %q is declared more than once", dependency.String()),
			))
			continue
		}
		seen[dependency.String()] = struct{}{}
		dependencies = append(dependencies, dependency)
	}
	sortDependencies(dependencies)
	return dependencies, diagnostics
}

func parseDependency(value string, occurrence resolve.Occurrence) (Dependency, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return Dependency{}, fmt.Errorf("identity must be non-empty and contain no surrounding whitespace")
	}
	if strings.Count(value, dependencySeparator) > 1 {
		return Dependency{}, fmt.Errorf("identity may contain at most one %q separator", dependencySeparator)
	}
	moduleID, interfaceName, hasInterface := strings.Cut(value, dependencySeparator)
	if moduleID == "" {
		return Dependency{}, fmt.Errorf("module import path is required")
	}
	if hasInterface && !interfaceNamePattern.MatchString(interfaceName) {
		return Dependency{}, fmt.Errorf(
			"named interface must match %s",
			interfaceNamePattern,
		)
	}
	return Dependency{
		ModuleID:         moduleID,
		Interface:        interfaceName,
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: physicalPosition(occurrence),
	}, nil
}

func assignPackages(model *Model, packages map[string]load.Package) {
	modulePaths := make(map[string]struct{}, len(model.modules))
	paths := make([]string, 0, len(packages))
	for _, module := range model.modules {
		modulePaths[module.GoModulePath] = struct{}{}
	}
	for packagePath := range packages {
		paths = append(paths, packagePath)
	}
	sort.Strings(paths)
	for _, packagePath := range paths {
		pkg := packages[packagePath]
		owner := ownerIndex(model.modules, pkg)
		item := Package{
			Path:         pkg.Path,
			Name:         pkg.Name,
			GoModulePath: pkg.ModulePath,
			Root:         owner >= 0 && model.modules[owner].RootPackage == pkg.Path,
		}
		if owner >= 0 {
			model.modules[owner].packages = append(model.modules[owner].packages, item)
			model.packageOwner[pkg.Path] = owner
		} else if _, sameGoModule := modulePaths[pkg.ModulePath]; sameGoModule {
			model.unassigned = append(model.unassigned, item)
		}
	}
}

func ownerIndex(modules []Module, pkg load.Package) int {
	owner := -1
	for index, module := range modules {
		if module.GoModulePath != pkg.ModulePath ||
			(pkg.Path != module.RootPackage && !strings.HasPrefix(pkg.Path, module.RootPackage+"/")) {
			continue
		}
		if owner == -1 || len(module.RootPackage) > len(modules[owner].RootPackage) {
			owner = index
		}
	}
	return owner
}

func discoverNamedInterfaces(
	model *Model,
	occurrences []resolve.Occurrence,
	packages map[string]load.Package,
) {
	seen := make(map[string]resolve.Occurrence)
	for _, occurrence := range occurrences {
		if occurrence.Annotation.Name != "NamedInterface" {
			continue
		}
		name, ok := namedInterfaceName(occurrence, &model.diagnostics)
		if !ok {
			continue
		}
		pkg, exists := packages[occurrence.PackagePath]
		if occurrence.Target != annotation.TargetPackage || !exists {
			model.diagnostics = append(model.diagnostics, occurrenceDiagnostic(
				occurrence,
				"invalid-target",
				"@NamedInterface must appear in package documentation for a loaded package",
			))
			continue
		}
		owner := ownerIndex(model.modules, pkg)
		if owner < 0 {
			model.diagnostics = append(model.diagnostics, occurrenceDiagnostic(
				occurrence,
				"unassigned-interface",
				fmt.Sprintf(
					"@NamedInterface %q package %s is not inside an @Module root",
					name,
					pkg.Path,
				),
			))
			continue
		}
		key := model.modules[owner].ID + "\x00" + name
		if previous, duplicate := seen[key]; duplicate {
			model.diagnostics = append(model.diagnostics, occurrenceDiagnostic(
				occurrence,
				"duplicate-interface",
				fmt.Sprintf(
					"module %s declares named interface %q more than once; first declaration is at %s",
					model.modules[owner].ID,
					name,
					renderPosition(previous.DisplayPosition),
				),
			))
			continue
		}
		seen[key] = occurrence
		model.modules[owner].namedInterfaces = append(
			model.modules[owner].namedInterfaces,
			NamedInterface{
				Name:             name,
				PackagePath:      pkg.Path,
				Position:         occurrence.DisplayPosition,
				PhysicalPosition: physicalPosition(occurrence),
			},
		)
	}
}

func namedInterfaceName(occurrence resolve.Occurrence, diagnostics *[]Diagnostic) (string, bool) {
	var names []string
	for _, argument := range occurrence.Annotation.Arguments {
		if argument.Name != "" && argument.Name != "name" {
			continue
		}
		if argument.Value.Kind != annotation.KindString {
			*diagnostics = append(*diagnostics, occurrenceDiagnostic(
				occurrence,
				"interface-kind",
				"@NamedInterface name must be a string",
			))
			return "", false
		}
		names = append(names, argument.Value.String)
	}
	if len(names) != 1 {
		*diagnostics = append(*diagnostics, occurrenceDiagnostic(
			occurrence,
			"interface-name",
			"@NamedInterface requires exactly one name",
		))
		return "", false
	}
	if !interfaceNamePattern.MatchString(names[0]) {
		*diagnostics = append(*diagnostics, occurrenceDiagnostic(
			occurrence,
			"interface-name",
			fmt.Sprintf("@NamedInterface name %q must match %s", names[0], interfaceNamePattern),
		))
		return "", false
	}
	return names[0], true
}

func validateDependencies(model *Model) {
	modules := make(map[string]Module, len(model.modules))
	for _, module := range model.modules {
		modules[module.ID] = module
	}
	for moduleIndex := range model.modules {
		module := &model.modules[moduleIndex]
		for _, dependency := range module.allowed {
			target, ok := modules[dependency.ModuleID]
			switch {
			case dependency.ModuleID == module.ID:
				model.diagnostics = append(model.diagnostics, dependencyDiagnostic(
					*module,
					dependency,
					"self-dependency",
					fmt.Sprintf("module %s must not declare a dependency on itself", module.ID),
				))
			case !ok:
				model.diagnostics = append(model.diagnostics, dependencyDiagnostic(
					*module,
					dependency,
					"unknown-module",
					fmt.Sprintf(
						"module %s allows unknown module %s; use an exact @Module root import path",
						module.ID,
						dependency.ModuleID,
					),
				))
			case dependency.Interface != "" && !hasNamedInterface(target, dependency.Interface):
				model.diagnostics = append(model.diagnostics, dependencyDiagnostic(
					*module,
					dependency,
					"unknown-interface",
					fmt.Sprintf(
						"module %s allows unknown interface %s on module %s",
						module.ID,
						dependency.Interface,
						target.ID,
					),
				))
			}
		}
	}
}

func hasNamedInterface(module Module, name string) bool {
	for _, item := range module.namedInterfaces {
		if item.Name == name {
			return true
		}
	}
	return false
}

func sortModel(model *Model) {
	for index := range model.modules {
		module := &model.modules[index]
		sort.SliceStable(module.packages, func(i, j int) bool {
			return module.packages[i].Path < module.packages[j].Path
		})
		sort.SliceStable(module.namedInterfaces, func(i, j int) bool {
			if module.namedInterfaces[i].Name != module.namedInterfaces[j].Name {
				return module.namedInterfaces[i].Name < module.namedInterfaces[j].Name
			}
			return module.namedInterfaces[i].PackagePath < module.namedInterfaces[j].PackagePath
		})
		sortDependencies(module.allowed)
	}
	sort.SliceStable(model.modules, func(i, j int) bool {
		return model.modules[i].ID < model.modules[j].ID
	})
	sort.SliceStable(model.unassigned, func(i, j int) bool {
		return model.unassigned[i].Path < model.unassigned[j].Path
	})
	sort.SliceStable(model.diagnostics, func(i, j int) bool {
		left, right := model.diagnostics[i], model.diagnostics[j]
		if left.PhysicalPosition.Filename != right.PhysicalPosition.Filename {
			return left.PhysicalPosition.Filename < right.PhysicalPosition.Filename
		}
		if left.PhysicalPosition.Offset != right.PhysicalPosition.Offset {
			return left.PhysicalPosition.Offset < right.PhysicalPosition.Offset
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Message < right.Message
	})
}

func rebuildPackageOwners(model *Model) {
	model.packageOwner = make(map[string]int)
	for moduleIndex, module := range model.modules {
		for _, pkg := range module.packages {
			model.packageOwner[pkg.Path] = moduleIndex
		}
	}
}

func sortDependencies(dependencies []Dependency) {
	sort.SliceStable(dependencies, func(i, j int) bool {
		if dependencies[i].ModuleID != dependencies[j].ModuleID {
			return dependencies[i].ModuleID < dependencies[j].ModuleID
		}
		return dependencies[i].Interface < dependencies[j].Interface
	})
}

func cloneModule(module Module) Module {
	result := module
	result.packages = append([]Package(nil), module.packages...)
	result.namedInterfaces = append([]NamedInterface(nil), module.namedInterfaces...)
	result.allowed = append([]Dependency(nil), module.allowed...)
	return result
}

func occurrenceDiagnostic(occurrence resolve.Occurrence, kind, message string) Diagnostic {
	return Diagnostic{
		Position:         occurrence.DisplayPosition,
		PhysicalPosition: physicalPosition(occurrence),
		ModuleID:         occurrence.PackagePath,
		PackagePath:      occurrence.PackagePath,
		Kind:             kind,
		Message:          message,
	}
}

func dependencyDiagnostic(module Module, dependency Dependency, kind, message string) Diagnostic {
	return Diagnostic{
		Position:         dependency.Position,
		PhysicalPosition: dependency.PhysicalPosition,
		ModuleID:         module.ID,
		PackagePath:      module.RootPackage,
		Kind:             kind,
		Message:          message,
	}
}

func physicalPosition(occurrence resolve.Occurrence) token.Position {
	return token.Position{
		Filename: occurrence.PhysicalFile,
		Offset:   occurrence.PhysicalOffset,
	}
}

func renderPosition(position token.Position) string {
	if position.Filename == "" {
		return "<unknown>:1:1"
	}
	line := position.Line
	if line <= 0 {
		line = 1
	}
	column := position.Column
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", position.Filename, line, column)
}
