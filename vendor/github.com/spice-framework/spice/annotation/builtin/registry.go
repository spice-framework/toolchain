// Package builtin defines the annotation metadata shipped with Spice.
package builtin

import "github.com/spice-framework/spice/annotation"

// Registry returns a fresh immutable-by-construction registry of built-in annotations.
func Registry() annotation.Registry {
	return annotation.MustRegistry(
		annotation.Definition{Name: "Application", Targets: annotation.Targets(annotation.TargetFunction)},
		annotation.Definition{Name: "Bean", Targets: annotation.Targets(annotation.TargetFunction, annotation.TargetMethod)},
		annotation.Definition{Name: "Component", Targets: annotation.Targets(annotation.TargetType)},
		annotation.Definition{
			Name:    "async.Execute",
			Targets: annotation.Targets(annotation.TargetMethod),
		},
		annotation.Definition{
			Name:    "cache.Cacheable",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:     "name",
					Kinds:    []annotation.Kind{annotation.KindString},
					Required: true,
				},
			},
		},
		annotation.Definition{
			Name:    "Configuration",
			Targets: annotation.Targets(annotation.TargetType),
		},
		annotation.Definition{
			Name:    "ConfigurationProperties",
			Targets: annotation.Targets(annotation.TargetType),
			Arguments: []annotation.ArgumentDefinition{
				{Name: "prefix", Kinds: []annotation.Kind{annotation.KindString}},
			},
		},
		annotation.Definition{Name: "Enum", Targets: annotation.Targets(annotation.TargetType)},
		annotation.Definition{
			Name:    "Controller",
			Targets: annotation.Targets(annotation.TargetType),
			Arguments: []annotation.ArgumentDefinition{
				{Name: "prefix", Kinds: []annotation.Kind{annotation.KindString}},
			},
		},
		annotation.Definition{
			Name:    "data.Transactional",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:  "isolation",
					Kinds: []annotation.Kind{annotation.KindString},
				},
				{
					Name:  "readOnly",
					Kinds: []annotation.Kind{annotation.KindBoolean},
				},
			},
		},
		annotation.Definition{
			Name:    "event.Listener",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:  "order",
					Kinds: []annotation.Kind{annotation.KindInteger},
				},
			},
		},
		annotation.Definition{
			Name:    "event.Topic",
			Targets: annotation.Targets(annotation.TargetType, annotation.TargetFunction),
		},
		annotation.Definition{
			Name:    "Get",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{Name: "path", Kinds: []annotation.Kind{annotation.KindString}, Required: true, Positional: true},
			},
		},
		annotation.Definition{
			Name:    "Module",
			Targets: annotation.Targets(annotation.TargetPackage),
			Arguments: []annotation.ArgumentDefinition{
				{Name: "allowedDependencies", Kinds: []annotation.Kind{annotation.KindList}},
			},
		},
		annotation.Definition{
			Name:       "NamedInterface",
			Targets:    annotation.Targets(annotation.TargetPackage),
			Repeatable: true,
			Arguments: []annotation.ArgumentDefinition{
				{Name: "name", Kinds: []annotation.Kind{annotation.KindString}, Required: true, Positional: true},
			},
		},
		annotation.Definition{Name: "OnStart", Targets: annotation.Targets(annotation.TargetMethod)},
		annotation.Definition{Name: "OnStop", Targets: annotation.Targets(annotation.TargetMethod)},
		annotation.Definition{
			Name:    "management.Enable",
			Targets: annotation.Targets(annotation.TargetFunction),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:             "expose",
					Kinds:            []annotation.Kind{annotation.KindList},
					ListElementKinds: []annotation.Kind{annotation.KindString},
					Required:         true,
				},
			},
		},
		annotation.Definition{
			Name:    "observability.Logging",
			Targets: annotation.Targets(annotation.TargetFunction),
		},
		annotation.Definition{
			Name:    "Post",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{Name: "path", Kinds: []annotation.Kind{annotation.KindString}, Required: true, Positional: true},
			},
		},
		annotation.Definition{
			Name:    "security.Authorize",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:  "authenticated",
					Kinds: []annotation.Kind{annotation.KindBoolean},
				},
				{
					Name:             "anyRoles",
					Kinds:            []annotation.Kind{annotation.KindList},
					ListElementKinds: []annotation.Kind{annotation.KindString},
				},
				{
					Name:             "allRoles",
					Kinds:            []annotation.Kind{annotation.KindList},
					ListElementKinds: []annotation.Kind{annotation.KindString},
				},
				{
					Name:             "allScopes",
					Kinds:            []annotation.Kind{annotation.KindList},
					ListElementKinds: []annotation.Kind{annotation.KindString},
				},
			},
		},
		annotation.Definition{
			Name:    "schedule.FixedDelay",
			Targets: annotation.Targets(annotation.TargetMethod),
			Arguments: []annotation.ArgumentDefinition{
				{
					Name:     "delay",
					Kinds:    []annotation.Kind{annotation.KindString},
					Required: true,
				},
				{
					Name:  "initialDelay",
					Kinds: []annotation.Kind{annotation.KindString},
				},
				{
					Name:  "continueOnError",
					Kinds: []annotation.Kind{annotation.KindBoolean},
				},
			},
		},
		annotation.Definition{Name: "Service", Targets: annotation.Targets(annotation.TargetType)},
	)
}
