package cli

import (
	"strings"
	"testing"
)

func TestRunVerifyAcceptsValidBeanCatalogWithoutExecutingProviders(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, `package sample

type Config struct{}
type Store struct{}
type Service struct{}

// @Bean
func ConfigProvider() Config { panic("must not execute") }

// @Bean
func StoreProvider(config Config) *Store { panic("must not execute") }

// @Bean
func ServiceProvider(config Config, store *Store) (*Service, error) {
	panic("must not execute")
}
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 0 || !strings.Contains(stdout, "3 annotations") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunVerifyRejectsInvalidBeanSignatures(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, `package sample

type Config struct{}

// @Bean
func MissingResult() {}

// @Bean
func ErrorOnly() error { return nil }

// @Bean
func Variadic(values ...Config) Config { return Config{} }
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "3 provider catalog error(s)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{"must return one provided value", "error cannot be the only result", "variadic provider functions are not supported"} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}

func TestRunVerifyRejectsDuplicateBeanOutput(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"shared/shared.go": "package shared\n\ntype Config struct{}\n",
		"a/a.go": `package a
import "example.com/fixture/shared"
// @Bean
func First() shared.Config { return shared.Config{} }
`,
		"b/b.go": `package b
import "example.com/fixture/shared"
// @Bean
func Second() shared.Config { return shared.Config{} }
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./...")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "1 provider catalog error(s)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{"exact type example.com/fixture/shared.Config", "example.com/fixture/a.First", "example.com/fixture/b.Second"} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}

func TestRunVerifyRejectsAliasDuplicateBeanOutput(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, `package sample

type Original struct{}
type Alias = Original
type Distinct Original

// @Bean
func OriginalProvider() Original { panic("must not execute") }

// @Bean
func AliasProvider() Alias { panic("must not execute") }

// @Bean
func DistinctProvider() Distinct { panic("must not execute") }
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "1 provider catalog error(s)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"exact type example.com/fixture.Alias",
		"example.com/fixture.AliasProvider",
		"example.com/fixture.OriginalProvider",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
	if strings.Contains(stderr, "DistinctProvider") {
		t.Fatalf("distinct named provider incorrectly entered alias duplicate diagnostic: %q", stderr)
	}
}

func TestRunVerifyRejectsBeanTargetAndArgumentsBeforeCatalog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		source   string
		expected string
	}{
		{
			name: "method",
			source: `package sample

type Config struct{}
// @Bean
func (Config) Provider() Config { return Config{} }
`,
			expected: "allowed target: function",
		},
		{
			name: "arguments",
			source: `package sample

type Config struct{}
// @Bean(name="config")
func Provider() Config { return Config{} }
`,
			expected: "does not accept arguments",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runModule(writeGoSource(t, test.source), "verify", ".")
			if code != 1 || stdout != "" || !strings.Contains(stderr, test.expected) || strings.Contains(stderr, "provider catalog error") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}
