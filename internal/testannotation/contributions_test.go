package testannotation

import (
	"reflect"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

func TestAttachOfficialMapsEverySupportedCapability(t *testing.T) {
	t.Parallel()
	stringValue := func(value string) annotation.Value {
		return annotation.Value{
			Kind:   annotation.KindString,
			String: value,
		}
	}
	identifierValue := func(value string) annotation.Value {
		return annotation.Value{
			Kind:       annotation.KindIdentifier,
			Identifier: value,
		}
	}
	tests := []struct {
		name      string
		arguments []annotation.Argument
		kind      sdk.ContributionKind
	}{
		{"Application", nil, sdk.ContributionApplication},
		{
			"Bean",
			[]annotation.Argument{
				{Name: "name", Value: stringValue("fixture")},
				{
					Name: "aliases",
					Value: annotation.Value{
						Kind: annotation.KindList,
						List: []annotation.Value{
							stringValue("one"),
							{
								Kind:    annotation.KindBoolean,
								Boolean: true,
							},
						},
					},
				},
			},
			sdk.ContributionProvider,
		},
		{
			"Service",
			[]annotation.Argument{
				{
					Name:  "constructor",
					Value: identifierValue("NewService"),
				},
				{Name: "name", Value: stringValue("service")},
			},
			sdk.ContributionStereotype,
		},
		{"Repository", nil, sdk.ContributionStereotype},
		{"Controller", nil, sdk.ContributionController},
		{"Configuration", nil, sdk.ContributionConfiguration},
		{"Module", nil, sdk.ContributionModule},
		{"NamedInterface", nil, sdk.ContributionNamedInterface},
		{"OnStart", nil, sdk.ContributionLifecycle},
		{"OnStop", nil, sdk.ContributionLifecycle},
		{"Get", nil, sdk.ContributionRoute},
		{"web.Get", nil, sdk.ContributionRoute},
		{"Post", nil, sdk.ContributionRoute},
		{"web.Post", nil, sdk.ContributionRoute},
		{"async.Execute", nil, sdk.ContributionAsync},
		{"cache.Cacheable", nil, sdk.ContributionCache},
		{"data.Transactional", nil, sdk.ContributionTransaction},
		{"event.Topic", nil, sdk.ContributionEventTopic},
		{"event.Listener", nil, sdk.ContributionEventListener},
		{"schedule.FixedDelay", nil, sdk.ContributionSchedule},
		{"security.Authorize", nil, sdk.ContributionAuthorization},
		{
			"Implements",
			[]annotation.Argument{
				{Value: identifierValue("payments.Processor")},
				{Value: stringValue("ignored")},
			},
			sdk.ContributionInterface,
		},
		{"Implements", nil, sdk.ContributionInterface},
		{
			"Qualifier",
			[]annotation.Argument{{Value: stringValue("stripe")}},
			sdk.ContributionBeanMetadata,
		},
		{"Qualifier", nil, sdk.ContributionBeanMetadata},
		{"Primary", nil, sdk.ContributionBeanMetadata},
		{"Fallback", nil, sdk.ContributionBeanMetadata},
		{
			"Order",
			[]annotation.Argument{{
				Value: annotation.Value{
					Kind:    annotation.KindInteger,
					Integer: 10,
				},
			}},
			sdk.ContributionBeanMetadata,
		},
		{"Order", nil, sdk.ContributionBeanMetadata},
		{"Singleton", nil, sdk.ContributionBeanMetadata},
		{"Prototype", nil, sdk.ContributionBeanMetadata},
		{"RequestScope", nil, sdk.ContributionBeanMetadata},
		{"SessionScope", nil, sdk.ContributionBeanMetadata},
	}
	occurrences := make([]resolve.Occurrence, len(tests)+1)
	for index, test := range tests {
		occurrences[index].Annotation = annotation.Annotation{
			Name:      test.name,
			Arguments: test.arguments,
		}
	}
	occurrences[len(tests)].Annotation.Name = "thirdparty.Custom"

	result, err := AttachOfficial(resolve.Result{
		Occurrences: occurrences,
	})
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	for index, test := range tests {
		contribution, found := result.Occurrences[index].
			Contribution(test.kind)
		if !found {
			t.Errorf(
				"@%s contribution = %#v, want %s",
				test.name,
				result.Occurrences[index].Contributions,
				test.kind,
			)
			continue
		}
		if err := contribution.Validate(); err != nil {
			t.Errorf("@%s contribution is invalid: %v", test.name, err)
		}
	}
	if contributions := result.Occurrences[len(tests)].Contributions; len(
		contributions,
	) != 0 {
		t.Fatalf(
			"unknown annotation contributions = %#v",
			contributions,
		)
	}
}

func TestAttachOfficialPreservesContributionsAndReportsInvalidMetadata(
	t *testing.T,
) {
	t.Parallel()
	application := sdk.Contribution{
		Kind:        sdk.ContributionApplication,
		Application: &sdk.ApplicationContribution{},
	}
	result, err := AttachOfficial(resolve.Result{
		Occurrences: []resolve.Occurrence{{
			Annotation: annotation.Annotation{Name: "Bean"},
			Contributions: []sdk.Contribution{
				application,
			},
		}},
	})
	if err != nil {
		t.Fatalf("AttachOfficial(existing) error = %v", err)
	}
	if !reflect.DeepEqual(
		result.Occurrences[0].Contributions,
		[]sdk.Contribution{application},
	) {
		t.Fatalf(
			"existing contributions = %#v",
			result.Occurrences[0].Contributions,
		)
	}

	_, err = AttachOfficial(resolve.Result{
		Occurrences: []resolve.Occurrence{{
			Annotation: annotation.Annotation{
				Name: "Bean",
				Arguments: []annotation.Argument{{
					Name: "name",
					Value: annotation.Value{
						Kind:   annotation.KindString,
						String: " invalid ",
					},
				}},
			},
		}},
	})
	if err == nil {
		t.Fatal("AttachOfficial(invalid) error = nil")
	}
}

func TestTrustAndArgumentHelpers(t *testing.T) {
	t.Parallel()
	result := Trust(resolve.Result{
		Occurrences: []resolve.Occurrence{
			{},
			{
				Contributions: []sdk.Contribution{{
					Kind:        sdk.ContributionApplication,
					Application: &sdk.ApplicationContribution{},
				}},
			},
		},
	})
	if result.Occurrences[0].Definition !=
		(annotation.DefinitionReference{}) {
		t.Fatalf(
			"empty occurrence definition = %#v",
			result.Occurrences[0].Definition,
		)
	}
	if result.Occurrences[1].Definition.Package !=
		"spice.test/annotation" {
		t.Fatalf(
			"trusted definition = %#v",
			result.Occurrences[1].Definition,
		)
	}

	arguments := []annotation.Argument{
		{
			Name: "string",
			Value: annotation.Value{
				Kind:    annotation.KindInteger,
				Integer: 1,
			},
		},
		{
			Name: "identifier",
			Value: annotation.Value{
				Kind:   annotation.KindString,
				String: "wrong",
			},
		},
		{
			Name: "list",
			Value: annotation.Value{
				Kind: annotation.KindString,
			},
		},
	}
	if value := stringArgument(arguments, "string"); value != "" {
		t.Fatalf("stringArgument() = %q", value)
	}
	if value := identifierArgument(
		arguments,
		"identifier",
	); value != "" {
		t.Fatalf("identifierArgument() = %q", value)
	}
	if value := stringListArgument(arguments, "list"); value != nil {
		t.Fatalf("stringListArgument() = %#v", value)
	}
	if _, found := firstArgument(nil); found {
		t.Fatal("firstArgument(nil) found = true")
	}
}
