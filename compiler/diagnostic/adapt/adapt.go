// Package adapt converts existing compiler-stage diagnostics into the shared
// Spice diagnostic contract without changing stage-local metadata models.
package adapt

import (
	"go/token"

	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	"github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/starter"
	"github.com/StevenBuglione/spice/compiler/validate"
)

// Application converts immutable application-model diagnostics.
func Application(
	workspaceRoot string,
	items []application.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = sourceDiagnostic(
			workspaceRoot,
			string(item.Stage),
			item.Kind,
			item.Message,
			item.Position,
			item.PhysicalPosition,
		)
	}
	return diagnostic.NewSet(result...)
}

// Generation converts deterministic renderer diagnostics.
func Generation(
	workspaceRoot string,
	items []generate.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"generation",
			item.Kind,
			item.Message,
			item.Position,
			item.PhysicalPosition,
		)
	}
	return diagnostic.NewSet(result...)
}

// Load converts package-driver and Go parser/type-checker diagnostics.
func Load(workspaceRoot string, items []load.Diagnostic) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		position := token.Position{
			Filename: item.Filename,
			Line:     item.Line,
			Column:   item.Column,
		}
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"load",
			item.Kind,
			item.Message,
			position,
			position,
		)
	}
	return diagnostic.NewSet(result...)
}

// Module converts application-module architecture diagnostics.
func Module(
	workspaceRoot string,
	items []modulith.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"module",
			item.Kind,
			item.Message,
			item.Position,
			item.PhysicalPosition,
		)
	}
	return diagnostic.NewSet(result...)
}

// Provider converts exact-type provider catalog diagnostics.
func Provider(
	workspaceRoot string,
	items []provider.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"provider",
			item.Kind,
			item.Message,
			item.Position,
			item.PhysicalPosition,
		)
	}
	return diagnostic.NewSet(result...)
}

// Resolution converts parsed annotation association diagnostics.
func Resolution(
	workspaceRoot string,
	items []resolve.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		physical := item.PhysicalPosition
		if physical.Filename == "" {
			physical = token.Position{
				Filename: item.PhysicalFile,
				Offset:   item.PhysicalOffset,
			}
		}
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"resolution",
			item.Kind,
			item.Message,
			item.Position,
			physical,
		)
	}
	return diagnostic.NewSet(result...)
}

// StarterDependencies converts dependency-alignment diagnostics, which are
// application-global and therefore intentionally source-less.
func StarterDependencies(
	items []starter.DependencyDiagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = diagnostic.New(
			diagnostic.Code("starter", item.Kind),
			diagnostic.SeverityError,
			item.Message,
			diagnostic.SourceLocation("", "", "", 1, 1, 0),
		)
	}
	return diagnostic.NewSet(result...)
}

// Validation converts typed annotation-definition diagnostics.
func Validation(
	workspaceRoot string,
	items []validate.Diagnostic,
) diagnostic.Set {
	result := make([]diagnostic.Diagnostic, len(items))
	for index, item := range items {
		result[index] = sourceDiagnostic(
			workspaceRoot,
			"validation",
			item.Code(),
			item.Message(),
			item.Position,
			item.PhysicalPosition,
		)
	}
	return diagnostic.NewSet(result...)
}

// Failure constructs one source-less operational compiler diagnostic.
func Failure(stage, kind, message string) diagnostic.Set {
	return diagnostic.NewSet(diagnostic.New(
		diagnostic.Code(stage, kind),
		diagnostic.SeverityError,
		message,
		diagnostic.SourceLocation("", "", "", 1, 1, 0),
	))
}

func sourceDiagnostic(
	workspaceRoot string,
	stage string,
	kind string,
	message string,
	display token.Position,
	physical token.Position,
) diagnostic.Diagnostic {
	physicalPath := physical.Filename
	if physicalPath == "" {
		physicalPath = display.Filename
	}
	offset := physical.Offset
	if physical.Filename == "" {
		offset = display.Offset
	}
	physicalLine := physical.Line
	physicalColumn := physical.Column
	if physicalLine <= 0 {
		physicalLine = display.Line
	}
	if physicalColumn <= 0 {
		physicalColumn = display.Column
	}
	return diagnostic.New(
		diagnostic.Code(stage, kind),
		diagnostic.SeverityError,
		message,
		diagnostic.SourceMappedLocation(
			workspaceRoot,
			display.Filename,
			physicalPath,
			display.Line,
			display.Column,
			display.Offset,
			physicalLine,
			physicalColumn,
			offset,
		),
	)
}
