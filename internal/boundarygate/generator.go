package boundarygate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spice-framework/toolchain/compiler/generate"
	"github.com/spice-framework/toolchain/internal/identity"
)

const (
	generatorCompatibilityPath    = "compatibility/generator.json"
	generatorCompatibilitySchema  = "spice.generator.compatibility/v1"
	maximumGeneratorContractBytes = 64 << 10
)

type generatorCompatibility struct {
	Schema               string `json:"schema"`
	Module               string `json:"module"`
	GeneratorVersion     string `json:"generator_version"`
	Status               string `json:"status"`
	ManifestSchema       int    `json:"manifest_schema"`
	GoFormatLine         string `json:"go_format_line"`
	AnalysisBuildTag     string `json:"analysis_build_tag"`
	AcceptedInputSchemas []int  `json:"accepted_input_schemas"`
	WriteSchema          int    `json:"write_schema"`
	GeneratedOwnership   string `json:"generated_ownership"`
	ManualEditPolicy     string `json:"manual_edit_policy"`
	StaleFilePolicy      string `json:"stale_file_policy"`
	PathPolicy           string `json:"path_policy"`
	Determinism          string `json:"determinism"`
}

func expectedGeneratorCompatibility() generatorCompatibility {
	return generatorCompatibility{
		Schema:               generatorCompatibilitySchema,
		Module:               identity.ToolchainModule,
		GeneratorVersion:     generate.GeneratorVersion,
		Status:               "frozen-contract",
		ManifestSchema:       generate.SchemaVersion,
		GoFormatLine:         generate.GoFormatLine,
		AnalysisBuildTag:     generate.AnalysisBuildTag,
		AcceptedInputSchemas: []int{1, 2, 3, 4, 5, 6},
		WriteSchema:          generate.SchemaVersion,
		GeneratedOwnership:   "manifest-only",
		ManualEditPolicy:     "reject",
		StaleFilePolicy:      "remove-only-when-owned-hash-matches",
		PathPolicy:           "module-relative-forward-slash-case-fold-unique",
		Determinism:          "same-input-same-bytes",
	}
}

func (gate verifier) generatorCompatibility() error {
	root, err := os.OpenRoot(gate.root)
	if err != nil {
		return fmt.Errorf("open generator compatibility root: %w", err)
	}
	content, readErr := root.ReadFile(filepath.FromSlash(generatorCompatibilityPath))
	readCloseErr := errors.Join(readErr, root.Close())
	if readCloseErr != nil {
		return fmt.Errorf("read generator compatibility contract: %w", readCloseErr)
	}
	if len(content) > maximumGeneratorContractBytes {
		return fmt.Errorf(
			"generator compatibility contract exceeds %d bytes",
			maximumGeneratorContractBytes,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var actual generatorCompatibility
	if decodeErr := decoder.Decode(&actual); decodeErr != nil {
		return fmt.Errorf("decode generator compatibility contract: %w", decodeErr)
	}
	if trailingErr := requireGeneratorContractEOF(decoder); trailingErr != nil {
		return trailingErr
	}
	expected := expectedGeneratorCompatibility()
	canonical, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return fmt.Errorf("encode expected generator compatibility contract: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(content, canonical) {
		return errors.New("generator compatibility contract differs from the reviewed canonical contract")
	}
	return nil
}

func requireGeneratorContractEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("generator compatibility contract contains trailing JSON")
		}
		return fmt.Errorf("decode trailing generator compatibility data: %w", err)
	}
	return nil
}
