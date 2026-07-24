// Package validate checks scanned annotations against typed definitions.
package validate

import (
	"fmt"
	"go/token"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/scan"
)

// Diagnostic is one source-positioned annotation validation failure.
type Diagnostic struct {
	Position   token.Position
	Annotation string
	Target     annotation.Target
	Name       string
	Allowed    []annotation.Target
	Unknown    bool
}

// Error formats an actionable, stable compiler-style diagnostic.
func (diagnostic Diagnostic) Error() string {
	position := diagnostic.Position
	filename := position.Filename
	if filename == "" {
		filename = "<unknown>"
	}
	if position.Line <= 0 {
		position.Line = 1
	}
	if position.Column <= 0 {
		position.Column = 1
	}

	if diagnostic.Unknown {
		return fmt.Sprintf(
			"%s:%d:%d: unknown annotation @%s; add a registered annotation definition before using it",
			filename,
			position.Line,
			position.Column,
			diagnostic.Annotation,
		)
	}

	allowed := make([]string, len(diagnostic.Allowed))
	for index, target := range diagnostic.Allowed {
		allowed[index] = string(target)
	}
	label := "targets"
	if len(allowed) == 1 {
		label = "target"
	}
	return fmt.Sprintf(
		"%s:%d:%d: annotation @%s cannot target %s %q; allowed %s: %s",
		filename,
		position.Line,
		position.Column,
		diagnostic.Annotation,
		diagnostic.Target,
		diagnostic.Name,
		label,
		strings.Join(allowed, ", "),
	)
}

// Occurrences validates declarations against registry and returns diagnostics
// in deterministic source order. Unknown annotations fail closed until a
// definition-loading mechanism explicitly registers them.
func Occurrences(occurrences []scan.Occurrence, registry annotation.Registry) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	for _, occurrence := range occurrences {
		definition, ok := registry.Lookup(occurrence.Annotation.Name)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{
				Position:   occurrence.Annotation.Position,
				Annotation: occurrence.Annotation.Name,
				Target:     occurrence.Target,
				Name:       occurrence.Name,
				Unknown:    true,
			})
			continue
		}
		if definition.Targets.Contains(occurrence.Target) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Position:   occurrence.Annotation.Position,
			Annotation: occurrence.Annotation.Name,
			Target:     occurrence.Target,
			Name:       occurrence.Name,
			Allowed:    definition.Targets.Values(),
		})
	}

	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i]
		right := diagnostics[j]
		if left.Position.Filename != right.Position.Filename {
			return left.Position.Filename < right.Position.Filename
		}
		if left.Position.Line != right.Position.Line {
			return left.Position.Line < right.Position.Line
		}
		if left.Position.Column != right.Position.Column {
			return left.Position.Column < right.Position.Column
		}
		if left.Annotation != right.Annotation {
			return left.Annotation < right.Annotation
		}
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.Name < right.Name
	})
	return diagnostics
}
