// Package libraryreleaseacceptance proves the real central library signer
// against the independent toolchain verifier using immutable external source.
package libraryreleaseacceptance

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	DevelopmentCommit = "4c308d1b9fda11cb2b045f2e0d9e1616d32d007d"
	StarterOIDCCommit = "cdc0f9b2766cd6a9939409bf0149bcdff12ca806"
	ReleaseVersion    = "v1.2.3"

	developmentSource = "https://github.com/spice-framework/development.git"
	starterSource     = "https://github.com/spice-framework/starter-oidc.git"
	canonicalSource   = "https://github.com/spice-framework/starter-oidc"
	starterModule     = "github.com/spice-framework/starter-oidc"
	starterRepository = "starter-oidc"
	requiredGoVersion = "go1.26.5"
	maxCommandOutput  = 1 << 20
)

type layout struct {
	toolchainRoot string
	development   string
	starter       string
	plan          string
	artifacts     string
	privateKey    string
	publicKey     string
}

type commandSpec struct {
	directory   string
	executable  string
	arguments   []string
	environment map[string]string
}

type acceptanceOperations struct {
	clone            func(context.Context, string, string, string) error
	createTag        func(context.Context, string, string, string) error
	validateCheckout func(context.Context, string, string, string, string) error
	generateKeyPair  func(string, string) error
	runCommand       func(context.Context, commandSpec) ([]byte, error)
	writePrivateFile func(string, []byte) error
}

type limitedOutput struct {
	content   bytes.Buffer
	maximum   int
	truncated bool
}

func (output *limitedOutput) Write(content []byte) (int, error) {
	remaining := output.maximum - output.content.Len()
	if remaining > 0 {
		retained := min(remaining, len(content))
		_, _ = output.content.Write(content[:retained])
	}
	if len(content) > remaining {
		output.truncated = true
	}
	return len(content), nil
}

// Run performs the network-capable hosted acceptance and removes all cloned
// sources, artifacts, and key material before returning.
func Run(ctx context.Context, toolchainRoot string, output io.Writer) error {
	return runWithOperations(ctx, toolchainRoot, output, acceptanceOperations{
		clone:            cloneExact,
		createTag:        createExactTag,
		validateCheckout: validateProductionCheckout,
		generateKeyPair:  generateEphemeralKeyPair,
		runCommand:       runCommand,
		writePrivateFile: writeNewPrivateFile,
	})
}

func runWithOperations(
	ctx context.Context,
	toolchainRoot string,
	output io.Writer,
	operations acceptanceOperations,
) (resultErr error) {
	if ctx == nil {
		return errors.New("library release acceptance: context is nil")
	}
	if output == nil {
		return errors.New("library release acceptance: output is nil")
	}
	if runtime.Version() != requiredGoVersion {
		return fmt.Errorf(
			"library release acceptance: Go version is %s; require exactly %s",
			runtime.Version(),
			requiredGoVersion,
		)
	}
	root, rootErr := validateToolchainRoot(toolchainRoot)
	if rootErr != nil {
		return rootErr
	}
	temporary, temporaryErr := os.MkdirTemp("", "spice-cross-producer-acceptance-*")
	if temporaryErr != nil {
		return fmt.Errorf("create cross-producer acceptance root: %w", temporaryErr)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(temporary)) }()

	paths := layout{
		toolchainRoot: root,
		development:   filepath.Join(temporary, "development"),
		starter:       filepath.Join(temporary, starterRepository),
		plan:          filepath.Join(temporary, "production-plan.json"),
		artifacts:     filepath.Join(temporary, "signed-artifacts"),
		privateKey:    filepath.Join(temporary, "ephemeral-release-key.pem"),
		publicKey:     filepath.Join(temporary, "ephemeral-release-key.pub.pem"),
	}

	if _, err := fmt.Fprintf(
		output,
		"cloning central signer %s and %s source %s\n",
		DevelopmentCommit,
		starterRepository,
		StarterOIDCCommit,
	); err != nil {
		return err
	}
	if err := operations.clone(ctx, paths.development, developmentSource, DevelopmentCommit); err != nil {
		return fmt.Errorf("prepare pinned central signer: %w", err)
	}
	if err := operations.clone(ctx, paths.starter, starterSource, StarterOIDCCommit); err != nil {
		return fmt.Errorf("prepare pinned starter source: %w", err)
	}
	if err := operations.createTag(ctx, paths.starter, ReleaseVersion, StarterOIDCCommit); err != nil {
		return err
	}
	if err := operations.validateCheckout(
		ctx,
		paths.starter,
		starterSource,
		StarterOIDCCommit,
		ReleaseVersion,
	); err != nil {
		return err
	}
	if err := operations.generateKeyPair(paths.privateKey, paths.publicKey); err != nil {
		return fmt.Errorf("create ephemeral Ed25519 acceptance key: %w", err)
	}

	plan, planErr := operations.runCommand(ctx, planCommand(paths))
	if planErr != nil {
		return fmt.Errorf("create central production plan: %w", planErr)
	}
	if !bytes.HasSuffix(plan, []byte("\n")) || !bytes.Contains(plan, []byte(StarterOIDCCommit)) {
		return errors.New("central production plan is not canonical or does not bind the pinned starter commit")
	}
	if err := operations.writePrivateFile(paths.plan, plan); err != nil {
		return fmt.Errorf("write central production plan: %w", err)
	}
	if _, err := operations.runCommand(ctx, signCommand(paths)); err != nil {
		return fmt.Errorf("run central production signer: %w", err)
	}
	verification, verificationErr := operations.runCommand(ctx, verifyCommand(paths))
	if verificationErr != nil {
		return fmt.Errorf("run independent library release verifier: %w", verificationErr)
	}
	if _, err := output.Write(verification); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		output,
		"cross-producer acceptance passed for %s at %s\n",
		starterModule,
		StarterOIDCCommit,
	); err != nil {
		return err
	}
	return nil
}

func validateToolchainRoot(configured string) (string, error) {
	root, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve toolchain root: %w", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- caller identifies the trusted current checkout.
	if err != nil {
		return "", fmt.Errorf("read toolchain go.mod: %w", err)
	}
	if !bytes.Contains(content, []byte("module github.com/spice-framework/toolchain\n")) {
		return "", errors.New("configured root is not the Spice toolchain module")
	}
	return root, nil
}

func cloneExact(ctx context.Context, target, source, commit string) error {
	if err := os.Mkdir(target, 0o750); err != nil {
		return err
	}
	commands := [][]string{
		{"init", "--quiet", target},
		{"-C", target, "config", "core.autocrlf", "false"},
		{"-C", target, "config", "core.eol", "lf"},
		{"-C", target, "remote", "add", "origin", source},
		{"-C", target, "fetch", "--quiet", "--depth=1", "origin", commit},
		{"-C", target, "checkout", "--quiet", "--detach", commit},
	}
	for _, arguments := range commands {
		if _, err := runCommand(ctx, commandSpec{
			executable: "git", arguments: arguments,
			environment: map[string]string{"GIT_TERMINAL_PROMPT": "0", "GCM_INTERACTIVE": "Never"},
		}); err != nil {
			return err
		}
	}
	head, err := gitValue(ctx, target, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if !strings.EqualFold(head, commit) {
		return fmt.Errorf("checkout HEAD is %s; require %s", head, commit)
	}
	origin, err := gitValue(ctx, target, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if origin != source {
		return fmt.Errorf("checkout origin is %q; require %q", origin, source)
	}
	return nil
}

func createExactTag(ctx context.Context, repository, version, commit string) error {
	if _, err := runCommand(ctx, commandSpec{
		directory:  repository,
		executable: "git",
		arguments:  []string{"tag", version, commit},
	}); err != nil {
		return fmt.Errorf("create ephemeral production tag: %w", err)
	}
	return nil
}

func validateProductionCheckout(
	ctx context.Context,
	repository string,
	origin string,
	commit string,
	version string,
) error {
	remote, err := gitValue(ctx, repository, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if remote != origin {
		return fmt.Errorf("production checkout origin is %q; require %q", remote, origin)
	}
	head, err := gitValue(ctx, repository, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if !strings.EqualFold(head, commit) {
		return fmt.Errorf("production checkout HEAD is %s; require %s", head, commit)
	}
	tag, err := gitValue(ctx, repository, "rev-parse", "--verify", "refs/tags/"+version+"^{commit}")
	if err != nil {
		return err
	}
	if !strings.EqualFold(tag, commit) {
		return fmt.Errorf("production tag resolves to %s; require %s", tag, commit)
	}
	status, err := gitValue(ctx, repository, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("production checkout is not clean: %s", status)
	}
	return nil
}

func generateEphemeralKeyPair(privatePath, publicPath string) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(
		privatePath,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	); err != nil {
		return err
	}
	return writeNewPrivateFile(
		publicPath,
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}),
	)
}

func writeNewPrivateFile(filename string, content []byte) (resultErr error) {
	directory, name := filepath.Dir(filename), filepath.Base(filename)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	if _, err := file.Write(content); err != nil {
		return err
	}
	return file.Sync()
}

func planCommand(paths layout) commandSpec {
	return commandSpec{
		directory:  paths.development,
		executable: "go",
		arguments: []string{
			"run", "-mod=vendor", "./cmd/spice-dev", "library-release", "plan",
			"--root=" + paths.starter,
			"--repo=" + starterRepository,
			"--version=" + ReleaseVersion,
		},
		environment: offlineGoEnvironment(),
	}
}

func signCommand(paths layout) commandSpec {
	return commandSpec{
		directory:  paths.development,
		executable: "go",
		arguments: []string{
			"run", "-mod=vendor", "./cmd/spice-dev", "library-release", "sign",
			"--root=" + paths.starter,
			"--plan=" + paths.plan,
			"--output=" + paths.artifacts,
			"--signing-key=" + paths.privateKey,
			"--trusted-public-key=" + paths.publicKey,
		},
		environment: offlineGoEnvironment(),
	}
}

func verifyCommand(paths layout) commandSpec {
	return commandSpec{
		directory:  paths.toolchainRoot,
		executable: "go",
		arguments: []string{
			"run", "-mod=vendor", "./cmd/spice-library-release-verify",
			"-artifacts=" + paths.artifacts,
			"-root=" + paths.starter,
			"-repository=" + starterRepository,
			"-source=" + canonicalSource,
			"-module=" + starterModule,
			"-version=" + ReleaseVersion,
			"-commit=" + StarterOIDCCommit,
			"-trusted-public-key=" + paths.publicKey,
		},
		environment: offlineGoEnvironment(),
	}
}

func offlineGoEnvironment() map[string]string {
	return map[string]string{
		"GOWORK":      "off",
		"GOPROXY":     "off",
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "-mod=vendor",
	}
}

func gitValue(ctx context.Context, repository string, arguments ...string) (string, error) {
	content, err := runCommand(ctx, commandSpec{
		directory: repository, executable: "git", arguments: arguments,
		environment: map[string]string{"GIT_TERMINAL_PROMPT": "0", "GCM_INTERACTIVE": "Never"},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

func runCommand(ctx context.Context, specification commandSpec) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G204,G702 -- executable and arguments are fixed by this repository's pinned acceptance contract.
	command := exec.CommandContext(ctx, specification.executable, specification.arguments...)
	command.Dir = specification.directory
	command.Env = mergedEnvironment(specification.environment)
	stdout := limitedOutput{maximum: maxCommandOutput}
	stderr := limitedOutput{maximum: maxCommandOutput}
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.truncated || stderr.truncated {
		return nil, errors.New("acceptance command output exceeded the bounded diagnostic limit")
	}
	if err != nil {
		return nil, fmt.Errorf(
			"%s %s: %w%s",
			specification.executable,
			strings.Join(specification.arguments, " "),
			err,
			boundedDiagnostic(stdout.content.Bytes(), stderr.content.Bytes()),
		)
	}
	return bytes.Clone(stdout.content.Bytes()), nil
}

func boundedDiagnostic(stdout, stderr []byte) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(string(stdout)); value != "" {
		parts = append(parts, "stdout: "+value)
	}
	if value := strings.TrimSpace(string(stderr)); value != "" {
		parts = append(parts, "stderr: "+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "; ")
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string, len(overrides))
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := values[strings.ToUpper(key)]; !replaced {
				result = append(result, entry)
			}
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	slices.Sort(result)
	return result
}
