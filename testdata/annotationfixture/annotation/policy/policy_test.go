package policy

import (
	"testing"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/sdktest"
)

func TestPolicyHandlerContract(t *testing.T) {
	t.Parallel()

	strict := policyInvocation()
	strict.Arguments = []sdk.InvocationArgument{
		sdktest.StringArgument("mode", "strict", false),
	}
	denied := policyInvocation()
	denied.Arguments = []sdk.InvocationArgument{
		sdktest.StringArgument("mode", "deny", false),
	}
	sdktest.RunHandlerCases(
		t,
		Policy(),
		sdktest.HandlerCase{
			Name:       "strict contribution",
			Invocation: strict,
			WantKinds: []sdk.ContributionKind{
				sdk.ContributionStereotype,
			},
		},
		sdktest.HandlerCase{
			Name:       "denied diagnostic",
			Invocation: denied,
			WantDiagnostics: []sdk.HandlerDiagnostic{{
				Code:     "policy-denied",
				Severity: "error",
				Message:  "fixture policy deliberately denied this declaration",
			}},
		},
	)
}

func policyInvocation() sdk.Invocation {
	return sdktest.Invocation(
		"example.com/spice-annotation-fixture/annotation/policy",
		"Policy",
		"fixture.Policy",
		sdk.Declaration{
			Target:      sdk.TargetType,
			SymbolID:    "type:Settings",
			Name:        "Settings",
			PackagePath: "example.com/application",
		},
	)
}
