package boundarygate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	releaseArtifactTarget    = "verify-release-artifacts"
	releaseArtifactInput     = "SPICE_DISTRIBUTION_VERIFIED_ARTIFACT_DIR"
	releaseArtifactRunnerAck = "SPICE_DISTRIBUTION_EPHEMERAL_RUNNER"
)

func (gate verifier) releaseArtifactEntrypoint() error {
	content, err := os.ReadFile(filepath.Join(gate.root, "Makefile"))
	if err != nil {
		return fmt.Errorf("read Makefile release artifact entrypoint: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	recipe := releaseArtifactTarget + ":\n\tgo run ./internal/boundarygate/cmd -mode=release-artifacts -artifacts=\"$(" + releaseArtifactInput + ")\"\n"
	targetRules := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, releaseArtifactTarget+":") {
			targetRules++
		}
	}
	if strings.Count(text, recipe) != 1 || targetRules != 1 {
		return errors.New("makefile must expose the exact release artifact verification recipe")
	}
	firstLine, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(firstLine, ".PHONY:") ||
		!containsField(strings.TrimPrefix(firstLine, ".PHONY:"), releaseArtifactTarget) {
		return errors.New("release artifact verification target must be phony")
	}
	return nil
}

func containsField(text, expected string) bool {
	for _, field := range strings.Fields(text) {
		if field == expected {
			return true
		}
	}
	return false
}

func (gate verifier) releaseArtifacts(ctx context.Context, directory string) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := normalizeReleaseArtifactDirectory(directory)
	if err != nil {
		return err
	}
	verify := gate.verifySubjects
	if verify == nil {
		verify = verifyReleaseSubjects
	}
	set, err := verify(ctx, directory)
	if err != nil {
		return fmt.Errorf("verify release subjects: %w", err)
	}
	scratch, err := os.MkdirTemp("", "spice-toolchain-release-artifacts-")
	if err != nil {
		return fmt.Errorf("create release verification scratch directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(scratch); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove release verification scratch directory: %w", cleanupErr))
		}
	}()

	extraction := filepath.Join(scratch, "Spice Toolchain installed bytes")
	installRoot, err := set.ExtractNativeContext(ctx, extraction, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("extract native release archive: %w", err)
	}
	binary := filepath.Join(installRoot, "spice"+executableSuffix())
	info, err := os.Lstat(binary)
	if err != nil {
		return fmt.Errorf("inspect installed Spice binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("installed Spice binary must be a physical regular file")
	}
	stdout, stderr, err := gate.releaseArtifactCommand(ctx, installRoot, binary, "--version")
	if err != nil {
		return fmt.Errorf("execute installed Spice binary: %w", err)
	}
	expected := fmt.Sprintf("spice %s (%s)\n", strings.TrimPrefix(set.Version(), "v"), set.Commit())
	if string(stdout) != expected {
		return errors.New("installed Spice identity did not match verified release metadata")
	}
	if len(stderr) != 0 {
		return fmt.Errorf("installed Spice wrote %d unexpected stderr bytes", len(stderr))
	}
	return nil
}

func (gate verifier) releaseArtifactCommand(
	ctx context.Context,
	directory, executable string,
	arguments ...string,
) ([]byte, []byte, error) {
	environment := releaseArtifactOverrides()
	if gate.executeStreams != nil {
		stdout, stderr, err := gate.executeStreams(ctx, directory, environment, executable, arguments...)
		return checkedReleaseArtifactOutput(stdout, stderr, err)
	}
	if gate.execute != nil {
		stdout, err := gate.execute(ctx, directory, environment, executable, arguments...)
		return checkedReleaseArtifactOutput(stdout, nil, err)
	}
	command := exec.CommandContext(ctx, executable, arguments...) // #nosec G204 -- executable is an independently verified release subject.
	command.Dir = directory
	command.Env = releaseArtifactEnvironment(os.Environ(), environment)
	stdout := newBoundedBuffer(maxCommandFailureOutput)
	stderr := newBoundedBuffer(maxCommandFailureOutput)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	return checkedReleaseArtifactOutput(stdout.Bytes(), stderr.Bytes(), err)
}

func checkedReleaseArtifactOutput(stdout, stderr []byte, err error) ([]byte, []byte, error) {
	if len(stdout) > maxCommandFailureOutput || len(stderr) > maxCommandFailureOutput {
		return nil, nil, errors.New("installed Spice output exceeded the verification limit")
	}
	return stdout, stderr, err
}

func releaseArtifactOverrides() map[string]string {
	return map[string]string{
		"CGO_ENABLED": "0",
		"GOFLAGS":     "-mod=vendor",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
}

func releaseArtifactEnvironment(ambient []string, overrides map[string]string) []string {
	values := make(map[string]string)
	for _, item := range ambient {
		name, value, found := strings.Cut(item, "=")
		if !found || !releaseArtifactAmbientAllowed(name) {
			continue
		}
		values[strings.ToUpper(name)] = name + "=" + value
	}
	for name, value := range overrides {
		values[strings.ToUpper(name)] = name + "=" + value
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func releaseArtifactAmbientAllowed(name string) bool {
	switch strings.ToUpper(name) {
	case "SYSTEMROOT", "WINDIR":
		return true
	default:
		return false
	}
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func newBoundedBuffer(maximum int) *boundedBuffer {
	return &boundedBuffer{remaining: maximum}
}

func (buffer *boundedBuffer) Write(content []byte) (int, error) {
	if len(content) > buffer.remaining {
		return 0, errors.New("installed Spice output exceeded the verification limit")
	}
	written, err := buffer.buffer.Write(content)
	buffer.remaining -= written
	return written, err
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }
