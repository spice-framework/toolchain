package service

import (
	"slices"

	"github.com/StevenBuglione/spice/compiler/diagnostic"
)

func cloneProviderGraph(graph ProviderGraph) ProviderGraph {
	result := ProviderGraph{
		Providers: make([]Provider, len(graph.Providers)),
		Edges:     slices.Clone(graph.Edges),
	}
	for index, item := range graph.Providers {
		result.Providers[index] = item
		result.Providers[index].Dependencies = slices.Clone(item.Dependencies)
	}
	return result
}

func cloneModuleGraph(graph ModuleGraph) ModuleGraph {
	result := ModuleGraph{
		Modules:            make([]Module, len(graph.Modules)),
		Edges:              slices.Clone(graph.Edges),
		UnassignedPackages: slices.Clone(graph.UnassignedPackages),
	}
	for index, item := range graph.Modules {
		result.Modules[index] = item
		result.Modules[index].Packages = slices.Clone(item.Packages)
		result.Modules[index].NamedInterfaces = slices.Clone(
			item.NamedInterfaces,
		)
		result.Modules[index].AllowedDependencies = slices.Clone(
			item.AllowedDependencies,
		)
	}
	return result
}

func cloneConfigurations(items []Configuration) []Configuration {
	result := make([]Configuration, len(items))
	for index, item := range items {
		result[index] = item
		result[index].Fields = slices.Clone(item.Fields)
	}
	return result
}

func cloneDefinitions(items []AnnotationDefinition) []AnnotationDefinition {
	result := make([]AnnotationDefinition, len(items))
	for index, item := range items {
		result[index] = item
		result[index].Targets = slices.Clone(item.Targets)
		result[index].Arguments = make(
			[]AnnotationArgument,
			len(item.Arguments),
		)
		result[index].Examples = slices.Clone(item.Examples)
		for argumentIndex, argument := range item.Arguments {
			result[index].Arguments[argumentIndex] = argument
			result[index].Arguments[argumentIndex].Kinds = slices.Clone(
				argument.Kinds,
			)
			result[index].Arguments[argumentIndex].ListElementKinds = slices.Clone(argument.ListElementKinds)
			result[index].Arguments[argumentIndex].AllowedStrings = slices.Clone(argument.AllowedStrings)
		}
	}
	return result
}

func cloneActions(items []diagnostic.SuggestedFix) []diagnostic.SuggestedFix {
	result := make([]diagnostic.SuggestedFix, len(items))
	for index, item := range items {
		result[index] = item
		result[index].Edits = make(
			[]diagnostic.TextEdit,
			len(item.Edits),
		)
		for editIndex, edit := range item.Edits {
			result[index].Edits[editIndex] = edit
			if edit.DocumentVersion != nil {
				version := *edit.DocumentVersion
				result[index].Edits[editIndex].DocumentVersion = &version
			}
			if edit.Location.Display != nil {
				display := *edit.Location.Display
				result[index].Edits[editIndex].Location.Display = &display
			}
		}
	}
	return result
}

func cloneResult(result Result) Result {
	result.diagnostics = diagnostic.NewSet(result.diagnostics.Items()...)
	result.annotations = slices.Clone(result.annotations)
	result.providerGraph = cloneProviderGraph(result.providerGraph)
	result.moduleGraph = cloneModuleGraph(result.moduleGraph)
	result.configurations = cloneConfigurations(result.configurations)
	result.definitions = cloneDefinitions(result.definitions)
	result.actions = cloneActions(result.actions)
	return result
}
