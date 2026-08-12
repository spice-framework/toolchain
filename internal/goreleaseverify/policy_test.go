package goreleaseverify

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestCheckPolicyAuthorizesExactClosedIdentities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		request PolicyRequest
	}{
		{
			name: "Spice foundation",
			request: PolicyRequest{
				Repository: "spice",
				Source:     "https://github.com/spice-framework/spice",
				Module:     "github.com/spice-framework/spice",
				Version:    spiceFoundationVersion,
				Profile:    ProfileGoModule,
			},
		},
		{
			name: "Toolchain distribution",
			request: PolicyRequest{
				Repository: "toolchain",
				Source:     "https://github.com/spice-framework/toolchain",
				Module:     "github.com/spice-framework/toolchain",
				Version:    toolchainDistributionVersion,
				Profile:    ProfileDistribution,
			},
		},
		{
			name: "Agent core",
			request: PolicyRequest{
				Repository: "spice-agent",
				Source:     "https://github.com/spice-framework/spice-agent",
				Module:     "github.com/spice-framework/spice-agent",
				Version:    agentCoreReleaseVersion,
				Profile:    ProfileGoModule,
			},
		},
		{
			name: "Agent provider",
			request: PolicyRequest{
				Repository: "spice-agent-provider-openai",
				Source:     "https://github.com/spice-framework/spice-agent-provider-openai",
				Module:     "github.com/spice-framework/spice-agent-provider-openai",
				Version:    agentProviderReleaseVersion,
				Profile:    ProfileGoModule,
			},
		},
		{
			name: "Agent coding tools",
			request: PolicyRequest{
				Repository: "spice-agent-tools-coding",
				Source:     "https://github.com/spice-framework/spice-agent-tools-coding",
				Module:     "github.com/spice-framework/spice-agent-tools-coding",
				Version:    agentCodingToolsReleaseVersion,
				Profile:    ProfileGoModule,
			},
		},
		{
			name: "Agent TUI",
			request: PolicyRequest{
				Repository: "spice-agent-tui",
				Source:     "https://github.com/spice-framework/spice-agent-tui",
				Module:     "github.com/spice-framework/spice-agent-tui",
				Version:    agentTUIReleaseVersion,
				Profile:    ProfileGoModule,
			},
		},
		{
			name: "Agent coding distribution",
			request: PolicyRequest{
				Repository: "spice-agent-coding",
				Source:     "https://github.com/spice-framework/spice-agent-coding",
				Module:     "github.com/spice-framework/spice-agent-coding",
				Version:    agentDistributionVersion,
				Profile:    ProfileDistribution,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authorization, err := CheckPolicy(test.request)
			if err != nil {
				t.Fatalf("CheckPolicy(exact) error = %v", err)
			}
			want := PolicyAuthorization(test.request)
			if authorization != want {
				t.Fatalf("CheckPolicy(exact) = %#v, want %#v", authorization, want)
			}
		})
	}
}

func TestCheckPolicyRejectsStaleFoundationAndToolchainIdentities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		request  PolicyRequest
		versions []string
	}{
		{
			name: "Spice foundation",
			request: PolicyRequest{
				Repository: "spice", Source: "https://github.com/spice-framework/spice",
				Module: "github.com/spice-framework/spice", Profile: ProfileGoModule,
			},
			versions: []string{"v0.1.0-preview.1", "v0.1.0-preview.2", "v0.1.0-preview.3"},
		},
		{
			name: "Toolchain distribution",
			request: PolicyRequest{
				Repository: "toolchain", Source: "https://github.com/spice-framework/toolchain",
				Module: "github.com/spice-framework/toolchain", Profile: ProfileDistribution,
			},
			versions: []string{
				"v0.1.0-preview.1",
				"v0.1.0-preview.2",
				"v0.1.0-preview.3",
				"v0.1.0-preview.4",
				"v0.1.0-preview.5",
			},
		},
		{
			name: "Agent TUI",
			request: PolicyRequest{
				Repository: "spice-agent-tui", Source: "https://github.com/spice-framework/spice-agent-tui",
				Module: "github.com/spice-framework/spice-agent-tui", Profile: ProfileGoModule,
			},
			versions: []string{"v0.1.0-preview.1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, version := range test.versions {
				request := test.request
				request.Version = version
				authorization, err := CheckPolicy(request)
				if err == nil || !strings.Contains(err.Error(), "do not match") ||
					authorization != (PolicyAuthorization{}) {
					t.Fatalf("CheckPolicy(stale %s) = %#v, %v", version, authorization, err)
				}
			}
		})
	}
}

func TestCheckPolicyRejectsStaleDistributionPreviews(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"v0.1.0-preview.1", "v0.1.0-preview.2", "v0.1.0-preview.3"} {
		request := PolicyRequest{
			Repository: "spice-agent-coding",
			Source:     "https://github.com/spice-framework/spice-agent-coding",
			Module:     "github.com/spice-framework/spice-agent-coding",
			Version:    version,
			Profile:    ProfileDistribution,
		}
		authorization, err := CheckPolicy(request)
		if err == nil || !strings.Contains(err.Error(), "do not match") || authorization != (PolicyAuthorization{}) {
			t.Fatalf("CheckPolicy(stale distribution %s) = %#v, %v", version, authorization, err)
		}
	}
}

func TestCheckPolicyRejectsUntrustedAndStaleIdentities(t *testing.T) {
	t.Parallel()
	valid := PolicyRequest{
		Repository: "spice-agent",
		Source:     "https://github.com/spice-framework/spice-agent",
		Module:     "github.com/spice-framework/spice-agent",
		Version:    agentCoreReleaseVersion,
		Profile:    ProfileGoModule,
	}
	for _, test := range []struct {
		name   string
		mutate func(*PolicyRequest)
		want   string
	}{
		{name: "missing", mutate: func(value *PolicyRequest) { value.Repository = "" }, want: "missing or invalid"},
		{name: "oversized", mutate: func(value *PolicyRequest) { value.Repository = strings.Repeat("x", maxPolicyInputBytes+1) }, want: "missing or invalid"},
		{name: "invalid UTF-8", mutate: func(value *PolicyRequest) { value.Module = string([]byte{0xff}) }, want: "missing or invalid"},
		{name: "control", mutate: func(value *PolicyRequest) { value.Source += "\nother" }, want: "missing or invalid"},
		{name: "unsupported profile", mutate: func(value *PolicyRequest) { value.Profile = "library-v1" }, want: "not independently authorized"},
		{name: "starter", mutate: func(value *PolicyRequest) { value.Repository = "starter-mail" }, want: "starter repositories"},
		{name: "unknown", mutate: func(value *PolicyRequest) { value.Repository += "-unknown" }, want: "not independently authorized"},
		{name: "source", mutate: func(value *PolicyRequest) { value.Source += "-fork" }, want: "do not match"},
		{name: "module", mutate: func(value *PolicyRequest) { value.Module += "/fork" }, want: "do not match"},
		{name: "stale preview.2", mutate: func(value *PolicyRequest) { value.Version = "v0.1.0-preview.2" }, want: "do not match"},
		{name: "stale preview.3", mutate: func(value *PolicyRequest) { value.Version = "v0.1.0-preview.3" }, want: "do not match"},
		{name: "stale preview.4", mutate: func(value *PolicyRequest) { value.Version = "v0.1.0-preview.4" }, want: "do not match"},
		{name: "stale preview.5", mutate: func(value *PolicyRequest) { value.Version = "v0.1.0-preview.5" }, want: "do not match"},
		{name: "stale preview.6", mutate: func(value *PolicyRequest) { value.Version = "v0.1.0-preview.6" }, want: "do not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := valid
			test.mutate(&request)
			authorization, err := CheckPolicy(request)
			if err == nil || !strings.Contains(err.Error(), test.want) || authorization != (PolicyAuthorization{}) {
				t.Fatalf("CheckPolicy(invalid) = %#v, %v, want %q", authorization, err, test.want)
			}
		})
	}
}

func TestCheckPolicyHasBoundedDeterministicFailure(t *testing.T) {
	t.Parallel()
	request := PolicyRequest{
		Repository: strings.Repeat("x", maxPolicyInputBytes+1),
		Source:     "https://github.com/spice-framework/spice-agent",
		Module:     "github.com/spice-framework/spice-agent",
		Version:    agentCoreReleaseVersion,
		Profile:    ProfileGoModule,
	}
	_, left := CheckPolicy(request)
	_, right := CheckPolicy(request)
	if left == nil || right == nil || left.Error() != right.Error() || len(left.Error()) > 128 {
		t.Fatalf("CheckPolicy(oversized) errors = %v and %v", left, right)
	}
}

func TestPolicyCheckBoundaryHasNoOperationalInputsOrDependencies(t *testing.T) {
	t.Parallel()
	requestType := reflect.TypeFor[PolicyRequest]()
	var fieldNames []string
	for index := range requestType.NumField() {
		fieldNames = append(fieldNames, requestType.Field(index).Name)
	}
	if want := []string{"Repository", "Source", "Module", "Version", "Profile"}; !slices.Equal(fieldNames, want) {
		t.Fatalf("PolicyRequest fields = %v, want %v", fieldNames, want)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve policy test source")
	}
	parsed, err := parser.ParseFile(
		token.NewFileSet(),
		filepath.Join(filepath.Dir(testFile), "policy.go"),
		nil,
		parser.ImportsOnly,
	)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, spec := range parsed.Imports {
		path, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			t.Fatal(unquoteErr)
		}
		imports = append(imports, path)
	}
	slices.Sort(imports)
	wantImports := []string{"errors", "fmt", "strings", "unicode", "unicode/utf8"}
	if !slices.Equal(imports, wantImports) {
		t.Fatalf("policy-check imports = %v, require pure allowlist %v", imports, wantImports)
	}
}
