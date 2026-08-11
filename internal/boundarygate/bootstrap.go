package boundarygate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	toolsBootstrapTarget = "tools-bootstrap"
	verifyReleaseTarget  = "verify-release"
)

var bootstrapModules = []string{
	".",
	"tools",
	"tools/actionlint",
	"testdata/annotationfixture",
	"testdata/annotationapp",
}

type repositoryFileDigest struct {
	Hash [sha256.Size]byte
	Mode fs.FileMode
}

func bootstrapEnvironment() map[string]string {
	return map[string]string{
		"GOAUTH":      "off",
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GONOPROXY":   "",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "https://proxy.golang.org",
		"GOSUMDB":     "sum.golang.org",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
}

func (gate verifier) bootstrapEntrypoints() error {
	content, err := os.ReadFile(filepath.Join(gate.root, "Makefile"))
	if err != nil {
		return fmt.Errorf("read Makefile release entrypoints: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	firstLine, _, _ := strings.Cut(text, "\n")
	entrypoints := []struct {
		target string
		mode   string
	}{
		{target: toolsBootstrapTarget, mode: "tools-bootstrap"},
		{target: verifyReleaseTarget, mode: "verify-release"},
	}
	for _, entrypoint := range entrypoints {
		target := entrypoint.target
		mode := entrypoint.mode
		recipe := target + ":\n\tgo run ./internal/boundarygate/cmd -mode=" + mode + "\n"
		rules := 0
		for line := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(line, target+":") {
				rules++
			}
		}
		if strings.Count(text, recipe) != 1 || rules != 1 ||
			!strings.HasPrefix(firstLine, ".PHONY:") ||
			!containsField(strings.TrimPrefix(firstLine, ".PHONY:"), target) {
			return fmt.Errorf("makefile must expose the exact phony %s recipe", target)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(gate.root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return fmt.Errorf("read CI release bootstrap boundary: %w", err)
	}
	workflowText := strings.ReplaceAll(string(workflow), "\r\n", "\n")
	setupStep := "      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6\n" +
		"        with:\n" +
		"          go-version: 1.26.5\n" +
		"          cache: false\n"
	bootstrapStep := "      - name: Bootstrap candidate-owned pinned graphs\n" +
		"        run: make tools-bootstrap\n"
	offlineStep := "      - name: Verify standalone release boundary offline\n" +
		"        env:\n" +
		"          GOPROXY: \"off\"\n" +
		"          GOSUMDB: \"off\"\n" +
		"          GOTOOLCHAIN: local\n" +
		"          GOWORK: \"off\"\n" +
		"        run: make verify-release\n"
	bootstrapIndex := strings.Index(workflowText, bootstrapStep)
	offlineIndex := strings.Index(workflowText, offlineStep)
	if strings.Count(workflowText, setupStep) != 1 || bootstrapIndex < 0 || offlineIndex <= bootstrapIndex ||
		strings.Count(workflowText, bootstrapStep) != 1 || strings.Count(workflowText, offlineStep) != 1 {
		return errors.New("CI must disable shared Go caches and bootstrap pinned graphs once before the exact offline release verifier")
	}
	return nil
}

func (gate verifier) toolsBootstrap(ctx context.Context) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := snapshotRepository(gate.root)
	if err != nil {
		return fmt.Errorf("snapshot repository before tools bootstrap: %w", err)
	}
	defer func() {
		after, snapshotErr := snapshotRepository(gate.root)
		if snapshotErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("snapshot repository after tools bootstrap: %w", snapshotErr))
			return
		}
		if !repositorySnapshotsEqual(before, after) {
			returnErr = errors.Join(returnErr, errors.New("tools bootstrap modified the repository"))
		}
	}()
	repositoryRoot, err := os.OpenRoot(gate.root)
	if err != nil {
		return fmt.Errorf("open tools bootstrap repository root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, repositoryRoot.Close())
	}()

	temporary, err := os.MkdirTemp("", "spice-toolchain-bootstrap-")
	if err != nil {
		return fmt.Errorf("create tools bootstrap mirror: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(temporary); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove tools bootstrap mirror: %w", cleanupErr))
		}
	}()
	mirrorRoot, err := os.OpenRoot(temporary)
	if err != nil {
		return fmt.Errorf("open tools bootstrap mirror root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, mirrorRoot.Close())
	}()

	for _, relative := range bootstrapModules {
		if err = mirrorModuleGraph(repositoryRoot, mirrorRoot, relative); err != nil {
			return err
		}
	}
	for _, relative := range bootstrapModules {
		if err = ctx.Err(); err != nil {
			return err
		}
		directory := filepath.Join(gate.root, filepath.FromSlash(relative))
		modfile := filepath.Join(temporary, filepath.FromSlash(relative), "bootstrap.mod")
		if err = gate.run(
			ctx,
			directory,
			bootstrapEnvironment(),
			"go",
			"mod",
			"download",
			"-modfile="+modfile,
			"all",
		); err != nil {
			return fmt.Errorf("bootstrap module %s: %w", relative, err)
		}
	}
	return nil
}

func mirrorModuleGraph(source, destination *os.Root, relative string) error {
	directory := filepath.FromSlash(relative)
	if err := destination.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create bootstrap module mirror %s: %w", relative, err)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(directory, name)
		content, err := source.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s/%s for bootstrap: %w", relative, name, err)
		}
		if err = destination.WriteFile(path, content, 0o600); err != nil {
			return fmt.Errorf("write bootstrap mirror %s/%s: %w", relative, name, err)
		}
		bootstrapName := "bootstrap" + filepath.Ext(name)
		bootstrapPath := filepath.Join(directory, bootstrapName)
		if err = destination.WriteFile(bootstrapPath, content, 0o600); err != nil {
			return fmt.Errorf("write bootstrap modfile %s/%s: %w", relative, bootstrapName, err)
		}
	}
	return nil
}

func snapshotRepository(root string) (map[string]repositoryFileDigest, error) {
	result := make(map[string]repositoryFileDigest)
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	walkErr := fs.WalkDir(directory.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("repository snapshot contains non-regular file %s", path)
		}
		content, err := directory.ReadFile(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		result[path] = repositoryFileDigest{
			Hash: sha256.Sum256(content),
			Mode: info.Mode().Perm(),
		}
		return nil
	})
	return result, errors.Join(walkErr, directory.Close())
}

func repositorySnapshotsEqual(left, right map[string]repositoryFileDigest) bool {
	if len(left) != len(right) || !slices.Equal(sortedRepositoryKeys(left), sortedRepositoryKeys(right)) {
		return false
	}
	for path, digest := range left {
		if right[path] != digest {
			return false
		}
	}
	return true
}

func sortedRepositoryKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}
