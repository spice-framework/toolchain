package libraryreleaseacceptance

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type failWriter struct {
	calls  int
	failAt int
	err    error
}

func (writer *failWriter) Write(content []byte) (int, error) {
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, writer.err
	}
	return len(content), nil
}

func TestPinnedCrossProducerIdentity(t *testing.T) {
	t.Parallel()
	for name, commit := range map[string]string{
		"development":  DevelopmentCommit,
		"starter OIDC": StarterOIDCCommit,
	} {
		if len(commit) != 40 {
			t.Fatalf("%s commit length = %d", name, len(commit))
		}
		if _, err := hex.DecodeString(commit); err != nil {
			t.Fatalf("%s commit is not lowercase hexadecimal: %v", name, err)
		}
	}
	if developmentSource != "https://github.com/spice-framework/development.git" ||
		starterSource != canonicalSource+".git" ||
		starterModule != "github.com/spice-framework/starter-oidc" ||
		starterRepository != "starter-oidc" ||
		ReleaseVersion != "v1.2.3" {
		t.Fatal("cross-producer identity constants drifted")
	}
}

func TestLimitedOutputBoundsRetainedDiagnostics(t *testing.T) {
	t.Parallel()
	output := limitedOutput{maximum: 3}
	written, err := output.Write([]byte("abcdef"))
	if err != nil || written != 6 || output.content.String() != "abc" || !output.truncated {
		t.Fatalf("limited output = (%d, %v, %q, %v)", written, err, output.content.String(), output.truncated)
	}
}

func TestCommandsBindProductionAndIndependentTrustInputs(t *testing.T) {
	t.Parallel()
	paths := layout{
		toolchainRoot: "toolchain",
		development:   "development",
		starter:       "starter",
		plan:          "plan.json",
		artifacts:     "artifacts",
		privateKey:    "ephemeral-private.pem",
		publicKey:     "ephemeral-public.pem",
	}
	plan := planCommand(paths)
	if !slices.Contains(plan.arguments, "library-release") ||
		!slices.Contains(plan.arguments, "plan") ||
		slices.Contains(plan.arguments, "--rehearsal") ||
		!slices.Contains(plan.arguments, "--repo="+starterRepository) ||
		!slices.Contains(plan.arguments, "--version="+ReleaseVersion) {
		t.Fatalf("production plan command = %v", plan.arguments)
	}
	sign := signCommand(paths)
	for _, required := range []string{
		"sign",
		"--signing-key=" + paths.privateKey,
		"--trusted-public-key=" + paths.publicKey,
		"--output=" + paths.artifacts,
	} {
		if !slices.Contains(sign.arguments, required) {
			t.Fatalf("sign command %v does not contain %q", sign.arguments, required)
		}
	}
	verify := verifyCommand(paths)
	for _, required := range []string{
		"-source=" + canonicalSource,
		"-module=" + starterModule,
		"-repository=" + starterRepository,
		"-commit=" + StarterOIDCCommit,
		"-version=" + ReleaseVersion,
		"-trusted-public-key=" + paths.publicKey,
	} {
		if !slices.Contains(verify.arguments, required) {
			t.Fatalf("verify command %v does not contain %q", verify.arguments, required)
		}
	}
	for _, specification := range []commandSpec{plan, sign, verify} {
		if specification.executable != "go" ||
			!slices.Contains(specification.arguments, "-mod=vendor") ||
			specification.environment["GOWORK"] != "off" ||
			specification.environment["GOPROXY"] != "off" ||
			specification.environment["GOTOOLCHAIN"] != "local" {
			t.Fatalf("offline command specification = %#v", specification)
		}
	}
}

func TestGenerateEphemeralKeyPair(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "private.pem")
	publicPath := filepath.Join(directory, "public.pem")
	if err := generateEphemeralKeyPair(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	privatePEM, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateBlock, privateRest := pem.Decode(privatePEM)
	publicBlock, publicRest := pem.Decode(publicPEM)
	if privateBlock == nil || privateBlock.Type != "PRIVATE KEY" || len(privateRest) != 0 ||
		publicBlock == nil || publicBlock.Type != "PUBLIC KEY" || len(publicRest) != 0 {
		t.Fatal("ephemeral keypair is not canonical PEM")
	}
	privateValue, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, ok := privateValue.(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T", privateValue)
	}
	publicValue, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, publicOK := publicValue.(ed25519.PublicKey)
	privatePublic, privatePublicOK := privateKey.Public().(ed25519.PublicKey)
	if !publicOK || !privatePublicOK || !bytes.Equal(publicKey, privatePublic) {
		t.Fatal("ephemeral public key does not match private key")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{privatePath, publicPath} {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("ephemeral key permissions = %o", info.Mode().Perm())
			}
		}
	}
	if err := generateEphemeralKeyPair(privatePath, publicPath); err == nil {
		t.Fatal("generateEphemeralKeyPair() overwrote existing material")
	}
	secondPrivate := filepath.Join(directory, "second-private.pem")
	if err := generateEphemeralKeyPair(secondPrivate, publicPath); err == nil {
		t.Fatal("generateEphemeralKeyPair() overwrote an existing public key")
	}
}

func TestMergedEnvironmentOverridesCaseInsensitively(t *testing.T) {
	t.Setenv("SPICE_ACCEPTANCE_ENV", "old")
	values := mergedEnvironment(map[string]string{
		"spice_acceptance_env":  "new",
		"SPICE_ACCEPTANCE_ONLY": "value",
	})
	joined := strings.ToUpper(strings.Join(values, "\n"))
	if !strings.Contains(joined, "SPICE_ACCEPTANCE_ENV=NEW") ||
		!strings.Contains(joined, "SPICE_ACCEPTANCE_ONLY=VALUE") ||
		strings.Contains(joined, "SPICE_ACCEPTANCE_ENV=OLD") {
		t.Fatalf("merged environment = %v", values)
	}
}

func TestRunRejectsInvalidInputsBeforeNetworkAccess(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // This negative boundary test proves a nil API context fails closed.
	if err := Run(nil, ".", io.Discard); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Run(nil) error = %v", err)
	}
	if err := Run(context.Background(), ".", nil); err == nil || !strings.Contains(err.Error(), "output is nil") {
		t.Fatalf("Run(nil output) error = %v", err)
	}
	if err := Run(context.Background(), t.TempDir(), io.Discard); err == nil ||
		!strings.Contains(err.Error(), "read toolchain go.mod") {
		t.Fatalf("Run(invalid root) error = %v", err)
	}
}

func TestRunWithOperationsCompletesAndRemovesSensitiveWorkspace(t *testing.T) {
	t.Parallel()
	var calls []string
	var temporaryRoot string
	operations := acceptanceOperations{
		clone: func(_ context.Context, target, source, commit string) error {
			calls = append(calls, "clone:"+source+"@"+commit)
			if temporaryRoot == "" {
				temporaryRoot = filepath.Dir(target)
			} else if filepath.Dir(target) != temporaryRoot {
				t.Fatalf("clone target %q escaped temporary root %q", target, temporaryRoot)
			}
			return nil
		},
		createTag: func(_ context.Context, repository, version, commit string) error {
			calls = append(calls, "tag:"+version+"@"+commit)
			if filepath.Dir(repository) != temporaryRoot {
				t.Fatalf("tag repository %q escaped temporary root %q", repository, temporaryRoot)
			}
			return nil
		},
		validateCheckout: func(_ context.Context, repository, origin, commit, version string) error {
			calls = append(calls, "validate:"+origin+"@"+commit+":"+version)
			if filepath.Dir(repository) != temporaryRoot {
				t.Fatalf("validated repository %q escaped temporary root %q", repository, temporaryRoot)
			}
			return nil
		},
		generateKeyPair: func(privatePath, publicPath string) error {
			calls = append(calls, "keys")
			if filepath.Dir(privatePath) != temporaryRoot || filepath.Dir(publicPath) != temporaryRoot {
				t.Fatal("ephemeral key paths escaped the temporary root")
			}
			return nil
		},
		runCommand: func(_ context.Context, specification commandSpec) ([]byte, error) {
			switch {
			case slices.Contains(specification.arguments, "plan"):
				calls = append(calls, "command:plan")
				return []byte("{\"commit\":\"" + StarterOIDCCommit + "\"}\n"), nil
			case slices.Contains(specification.arguments, "sign"):
				calls = append(calls, "command:sign")
				return nil, nil
			default:
				calls = append(calls, "command:verify")
				return []byte("verified\n"), nil
			}
		},
		writePrivateFile: func(filename string, content []byte) error {
			calls = append(calls, "write-plan")
			if filepath.Dir(filename) != temporaryRoot || !bytes.HasSuffix(content, []byte("\n")) {
				t.Fatal("production plan was not canonical or remained outside the temporary root")
			}
			return nil
		},
	}

	var output bytes.Buffer
	if err := runWithOperations(context.Background(), filepath.Join("..", ".."), &output, operations); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"clone:" + developmentSource + "@" + DevelopmentCommit,
		"clone:" + starterSource + "@" + StarterOIDCCommit,
		"tag:" + ReleaseVersion + "@" + StarterOIDCCommit,
		"validate:" + starterSource + "@" + StarterOIDCCommit + ":" + ReleaseVersion,
		"keys",
		"command:plan",
		"write-plan",
		"command:sign",
		"command:verify",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("acceptance calls = %v, want %v", calls, wantCalls)
	}
	if !strings.Contains(output.String(), "verified\n") ||
		!strings.Contains(output.String(), "cross-producer acceptance passed") {
		t.Fatalf("acceptance output = %q", output.String())
	}
	if _, err := os.Stat(temporaryRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary acceptance root still exists: %v", err)
	}
}

func TestRunWithOperationsRejectsNonCanonicalPlan(t *testing.T) {
	t.Parallel()
	operations := acceptanceOperations{
		clone:            func(context.Context, string, string, string) error { return nil },
		createTag:        func(context.Context, string, string, string) error { return nil },
		validateCheckout: func(context.Context, string, string, string, string) error { return nil },
		generateKeyPair:  func(string, string) error { return nil },
		runCommand:       func(context.Context, commandSpec) ([]byte, error) { return []byte("{}"), nil },
		writePrivateFile: func(string, []byte) error { return nil },
	}
	err := runWithOperations(context.Background(), filepath.Join("..", ".."), io.Discard, operations)
	if err == nil || !strings.Contains(err.Error(), "production plan is not canonical") {
		t.Fatalf("noncanonical plan error = %v", err)
	}
}

func TestRunWithOperationsReportsEveryFailedStage(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("injected acceptance failure")
	tests := []struct {
		name   string
		mutate func(*acceptanceOperations)
		want   string
	}{
		{
			name: "clone",
			mutate: func(operations *acceptanceOperations) {
				operations.clone = func(context.Context, string, string, string) error { return sentinel }
			},
			want: "prepare pinned central signer",
		},
		{
			name: "tag",
			mutate: func(operations *acceptanceOperations) {
				operations.createTag = func(context.Context, string, string, string) error { return sentinel }
			},
			want: sentinel.Error(),
		},
		{
			name: "checkout validation",
			mutate: func(operations *acceptanceOperations) {
				operations.validateCheckout = func(context.Context, string, string, string, string) error {
					return sentinel
				}
			},
			want: sentinel.Error(),
		},
		{
			name: "key generation",
			mutate: func(operations *acceptanceOperations) {
				operations.generateKeyPair = func(string, string) error { return sentinel }
			},
			want: "create ephemeral Ed25519 acceptance key",
		},
		{
			name: "plan command",
			mutate: func(operations *acceptanceOperations) {
				operations.runCommand = func(context.Context, commandSpec) ([]byte, error) { return nil, sentinel }
			},
			want: "create central production plan",
		},
		{
			name: "plan write",
			mutate: func(operations *acceptanceOperations) {
				operations.writePrivateFile = func(string, []byte) error { return sentinel }
			},
			want: "write central production plan",
		},
		{
			name: "sign command",
			mutate: func(operations *acceptanceOperations) {
				calls := 0
				operations.runCommand = func(context.Context, commandSpec) ([]byte, error) {
					calls++
					if calls == 1 {
						return canonicalAcceptancePlan(), nil
					}
					return nil, sentinel
				}
			},
			want: "run central production signer",
		},
		{
			name: "verification command",
			mutate: func(operations *acceptanceOperations) {
				calls := 0
				operations.runCommand = func(context.Context, commandSpec) ([]byte, error) {
					calls++
					if calls == 1 {
						return canonicalAcceptancePlan(), nil
					}
					if calls == 2 {
						return nil, nil
					}
					return nil, sentinel
				}
			},
			want: "run independent library release verifier",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			operations := successfulAcceptanceOperations()
			test.mutate(&operations)
			err := runWithOperations(
				context.Background(),
				filepath.Join("..", ".."),
				io.Discard,
				operations,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runWithOperations() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunWithOperationsPropagatesOutputFailures(t *testing.T) {
	t.Parallel()
	for _, failAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("write %d", failAt), func(t *testing.T) {
			t.Parallel()
			sentinel := errors.New("injected output failure")
			writer := &failWriter{failAt: failAt, err: sentinel}
			err := runWithOperations(
				context.Background(),
				filepath.Join("..", ".."),
				writer,
				successfulAcceptanceOperations(),
			)
			if !errors.Is(err, sentinel) {
				t.Fatalf("runWithOperations() error = %v", err)
			}
		})
	}
}

func canonicalAcceptancePlan() []byte {
	return []byte("{\"commit\":\"" + StarterOIDCCommit + "\"}\n")
}

func successfulAcceptanceOperations() acceptanceOperations {
	return acceptanceOperations{
		clone:            func(context.Context, string, string, string) error { return nil },
		createTag:        func(context.Context, string, string, string) error { return nil },
		validateCheckout: func(context.Context, string, string, string, string) error { return nil },
		generateKeyPair:  func(string, string) error { return nil },
		runCommand: func(_ context.Context, specification commandSpec) ([]byte, error) {
			if slices.Contains(specification.arguments, "plan") {
				return canonicalAcceptancePlan(), nil
			}
			return []byte("verified\n"), nil
		},
		writePrivateFile: func(string, []byte) error { return nil },
	}
}

func TestValidateToolchainRoot(t *testing.T) {
	t.Parallel()
	root, err := validateToolchainRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("validated root = %q", root)
	}

	wrong := t.TempDir()
	if err := os.WriteFile(filepath.Join(wrong, "go.mod"), []byte("module example.com/not-spice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateToolchainRoot(wrong); err == nil ||
		!strings.Contains(err.Error(), "not the Spice toolchain module") {
		t.Fatalf("validateToolchainRoot(wrong module) error = %v", err)
	}
}

func TestWriteNewPrivateFileIsExclusive(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secret")
	if err := writeNewPrivateFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeNewPrivateFile(path, []byte("second")); err == nil {
		t.Fatal("writeNewPrivateFile overwrote an existing file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("content = %q", content)
	}
	if err := writeNewPrivateFile(filepath.Join(path, "child"), nil); err == nil {
		t.Fatal("writeNewPrivateFile accepted a non-directory parent")
	}
}

func TestRunCommandSuccessFailureAndCancellation(t *testing.T) {
	t.Parallel()
	content, commandErr := runCommand(context.Background(), commandSpec{
		executable: "go",
		arguments:  []string{"env", "GOVERSION"},
	})
	if commandErr != nil {
		t.Fatal(commandErr)
	}
	if strings.TrimSpace(string(content)) != requiredGoVersion {
		t.Fatalf("go env GOVERSION = %q", content)
	}

	_, commandErr = runCommand(context.Background(), commandSpec{
		executable: "go",
		arguments:  []string{"tool", "spice-command-that-does-not-exist"},
	})
	if commandErr == nil || !strings.Contains(commandErr.Error(), "spice-command-that-does-not-exist") {
		t.Fatalf("failed command error = %v", commandErr)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, commandErr = runCommand(canceled, commandSpec{executable: "go", arguments: []string{"version"}})
	if !errors.Is(commandErr, context.Canceled) {
		t.Fatalf("canceled command error = %v", commandErr)
	}

	directory := t.TempDir()
	left := filepath.Join(directory, "left.txt")
	right := filepath.Join(directory, "right.txt")
	if err := os.WriteFile(left, bytes.Repeat([]byte("a"), maxCommandOutput+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, bytes.Repeat([]byte("b"), maxCommandOutput+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, commandErr = runCommand(context.Background(), commandSpec{
		executable: "git",
		arguments:  []string{"diff", "--no-index", "--no-color", left, right},
	})
	if commandErr == nil || !strings.Contains(commandErr.Error(), "exceeded the bounded diagnostic limit") {
		t.Fatalf("oversized command error = %v", commandErr)
	}
}

func TestBoundedDiagnostic(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{name: "empty"},
		{name: "stdout", stdout: " result\n", want: ": stdout: result"},
		{name: "stderr", stderr: " failure\n", want: ": stderr: failure"},
		{name: "both", stdout: "result", stderr: "failure", want: ": stdout: result; stderr: failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := boundedDiagnostic([]byte(test.stdout), []byte(test.stderr)); got != test.want {
				t.Fatalf("boundedDiagnostic() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLocalCheckoutValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source")
	if _, err := runCommand(ctx, commandSpec{executable: "git", arguments: []string{"init", "--quiet", source}}); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"config", "user.name", "Spice Acceptance"},
		{"config", "user.email", "acceptance@example.invalid"},
	} {
		if _, err := runCommand(ctx, commandSpec{directory: source, executable: "git", arguments: arguments}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.com/fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, commandSpec{
		directory:  source,
		executable: "git",
		arguments:  []string{"add", "go.mod"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, commandSpec{
		directory:  source,
		executable: "git",
		arguments:  []string{"commit", "--quiet", "-m", "fixture"},
	}); err != nil {
		t.Fatal(err)
	}
	commit, valueErr := gitValue(ctx, source, "rev-parse", "HEAD")
	if valueErr != nil {
		t.Fatal(valueErr)
	}

	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := cloneExact(ctx, checkout, source, commit); err != nil {
		t.Fatal(err)
	}
	if err := createExactTag(ctx, checkout, ReleaseVersion, commit); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionCheckout(ctx, checkout, source, commit, ReleaseVersion); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionCheckout(ctx, checkout, source, commit, ReleaseVersion); err == nil ||
		!strings.Contains(err.Error(), "not clean") {
		t.Fatalf("dirty checkout error = %v", err)
	}
	if err := os.Remove(filepath.Join(checkout, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, commandSpec{
		directory:  checkout,
		executable: "git",
		arguments:  []string{"remote", "set-url", "origin", source + "-wrong"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionCheckout(ctx, checkout, source, commit, ReleaseVersion); err == nil ||
		!strings.Contains(err.Error(), "production checkout origin") {
		t.Fatalf("wrong origin error = %v", err)
	}
	if _, err := runCommand(ctx, commandSpec{
		directory:  checkout,
		executable: "git",
		arguments:  []string{"remote", "set-url", "origin", source},
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionCheckout(ctx, checkout, source, strings.Repeat("0", 40), ReleaseVersion); err == nil ||
		!strings.Contains(err.Error(), "production checkout HEAD") {
		t.Fatalf("wrong HEAD error = %v", err)
	}
	for _, arguments := range [][]string{
		{"config", "user.name", "Spice Acceptance"},
		{"config", "user.email", "acceptance@example.invalid"},
		{"commit", "--quiet", "--allow-empty", "-m", "second fixture"},
	} {
		if _, err := runCommand(ctx, commandSpec{directory: checkout, executable: "git", arguments: arguments}); err != nil {
			t.Fatal(err)
		}
	}
	secondCommit, secondValueErr := gitValue(ctx, checkout, "rev-parse", "HEAD")
	if secondValueErr != nil {
		t.Fatal(secondValueErr)
	}
	if err := validateProductionCheckout(ctx, checkout, source, secondCommit, ReleaseVersion); err == nil ||
		!strings.Contains(err.Error(), "production tag resolves") {
		t.Fatalf("wrong tag error = %v", err)
	}

	if err := cloneExact(ctx, checkout, source, commit); err == nil {
		t.Fatal("cloneExact accepted an existing target")
	}
	if err := createExactTag(ctx, filepath.Join(t.TempDir(), "missing"), ReleaseVersion, commit); err == nil ||
		!strings.Contains(err.Error(), "create ephemeral production tag") {
		t.Fatalf("createExactTag(missing repository) error = %v", err)
	}
}
