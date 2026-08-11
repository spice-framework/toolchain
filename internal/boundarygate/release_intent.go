package boundarygate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spice-framework/toolchain/internal/identity"
)

const releaseIntentPath = "spice-release.json"

const maximumReleaseIntentBytes = 16 << 10

type releaseIntent struct {
	Schema     int    `json:"schema"`
	Profile    string `json:"profile"`
	Repository string `json:"repository"`
	Module     string `json:"module"`
	Version    string `json:"version"`
}

func expectedReleaseIntent() releaseIntent {
	return releaseIntent{
		Schema:     1,
		Profile:    "go-distribution-v1",
		Repository: "toolchain",
		Module:     identity.ToolchainModule,
		Version:    "v0.1.0-preview.3",
	}
}

func (gate verifier) releaseIntent() (returnErr error) {
	root, err := os.OpenRoot(gate.root)
	if err != nil {
		return fmt.Errorf("open release intent root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	path := filepath.FromSlash(releaseIntentPath)
	info, err := root.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect release intent: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("release intent must be a physical regular file")
	}
	if info.Size() > maximumReleaseIntentBytes {
		return fmt.Errorf("release intent exceeds %d bytes", maximumReleaseIntentBytes)
	}
	content, err := root.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read release intent: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var actual releaseIntent
	if err = decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode release intent: %w", err)
	}
	var trailing any
	if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return errors.New("release intent contains trailing JSON")
		}
		return fmt.Errorf("decode trailing release intent data: %w", trailingErr)
	}
	canonical, err := json.MarshalIndent(expectedReleaseIntent(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode expected release intent: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(content, canonical) {
		return errors.New("release intent differs from the reviewed canonical contract")
	}
	return nil
}
