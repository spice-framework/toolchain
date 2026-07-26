package starter_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

func TestSelectionRoundTripsCanonicalCatalog(t *testing.T) {
	catalog := newCatalog(
		t,
		annotatedManifest(t, searchID, "1.2.0", "search.Enable", "search.client"),
		constructorManifest(t, "example.com/acme/starter/cache"),
	)
	content, err := catalog.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	parsed, err := compilerstarter.ParseWithCompatibility(
		content,
		publicstarter.APIVersion,
		currentGo,
	)
	if err != nil {
		t.Fatalf("ParseWithCompatibility() error = %v", err)
	}
	manifests := parsed.Manifests()
	gotIDs := []string{manifests[0].Spec().ID, manifests[1].Spec().ID}
	if !slices.Equal(
		gotIDs,
		[]string{"example.com/acme/starter/cache", searchID},
	) {
		t.Fatalf("manifest IDs = %v", gotIDs)
	}
	reencoded, err := parsed.JSON()
	if err != nil {
		t.Fatalf("JSON(round trip) error = %v", err)
	}
	if !bytes.Equal(content, reencoded) {
		t.Fatalf("selection changed after round trip:\n%s\n---\n%s", content, reencoded)
	}
}

func TestSelectionFailsClosed(t *testing.T) {
	valid, err := newCatalog(
		t,
		annotatedManifest(t, searchID, "1.2.0", "search.Enable", "search.client"),
	).JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}

	tests := []struct {
		name      string
		content   []byte
		spiceAPI  string
		goVersion string
		want      string
	}{
		{name: "invalid JSON", content: []byte("{"), want: "decode starter selection"},
		{name: "unknown field", content: []byte(`{"schema":"spice.starters/v1","manifests":[],"extra":true}`), want: "unknown field"},
		{name: "trailing value", content: append(append([]byte(nil), valid...), []byte(`{}`)...), want: "trailing JSON value"},
		{name: "schema", content: []byte(`{"schema":"future","manifests":[{}]}`), want: "starter selection schema"},
		{name: "empty", content: []byte(`{"schema":"spice.starters/v1","manifests":[]}`), want: "at least one manifest"},
		{name: "invalid manifest", content: []byte(`{"schema":"spice.starters/v1","manifests":[{}]}`), want: "manifest schema"},
		{name: "Spice API", content: valid, spiceAPI: "v2alpha1", want: "requires Spice API"},
		{name: "Go version", content: valid, goVersion: "go1.25.9", want: "requires Go 1.26.0 or newer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.spiceAPI == "" {
				test.spiceAPI = publicstarter.APIVersion
			}
			if test.goVersion == "" {
				test.goVersion = currentGo
			}
			_, parseErr := compilerstarter.ParseWithCompatibility(
				test.content,
				test.spiceAPI,
				test.goVersion,
			)
			if parseErr == nil || !strings.Contains(parseErr.Error(), test.want) {
				t.Fatalf("ParseWithCompatibility() error = %v, want %q", parseErr, test.want)
			}
		})
	}

	if _, err := (compilerstarter.Catalog{}).JSON(); err == nil {
		t.Fatal("zero Catalog.JSON() succeeded")
	}
}
