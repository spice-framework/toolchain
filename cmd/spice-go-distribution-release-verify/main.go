// Command spice-go-distribution-release-verify independently verifies a
// closed-policy Spice Go binary distribution against an exact trusted commit.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spice-framework/toolchain/internal/goreleaseverify"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("spice-go-distribution-release-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var artifacts, verifiedOutput, root, repository, source, module, version, commit, profile string
	flags.StringVar(&artifacts, "artifacts", "", "Go distribution artifact directory")
	flags.StringVar(&verifiedOutput, "verified-output", "", "required absent verifier-owned output directory")
	flags.StringVar(&root, "root", ".", "trusted candidate repository root")
	flags.StringVar(&repository, "repository", "", "trusted catalog repository name")
	flags.StringVar(&source, "source", "", "trusted canonical HTTPS source URL")
	flags.StringVar(&module, "module", "", "trusted canonical Go module path")
	flags.StringVar(&version, "version", "", "catalog-authorized release version")
	flags.StringVar(&commit, "commit", "", "exact trusted Git commit object ID")
	flags.StringVar(&profile, "profile", "", "exact release profile")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return writeDistributionExit(stderr, 2, "unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if artifacts == "" || verifiedOutput == "" || repository == "" || source == "" || module == "" ||
		version == "" || commit == "" || profile == "" {
		return writeDistributionExit(
			stderr,
			2,
			"-artifacts, -verified-output, -repository, -source, -module, -version, -commit, and -profile are required",
		)
	}
	result, err := goreleaseverify.VerifyDistribution(ctx, goreleaseverify.Config{
		Directory: artifacts, VerifiedOutput: verifiedOutput,
		Repository: root, RepositoryName: repository,
		CanonicalSource: source, Module: module, Version: version,
		Commit: commit, Profile: profile,
	})
	if err != nil {
		return writeDistributionExit(stderr, 1, "%v", err)
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Spice Go distribution %s@%s verified: %d artifacts at %s.\n",
		result.Module,
		version,
		len(result.Files),
		result.Commit,
	); err != nil {
		return 1
	}
	return 0
}

func writeDistributionExit(writer io.Writer, code int, format string, arguments ...any) int {
	if _, err := fmt.Fprintf(writer, "spice-go-distribution-release-verify: "+format+"\n", arguments...); err != nil {
		return 1
	}
	return code
}
