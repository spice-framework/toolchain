// Command spice-release-verify independently authenticates and verifies a
// complete Spice release artifact directory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spice-framework/toolchain/internal/releaseverify"
)

const maxTrustedKeyFileBytes = 64 << 10

func main() {
	//nolint:forbidigo // This process entrypoint owns the command exit status.
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet("spice-release-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var artifacts, root, version, commit, trustedPublicKey string
	flags.StringVar(&artifacts, "artifacts", "", "release artifact directory")
	flags.StringVar(&root, "root", ".", "trusted repository root")
	flags.StringVar(&version, "version", "", "canonical v-prefixed release version")
	flags.StringVar(&commit, "commit", "", "exact trusted Git commit object ID")
	flags.StringVar(
		&trustedPublicKey,
		"trusted-public-key",
		"",
		"trusted Ed25519 public-key PEM file",
	)
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return writeExit(stderr, 2, "unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if artifacts == "" || version == "" || commit == "" || trustedPublicKey == "" {
		return writeExit(
			stderr,
			2,
			"-artifacts, -version, -commit, and -trusted-public-key are required",
		)
	}
	key, err := readBoundedFile(trustedPublicKey, maxTrustedKeyFileBytes)
	if err != nil {
		return writeExit(stderr, 1, "read trusted public key: %v", err)
	}
	result, err := releaseverify.Verify(ctx, releaseverify.Config{
		Directory:        artifacts,
		Version:          version,
		Repository:       root,
		Commit:           commit,
		TrustedPublicKey: key,
	})
	if err != nil {
		return writeExit(stderr, 1, "%v", err)
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Spice release %s verified: %d artifacts at %s.\n",
		version,
		len(result.Files),
		result.Commit,
	); err != nil {
		return 1
	}
	return 0
}

func readBoundedFile(filename string, maximum int64) (result []byte, resultErr error) {
	// #nosec G304 -- the operator explicitly supplies the trusted public-key path.
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("file is not a regular file bounded to %d bytes", maximum)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return data, nil
}

func writeExit(writer io.Writer, code int, format string, arguments ...any) int {
	if _, err := fmt.Fprintf(writer, "spice-release-verify: "+format+"\n", arguments...); err != nil {
		return 1
	}
	return code
}
