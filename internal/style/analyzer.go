package style

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
)

// NewAnalyzer constructs the standalone structural style analyzer.
func NewAnalyzer() *analysis.Analyzer {
	var configurationPath string
	var loadOnce sync.Once
	var configuration Configuration
	var configurationErr error
	analyzer := &analysis.Analyzer{
		Name:     "spicestyle",
		Doc:      "enforce the Spice java-structured application source profile",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
	}
	analyzer.Flags.StringVar(&configurationPath, "config", ".spice/style.json", "strict Spice style configuration")
	analyzer.Run = func(pass *analysis.Pass) (any, error) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		loadOnce.Do(func() {
			path := configurationPath
			if !filepath.IsAbs(path) {
				path = filepath.Join(workingDirectory, path)
			}
			configuration, configurationErr = LoadConfiguration(path)
		})
		if configurationErr != nil {
			return nil, configurationErr
		}
		NewValidator(pass, configuration, workingDirectory).Validate()
		// The go/analysis Run contract requires a nil result when ResultType is nil.
		//nolint:nilnil // Returning a sentinel value would violate that upstream contract.
		return nil, nil
	}
	return analyzer
}

// Analyzer is the exact analysis entrypoint consumed by the standalone tool.
var Analyzer = NewAnalyzer()
