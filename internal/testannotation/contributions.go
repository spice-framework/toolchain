// Package testannotation adapts raw parser fixtures to typed SDK semantics.
//
// Production compiler analysis never imports this package. Product
// invocations resolve explicit descriptors and receive contributions from an
// authorized Go tool. Older model unit tests intentionally exercise malformed
// annotation arguments below that boundary, so this adapter supplies only the
// semantic capability markers while those models continue to validate the raw
// values they own.
package testannotation

import (
	"fmt"
	"net/http"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

// AttachOfficial adds typed capability markers for official annotations in a
// raw resolve.Annotations result. Unknown third-party annotations are left
// untouched.
func AttachOfficial(result resolve.Result) (resolve.Result, error) {
	var err error
	for index, occurrence := range result.Occurrences {
		if len(occurrence.Contributions) != 0 {
			continue
		}
		contributions := officialContributions(occurrence.Annotation)
		if len(contributions) == 0 {
			continue
		}
		result, err = result.WithContributions(index, contributions)
		if err != nil {
			return resolve.Result{}, fmt.Errorf(
				"attach test contribution for @%s: %w",
				occurrence.Annotation.Name,
				err,
			)
		}
	}
	return result, nil
}

// Trust marks attached test contributions as descriptor-backed. Use it only in
// unit tests whose subject is the SDK payload itself rather than legacy raw
// model parsing.
func Trust(result resolve.Result) resolve.Result {
	for index := range result.Occurrences {
		if len(result.Occurrences[index].Contributions) == 0 {
			continue
		}
		result.Occurrences[index].Definition = annotation.DefinitionReference{
			Package: "spice.test/annotation",
			Symbol:  "Descriptor",
		}
	}
	return result
}

func officialContributions(
	value annotation.Annotation,
) []sdk.Contribution {
	classifiers := []func(
		annotation.Annotation,
	) ([]sdk.Contribution, bool){
		foundationContributions,
		integrationContributions,
		beanSelectionContributions,
	}
	for _, classify := range classifiers {
		if contributions, found := classify(value); found {
			return contributions
		}
	}
	return nil
}

func foundationContributions(
	value annotation.Annotation,
) ([]sdk.Contribution, bool) {
	switch value.Name {
	case "Application":
		return []sdk.Contribution{{
			Kind:        sdk.ContributionApplication,
			Application: &sdk.ApplicationContribution{},
		}}, true
	case "Bean":
		return []sdk.Contribution{
			providerContribution(value.Arguments),
		}, true
	case "Service", "Repository":
		role := "service"
		if value.Name == "Repository" {
			role = "repository"
		}
		return []sdk.Contribution{
			stereotypeContribution(role, value.Arguments),
		}, true
	case "Controller":
		return []sdk.Contribution{{
			Kind:       sdk.ContributionController,
			Controller: &sdk.ControllerContribution{},
		}}, true
	case "Configuration":
		return []sdk.Contribution{{
			Kind:          sdk.ContributionConfiguration,
			Configuration: &sdk.ConfigurationContribution{},
		}}, true
	case "Module":
		return []sdk.Contribution{{
			Kind:   sdk.ContributionModule,
			Module: &sdk.ModuleContribution{},
		}}, true
	case "NamedInterface":
		return []sdk.Contribution{{
			Kind: sdk.ContributionNamedInterface,
			NamedInterface: &sdk.NamedInterfaceContribution{
				Name: "fixture",
			},
		}}, true
	case "OnStart":
		return lifecycleContribution(sdk.LifecycleStart), true
	case "OnStop":
		return lifecycleContribution(sdk.LifecycleStop), true
	default:
		return nil, false
	}
}

func integrationContributions(
	value annotation.Annotation,
) ([]sdk.Contribution, bool) {
	switch value.Name {
	case "Get", "web.Get":
		return routeContribution(http.MethodGet), true
	case "Post", "web.Post":
		return routeContribution(http.MethodPost), true
	case "async.Execute":
		return []sdk.Contribution{{
			Kind:  sdk.ContributionAsync,
			Async: &sdk.AsyncContribution{},
		}}, true
	case "cache.Cacheable":
		return []sdk.Contribution{{
			Kind:  sdk.ContributionCache,
			Cache: &sdk.CacheContribution{Name: "fixture"},
		}}, true
	case "data.Transactional":
		return []sdk.Contribution{{
			Kind:        sdk.ContributionTransaction,
			Transaction: &sdk.TransactionContribution{},
		}}, true
	case "event.Topic":
		return []sdk.Contribution{{
			Kind:       sdk.ContributionEventTopic,
			EventTopic: &sdk.EventTopicContribution{},
		}}, true
	case "event.Listener":
		return []sdk.Contribution{{
			Kind: sdk.ContributionEventListener,
			EventListener: &sdk.EventListenerContribution{
				Order: 0,
			},
		}}, true
	case "schedule.FixedDelay":
		return []sdk.Contribution{{
			Kind: sdk.ContributionSchedule,
			Schedule: &sdk.ScheduleContribution{
				Delay: "1s",
			},
		}}, true
	case "security.Authorize":
		return []sdk.Contribution{{
			Kind:          sdk.ContributionAuthorization,
			Authorization: &sdk.AuthorizationContribution{},
		}}, true
	default:
		return nil, false
	}
}

func beanSelectionContributions(
	value annotation.Annotation,
) ([]sdk.Contribution, bool) {
	switch value.Name {
	case "Implements":
		return interfaceContribution(value.Arguments), true
	case "Qualifier":
		return qualifierContribution(value.Arguments), true
	case "Primary":
		return beanMetadataContribution(
			sdk.BeanMetadataContribution{Primary: true},
		), true
	case "Fallback":
		return beanMetadataContribution(
			sdk.BeanMetadataContribution{Fallback: true},
		), true
	case "Order":
		order := int64(0)
		if argument, found := firstArgument(value.Arguments); found &&
			argument.Value.Kind == annotation.KindInteger {
			order = argument.Value.Integer
		}
		return beanMetadataContribution(
			sdk.BeanMetadataContribution{Order: &order},
		), true
	case "Singleton":
		return scopeContribution(sdk.BeanScopeSingleton), true
	case "Prototype":
		return scopeContribution(sdk.BeanScopePrototype), true
	case "RequestScope":
		return scopeContribution(sdk.BeanScopeRequest), true
	case "SessionScope":
		return scopeContribution(sdk.BeanScopeSession), true
	default:
		return nil, false
	}
}

func providerContribution(
	arguments []annotation.Argument,
) sdk.Contribution {
	return sdk.Contribution{
		Kind: sdk.ContributionProvider,
		Provider: &sdk.ProviderContribution{
			Name:    stringArgument(arguments, "name"),
			Aliases: stringListArgument(arguments, "aliases"),
		},
	}
}

func stereotypeContribution(
	role string,
	arguments []annotation.Argument,
) sdk.Contribution {
	constructor := identifierArgument(arguments, "constructor")
	return sdk.Contribution{
		Kind: sdk.ContributionStereotype,
		Stereotype: &sdk.StereotypeContribution{
			Role:        role,
			Construct:   true,
			Constructor: constructor,
			Name:        stringArgument(arguments, "name"),
			Aliases:     stringListArgument(arguments, "aliases"),
		},
	}
}

func lifecycleContribution(
	phase sdk.LifecyclePhase,
) []sdk.Contribution {
	return []sdk.Contribution{{
		Kind: sdk.ContributionLifecycle,
		Lifecycle: &sdk.LifecycleContribution{
			Phase: phase,
		},
	}}
}

func routeContribution(method string) []sdk.Contribution {
	return []sdk.Contribution{{
		Kind: sdk.ContributionRoute,
		Route: &sdk.RouteContribution{
			Method: method,
			Path:   "/",
		},
	}}
}

func interfaceContribution(
	arguments []annotation.Argument,
) []sdk.Contribution {
	interfaces := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if argument.Value.Kind == annotation.KindIdentifier {
			interfaces = append(
				interfaces,
				argument.Value.Identifier,
			)
		}
	}
	if len(interfaces) == 0 {
		interfaces = []string{"Invalid"}
	}
	return []sdk.Contribution{{
		Kind: sdk.ContributionInterface,
		Interface: &sdk.InterfaceBindingContribution{
			Interfaces: interfaces,
		},
	}}
}

func qualifierContribution(
	arguments []annotation.Argument,
) []sdk.Contribution {
	qualifier := "fixture"
	if argument, found := firstArgument(arguments); found &&
		argument.Value.Kind == annotation.KindString &&
		argument.Value.String != "" {
		qualifier = argument.Value.String
	}
	return beanMetadataContribution(sdk.BeanMetadataContribution{
		Qualifiers: []string{qualifier},
	})
}

func beanMetadataContribution(
	metadata sdk.BeanMetadataContribution,
) []sdk.Contribution {
	return []sdk.Contribution{{
		Kind:         sdk.ContributionBeanMetadata,
		BeanMetadata: &metadata,
	}}
}

func scopeContribution(scope sdk.BeanScope) []sdk.Contribution {
	return beanMetadataContribution(sdk.BeanMetadataContribution{
		Scope: scope,
	})
}

func firstArgument(
	arguments []annotation.Argument,
) (annotation.Argument, bool) {
	if len(arguments) == 0 {
		return annotation.Argument{}, false
	}
	return arguments[0], true
}

func stringArgument(
	arguments []annotation.Argument,
	name string,
) string {
	for _, argument := range arguments {
		if argument.Name == name &&
			argument.Value.Kind == annotation.KindString {
			return argument.Value.String
		}
	}
	return ""
}

func identifierArgument(
	arguments []annotation.Argument,
	name string,
) string {
	for _, argument := range arguments {
		if argument.Name == name &&
			argument.Value.Kind == annotation.KindIdentifier {
			return argument.Value.Identifier
		}
	}
	return ""
}

func stringListArgument(
	arguments []annotation.Argument,
	name string,
) []string {
	for _, argument := range arguments {
		if argument.Name != name ||
			argument.Value.Kind != annotation.KindList {
			continue
		}
		result := make([]string, 0, len(argument.Value.List))
		for _, item := range argument.Value.List {
			if item.Kind == annotation.KindString {
				result = append(result, item.String)
			}
		}
		return result
	}
	return nil
}
