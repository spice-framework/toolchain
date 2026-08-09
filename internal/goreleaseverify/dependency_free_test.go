package goreleaseverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyFreeSpicePolicyAndCommittedGraph(t *testing.T) {
	t.Parallel()
	policy := releasePolicies["spice"]
	if policy.module != "github.com/spice-framework/spice" || policy.version != "v0.1.0-preview.2" ||
		len(policy.requiredModules) != 0 {
		t.Fatalf("dependency-free Spice policy = %#v", policy)
	}
	for _, test := range []struct {
		name    string
		variant string
		want    string
	}{
		{name: "valid"},
		{name: "sum only", variant: "sum-only", want: "omit both"},
		{name: "vendor metadata only", variant: "vendor-only", want: "omit both"},
		{name: "both graph files", variant: "both", want: "noncanonical"},
		{name: "unexpected vendor", variant: "unexpected-vendor", want: "unexpected vendor"},
		{name: "requirement", variant: "require", want: "must not select"},
		{name: "tool", variant: "tool", want: "must not contain tool"},
		{name: "exclude", variant: "exclude", want: "exclude or ignore"},
		{name: "ignore", variant: "ignore", want: "exclude or ignore"},
		{name: "replacement", variant: "replace", want: "must not contain replace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, source := dependencyFreeSourceFixture(t, test.variant, policy)
			trusted, err := trustedSource(t.Context(), config, policy)
			if err != nil {
				t.Fatalf("trustedSource() error = %v", err)
			}
			if trusted.commit != source.commit {
				t.Fatalf("trusted commit = %q, want %q", trusted.commit, source.commit)
			}
			modules, err := validateCommittedModule(t.Context(), trusted, policy)
			if test.want == "" {
				if err != nil || len(modules) != 0 {
					t.Fatalf("validateCommittedModule(valid) = %#v, %v", modules, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCommittedModule(%s) error = %v, want %q", test.variant, err, test.want)
			}
		})
	}
}

func TestVerifyDependencyFreeGraphAndBuild(t *testing.T) {
	t.Parallel()
	policy := releasePolicies["spice"]
	newWorkspace := func(t *testing.T) isolatedWorkspace {
		t.Helper()
		base := t.TempDir()
		source := filepath.Join(base, "source")
		for _, directory := range []string{
			source,
			filepath.Join(base, "module-cache"), filepath.Join(base, "build-cache"),
			filepath.Join(base, "go-path"), filepath.Join(base, "temporary"),
		} {
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(source, "go.mod"), []byte(
			"module "+policy.module+"\n\ngo 1.26.0\n\ntoolchain go1.26.5\n",
		))
		writeFile(t, filepath.Join(source, "spice.go"), []byte("package spice\n"))
		return isolatedWorkspace{
			base: base, source: source,
			moduleCache: filepath.Join(base, "module-cache"), buildCache: filepath.Join(base, "build-cache"),
			goPath: filepath.Join(base, "go-path"), temporary: filepath.Join(base, "temporary"),
		}
	}
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		runner := &dependencyFreeRunner{module: policy.module}
		if err := verifyDependencyFreeAndBuild(t.Context(), newWorkspace(t), policy, runner); err != nil {
			t.Fatalf("verifyDependencyFreeAndBuild() error = %v", err)
		}
		if !runner.built {
			t.Fatal("dependency-free verifier did not run the read-only build")
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*dependencyFreeRunner)
		want   string
	}{
		{name: "selected module", mutate: func(value *dependencyFreeRunner) { value.moduleGraph = "example.com/other@v1.0.0\n" }, want: "selected module graph"},
		{name: "package smuggling", mutate: func(value *dependencyFreeRunner) { value.packageGraph = "example.com/other@v1.0.0\n" }, want: "unexpected module"},
		{name: "build failure", mutate: func(value *dependencyFreeRunner) { value.buildErr = errors.New("failed") }, want: "read-only graph"},
		{name: "module mutation", mutate: func(value *dependencyFreeRunner) { value.mutateMod = true }, want: "changed during"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &dependencyFreeRunner{module: policy.module}
			test.mutate(runner)
			if err := verifyDependencyFreeAndBuild(t.Context(), newWorkspace(t), policy, runner); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyDependencyFreeAndBuild(%s) error = %v", test.name, err)
			}
		})
	}
	t.Run("unexpected filesystem graph", func(t *testing.T) {
		t.Parallel()
		workspace := newWorkspace(t)
		writeFile(t, filepath.Join(workspace.source, "go.sum"), nil)
		if err := verifyDependencyFreeAndBuild(
			t.Context(), workspace, policy, &dependencyFreeRunner{module: policy.module},
		); err == nil || !strings.Contains(err.Error(), "unexpectedly contains go.sum") {
			t.Fatalf("verifyDependencyFreeAndBuild(go.sum) error = %v", err)
		}
	})
}

type dependencyFreeRunner struct {
	module       string
	moduleGraph  string
	packageGraph string
	buildErr     error
	mutateMod    bool
	built        bool
}

func (runner *dependencyFreeRunner) Output(
	_ context.Context,
	root string,
	_ []string,
	arguments ...string,
) ([]byte, error) {
	command := strings.Join(arguments, " ")
	switch command {
	case "version":
		return []byte("go version go1.26.5 fixture\n"), nil
	case "list -mod=readonly -m -f={{.Path}}@{{.Version}} all":
		if runner.moduleGraph != "" {
			return []byte(runner.moduleGraph), nil
		}
		return []byte(runner.module + "@\n"), nil
	case "list -mod=readonly -deps -f={{with .Module}}{{.Path}}@{{.Version}}{{end}} ./...":
		if runner.packageGraph != "" {
			return []byte(runner.packageGraph), nil
		}
		return []byte(runner.module + "@\n\n"), nil
	case "build -mod=readonly -trimpath ./...":
		runner.built = true
		if runner.mutateMod {
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module compromised\n"), 0o600); err != nil {
				return nil, err
			}
		}
		return nil, runner.buildErr
	default:
		return nil, fmt.Errorf("unexpected dependency-free command %q", command)
	}
}

func dependencyFreeSourceFixture(
	t *testing.T,
	variant string,
	policy releasePolicy,
) (Config, sourceIdentity) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module " + policy.module + "\n\ngo 1.26.0\n\ntoolchain go1.26.5\n"
	switch variant {
	case "require":
		goMod += "\nrequire example.com/dependency v1.0.0\n"
	case "tool":
		goMod += "\ntool example.com/dependency/cmd/tool\n"
	case "replace":
		goMod += "\nreplace example.com/dependency => example.com/other v1.0.0\n"
	case "exclude":
		goMod += "\nexclude example.com/dependency v1.0.0\n"
	case "ignore":
		goMod += "\nignore hidden\n"
	}
	writeFile(t, filepath.Join(root, "go.mod"), []byte(goMod))
	writeFile(t, filepath.Join(root, "LICENSE"), []byte("Apache-2.0\n"))
	writeFile(t, filepath.Join(root, "README.md"), []byte("# Spice\n"))
	writeFile(t, filepath.Join(root, "spice.go"), []byte("package spice\n"))
	writeFile(t, filepath.Join(root, policy.metadataFile), []byte(fmt.Sprintf(`{
  "schema": 1,
  "profile": "go-module-v1",
  "repository": %q,
  "module": %q,
  "version": %q
}
`, policy.repository, policy.module, policy.version)))
	if variant == "sum-only" || variant == "both" {
		writeFile(t, filepath.Join(root, "go.sum"), nil)
	}
	if variant == "vendor-only" || variant == "both" {
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, "vendor", "modules.txt"), []byte("## empty\n"))
	}
	if variant == "unexpected-vendor" {
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, "vendor", "smuggled.go"), []byte("package smuggled\n"))
	}
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "test@spice.invalid")
	runGit(t, root, "remote", "add", "origin", policy.source+".git")
	runGit(t, root, "add", ".")
	runGitEnv(t, root, []string{
		"GIT_AUTHOR_DATE=2026-08-09T00:00:00Z", "GIT_COMMITTER_DATE=2026-08-09T00:00:00Z",
	}, "commit", "-q", "-m", "dependency-free fixture")
	runGit(t, root, "tag", policy.version)
	commit := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	config := Config{
		Repository: root, RepositoryName: policy.repository,
		CanonicalSource: policy.source, Module: policy.module,
		Version: policy.version, Commit: commit, Profile: ProfileGoModule,
	}
	return config, sourceIdentity{root: root, commit: commit}
}

func TestDependencyFreeGraphOutputIsExact(t *testing.T) {
	t.Parallel()
	runner := &dependencyFreeRunner{module: "example.com/module"}
	output, err := runner.Output(t.Context(), t.TempDir(), nil, "version")
	if err != nil || !bytes.Contains(output, []byte("go1.26.5")) {
		t.Fatalf("dependencyFreeRunner version = %q, %v", output, err)
	}
}
