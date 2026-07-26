package starter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"

	publicstarter "github.com/StevenBuglione/spice/starter"
)

const (
	// SelectionSchema is the wire schema for an application-selected starter
	// catalog.
	SelectionSchema = "spice.starters/v1"
)

type selectionDocument struct {
	Schema    string            `json:"schema"`
	Manifests []json.RawMessage `json:"manifests"`
}

// Parse strictly decodes an application-selected starter catalog and checks
// compatibility against the current Spice API and Go runtime.
func Parse(content []byte) (Catalog, error) {
	return ParseWithCompatibility(
		content,
		publicstarter.APIVersion,
		runtime.Version(),
	)
}

// ParseWithCompatibility strictly decodes an application-selected starter
// catalog using an explicit compatibility environment.
func ParseWithCompatibility(
	content []byte,
	spiceAPI string,
	goVersion string,
) (Catalog, error) {
	var document selectionDocument
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Catalog{}, fmt.Errorf("decode starter selection: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Catalog{}, errors.New("decode starter selection: trailing JSON value")
		}
		return Catalog{}, fmt.Errorf("decode starter selection trailing data: %w", err)
	}
	if document.Schema != SelectionSchema {
		return Catalog{}, fmt.Errorf(
			"starter selection schema must be %q, got %q",
			SelectionSchema,
			document.Schema,
		)
	}
	if len(document.Manifests) == 0 {
		return Catalog{}, errors.New("starter selection requires at least one manifest")
	}

	manifests := make([]publicstarter.Manifest, len(document.Manifests))
	for index, encoded := range document.Manifests {
		manifest, err := publicstarter.Parse(encoded)
		if err != nil {
			return Catalog{}, fmt.Errorf(
				"decode starter selection manifest %d: %w",
				index,
				err,
			)
		}
		manifests[index] = manifest
	}
	catalog, err := NewWithCompatibility(spiceAPI, goVersion, manifests...)
	if err != nil {
		return Catalog{}, fmt.Errorf("validate starter selection: %w", err)
	}
	return catalog, nil
}

// JSON returns a canonical selection document with manifests sorted by their
// catalog identity.
func (catalog Catalog) JSON() ([]byte, error) {
	manifests := catalog.Manifests()
	if len(manifests) == 0 {
		return nil, errors.New("encode starter selection: catalog is empty")
	}
	raw := make([]json.RawMessage, len(manifests))
	for index, manifest := range manifests {
		encoded, err := manifest.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf(
				"encode starter selection manifest %d: %w",
				index,
				err,
			)
		}
		raw[index] = encoded
	}
	content, err := json.MarshalIndent(
		selectionDocument{
			Schema:    SelectionSchema,
			Manifests: raw,
		},
		"",
		"  ",
	)
	if err != nil {
		return nil, fmt.Errorf("encode starter selection: %w", err)
	}
	return append(content, '\n'), nil
}
