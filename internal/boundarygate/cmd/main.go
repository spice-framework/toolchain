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

func main() {
	os.Exit(run())
}

func run() int {
	mode := flag.String("mode", "verify", "verification mode: fast, check, benchmark, or verify")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := boundarygate.Run(ctx, ".", *mode, os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "boundary verification failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}
