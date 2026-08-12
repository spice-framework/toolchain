// Command boundarygate runs the standalone repository's verification contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spice-framework/toolchain/internal/boundarygate"
)

const (
	focusedVerificationTimeout = 20 * time.Minute
	fullVerificationTimeout    = 30 * time.Minute
)

func main() {
	os.Exit(run())
}

func run() int {
	mode := flag.String("mode", "verify", "verification mode: fast, check, benchmark, tools-bootstrap, release-artifacts, verify-release, or verify")
	artifacts := flag.String("artifacts", "", "absolute directory containing verified Toolchain release subjects")
	flag.Parse()
	ctx, cancel := verificationContext(context.Background(), *mode)
	defer cancel()
	if err := boundarygate.RunConfigured(ctx, ".", *mode, *artifacts, os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "boundary verification failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}

func timeoutForMode(mode string) time.Duration {
	switch mode {
	case "verify", "verify-release":
		return fullVerificationTimeout
	default:
		return focusedVerificationTimeout
	}
}

func verificationContext(parent context.Context, mode string) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeoutForMode(mode))
}
