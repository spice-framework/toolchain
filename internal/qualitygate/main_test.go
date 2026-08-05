package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestVerifyOrchestration(t *testing.T) {
	silenceOutput(t)
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module "+modulePath+"\n\ngo 1.26.0\n")
	writeTestFile(t, root, "tools/go.mod", "module "+modulePath+"/tools\n\ngo 1.26.0\n")
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(
		t,
		root,
		"docs/spring-coverage.md",
		"| Area | Spring capability | Spice direction | Status |\n"+
			"|---|---|---|---|\n"+
			"| Core | Test | Executable contract | available |\n",
	)
	writeTestFile(
		t,
		root,
		"docs/api-compatibility.json",
		`{"schema":"spice.api-maturity/v1","module":"`+modulePath+`","classifications":[{"prefix":"sample","maturity":"preview-stable","reason":"test fixture"}]}`,
	)
	writeTestFile(t, root, "vendor/modules.txt", "# test vendor tree\n")
	writeBenchmarkFixture(t, root)

	originalRun, originalCapture := runExternal, captureExternal
	originalBootstrapCheck := runBootstrapCheck
	var calls []string
	var callsMu sync.Mutex
	recordCall := func(value string) {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls = append(calls, value)
	}
	runExternal = func(
		_ context.Context,
		directory string,
		_ map[string]string,
		executable string,
		arguments ...string,
	) error {
		recordCall(executable + " " + strings.Join(arguments, " "))
		simulateCleanRoomCommand(t, directory, executable, arguments)
		for index, argument := range arguments {
			if profile, found := strings.CutPrefix(
				argument,
				"-coverprofile=",
			); found {
				if err := os.WriteFile(
					profile,
					[]byte("mode: atomic\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if len(arguments) >= 2 &&
				arguments[0] == "mod" &&
				arguments[1] == "vendor" &&
				argument == "-o" &&
				index+1 < len(arguments) {
				copyTestTree(t, filepath.Join(directory, "vendor"), arguments[index+1])
			}
		}
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		executable string,
		arguments ...string,
	) (string, error) {
		recordCall(executable + " " + strings.Join(arguments, " "))
		switch {
		case executable == "go" &&
			slices.Equal(arguments, []string{"version"}):
			return "go version " + requiredGoVersion + " test/arch\n", nil
		case executable == "go" && slices.Equal(arguments, []string{
			"list", "-mod=vendor", "-f", "{{.ImportPath}}", "./...",
		}):
			return modulePath + "/sample\n", nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t90.0%\n", nil
		case slices.Contains(arguments, "-bench"):
			return benchmarkFixtureOutput(arguments), nil
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		default:
			return "", nil
		}
	}
	runBootstrapCheck = func(context.Context, string) error {
		recordCall("bootstrap recovery")
		return nil
	}
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
		runBootstrapCheck = originalBootstrapCheck
	})

	if err := verify(context.Background(), root, true); err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if err := format(context.Background(), root, true); err != nil {
		t.Fatalf("format(write=true) error = %v", err)
	}
	callsMu.Lock()
	recordedCalls := slices.Clone(calls)
	callsMu.Unlock()
	for _, expected := range []string{
		"go vet ./...",
		"run --timeout=10m",
		"-include-pkgs=" + modulePath,
		"-race -shuffle=on",
		"-coverprofile=",
		"-fuzztime=" + fuzzSmokeExecutions,
		"-mod=vendor -count=1",
		"generate --check --target Spice ./compiler/...",
		"bootstrap recovery",
	} {
		if !containsCall(recordedCalls, expected) {
			t.Fatalf("calls do not contain %q:\n%s", expected, strings.Join(recordedCalls, "\n"))
		}
	}
}

func TestCheckCanonicalNamespaceRejectsLegacySourceAndSkipsLocalArtifacts(
	t *testing.T,
) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "canonical.go", "package canonical\n")
	writeTestFile(t, root, ".tmp/reference.txt", legacyModulePath)
	writeTestFile(t, root, ".idea/workspace.xml", legacyModulePath)
	if err := checkCanonicalNamespace(root); err != nil {
		t.Fatalf("checkCanonicalNamespace(canonical) error = %v", err)
	}

	writeTestFile(
		t,
		root,
		"docs/import.md",
		"import "+legacyModulePath+"/annotation/core\n",
	)
	err := checkCanonicalNamespace(root)
	if err == nil ||
		!strings.Contains(err.Error(), legacyModulePath) ||
		!strings.Contains(err.Error(), "docs/import.md") {
		t.Fatalf(
			"checkCanonicalNamespace(legacy) error = %v, want path and namespace",
			err,
		)
	}
}

func TestValidateAPIMaturityRequiresCompleteNonStaleCoverage(t *testing.T) {
	t.Parallel()
	valid := `{
  "schema": "spice.api-maturity/v1",
  "module": "` + modulePath + `",
  "classifications": [
    {"prefix":"annotation","maturity":"experimental","reason":"feature descriptors"},
    {"prefix":"annotation/sdk","maturity":"preview-stable","reason":"extension contract"},
    {"prefix":"compiler","maturity":"internal","reason":"toolchain implementation"}
  ]
}`
	packages := strings.Join([]string{
		modulePath + "/annotation/core",
		modulePath + "/annotation/sdk",
		modulePath + "/annotation/sdk/protocol",
		modulePath + "/compiler/provider",
	}, "\n")
	if err := validateAPIMaturity([]byte(valid), packages); err != nil {
		t.Fatalf("validateAPIMaturity() error = %v", err)
	}

	for name, test := range map[string]struct {
		policy   string
		packages string
		want     string
	}{
		"unclassified package": {
			policy:   valid,
			packages: packages + "\n" + modulePath + "/web",
			want:     "has no API maturity classification",
		},
		"stale prefix": {
			policy: strings.Replace(
				valid,
				`]`,
				`,{"prefix":"removed","maturity":"internal","reason":"stale"}]`,
				1,
			),
			packages: packages,
			want:     `prefix "removed" matches no Go package`,
		},
		"invalid maturity": {
			policy: strings.Replace(
				valid,
				`"preview-stable"`,
				`"stable"`,
				1,
			),
			packages: packages,
			want:     `invalid maturity "stable"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := validateAPIMaturity([]byte(test.policy), test.packages)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateAPIMaturity() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCheckSpringCoverageRejectsUnresolvedAndUndocumentedNonGoals(
	t *testing.T,
) {
	t.Parallel()
	for name, test := range map[string]struct {
		row     string
		wantErr string
	}{
		"available": {
			row: "| Core | DI | Exact generated calls | available |",
		},
		"integration": {
			row: "| Data | SQL | Driver integrations | integration |",
		},
		"deliberate non-goal": {
			row: "| Data | JPA | Deliberately uses explicit SQL | not-planned |",
		},
		"unresolved": {
			row:     "| Core | DI | Work remains | in-progress |",
			wantErr: `unresolved or invalid status "in-progress"`,
		},
		"planned": {
			row:     "| Core | DI | Work remains | planned |",
			wantErr: `unresolved or invalid status "planned"`,
		},
		"unexplained non-goal": {
			row:     "| Data | JPA | Explicit SQL | not-planned |",
			wantErr: "without a deliberate rationale",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeTestFile(
				t,
				root,
				"docs/spring-coverage.md",
				"| Area | Spring capability | Spice direction | Status |\n"+
					"|---|---|---|---|\n"+
					test.row+"\n",
			)
			err := checkSpringCoverage(root)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("checkSpringCoverage() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf(
					"checkSpringCoverage() error = %v, want containing %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestRepositoryAndFilesystemHelpers(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("repositoryRoot() error = %v", err)
	}
	data, err := readRootFile(root, "go.mod")
	if err != nil {
		t.Fatalf("readRootFile() error = %v", err)
	}
	if !strings.Contains(string(data), "module "+modulePath) {
		t.Fatalf("go.mod = %q", data)
	}
	tree := t.TempDir()
	writeTestFile(t, tree, "a.go", "package a\n")
	writeTestFile(t, tree, "nested/b.go", "package nested\n")
	writeTestFile(t, tree, "vendor/ignored.go", "package ignored\n")
	writeTestFile(t, tree, ".git/ignored.go", "package ignored\n")
	writeTestFile(t, tree, "out/ignored.go", "not valid Go\n")
	writeTestFile(t, tree, "dist/ignored.go", "not valid Go\n")
	writeTestFile(t, tree, "bin/ignored.go", "not valid Go\n")
	files, err := goFiles(tree)
	if err != nil {
		t.Fatalf("goFiles() error = %v", err)
	}
	if !slices.Equal(files, []string{"a.go", filepath.Join("nested", "b.go")}) {
		t.Fatalf("goFiles() = %#v", files)
	}

	first, err := treeDigest(tree)
	if err != nil {
		t.Fatalf("treeDigest() error = %v", err)
	}
	second, err := treeDigest(tree)
	if err != nil {
		t.Fatalf("treeDigest() second error = %v", err)
	}
	if !equalDigests(first, second) {
		t.Fatal("identical trees have different digests")
	}
	second["different"] = [32]byte{1}
	if equalDigests(first, second) {
		t.Fatal("different trees have equal digests")
	}
}

func TestCoverageAndExecutableHelpers(t *testing.T) {
	total, err := totalCoverage("file.go:1:\tFunction\t50.0%\ntotal:\t(statements)\t87.5%\n")
	if err != nil || total != 87.5 {
		t.Fatalf("totalCoverage() = %v, %v", total, err)
	}
	if _, coverageErr := totalCoverage("no total"); coverageErr == nil {
		t.Fatal("totalCoverage() error = nil")
	}
	for _, executable := range []string{"go", "gofumpt", "goimports", "golangci-lint", "gosec", "govulncheck", "gradlew", "gradlew.bat", "nilaway", "spice", "xvfb-run"} {
		if executableErr := validateExecutable(executable); executableErr != nil {
			t.Fatalf("validateExecutable(%q) error = %v", executable, executableErr)
		}
	}
	if executableErr := validateExecutable("curl"); executableErr == nil {
		t.Fatal("validateExecutable(curl) error = nil")
	}

	stdout, err := capture(context.Background(), t.TempDir(), "go", "version")
	if err != nil || !strings.Contains(stdout, requiredGoVersion) {
		t.Fatalf("capture(go version) = %q, %v", stdout, err)
	}
	if err := command(context.Background(), t.TempDir(), nil, "unapproved"); err == nil {
		t.Fatal("command(unapproved) error = nil")
	}
}

func TestCheckGeneratedTargetBoundariesRejectsMonolithsAndOversizedUnits(
	t *testing.T,
) {
	t.Parallel()
	root := t.TempDir()
	validPath := "internal/spicegen/app/spice_assembly_gen.go"
	writeTestFile(
		t,
		root,
		validPath,
		strings.Repeat("line\n", maximumGeneratedTargetLines),
	)
	writeTestFile(
		t,
		root,
		"internal/spicegen/app/sources/orders/orders_spice_gen.go",
		strings.Repeat("line\n", maximumGeneratedTargetLines+1),
	)
	writeQualityGateOwnershipManifest(t, root, []string{
		validPath,
		"internal/spicegen/app/sources/orders/orders_spice_gen.go",
	})
	if err := checkGeneratedTargetBoundaries(root); err != nil {
		t.Fatalf("checkGeneratedTargetBoundaries(valid) error = %v", err)
	}

	writeTestFile(
		t,
		root,
		validPath,
		strings.Repeat("line\n", maximumGeneratedTargetLines+1),
	)
	if err := checkGeneratedTargetBoundaries(root); err == nil ||
		!strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("checkGeneratedTargetBoundaries(oversized) error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(validPath))); err != nil {
		t.Fatal(err)
	}
	writeTestFile(
		t,
		root,
		"internal/spicegen/app/zz_spice_gen.go",
		"package spicegen\n",
	)
	writeQualityGateOwnershipManifest(t, root, []string{
		"internal/spicegen/app/sources/orders/orders_spice_gen.go",
		"internal/spicegen/app/zz_spice_gen.go",
	})
	if err := checkGeneratedTargetBoundaries(root); err == nil ||
		!strings.Contains(err.Error(), "retired generated target monolith") {
		t.Fatalf("checkGeneratedTargetBoundaries(monolith) error = %v", err)
	}
}

func TestFilterGeneratedCoverageProfileKeepsProductSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(
		t,
		root,
		"generated.go",
		"// Code generated by Spice. DO NOT EDIT.\n\npackage generated\n",
	)
	writeTestFile(
		t,
		root,
		"tagged.go",
		"//go:build !spice_generate\n\n"+
			"// Code generated by Spice. DO NOT EDIT.\n\n"+
			"package generated\n",
	)
	writeTestFile(
		t,
		root,
		"product.go",
		"package product\n\n"+
			"// Product source.\n"+
			"// Product source.\n"+
			"// Product source.\n"+
			"// Documentation may mention this text without being generated.\n"+
			"// Code generated by Spice. DO NOT EDIT.\n",
	)
	profilePath := filepath.Join(root, "coverage.out")
	productProfilePath := filepath.Join(root, "coverage-product.out")
	profile := "mode: atomic\n" +
		modulePath + "/generated.go:1.1,1.2 1 0\n" +
		modulePath + "/tagged.go:1.1,1.2 1 0\n" +
		modulePath + "/product.go:1.1,1.2 1 1\n" +
		"example.com/external/file.go:1.1,1.2 1 0\n"
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	profileRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := profileRoot.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()
	if filterErr := filterGeneratedCoverageProfile(
		root,
		profileRoot,
		filepath.Base(profilePath),
		filepath.Base(productProfilePath),
	); filterErr != nil {
		t.Fatalf(
			"filterGeneratedCoverageProfile() error = %v",
			filterErr,
		)
	}
	filtered, err := os.ReadFile(productProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"mode: atomic",
		modulePath + "/product.go:",
		"example.com/external/file.go:",
	} {
		if !strings.Contains(string(filtered), expected) {
			t.Fatalf("filtered profile omitted %q:\n%s", expected, filtered)
		}
	}
	for _, excluded := range []string{
		modulePath + "/generated.go:",
		modulePath + "/tagged.go:",
	} {
		if strings.Contains(string(filtered), excluded) {
			t.Fatalf("filtered profile retained %q:\n%s", excluded, filtered)
		}
	}
}

func TestEnvironmentAndCleanupHelpers(t *testing.T) {
	environment := mergedEnvironment(map[string]string{"SPICE_QUALITY_TEST": "present"})
	if !slices.Contains(environment, "SPICE_QUALITY_TEST=present") {
		t.Fatalf("mergedEnvironment() missing override: %#v", environment)
	}
	want := "Spice_Test"
	if runtime.GOOS == "windows" {
		want = "SPICE_TEST"
	}
	if got := environmentKey("Spice_Test"); got != want {
		t.Fatalf("environmentKey() = %q, want %q", got, want)
	}

	path := filepath.Join(t.TempDir(), "temporary")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	removeTemporaryDirectory(path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists: %v", err)
	}
}

func TestCheckGoVersionAndUnknownMode(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	originalCapture := captureExternal
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		if slices.Equal(arguments, []string{"version"}) {
			return "go version go0.0.1 test/arch", nil
		}
		return "", nil
	}
	t.Cleanup(func() { captureExternal = originalCapture })
	if err := checkGoVersion(context.Background(), root); err == nil {
		t.Fatal("checkGoVersion() error = nil for wrong version")
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		if slices.Equal(arguments, []string{"version"}) {
			return "go version " + requiredGoVersion + " test/arch", nil
		}
		return "", nil
	}
	if err := run(context.Background(), "unknown"); err == nil {
		t.Fatal("run(unknown) error = nil")
	}
}

func TestRunModesWithFakeExternal(t *testing.T) {
	silenceOutput(t)
	apiPackages := apiMaturityFixturePackages(t)
	originalRun, originalCapture := runExternal, captureExternal
	runExternal = func(
		_ context.Context,
		directory string,
		_ map[string]string,
		executable string,
		arguments ...string,
	) error {
		simulateCleanRoomCommand(t, directory, executable, arguments)
		for index, argument := range arguments {
			if profile, found := strings.CutPrefix(
				argument,
				"-coverprofile=",
			); found {
				if err := os.WriteFile(
					profile,
					[]byte("mode: atomic\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if len(arguments) >= 2 &&
				arguments[0] == "mod" &&
				arguments[1] == "vendor" &&
				argument == "-o" &&
				index+1 < len(arguments) {
				copyTestTree(t, filepath.Join(directory, "vendor"), arguments[index+1])
			}
		}
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		executable string,
		arguments ...string,
	) (string, error) {
		switch {
		case executable == "go" &&
			slices.Equal(arguments, []string{"version"}):
			return "go version " + requiredGoVersion + " test/arch", nil
		case executable == "go" && slices.Equal(arguments, []string{
			"list", "-mod=vendor", "-f", "{{.ImportPath}}", "./...",
		}):
			return apiPackages, nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t90.0%\n", nil
		case slices.Contains(arguments, "-bench"):
			return benchmarkFixtureOutput(arguments), nil
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})

	for _, mode := range []string{"benchmark", "check", "coverage", "dogfood", "fmt", "fuzz", "lint", "security", "smoke", "test", "vet", "offline", "verify", "verify-release"} {
		if err := run(context.Background(), mode); err != nil {
			t.Fatalf("run(%q) error = %v", mode, err)
		}
	}
}

func simulateCleanRoomCommand(
	t *testing.T,
	directory string,
	executable string,
	arguments []string,
) {
	t.Helper()
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(executable)), ".exe")
	if name != "spice" || len(arguments) == 0 {
		return
	}
	switch arguments[0] {
	case "new":
		index := slices.Index(arguments, "--directory")
		if index < 0 || index+1 >= len(arguments) {
			t.Fatal("simulated spice new is missing --directory")
		}
		root := arguments[index+1]
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
		writeTestFile(
			t,
			root,
			"go.mod",
			"module example.com/spice-clean-room\n\ngo 1.26.0\n",
		)
	case "add":
		if !slices.Contains(arguments, "--apply") {
			return
		}
		path := filepath.Join(directory, "go.mod")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content = append(content, []byte("\nrequire golang.org/x/sync v0.22.0\n")...)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func apiMaturityFixturePackages(t *testing.T) string {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	content, err := readRootFile(root, "docs/api-compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy apiMaturityPolicy
	if err := json.Unmarshal(content, &policy); err != nil {
		t.Fatal(err)
	}
	packages := make([]string, 0, len(policy.Classifications))
	for _, rule := range policy.Classifications {
		packages = append(packages, modulePath+"/"+rule.Prefix)
	}
	return strings.Join(packages, "\n") + "\n"
}

func TestRunParallelValidatesWorkersAndReturnsStableFailure(t *testing.T) {
	t.Parallel()

	if err := runParallel(nil, 0); err == nil {
		t.Fatal("runParallel() accepted zero workers")
	}
	first := errors.New("first")
	second := errors.New("second")
	err := runParallel([]verificationStep{
		{name: "first stage", run: func() error { return first }},
		{name: "second stage", run: func() error { return second }},
	}, 2)
	if !errors.Is(err, first) {
		t.Fatalf("runParallel() error = %v", err)
	}
}

func TestQualityGateFailurePaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module "+modulePath+"\n\ngo 1.26.0\n")
	writeTestFile(t, root, "tools/go.mod", "module "+modulePath+"/tools\n\ngo 1.26.0\n")
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(t, root, "vendor/modules.txt", "# committed\n")

	originalRun, originalCapture := runExternal, captureExternal
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})

	sentinel := errors.New("external failure")
	runExternal = func(context.Context, string, map[string]string, string, ...string) error {
		return sentinel
	}
	captureExternal = func(context.Context, string, string, ...string) (string, error) {
		return "", sentinel
	}
	if err := checkGoVersion(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("checkGoVersion() error = %v", err)
	}
	if err := fileBatches(context.Background(), root, "go", "-w", []string{"main.go"}); !errors.Is(err, sentinel) {
		t.Fatalf("fileBatches() error = %v", err)
	}
	if err := test(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("test() error = %v", err)
	}
	if err := smoke(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("smoke() error = %v", err)
	}

	runExternal = func(context.Context, string, map[string]string, string, ...string) error {
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		switch {
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		case len(arguments) >= 2 && arguments[0] == "mod" && arguments[1] == "tidy":
			return "go.mod differs", nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t84.9%\n", nil
		default:
			return "main.go\n", nil
		}
	}
	if err := format(context.Background(), root, false); err == nil {
		t.Fatal("format(check) error = nil for unformatted file")
	}
	if err := checkModuleTidy(context.Background(), root); err == nil {
		t.Fatal("checkModuleTidy() error = nil for diff")
	}
	if err := coverage(context.Background(), root); err == nil {
		t.Fatal("coverage() error = nil below floor")
	}

	captureExternal = func(context.Context, string, string, ...string) (string, error) {
		return "", nil
	}
	if _, err := toolPath(context.Background(), root, "missing"); err == nil {
		t.Fatal("toolPath() error = nil for empty path")
	}
	if err := checkVendor(context.Background(), root); err == nil {
		t.Fatal("checkVendor() error = nil for missing generated vendor tree")
	}
	if _, err := treeDigest(filepath.Join(root, "missing")); err == nil {
		t.Fatal("treeDigest() error = nil for missing root")
	}

	if err := command(context.Background(), root, nil, "go", "version"); err != nil {
		t.Fatalf("command(go version) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := capture(cancelled, root, "go", "version"); err == nil {
		t.Fatal("capture(cancelled) error = nil")
	}
}

func TestTestAndCoverageCombinesRaceAndCoverage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "sample.go", "package sample\n")
	originalRun, originalCapture := runExternal, captureExternal
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})
	var calls [][]string
	runExternal = func(
		_ context.Context,
		_ string,
		_ map[string]string,
		executable string,
		arguments ...string,
	) error {
		call := append([]string{executable}, arguments...)
		calls = append(calls, call)
		for _, argument := range arguments {
			const prefix = "-coverprofile="
			if profile, found := strings.CutPrefix(argument, prefix); found {
				return os.WriteFile(
					profile,
					[]byte(
						"mode: atomic\n"+
							modulePath+"/sample.go:1.1,1.2 1 1\n",
					),
					0o600,
				)
			}
		}
		return nil
	}
	captureExternal = func(
		context.Context,
		string,
		string,
		...string,
	) (string, error) {
		return "total:\t(statements)\t100.0%\n", nil
	}

	if err := testAndCoverage(context.Background(), root); err != nil {
		t.Fatalf("testAndCoverage() error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("testAndCoverage() made %d commands, want 1: %v", len(calls), calls)
	}
	if !slices.ContainsFunc(calls[0], func(argument string) bool {
		return strings.HasPrefix(argument, "-coverprofile=")
	}) {
		t.Fatalf("test command does not emit coverage: %v", calls[0])
	}
	if !slices.Contains(calls[0], "-race") ||
		!slices.Contains(calls[0], "-shuffle=on") ||
		!slices.Contains(calls[0], "-covermode=atomic") {
		t.Fatalf("combined race and coverage command = %v", calls[0])
	}
}

func writeQualityGateOwnershipManifest(
	t *testing.T,
	root string,
	files []string,
) {
	t.Helper()
	manifestFiles := make([]map[string]string, 0, len(files))
	for _, file := range files {
		manifestFiles = append(manifestFiles, map[string]string{"path": file})
	}
	manifest := map[string]any{
		"target": map[string]string{"output_dir": "internal/spicegen/app"},
		"files":  manifestFiles,
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	writeTestFile(t, root, ".spice/app.manifest.json", string(content))
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBenchmarkFixture(t *testing.T, root string) {
	t.Helper()
	writeTestFile(
		t,
		root,
		"benchmarks/budgets.json",
		`{
  "schema": "spice.benchmarks/v1",
  "benchmarks": [{
    "name": "BenchmarkFixture",
    "package": "./fixture",
    "reference_ns_per_op": 10,
    "maximum_ns_per_op": 100,
    "maximum_bytes_per_op": 100,
    "maximum_allocs_per_op": 10,
    "rationale": "Quality-gate orchestration fixture."
  }]
}
`,
	)
}

func benchmarkFixtureOutput(arguments []string) string {
	name := "BenchmarkFixture"
	for index, argument := range arguments {
		if argument == "-bench" && index+1 < len(arguments) {
			name = strings.Trim(arguments[index+1], "^$")
			break
		}
	}
	return strings.Repeat(
		name+"-1  100  20 ns/op  30 B/op  2 allocs/op\n",
		5,
	)
}

func copyTestTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsCall(calls []string, fragment string) bool {
	for _, call := range calls {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

func silenceOutput(t *testing.T) {
	t.Helper()
	original := output
	output = log.New(io.Discard, "", 0)
	t.Cleanup(func() { output = original })
}
