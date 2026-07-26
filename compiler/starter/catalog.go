// Package starter adapts explicitly supplied public starter manifests into
// compiler annotation and application-bootstrap metadata.
package starter

import (
	"fmt"
	"runtime"
	"sort"

	"github.com/StevenBuglione/spice/annotation"
	compilerbootstrap "github.com/StevenBuglione/spice/compiler/bootstrap"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

// Catalog is an immutable, deterministic set of compatible starter manifests
// and their compiler contributions.
type Catalog struct {
	manifests            []publicstarter.Manifest
	definitions          []annotation.Definition
	bootstrapDefinitions []compilerbootstrap.Definition
	entryPoints          map[string][]compilerbootstrap.EntryPoint
}

// New validates manifests against the current Spice API and Go runtime.
func New(manifests ...publicstarter.Manifest) (Catalog, error) {
	return NewWithCompatibility(publicstarter.APIVersion, runtime.Version(), manifests...)
}

// NewWithCompatibility validates, normalizes, and indexes an explicitly
// supplied manifest set. It exists so build tools and tests can make the
// compatibility environment deterministic.
func NewWithCompatibility(
	spiceAPI string,
	goVersion string,
	manifests ...publicstarter.Manifest,
) (Catalog, error) {
	if len(manifests) == 0 {
		return Catalog{}, fmt.Errorf("starter catalog requires at least one manifest")
	}

	normalized := append([]publicstarter.Manifest(nil), manifests...)
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].Spec().ID < normalized[j].Spec().ID
	})

	result := Catalog{
		manifests:   normalized,
		entryPoints: make(map[string][]compilerbootstrap.EntryPoint),
	}
	manifestIDs := make(map[string]struct{}, len(normalized))
	annotationSources := make(map[string]string)
	capabilitySources := make(map[compilerbootstrap.Capability]string)
	for _, definition := range compilerbootstrap.Builtins() {
		capabilitySources[definition.Capability] = "Spice built-in"
	}
	for _, manifest := range normalized {
		if err := manifest.Compatible(spiceAPI, goVersion); err != nil {
			return Catalog{}, fmt.Errorf("compose starter catalog: %w", err)
		}
		spec := manifest.Spec()
		if _, duplicate := manifestIDs[spec.ID]; duplicate {
			return Catalog{}, fmt.Errorf(
				"compose starter catalog: duplicate starter manifest %q",
				spec.ID,
			)
		}
		manifestIDs[spec.ID] = struct{}{}

		for _, definition := range manifest.Definitions() {
			if source, duplicate := annotationSources[definition.Name]; duplicate {
				return Catalog{}, fmt.Errorf(
					"compose starter catalog: annotation @%s is contributed by both %q and %q",
					definition.Name,
					source,
					spec.ID,
				)
			}
			annotationSources[definition.Name] = spec.ID
			result.definitions = append(result.definitions, definition)
		}

		for _, feature := range spec.ApplicationFeatures {
			entryPoints := bootstrapEntryPoints(feature.EntryPoints)
			capability := compilerbootstrap.Capability(feature.Capability)
			if source, duplicate := capabilitySources[capability]; duplicate {
				return Catalog{}, fmt.Errorf(
					"compose starter catalog: capability %q is contributed by both %q and %q",
					capability,
					source,
					spec.ID,
				)
			}
			capabilitySources[capability] = spec.ID
			result.bootstrapDefinitions = append(
				result.bootstrapDefinitions,
				bootstrapDefinition(spec, feature, entryPoints),
			)
			result.entryPoints[feature.Annotation] = append(
				[]compilerbootstrap.EntryPoint(nil),
				entryPoints...,
			)
		}
	}

	sort.SliceStable(result.definitions, func(i, j int) bool {
		return result.definitions[i].Name < result.definitions[j].Name
	})
	sort.SliceStable(result.bootstrapDefinitions, func(i, j int) bool {
		left, right := result.bootstrapDefinitions[i], result.bootstrapDefinitions[j]
		if left.Capability != right.Capability {
			return left.Capability < right.Capability
		}
		return left.Annotation < right.Annotation
	})
	return result, nil
}

// Registry returns base plus every contributed annotation definition. A
// collision with a built-in or another base definition fails closed.
func (catalog Catalog) Registry(base annotation.Registry) (annotation.Registry, error) {
	definitions := base.Definitions()
	definitions = append(definitions, catalog.definitions...)
	registry, err := annotation.NewRegistry(definitions...)
	if err != nil {
		return annotation.Registry{}, fmt.Errorf("compose starter annotation registry: %w", err)
	}
	return registry, nil
}

// Manifests returns compatible manifests in stable identity order.
func (catalog Catalog) Manifests() []publicstarter.Manifest {
	return append([]publicstarter.Manifest(nil), catalog.manifests...)
}

// BootstrapDefinitions returns fresh feature definitions suitable for
// application.BuildOptions.
func (catalog Catalog) BootstrapDefinitions() []compilerbootstrap.Definition {
	return cloneBootstrapDefinitions(catalog.bootstrapDefinitions)
}

// EntryPoints returns the explicit starter constructors associated with a
// contributed application annotation.
func (catalog Catalog) EntryPoints(annotationName string) []compilerbootstrap.EntryPoint {
	return append(
		[]compilerbootstrap.EntryPoint(nil),
		catalog.entryPoints[annotationName]...,
	)
}

func bootstrapDefinition(
	spec publicstarter.Spec,
	feature publicstarter.FeatureSpec,
	entryPoints []compilerbootstrap.EntryPoint,
) compilerbootstrap.Definition {
	options := make([]compilerbootstrap.OptionDefinition, len(feature.Options))
	for index, option := range feature.Options {
		options[index] = compilerbootstrap.OptionDefinition{
			Name:           option.Name,
			Kind:           option.Kind,
			ListItemKinds:  append([]annotation.Kind(nil), option.ListItemKinds...),
			AllowedStrings: append([]string(nil), option.AllowedStrings...),
			Required:       option.Required,
			UniqueItems:    option.UniqueItems,
			MinimumItems:   option.MinimumItems,
			SortItems:      option.SortItems,
		}
	}
	requirements := make([]compilerbootstrap.RuntimeCapability, len(feature.Requirements))
	for index, requirement := range feature.Requirements {
		requirements[index] = compilerbootstrap.RuntimeCapability(requirement)
	}
	return compilerbootstrap.Definition{
		Annotation:    feature.Annotation,
		Capability:    compilerbootstrap.Capability(feature.Capability),
		Options:       options,
		Requirements:  requirements,
		SourceID:      spec.ID,
		SourceVersion: spec.Version,
		EntryPoints:   append([]compilerbootstrap.EntryPoint(nil), entryPoints...),
	}
}

func bootstrapEntryPoints(values []publicstarter.EntryPoint) []compilerbootstrap.EntryPoint {
	result := make([]compilerbootstrap.EntryPoint, len(values))
	for index, value := range values {
		result[index] = compilerbootstrap.EntryPoint{
			Package: value.Package,
			Symbol:  value.Symbol,
		}
	}
	return result
}

func cloneBootstrapDefinitions(
	definitions []compilerbootstrap.Definition,
) []compilerbootstrap.Definition {
	result := make([]compilerbootstrap.Definition, len(definitions))
	for index, definition := range definitions {
		result[index] = definition
		result[index].Options = make([]compilerbootstrap.OptionDefinition, len(definition.Options))
		for optionIndex, option := range definition.Options {
			result[index].Options[optionIndex] = option
			result[index].Options[optionIndex].ListItemKinds = append(
				[]annotation.Kind(nil),
				option.ListItemKinds...,
			)
			result[index].Options[optionIndex].AllowedStrings = append(
				[]string(nil),
				option.AllowedStrings...,
			)
		}
		result[index].Requirements = append(
			[]compilerbootstrap.RuntimeCapability(nil),
			definition.Requirements...,
		)
		result[index].EntryPoints = append(
			[]compilerbootstrap.EntryPoint(nil),
			definition.EntryPoints...,
		)
	}
	return result
}
