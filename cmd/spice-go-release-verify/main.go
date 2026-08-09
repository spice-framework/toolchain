// Command spice-go-release-verify independently verifies generic Spice Go
// module artifacts against compiled policy and an exact trusted checkout.
package main

import (
	"context"
	"encoding/json"
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
	if len(arguments) > 0 && arguments[0] == "policy-check" {
		return runPolicyCheck(arguments[1:], stdout, stderr)
	}
	return runVerification(ctx, arguments, stdout, stderr)
}

func runVerification(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("spice-go-release-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var artifacts, verifiedOutput, root, repository, source, module, version, commit, profile string
	flags.StringVar(&artifacts, "artifacts", "", "generic Go module artifact directory")
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
		return writeExit(stderr, 2, "unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if artifacts == "" || verifiedOutput == "" || repository == "" || source == "" || module == "" ||
		version == "" || commit == "" || profile == "" {
		return writeExit(
			stderr,
			2,
			"-artifacts, -verified-output, -repository, -source, -module, -version, -commit, and -profile are required",
		)
	}
	result, err := goreleaseverify.Verify(ctx, goreleaseverify.Config{
		Directory: artifacts, VerifiedOutput: verifiedOutput,
		Repository: root, RepositoryName: repository,
		CanonicalSource: source, Module: module, Version: version,
		Commit: commit, Profile: profile,
	})
	if err != nil {
		return writeExit(stderr, 1, "%v", err)
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Spice Go module release %s@%s verified: %d artifacts at %s.\n",
		result.Module,
		version,
		len(result.Files),
		result.Commit,
	); err != nil {
		return 1
	}
	return 0
}

func runPolicyCheck(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("spice-go-release-verify policy-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var repository, source, module, version, profile string
	flags.StringVar(&repository, "repository", "", "closed-policy repository name")
	flags.StringVar(&source, "source", "", "canonical HTTPS source URL")
	flags.StringVar(&module, "module", "", "canonical Go module path")
	flags.StringVar(&version, "version", "", "proposed release version")
	flags.StringVar(&profile, "profile", "", "release profile")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return writeExit(stderr, 2, "invalid policy-check arguments")
	}
	if repository == "" || source == "" || module == "" || version == "" || profile == "" {
		return writeExit(
			stderr,
			2,
			"policy-check requires -repository, -source, -module, -version, and -profile",
		)
	}
	authorization, err := goreleaseverify.CheckPolicy(goreleaseverify.PolicyRequest{
		Repository: repository,
		Source:     source,
		Module:     module,
		Version:    version,
		Profile:    profile,
	})
	if err != nil {
		return writeExit(stderr, 1, "%v", err)
	}
	payload, err := json.Marshal(policyCheckOutput{
		Profile:    authorization.Profile,
		Repository: authorization.Repository,
		Module:     authorization.Module,
		Version:    authorization.Version,
		Source:     authorization.Source,
	})
	if err != nil {
		return writeExit(stderr, 1, "encode authorized release policy")
	}
	payload = append(payload, '\n')
	if len(payload) > maxPolicyCheckOutputBytes {
		return writeExit(stderr, 1, "authorized release policy exceeds output bound")
	}
	if _, err := stdout.Write(payload); err != nil {
		return 1
	}
	return 0
}

const maxPolicyCheckOutputBytes = 2 << 10

type policyCheckOutput struct {
	Profile    string `json:"profile"`
	Repository string `json:"repository"`
	Module     string `json:"module"`
	Version    string `json:"version"`
	Source     string `json:"source"`
}

func writeExit(writer io.Writer, code int, format string, arguments ...any) int {
	if _, err := fmt.Fprintf(writer, "spice-go-release-verify: "+format+"\n", arguments...); err != nil {
		return 1
	}
	return code
}
