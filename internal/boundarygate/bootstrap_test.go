package boundarygate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBootstrapEnvironmentUsesOnlyPublicAuthenticatedGraphSettings(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"GOAUTH": "off", "GOENV": "off", "GOFLAGS": "", "GONOPROXY": "",
		"GONOSUMDB": "", "GOPRIVATE": "", "GOPROXY": "https://proxy.golang.org",
		"GOSUMDB": "sum.golang.org", "GOTOOLCHAIN": "local", "GOWORK": "off",
	}
	if got := bootstrapEnvironment(); len(got) != len(want) {
		t.Fatalf("bootstrap environment = %#v", got)
	} else {
		for name, value := range want {
			if got[name] != value {
				t.Fatalf("bootstrap environment[%s] = %q, want %q", name, got[name], value)
			}
		}
	}
}

func TestToolsBootstrapUsesExternalMirroredGraphsInDeterministicOrder(t *testing.T) {
	t.Parallel()
	root := bootstrapFixture(t)
	before, err := snapshotRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	var temporary string
	gate := verifier{root: root, execute: func(
		_ context.Context,
		directory string,
		environment map[string]string,
		executable string,
		arguments ...string,
	) ([]byte, error) {
		if executable != "go" || len(arguments) != 4 ||
			!slices.Equal(arguments[:2], []string{"mod", "download"}) || arguments[3] != "all" {
			t.Fatalf("bootstrap command = %s %q", executable, arguments)
		}
		if environment["GOPROXY"] != "https://proxy.golang.org" ||
			environment["GOSUMDB"] != "sum.golang.org" || environment["GOPRIVATE"] != "" {
			t.Fatalf("bootstrap environment = %#v", environment)
		}
		modfile := strings.TrimPrefix(arguments[2], "-modfile=")
		if modfile == arguments[2] || strings.HasPrefix(modfile, root) || filepath.Base(modfile) != "bootstrap.mod" {
			t.Fatalf("bootstrap modfile = %q", modfile)
		}
		if temporary == "" {
			temporary = filepath.Dir(modfile)
		}
		module, readErr := os.ReadFile(modfile)
		if readErr != nil {
			t.Fatal(readErr)
		}
		relative, relErr := filepath.Rel(root, directory)
		if relErr != nil {
			t.Fatal(relErr)
		}
		wantModule, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
		if readErr != nil || !slices.Equal(module, wantModule) {
			t.Fatalf("mirrored module %s differs: %v", relative, readErr)
		}
		calls = append(calls, filepath.ToSlash(relative))
		return nil, nil
	}}
	if err = gate.toolsBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, bootstrapModules) {
		t.Fatalf("bootstrap order = %v, want %v", calls, bootstrapModules)
	}
	if _, err = os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bootstrap mirror remains: %v", err)
	}
	after, err := snapshotRepository(root)
	if err != nil || !repositorySnapshotsEqual(before, after) {
		t.Fatalf("repository changed after bootstrap: %v", err)
	}
}

func TestToolsBootstrapPropagatesCancellationFailureAndMutation(t *testing.T) {
	t.Parallel()
	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := (verifier{root: bootstrapFixture(t), execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			calls++
			return nil, nil
		}}).toolsBootstrap(ctx)
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("canceled bootstrap = calls %d, error %v", calls, err)
		}
	})
	t.Run("command failure", func(t *testing.T) {
		t.Parallel()
		root := bootstrapFixture(t)
		sentinel := errors.New("download failed")
		calls := 0
		var mirror string
		err := (verifier{root: root, execute: func(_ context.Context, _ string, _ map[string]string, _ string, arguments ...string) ([]byte, error) {
			calls++
			mirror = filepath.Dir(filepath.Dir(strings.TrimPrefix(arguments[2], "-modfile=")))
			if calls == 2 {
				return nil, sentinel
			}
			return nil, nil
		}}).toolsBootstrap(context.Background())
		if !errors.Is(err, sentinel) || calls != 2 {
			t.Fatalf("failed bootstrap = calls %d, error %v", calls, err)
		}
		if _, statErr := os.Stat(mirror); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failed bootstrap mirror remains: %v", statErr)
		}
	})
	t.Run("in-flight cancellation", func(t *testing.T) {
		t.Parallel()
		root := bootstrapFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := (verifier{root: root, execute: func(callContext context.Context, _ string, _ map[string]string, _ string, _ ...string) ([]byte, error) {
			calls++
			cancel()
			return nil, callContext.Err()
		}}).toolsBootstrap(ctx)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("in-flight canceled bootstrap = calls %d, error %v", calls, err)
		}
	})
	t.Run("repository mutation", func(t *testing.T) {
		t.Parallel()
		root := bootstrapFixture(t)
		err := (verifier{root: root, execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			return nil, os.WriteFile(filepath.Join(root, "mutated.txt"), []byte("mutation\n"), 0o600)
		}}).toolsBootstrap(context.Background())
		if err == nil || !strings.Contains(err.Error(), "modified the repository") {
			t.Fatalf("mutating bootstrap error = %v", err)
		}
	})
	t.Run("missing checksum graph", func(t *testing.T) {
		t.Parallel()
		root := bootstrapFixture(t)
		if err := os.Remove(filepath.Join(root, "tools", "actionlint", "go.sum")); err != nil {
			t.Fatal(err)
		}
		calls := 0
		err := (verifier{root: root, execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			calls++
			return nil, nil
		}}).toolsBootstrap(context.Background())
		if err == nil || calls != 0 || !strings.Contains(err.Error(), "go.sum") {
			t.Fatalf("missing checksum bootstrap = calls %d, error %v", calls, err)
		}
	})
}

func TestBootstrapEntrypointsAreExact(t *testing.T) {
	t.Parallel()
	valid := validCandidateMakefile()
	workflow := validCandidateCIWorkflow()
	for _, test := range []struct {
		name           string
		mutateMakefile func(string) string
		mutateWorkflow func(string) string
	}{
		{name: "missing phony", mutateMakefile: func(value string) string { return strings.Replace(value, " "+toolsBootstrapTarget, "", 1) }},
		{name: "wrong bootstrap mode", mutateMakefile: func(value string) string { return strings.Replace(value, "mode=tools-bootstrap", "mode=check", 1) }},
		{name: "wrong release mode", mutateMakefile: func(value string) string { return strings.Replace(value, "mode=verify-release", "mode=verify", 1) }},
		{name: "duplicate release target", mutateMakefile: func(value string) string { return value + "\nverify-release: bypass\n\t@echo bypass\n" }},
		{name: "missing CI bootstrap", mutateWorkflow: func(value string) string {
			return strings.Replace(value, "      - name: Bootstrap candidate-owned pinned graphs\n        run: make tools-bootstrap\n", "", 1)
		}},
		{name: "CI restores shared cache", mutateWorkflow: func(value string) string {
			return strings.Replace(value, "          cache: false", "          cache: true", 1)
		}},
		{name: "CI verification permits network", mutateWorkflow: func(value string) string {
			return strings.Replace(value, "          GOPROXY: \"off\"", "          GOPROXY: https://proxy.golang.org", 1)
		}},
		{name: "CI verification precedes bootstrap", mutateWorkflow: func(value string) string {
			bootstrap := "      - name: Bootstrap candidate-owned pinned graphs\n        run: make tools-bootstrap\n"
			offline := "      - name: Verify standalone release boundary offline\n" +
				"        env:\n          GOPROXY: \"off\"\n          GOSUMDB: \"off\"\n" +
				"          GOTOOLCHAIN: local\n          GOWORK: \"off\"\n" +
				"        run: make verify-release\n"
			return strings.Replace(value, bootstrap+offline, offline+bootstrap, 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			makefile := valid
			if test.mutateMakefile != nil {
				makefile = test.mutateMakefile(makefile)
			}
			ci := workflow
			if test.mutateWorkflow != nil {
				ci = test.mutateWorkflow(ci)
			}
			writeGateFile(t, root, "Makefile", makefile)
			writeGateFile(t, root, ".github/workflows/ci.yml", ci)
			if err := (verifier{root: root}).bootstrapEntrypoints(); err == nil {
				t.Fatal("mutated bootstrap entrypoints succeeded")
			}
		})
	}
	root := t.TempDir()
	writeGateFile(t, root, "Makefile", valid)
	writeGateFile(t, root, ".github/workflows/ci.yml", workflow)
	if err := (verifier{root: root}).bootstrapEntrypoints(); err != nil {
		t.Fatal(err)
	}
}

func validCandidateCIWorkflow() string {
	return "steps:\n" +
		"      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6\n" +
		"        with:\n" +
		"          go-version: 1.26.5\n" +
		"          cache: false\n" +
		"      - name: Bootstrap candidate-owned pinned graphs\n" +
		"        run: make tools-bootstrap\n" +
		"      - name: Verify standalone release boundary offline\n" +
		"        env:\n" +
		"          GOPROXY: \"off\"\n" +
		"          GOSUMDB: \"off\"\n" +
		"          GOTOOLCHAIN: local\n" +
		"          GOWORK: \"off\"\n" +
		"        run: make verify-release\n"
}

func bootstrapFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, relative := range bootstrapModules {
		module := "example.com/toolchain"
		if relative != "." {
			module += "/" + strings.ReplaceAll(relative, "testdata/", "test/")
		}
		content := "module " + module + "\n\ngo 1.26.0\n"
		if relative == "testdata/annotationapp" {
			content += "\nreplace example.com/toolchain/test/annotationfixture => ../annotationfixture\n\nreplace example.com/toolchain => ../..\n"
		}
		writeGateFile(t, root, filepath.ToSlash(filepath.Join(relative, "go.mod")), content)
		writeGateFile(t, root, filepath.ToSlash(filepath.Join(relative, "go.sum")), "example.com/dependency v1.0.0 h1:fixture\n")
	}
	writeGateFile(t, root, "marker.txt", "unchanged\n")
	return root
}

func validCandidateMakefile() string {
	return ".PHONY: fast " + toolsBootstrapTarget + " verify " + verifyReleaseTarget + " " + releaseArtifactTarget + "\n\n" +
		toolsBootstrapTarget + ":\n\tgo run ./internal/boundarygate/cmd -mode=tools-bootstrap\n\n" +
		"verify:\n\tgo run ./internal/boundarygate/cmd -mode=verify\n\n" +
		verifyReleaseTarget + ":\n\tgo run ./internal/boundarygate/cmd -mode=verify-release\n\n" +
		releaseArtifactTarget + ":\n\tgo run ./internal/boundarygate/cmd -mode=release-artifacts -artifacts=\"$(" + releaseArtifactInput + ")\"\n"
}
