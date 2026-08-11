package main

import (
	"slices"
	"testing"
)

func TestNormalizeArgumentsMapsDocumentedFormats(t *testing.T) {
	t.Parallel()
	input := []string{"--format=json", "--config=.spice/style.json", "./..."}
	wanted := []string{"verify", "--format=json", "--style=.spice/style.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsMapsSeparatedConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"--config", "profile.json", "./..."}
	wanted := []string{"verify", "--style=profile.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsPreservesTextAndReportsMissingConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"--format=text", "-config", "./..."}
	wanted := []string{"verify", "--format=text", "--style=./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
	missing := []string{"--config"}
	wantedMissing := []string{"verify", "--style"}
	if got := normalizeArguments(missing); !slices.Equal(got, wantedMissing) {
		t.Fatalf("normalizeArguments(missing) = %v, want %v", got, wantedMissing)
	}
}

func TestNormalizeArgumentsDefaultsToSchemaTwoConfiguration(t *testing.T) {
	t.Parallel()
	wanted := []string{"verify", "./...", "--style=.spice/style.json"}
	if got := normalizeArguments([]string{"./..."}); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments(default) = %v, want %v", got, wanted)
	}
}
