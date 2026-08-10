package style

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigurationIsStrictAndDefensive(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "style.json")
	if writeErr := os.WriteFile(path, content, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	configuration, err := LoadConfiguration(path)
	if err != nil {
		t.Fatalf("LoadConfiguration() error = %v", err)
	}
	configuration.SourceRoots[0] = "changed"
	again, err := LoadConfiguration(path)
	if err != nil {
		t.Fatalf("LoadConfiguration() second error = %v", err)
	}
	if again.SourceRoots[0] == "changed" {
		t.Fatal("LoadConfiguration returned aliased source roots")
	}
}

func TestLoadConfigurationRejectsMalformedBoundaries(t *testing.T) {
	t.Parallel()
	valid, err := os.ReadFile(filepath.Join("testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"unknown field": func(value string) string {
			return strings.Replace(value, "\"profile\":", "\"unknown\":true,\"profile\":", 1)
		},
		"wrong schema": func(value string) string {
			return strings.Replace(value, "\"schemaVersion\": 1", "\"schemaVersion\": 2", 1)
		},
		"wrong profile": func(value string) string {
			return strings.Replace(value, "java-structured", "unstructured", 1)
		},
		"empty source roots": func(value string) string {
			return strings.Replace(value, "[\n    \"testdata/src\"\n  ]", "[]", 1)
		},
		"duplicate source roots": func(value string) string {
			return strings.Replace(value, "\"testdata/src\"", "\"testdata/src\", \"testdata/src\"", 1)
		},
		"unsupported rule level": func(value string) string {
			return strings.Replace(value, "\"onePrimaryTypePerFile\": \"error\"", "\"onePrimaryTypePerFile\": \"fatal\"", 1)
		},
		"zero line limit": func(value string) string {
			return strings.Replace(value, "\"maxTypeFileLines\": 500", "\"maxTypeFileLines\": 0", 1)
		},
		"excessive line limit": func(value string) string {
			return strings.Replace(value, "\"maxTypeFileLines\": 500", "\"maxTypeFileLines\": 10001", 1)
		},
		"trailing value": func(value string) string { return value + "{}" },
		"parent root": func(value string) string {
			return strings.Replace(value, "\"testdata/src\"", "\"../src\"", 1)
		},
		"backslash boundary glob": func(value string) string {
			return strings.Replace(value, "**/doc.go", "**\\\\doc.go", 1)
		},
		"parent boundary glob": func(value string) string {
			return strings.Replace(value, "**/doc.go", "../doc.go", 1)
		},
		"empty reason": func(value string) string {
			return strings.Replace(value, "\"Go process entrypoint\"", "\"\"", 1)
		},
		"two function selectors": func(value string) string {
			return strings.Replace(value, "\"symbol\": \"main\"", "\"symbol\": \"main\", \"symbolPattern\": \"main\"", 1)
		},
		"invalid symbol pattern": func(value string) string {
			return strings.Replace(value, "\"symbol\": \"main\"", "\"symbolPattern\": \"[\"", 1)
		},
		"negative function maximum": func(value string) string {
			return strings.Replace(value, "\"maximum\": 1", "\"maximum\": -1", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "style.json")
			if err := os.WriteFile(path, []byte(mutate(string(valid))), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfiguration(path); err == nil {
				t.Fatal("LoadConfiguration() error = nil")
			}
		})
	}
	configurationErr := newConfigurationError("fixed detail")
	if got := configurationErr.Error(); got != "invalid Spice style configuration: fixed detail" {
		t.Fatalf("configuration error = %q", got)
	}
}

func TestLoadConfigurationRejectsMissingInvalidAndOversizedFiles(t *testing.T) {
	t.Parallel()
	if _, err := LoadConfiguration(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing configuration error = nil")
	}
	for name, content := range map[string]string{
		"invalid":   "{",
		"oversized": strings.Repeat("x", maximumConfigurationBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "style.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfiguration(path); err == nil {
				t.Fatal("LoadConfiguration() error = nil")
			}
		})
	}
}
