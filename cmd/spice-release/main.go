// Command spice-release builds deterministic, signed Spice release artifacts.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spice-framework/toolchain/compiler/generate"
	"github.com/spice-framework/toolchain/internal/gitenv"
	spicerelease "github.com/spice-framework/toolchain/internal/release"
	"golang.org/x/mod/semver"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet("spice-release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		root       string
		output     string
		version    string
		targets    string
		signingKey string
		epoch      int64
		rehearsal  bool
	)
	flags.StringVar(&root, "root", ".", "repository root")
	flags.StringVar(&output, "output", "dist", "new release output directory")
	flags.StringVar(&version, "version", "", "canonical v-prefixed release version")
	flags.StringVar(
		&targets,
		"targets",
		defaultTargetText(),
		"comma-separated GOOS/GOARCH release targets",
	)
	flags.StringVar(
		&signingKey,
		"signing-key",
		"",
		"Ed25519 PKCS#8 PEM or base64 private-key file",
	)
	flags.Int64Var(
		&epoch,
		"source-date-epoch",
		0,
		"reproducible Unix timestamp (defaults to SOURCE_DATE_EPOCH or HEAD)",
	)
	flags.BoolVar(
		&rehearsal,
		"rehearsal",
		false,
		"allow an unsigned build without an exact clean release tag",
	)
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return writeExit(
			stderr,
			2,
			"spice-release: unexpected arguments: %s\n",
			strings.Join(flags.Args(), " "),
		)
	}
	if version == "" {
		return writeExit(stderr, 2, "spice-release: -version is required\n")
	}
	parsedTargets, err := spicerelease.ParseTargets(targets)
	if err != nil {
		return writeExit(stderr, 2, "spice-release: %v\n", err)
	}
	if intentErr := validateReleaseIntent(version, signingKey, rehearsal); intentErr != nil {
		return writeExit(stderr, 2, "spice-release: %v\n", intentErr)
	}
	resolvedEpoch, err := sourceEpoch(ctx, root, epoch)
	if err != nil {
		return writeExit(stderr, 1, "spice-release: %v\n", err)
	}
	commit := ""
	if !rehearsal {
		var validationErr error
		commit, validationErr = validateReleaseCheckout(
			ctx,
			root,
			version,
			resolvedEpoch,
		)
		if validationErr != nil {
			return writeExit(
				stderr,
				1,
				"spice-release: %v\n",
				validationErr,
			)
		}
	}
	var key []byte
	if signingKey != "" {
		key, err = readSigningKey(signingKey)
		if err != nil {
			return writeExit(
				stderr,
				1,
				"spice-release: read signing key: %v\n",
				err,
			)
		}
		defer clear(key)
	}
	result, err := spicerelease.Build(ctx, spicerelease.Config{
		Root:          root,
		OutputDir:     output,
		Version:       version,
		Commit:        commit,
		Epoch:         resolvedEpoch,
		Targets:       parsedTargets,
		PrivateKey:    key,
		AllowUnsigned: rehearsal,
	})
	if err != nil {
		return writeExit(stderr, 1, "spice-release: %v\n", err)
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Spice release %s created %d artifact(s) in %s.\n",
		version,
		len(result.Files),
		result.OutputDir,
	); err != nil {
		return 1
	}
	return 0
}

func validateReleaseIntent(
	version string,
	signingKey string,
	rehearsal bool,
) error {
	if !semver.IsValid(version) {
		return fmt.Errorf("version %q is not canonical semantic version", version)
	}
	if semver.Build(version) != "" && !rehearsal {
		return fmt.Errorf(
			"production version %q must not contain build metadata",
			version,
		)
	}
	if semver.Canonical(version) != version {
		return fmt.Errorf("version %q is not canonical semantic version", version)
	}
	if rehearsal {
		if signingKey != "" {
			return fmt.Errorf("-rehearsal cannot be combined with -signing-key")
		}
	} else if signingKey == "" {
		return fmt.Errorf("-signing-key is required outside rehearsal")
	}
	if version != generate.GeneratorVersion {
		return fmt.Errorf(
			"release version %q does not match frozen generator version %q",
			version,
			generate.GeneratorVersion,
		)
	}
	return nil
}

func writeExit(
	writer io.Writer,
	code int,
	format string,
	arguments ...any,
) int {
	if _, err := fmt.Fprintf(writer, format, arguments...); err != nil {
		return 1
	}
	return code
}

func defaultTargetText() string {
	targets := spicerelease.DefaultTargets()
	values := make([]string, len(targets))
	for index, target := range targets {
		values[index] = target.String()
	}
	return strings.Join(values, ",")
}

func sourceEpoch(
	ctx context.Context,
	root string,
	explicit int64,
) (time.Time, error) {
	if explicit != 0 {
		return time.Unix(explicit, 0).UTC(), nil
	}
	if value := os.Getenv("SOURCE_DATE_EPOCH"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf(
				"parse SOURCE_DATE_EPOCH: %w",
				err,
			)
		}
		return time.Unix(parsed, 0).UTC(), nil
	}
	output, err := gitOutput(ctx, root, "show", "-s", "--format=%ct", "HEAD")
	if err != nil {
		return time.Time{}, fmt.Errorf("read HEAD source epoch: %w", err)
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse HEAD source epoch: %w", err)
	}
	return time.Unix(parsed, 0).UTC(), nil
}

func validateReleaseCheckout(
	ctx context.Context,
	root string,
	version string,
	epoch time.Time,
) (string, error) {
	status, err := gitOutput(
		ctx,
		root,
		"status",
		"--porcelain",
		"--untracked-files=normal",
	)
	if err != nil {
		return "", fmt.Errorf("inspect release checkout: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return "", fmt.Errorf("release checkout has modifications or untracked files")
	}
	head, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve release HEAD: %w", err)
	}
	tag, err := gitOutput(
		ctx,
		root,
		"rev-parse",
		"--verify",
		"refs/tags/"+version+"^{commit}",
	)
	if err != nil {
		return "", fmt.Errorf("resolve release tag %q: %w", version, err)
	}
	if strings.TrimSpace(tag) != strings.TrimSpace(head) {
		return "", fmt.Errorf("release tag %q does not identify HEAD", version)
	}
	commitEpoch, err := gitOutput(ctx, root, "show", "-s", "--format=%ct", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read release HEAD epoch: %w", err)
	}
	parsedEpoch, err := strconv.ParseInt(strings.TrimSpace(commitEpoch), 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse release HEAD epoch: %w", err)
	}
	if epoch.Unix() != parsedEpoch {
		return "", fmt.Errorf(
			"release source epoch %d does not match HEAD epoch %d",
			epoch.Unix(),
			parsedEpoch,
		)
	}
	return strings.TrimSpace(head), nil
}

func gitOutput(
	ctx context.Context,
	root string,
	arguments ...string,
) (string, error) {
	// #nosec G204 -- executable is fixed and every caller supplies only
	// repository-owned Git arguments; no shell is involved.
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = root
	command.Env = gitenv.ReadOnly(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", errors.Join(
			err,
			fmt.Errorf("%s", strings.TrimSpace(stderr.String())),
		)
	}
	return stdout.String(), nil
}

func readSigningKey(filename string) ([]byte, error) {
	const maximumKeyBytes = 1 << 20
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return nil, err
	}
	file, err := root.Open(filepath.Base(filename))
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumKeyBytes+1))
	if err := errors.Join(readErr, file.Close(), root.Close()); err != nil {
		return nil, err
	}
	if len(data) > maximumKeyBytes {
		return nil, fmt.Errorf(
			"signing key exceeds %d bytes",
			maximumKeyBytes,
		)
	}
	return data, nil
}
