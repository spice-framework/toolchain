// Command fast runs Spice's affected-package edit loop without compiling the
// complete verification orchestrator.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/StevenBuglione/spice/internal/qualitygate/fastgate"
)

const moduleDeclaration = "module github.com/StevenBuglione/spice"

func main() {
	os.Exit(execute()) // Entrypoint exception: return the gate failure to make.
}

func execute() int {
	base := flag.String(
		"base",
		"",
		"optional Git revision used as the affected-work comparison base",
	)
	flag.Parse()
	root, err := repositoryRoot()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fast gate failed: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := fastgate.Run(ctx, fastgate.Config{
		RepositoryRoot: root,
		Base:           *base,
		Stdout:         os.Stdout,
	}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fast gate failed: %v\n", err)
		return 1
	}
	return 0
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		found, inspectErr := isRepositoryRoot(current)
		if inspectErr != nil {
			return "", inspectErr
		}
		if found {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("find Spice repository root: go.mod not found")
		}
		current = parent
	}
}

func isRepositoryRoot(path string) (result bool, resultErr error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return false, fmt.Errorf("open candidate repository root %s: %w", path, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	content, err := root.ReadFile("go.mod")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read candidate go.mod in %s: %w", path, err)
	}
	return bytes.Contains(content, []byte(moduleDeclaration)), nil
}
