package service

import (
	"go/token"
	"path/filepath"
	"slices"
	"sort"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func summarizeAnnotations(
	workspaceRoot string,
	resolution resolve.Result,
) []Annotation {
	result := make([]Annotation, len(resolution.Occurrences))
	for index, occurrence := range resolution.Occurrences {
		physical := occurrence.PhysicalPosition
		if physical.Filename == "" {
			physical = token.Position{
				Filename: occurrence.PhysicalFile,
				Offset:   occurrence.PhysicalOffset,
			}
		}
		result[index] = Annotation{
			Name:        occurrence.Annotation.Name,
			Raw:         occurrence.Annotation.Raw,
			Target:      occurrence.Target,
			Declaration: occurrence.Name,
			SymbolID:    occurrence.SymbolID,
			PackagePath: occurrence.PackagePath,
			Location: diagnostic.SourceMappedLocation(
				workspaceRoot,
				occurrence.DisplayPosition.Filename,
				physical.Filename,
				occurrence.DisplayPosition.Line,
				occurrence.DisplayPosition.Column,
				occurrence.DisplayPosition.Offset,
				physical.Line,
				physical.Column,
				physical.Offset,
			),
		}
	}
	return result
}

func summarizeProviderGraph(model application.Model) ProviderGraph {
	providers := model.Providers()
	result := ProviderGraph{
		Providers: make([]Provider, len(providers)),
	}
	for index, item := range providers {
		dependencies := make(
			[]ProviderDependency,
			len(item.Dependencies),
		)
		for dependencyIndex, dependency := range item.Dependencies {
			dependencies[dependencyIndex] = ProviderDependency{
				Index:  dependency.Index,
				Name:   dependency.Name,
				TypeID: dependency.TypeID,
			}
		}
		result.Providers[index] = Provider{
			ID:             item.SymbolID,
			Name:           item.Name,
			PackagePath:    item.PackagePath,
			OutputTypeID:   item.OutputTypeID,
			Source:         item.Source,
			Dependencies:   dependencies,
			ReturnsCleanup: item.ReturnsCleanup,
			ReturnsError:   item.ReturnsError,
		}
	}
	edges := model.Edges()
	result.Edges = make([]ProviderEdge, len(edges))
	for index, edge := range edges {
		result.Edges[index] = ProviderEdge{
			ConsumerID:     edge.ConsumerID,
			DependencyID:   edge.DependencyID,
			RequiredTypeID: edge.RequiredTypeID,
			ParameterIndex: edge.ParameterIndex,
			ParameterName:  edge.ParameterName,
		}
	}
	return result
}

func summarizeModuleGraph(model modulith.Model) ModuleGraph {
	modules := model.Modules()
	result := ModuleGraph{Modules: make([]Module, len(modules))}
	for index, item := range modules {
		packages := item.Packages()
		packagePaths := make([]string, len(packages))
		for packageIndex, ownedPackage := range packages {
			packagePaths[packageIndex] = ownedPackage.Path
		}
		interfaces := item.NamedInterfaces()
		named := make([]NamedInterface, len(interfaces))
		for interfaceIndex, namedInterface := range interfaces {
			named[interfaceIndex] = NamedInterface{
				Name:        namedInterface.Name,
				PackagePath: namedInterface.PackagePath,
			}
		}
		allowedItems := item.AllowedDependencies()
		allowed := make([]ModuleDependency, len(allowedItems))
		for dependencyIndex, dependency := range allowedItems {
			allowed[dependencyIndex] = ModuleDependency{
				ModuleID: dependency.ModuleID,
				API:      dependency.Interface,
			}
		}
		result.Modules[index] = Module{
			ID:                  item.ID,
			RootPackage:         item.RootPackage,
			Packages:            packagePaths,
			NamedInterfaces:     named,
			AllowedDependencies: allowed,
		}
	}
	edges := model.Edges()
	result.Edges = make([]ModuleEdge, len(edges))
	for index, edge := range edges {
		result.Edges[index] = ModuleEdge{
			FromModule:  edge.FromModule,
			ToModule:    edge.ToModule,
			FromPackage: edge.FromPackage,
			ToPackage:   edge.ToPackage,
			API:         edge.API,
			Exported:    edge.Exported,
			Allowed:     edge.Allowed,
		}
	}
	unassigned := model.UnassignedPackages()
	result.UnassignedPackages = make([]string, len(unassigned))
	for index, item := range unassigned {
		result.UnassignedPackages[index] = item.Path
	}
	return result
}

func summarizeConfigurations(model application.Model) []Configuration {
	items := model.Configurations()
	result := make([]Configuration, len(items))
	for index, item := range items {
		fields := item.Fields()
		summaries := make([]ConfigurationField, len(fields))
		for fieldIndex, field := range fields {
			defaultValue := field.Default
			if field.Secret {
				defaultValue = ""
			}
			summaries[fieldIndex] = ConfigurationField{
				Name:        field.Name,
				Key:         field.Key,
				TypeID:      field.TypeID,
				Environment: field.Environment,
				Default:     defaultValue,
				HasDefault:  field.HasDefault,
				Required:    field.Required,
				Secret:      field.Secret,
			}
		}
		result[index] = Configuration{
			SymbolID:    item.SymbolID,
			Name:        item.Name,
			PackagePath: item.PackagePath,
			TypeID:      item.TypeID,
			Prefix:      item.Prefix,
			Module:      item.Module,
			Fields:      summaries,
		}
	}
	return result
}

func summarizeDefinitions(
	registry annotation.Registry,
) []AnnotationDefinition {
	items := registry.Definitions()
	result := make([]AnnotationDefinition, len(items))
	for index, item := range items {
		arguments := make([]AnnotationArgument, len(item.Arguments))
		for argumentIndex, argument := range item.Arguments {
			arguments[argumentIndex] = AnnotationArgument{
				Name:             argument.Name,
				Kinds:            slices.Clone(argument.Kinds),
				ListElementKinds: slices.Clone(argument.ListElementKinds),
				Required:         argument.Required,
				Positional:       argument.Positional,
			}
		}
		result[index] = AnnotationDefinition{
			Name:       item.Name,
			Targets:    item.Targets.Values(),
			Repeatable: item.Repeatable,
			Arguments:  arguments,
		}
	}
	return result
}

func actionsFromDiagnostics(
	set diagnostic.Set,
) []diagnostic.SuggestedFix {
	var result []diagnostic.SuggestedFix
	for _, item := range set.Items() {
		result = append(result, item.Fixes...)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Title < result[right].Title
	})
	return cloneActions(result)
}

func versionDiagnostics(
	set diagnostic.Set,
	overlay map[string]Document,
) diagnostic.Set {
	items := set.Items()
	for itemIndex := range items {
		for fixIndex := range items[itemIndex].Fixes {
			for editIndex := range items[itemIndex].Fixes[fixIndex].Edits {
				edit := &items[itemIndex].Fixes[fixIndex].Edits[editIndex]
				if edit.DocumentVersion != nil {
					continue
				}
				nativePath := filepathFromSlash(edit.Location.Path)
				document, found := overlay[nativePath]
				if !found {
					continue
				}
				version := document.Version
				edit.DocumentVersion = &version
			}
		}
	}
	return diagnostic.NewSet(items...)
}

func filepathFromSlash(value string) string {
	return filepath.Clean(filepath.FromSlash(value))
}
