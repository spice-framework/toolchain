package main

import (
	"slices"
	"testing"
)

func TestNormalizeArgumentsMapsDocumentedFormats(t *testing.T) {
	t.Parallel()
	input := []string{"spicestyle", "--format=json", "--config=.spice/style.json", "./..."}
	wanted := []string{"spicestyle", "-json", "-spicestyle.config=.spice/style.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsMapsSeparatedConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"spicestyle", "--config", "profile.json", "./..."}
	wanted := []string{"spicestyle", "-spicestyle.config=profile.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsPreservesTextAndReportsMissingConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"spicestyle", "--format=text", "-format=text", "-config", "./..."}
	wanted := []string{"spicestyle", "-spicestyle.config=./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
	missing := []string{"spicestyle", "--config"}
	wantedMissing := []string{"spicestyle", "-spicestyle.config"}
	if got := normalizeArguments(missing); !slices.Equal(got, wantedMissing) {
		t.Fatalf("normalizeArguments(missing) = %v, want %v", got, wantedMissing)
	}
}
