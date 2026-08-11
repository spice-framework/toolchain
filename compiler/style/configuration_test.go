package style

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeConfigurationAcceptsCanonicalImplementedSubsetDefensively(t *testing.T) {
	t.Parallel()
	content := canonicalTestConfiguration(t)
	configuration, err := DecodeConfiguration(content)
	if err != nil {
		t.Fatalf("DecodeConfiguration() error = %v", err)
	}
	configuration.BuildSelections[0].SourceRoots[0] = "changed"
	again, err := DecodeConfiguration(content)
	if err != nil {
		t.Fatalf("DecodeConfiguration() second error = %v", err)
	}
	if again.BuildSelections[0].SourceRoots[0] == "changed" {
		t.Fatal("DecodeConfiguration returned aliased build-selection roots")
	}
}

func TestCanonicalPolicyPointerMatchesCompiledIdentity(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "..", "CODE_STYLE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{CanonicalPolicyCommit, CanonicalPolicySHA256} {
		if !strings.Contains(string(content), identity) {
			t.Fatalf("CODE_STYLE.md omits canonical identity %s", identity)
		}
	}
}

func TestDecodeConfigurationRejectsSchemaOneWithMigration(t *testing.T) {
	t.Parallel()
	content := strings.Replace(string(canonicalTestConfiguration(t)), `"schemaVersion": 2`, `"schemaVersion": 1`, 1)
	_, err := DecodeConfiguration([]byte(content))
	if err == nil || !strings.Contains(err.Error(), "schemaVersion 1 is retired") ||
		!strings.Contains(err.Error(), "buildSelections") ||
		!strings.Contains(err.Error(), "spice.style.configuration.schema") {
		t.Fatalf("schema-one error = %v", err)
	}
}

func TestDecodeConfigurationAcceptsEverySchemaTwoTypedRule(t *testing.T) {
	t.Parallel()
	content := string(canonicalTestConfiguration(t))
	for _, rule := range []string{
		"explicitManagedScopes",
		"privateManagedFields",
		"moduleOwnership",
		"routeClassification",
	} {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			mutated := strings.Replace(content, `"`+rule+`": "off"`, `"`+rule+`": "error"`, 1)
			if _, err := DecodeConfiguration([]byte(mutated)); err != nil {
				t.Fatalf("enabled rule %s error = %v", rule, err)
			}
		})
	}
}

func TestDecodeConfigurationRejectsSelectionMutation(t *testing.T) {
	t.Parallel()
	content := string(canonicalTestConfiguration(t))
	tests := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"unknown field": {
			mutate: func(value string) string {
				return strings.Replace(value, `"profile":`, `"unknown": true, "profile":`, 1)
			},
			want: "unknown field",
		},
		"platform": {
			mutate: func(value string) string {
				return strings.Replace(value, `"goos": "linux"`, `"goos": "ambient"`, 1)
			},
			want: "unsupported pair ambient/amd64",
		},
		"ambient cgo tag": {
			mutate: func(value string) string {
				return strings.Replace(value, `"tags": []`, `"tags": ["cgo"]`, 1)
			},
			want: `invalid tag "cgo"`,
		},
		"order": {
			mutate: func(value string) string {
				value = strings.Replace(value, `"goos": "linux"`, `"goos": "windows"`, 1)
				return strings.Replace(value, `"goarch": "amd64"`, `"goarch": "arm64"`, 1)
			},
			want: "out of canonical order",
		},
		"uncovered root": {
			mutate: func(value string) string {
				return strings.ReplaceAll(value, `"sourceRoots": [
        "testdata/src"
      ]`, `"sourceRoots": [
        "other"
      ]`)
			},
			want: "undeclared root",
		},
		"generated root outside source universe": {
			mutate: func(value string) string {
				return strings.Replace(
					value,
					`"testdata/src/example.com/valid/internal/spicegen"`,
					`"testdata/generated"`,
					1,
				)
			},
			want: "outside the declared source universe",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeConfiguration([]byte(test.mutate(content)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeConfiguration() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadConfigurationCoversFileBoundaries(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	validPath := filepath.Join(directory, "style.json")
	if err := os.WriteFile(validPath, canonicalTestConfiguration(t), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := LoadConfiguration(validPath)
	if err != nil || configuration.SchemaVersion != styleSchemaVersion {
		t.Fatalf("LoadConfiguration() = %#v, %v", configuration, err)
	}
	if _, err = LoadConfiguration(filepath.Join(directory, "missing.json")); err == nil {
		t.Fatal("missing configuration error = nil")
	}
	oversizedPath := filepath.Join(directory, "oversized.json")
	if err = os.WriteFile(oversizedPath, []byte(strings.Repeat("x", maximumConfigurationBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfiguration(oversizedPath); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("oversized configuration error = %v", err)
	}
	_, err = DecodeConfiguration(append(canonicalTestConfiguration(t), []byte("\n{}")...))
	if err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("trailing configuration error = %v", err)
	}
	_, err = DecodeConfiguration(append(canonicalTestConfiguration(t), []byte("\n{")...))
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("malformed trailing configuration error = %v", err)
	}
}

func TestConfigurationValidationRejectsEveryBoundary(t *testing.T) {
	t.Parallel()
	base, err := DecodeConfiguration(canonicalTestConfiguration(t))
	if err != nil {
		t.Fatal(err)
	}
	falseValue := false
	for _, test := range []struct {
		name   string
		mutate func(*Configuration)
		want   string
	}{
		{name: "unsupported schema", mutate: func(value *Configuration) { value.SchemaVersion = 3 }, want: "schemaVersion is 3"},
		{name: "profile", mutate: func(value *Configuration) { value.Profile = "ordinary" }, want: "profile must equal"},
		{name: "empty source roots", mutate: func(value *Configuration) { value.SourceRoots = nil }, want: "sourceRoots must not be empty"},
		{name: "unsorted source roots", mutate: func(value *Configuration) { value.SourceRoots = []string{"z", "a"} }, want: "sourceRoots must be sorted"},
		{name: "duplicate source root", mutate: func(value *Configuration) { value.SourceRoots = []string{"source", "source"} }, want: "duplicate"},
		{name: "overlapping source roots", mutate: func(value *Configuration) {
			value.SourceRoots = []string{"testdata/src", "testdata/src/example.com"}
		}, want: "overlapping roots"},
		{name: "invalid source root", mutate: func(value *Configuration) { value.SourceRoots = []string{"../source"} }, want: "invalid root"},
		{name: "generated as source", mutate: func(value *Configuration) { value.GeneratedRoots = []string{"testdata/src"} }, want: "reintroduced"},
		{name: "rule level", mutate: func(value *Configuration) { value.Rules.PackageFunctions = "fatal" }, want: "unsupported level"},
		{name: "line minimum", mutate: func(value *Configuration) { value.Rules.MaxTypeFileLines = 0 }, want: "between 1 and 10000"},
		{name: "line maximum", mutate: func(value *Configuration) { value.Rules.MaxTypeFileLines = 10_001 }, want: "between 1 and 10000"},
		{name: "no selections", mutate: func(value *Configuration) { value.BuildSelections = nil }, want: "must not be empty"},
		{name: "selection name", mutate: func(value *Configuration) { value.BuildSelections[0].Name = "INVALID" }, want: "invalid name"},
		{name: "duplicate selection name", mutate: func(value *Configuration) { value.BuildSelections[1].Name = value.BuildSelections[0].Name }, want: "duplicate selection name"},
		{name: "missing cgo", mutate: func(value *Configuration) { value.BuildSelections[0].CGOEnabled = nil }, want: "omits cgoEnabled"},
		{name: "unsupported pair", mutate: func(value *Configuration) { value.BuildSelections[0].GOARCH = "sparc" }, want: "unsupported pair"},
		{name: "empty selection roots", mutate: func(value *Configuration) { value.BuildSelections[0].SourceRoots = nil }, want: "must not be empty"},
		{name: "duplicate context", mutate: func(value *Configuration) {
			value.BuildSelections[1].GOOS = value.BuildSelections[0].GOOS
			value.BuildSelections[1].GOARCH = value.BuildSelections[0].GOARCH
			value.BuildSelections[1].CGOEnabled = &falseValue
			value.BuildSelections[1].Tags = nil
		}, want: "duplicates a build context"},
		{name: "unsorted tags", mutate: func(value *Configuration) { value.BuildSelections[0].Tags = []string{"z", "a"} }, want: "tags must be sorted"},
		{name: "duplicate tag", mutate: func(value *Configuration) { value.BuildSelections[0].Tags = []string{"tag", "tag"} }, want: "duplicate tag"},
		{name: "uncovered source", mutate: func(value *Configuration) { value.SourceRoots = append(value.SourceRoots, "uncovered") }, want: "not covered"},
		{name: "public route", mutate: func(value *Configuration) { value.PublicRoutes = []PublicRoute{{}} }, want: "publicRoutes[0]"},
		{name: "boundary glob", mutate: func(value *Configuration) { value.AllowedBoundaryFiles = []string{`bad\\glob`} }, want: "slash-separated"},
		{name: "function selector", mutate: func(value *Configuration) {
			value.PackageFunctionExceptions = []PackageFunctionException{{Glob: "**/main.go", Reason: "required"}}
		}, want: "exactly one"},
		{name: "function pattern", mutate: func(value *Configuration) {
			value.PackageFunctionExceptions = []PackageFunctionException{{Glob: "**/main.go", SymbolPattern: "[", Reason: "required"}}
		}, want: "symbolPattern is invalid"},
		{name: "function maximum", mutate: func(value *Configuration) {
			value.PackageFunctionExceptions = []PackageFunctionException{{Glob: "**/main.go", Symbol: "main", Maximum: -1, Reason: "required"}}
		}, want: "must not be negative"},
		{name: "contribution maximum", mutate: func(value *Configuration) {
			value.PackageFunctionExceptions = []PackageFunctionException{{Glob: "**/*_bean.go", ContributionKind: "provider", Reason: "required"}}
		}, want: "requires a positive maximum"},
		{name: "symbol maximum", mutate: func(value *Configuration) {
			value.PackageFunctionExceptions = []PackageFunctionException{{Glob: "**/main.go", Symbol: "main", Maximum: 1, Reason: "required"}}
		}, want: "valid only with contributionKind"},
		{name: "variable exception", mutate: func(value *Configuration) {
			value.PackageVariableExceptions = []PackageVariableException{{Glob: "**/assets.go"}}
		}, want: "symbol, type, reason, and issue"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configuration := base.Clone()
			test.mutate(&configuration)
			err := configuration.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConfigurationErrorExposesCanonicalCode(t *testing.T) {
	t.Parallel()
	failure := configurationBuildError("detail")
	if failure.Code() != "spice.style.configuration.build-selection" {
		t.Fatalf("ConfigurationError.Code() = %q", failure.Code())
	}
}

func canonicalTestConfiguration(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "internal", "style", "testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func FuzzDecodeConfigurationIsDeterministic(f *testing.F) {
	content, err := os.ReadFile(filepath.Join("..", "..", "internal", "style", "testdata", "style.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(content)
	f.Add([]byte(`{"schemaVersion":2}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		first, firstErr := DecodeConfiguration(input)
		second, secondErr := DecodeConfiguration(input)
		if fmt.Sprint(firstErr) != fmt.Sprint(secondErr) {
			t.Fatalf("DecodeConfiguration() errors differ: %v != %v", firstErr, secondErr)
		}
		if firstErr == nil && !reflect.DeepEqual(first, second) {
			t.Fatal("DecodeConfiguration() success is nondeterministic")
		}
	})
}

func BenchmarkDecodeSchemaTwoConfiguration(b *testing.B) {
	content, err := os.ReadFile(filepath.Join("..", "..", "internal", "style", "testdata", "style.json"))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, decodeErr := DecodeConfiguration(content); decodeErr != nil {
			b.Fatal(decodeErr)
		}
	}
}
