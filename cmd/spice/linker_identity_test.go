package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	versionLinkSymbol = "github.com/spice-framework/toolchain/internal/cli.Version"
	commitLinkSymbol  = "github.com/spice-framework/toolchain/internal/cli.Commit"
	linkedCommit      = "0123456789abcdef0123456789abcdef01234567"
)

func TestExecutableLinkerIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		linker     string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "development defaults", wantCode: 0,
			wantStdout: "spice v0.1.0-preview.8 (development)\n",
		},
		{
			name: "release assignments",
			linker: "-buildid= -X=" + versionLinkSymbol + "=0.1.0-preview.8" +
				" -X=" + commitLinkSymbol + "=" + linkedCommit,
			wantCode: 0, wantStdout: "spice 0.1.0-preview.8 (" + linkedCommit + ")\n",
		},
		{
			name: "malformed release version",
			linker: "-buildid= -X=" + versionLinkSymbol + "=not-semver" +
				" -X=" + commitLinkSymbol + "=" + linkedCommit,
			wantCode: 1, wantStderr: "Spice version identity is invalid: release version must be canonical SemVer without a v prefix or build metadata\n",
		},
		{
			name: "shorthand release version",
			linker: "-buildid= -X=" + versionLinkSymbol + "=1.2" +
				" -X=" + commitLinkSymbol + "=" + linkedCommit,
			wantCode: 1, wantStderr: "Spice version identity is invalid: release version must be canonical SemVer without a v prefix or build metadata\n",
		},
		{
			name: "empty release version",
			linker: "-buildid= -X=" + versionLinkSymbol + "=" +
				" -X=" + commitLinkSymbol + "=" + linkedCommit,
			wantCode: 1, wantStderr: "Spice version identity is invalid: release version must be canonical SemVer without a v prefix or build metadata\n",
		},
		{
			name: "empty release commit",
			linker: "-buildid= -X=" + versionLinkSymbol + "=0.1.0-preview.8" +
				" -X=" + commitLinkSymbol + "=",
			wantCode: 1, wantStderr: "Spice version identity is invalid: release commit must be exactly 40 lowercase hexadecimal characters\n",
		},
		{
			name:       "version symbol only",
			linker:     "-buildid= -X=" + versionLinkSymbol + "=0.1.0-preview.8",
			wantCode:   1,
			wantStderr: "Spice version identity is invalid: development version and commit must be selected together\n",
		},
		{
			name:       "commit symbol only",
			linker:     "-buildid= -X=" + commitLinkSymbol + "=" + linkedCommit,
			wantCode:   1,
			wantStderr: "Spice version identity is invalid: development version and commit must be selected together\n",
		},
		{
			name: "malformed release commit",
			linker: "-buildid= -X=" + versionLinkSymbol + "=0.1.0-preview.8" +
				" -X=" + commitLinkSymbol + "=abc",
			wantCode: 1, wantStderr: "Spice version identity is invalid: release commit must be exactly 40 lowercase hexadecimal characters\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binary := buildSpiceIdentityFixture(t, test.linker)
			command := exec.CommandContext(t.Context(), binary, "--version") // #nosec G204 -- exact test-built binary and fixed argument.
			command.Env = offlineExecutableEnvironment()
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			code := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("run linked Spice binary: %v", err)
			}
			if code != test.wantCode || stdout.String() != test.wantStdout || stderr.String() != test.wantStderr {
				t.Fatalf(
					"linked identity exit=%d stdout=%q stderr=%q",
					code, stdout.String(), stderr.String(),
				)
			}
		})
	}
}

func buildSpiceIdentityFixture(t *testing.T, linker string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "Spice linked identity π")
	if err = os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "spice"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(directory, name)
	arguments := []string{"build", "-mod=vendor", "-trimpath", "-buildvcs=false", "-o", binary}
	if linker != "" {
		arguments = append(arguments, "-ldflags="+linker)
	}
	arguments = append(arguments, "./cmd/spice")
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), goExecutable, arguments...) // #nosec G204 -- selected Go tool and discrete test-owned arguments.
	command.Dir = root
	command.Env = offlineExecutableEnvironment()
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		t.Fatalf("build linked Spice binary: %v\n%s", buildErr, output)
	}
	return binary
}

func offlineExecutableEnvironment() []string {
	overrides := map[string]string{
		"CGO_ENABLED": "0", "GOFLAGS": "", "GOPROXY": "off", "GOSUMDB": "off",
		"GOTOOLCHAIN": "local", "GOWORK": "off",
	}
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := overrides[strings.ToUpper(name)]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}
