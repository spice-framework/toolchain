package cli

import (
	"strings"
	"testing"
)

func TestRunVerifyAcceptsTypedApplicationRootsWithoutExecutingMarker(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"app.go": `package fixture

type Config struct{}

// @Bean
func ConfigProvider() Config {
	panic("provider bodies must not execute during verification")
}

// @Application
func Application(Config) {
	panic("application marker bodies must not execute during verification")
}
`,
	})

	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 0 || !strings.Contains(stdout, "2 annotations") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunVerifyRejectsInvalidApplicationRootsAndSignatures(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"app.go": `package fixture

type Service struct{}
type Contract interface{ Serve() }

// @Bean
func ServiceProvider() *Service { return &Service{} }

// @Application
func MissingProvider(Contract) {}

// @Application
func InvalidResult(*Service) error { return nil }
`,
	})

	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"no @Bean provider produces that type",
		"must return no results",
		"2 application model error(s)",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}
