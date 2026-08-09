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
	for _, request := range []PolicyRequest{
		{
			Repository: "spice-agent",
			Source:     "https://github.com/spice-framework/spice-agent",
			Module:     "github.com/spice-framework/spice-agent",
			Version:    agentCoreReleaseVersion,
			Profile:    ProfileGoModule,
		},
		{
			Repository: "spice-agent-coding",
			Source:     "https://github.com/spice-framework/spice-agent-coding",
			Module:     "github.com/spice-framework/spice-agent-coding",
			Version:    agentDistributionVersion,
			Profile:    ProfileDistribution,
		},
	} {
		request := request
		t.Run(request.Profile, func(t *testing.T) {
			t.Parallel()
			authorization, err := CheckPolicy(request)
			if err != nil {
				t.Fatalf("CheckPolicy(exact) error = %v", err)
			}
			want := PolicyAuthorization(request)
			if authorization != want {
				t.Fatalf("CheckPolicy(exact) = %#v, want %#v", authorization, want)
			}
		})
	}
}

func TestCheckPolicyRejectsStaleDistributionPreviews(t *testing.T) {
	t.Parallel()
	for _, version := range []string{agentExtensionVersion, "v0.1.0-preview.2"} {
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
