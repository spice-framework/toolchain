package generate

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"github.com/spice-framework/toolchain/compiler/application"
	"github.com/spice-framework/toolchain/compiler/provider"
)

type generatedLoggingScope struct {
	module    string
	component string
}

func loggingScopes(
	model application.Model,
	applicationTarget application.Target,
	providers []provider.Provider,
	providerModules map[string]string,
	moduleIdentities []string,
) []generatedLoggingScope {
	scopes := make(map[string]generatedLoggingScope)
	add := func(module, component string) {
		if module == "" {
			return
		}
		key := module + "\x00" + component
		scopes[key] = generatedLoggingScope{module: module, component: component}
	}
	add(applicationTarget.PackagePath, "")
	for _, module := range model.Modules() {
		add(module.ID, "")
	}
	for _, moduleID := range moduleIdentities {
		add(moduleID, "")
	}
	for _, item := range providers {
		module := effectiveProviderModule(item, providerModules)
		add(module, "")
		if item.Source != provider.SourceLogging {
			add(module, item.SymbolID)
		}
	}
	for _, controller := range model.Controllers() {
		add(controller.Module, "")
		for _, route := range controller.Routes() {
			add(route.Module, "")
		}
	}
	for _, service := range model.Policies() {
		add(service.Module, "")
	}
	for _, boundary := range model.Transactions() {
		add(boundary.Module, "")
	}
	for _, boundary := range model.Caches() {
		add(boundary.Module, "")
	}
	add(applicationTarget.PackagePath, "spice.schedule")
	result := make([]generatedLoggingScope, 0, len(scopes))
	for _, scope := range scopes {
		result = append(result, scope)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].module != result[right].module {
			return result[left].module < result[right].module
		}
		return result[left].component < result[right].component
	})
	return result
}

func writeLoggingSetup(
	source *bytes.Buffer,
	features commandFeatures,
	scopes []generatedLoggingScope,
) {
	if !features.logging {
		return
	}
	fmt.Fprintf(source, "\tloggingFormatValue, err := configurationSnapshot.RequiredString(%s)\n", strconv.Quote(loggingFormatKey))
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode Spice logging format: %w\", err))\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\tloggingLevelValue, err := configurationSnapshot.RequiredString(%s)\n", strconv.Quote(loggingLevelKey))
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode Spice logging level: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tloggingLevel, err := spicelogging.ParseLevel(loggingLevelValue)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode Spice logging level: %w\", err))\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\tloggingAddSource, err := configurationSnapshot.Boolean(%s)\n", strconv.Quote(loggingAddSourceKey))
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode Spice logging source policy: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tloggingScopes := []spicelogging.Scope{\n")
	for _, scope := range scopes {
		source.WriteString("\t\t{Module: ")
		source.WriteString(strconv.Quote(scope.module))
		if scope.component != "" {
			source.WriteString(", Component: ")
			source.WriteString(strconv.Quote(scope.component))
		}
		source.WriteString("},\n")
	}
	source.WriteString("\t}\n")
	fmt.Fprintf(source, "\tloggingLevelsValue, _ := configurationSnapshot.Lookup(%s)\n", strconv.Quote(loggingLevelsKey))
	source.WriteString("\tloggingLevels, err := spicelogging.ParseLevelRules(loggingLevelsValue, loggingScopes)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"decode Spice logging scope levels: %w\", err))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tloggingConfiguration := spicelogging.Configuration{\n")
	source.WriteString("\t\tFormat: spicelogging.Format(loggingFormatValue), Level: loggingLevel,\n")
	source.WriteString("\t\tLevels: loggingLevels, AddSource: loggingAddSource,\n")
	source.WriteString("\t}\n")
	source.WriteString("\tvar loggingWriter io.Writer\n")
	source.WriteString("\tvar loggingHandler slog.Handler\n")
	source.WriteString("\tif options.Logging != nil {\n")
	source.WriteString("\t\tif (options.Logging.Writer == nil) == (options.Logging.Handler == nil) {\n")
	source.WriteString("\t\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"configure Spice logging: exactly one writer or handler is required\"))\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t\tloggingWriter = options.Logging.Writer\n")
	source.WriteString("\t\tloggingHandler = options.Logging.Handler\n")
	source.WriteString("\t\tif options.Logging.Configuration != nil {\n")
	source.WriteString("\t\t\tloggingConfiguration = *options.Logging.Configuration\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t}\n")
	source.WriteString("\tif options.Logging != nil && options.Logger != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"configure Spice logging: Logging and deprecated Logger conflict\"))\n")
	source.WriteString("\t}\n")
	source.WriteString("\tif options.Logging == nil && options.Logger != nil {\n")
	source.WriteString("\t\tloggingHandler = options.Logger.Handler()\n")
	source.WriteString("\t}\n")
	source.WriteString("\tapplication.logger, err = spicelogging.New(spicelogging.Options{\n")
	source.WriteString("\t\tApplication: TargetID, Configuration: loggingConfiguration,\n")
	source.WriteString("\t\tWriter: loggingWriter, Handler: loggingHandler, Scopes: loggingScopes,\n")
	source.WriteString("\t})\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"configure Spice logging: %w\", err))\n")
	source.WriteString("\t}\n")
}

func selectedApplicationLoggerVariable(
	providers []provider.Provider,
	variables map[string]string,
) string {
	for _, item := range providers {
		if item.Source == provider.SourceLogging || item.Fallback ||
			item.OutputTypeID != "*github.com/spice-framework/spice/logging.Logger" {
			continue
		}
		return variables[item.SymbolID]
	}
	return ""
}

func writeSelectedApplicationLogger(source *bytes.Buffer, variable string) {
	if variable == "" {
		return
	}
	fmt.Fprintf(source, "\tapplication.logger = dependencies.%s\n", variable)
	source.WriteString("\tif application.logger == nil {\n")
	source.WriteString("\t\treturn nil, application.coordinator.Abort(ctx, fmt.Errorf(\"configure Spice logging: application logger provider returned nil\"))\n")
	source.WriteString("\t}\n")
}

func writeLoggingAccessors(source *bytes.Buffer, features commandFeatures) {
	if !features.logging {
		return
	}
	source.WriteString("// Logger returns the selected application-owned Spice logger.\n")
	source.WriteString("func (application *Application) Logger() *spicelogging.Logger {\n")
	source.WriteString("\tif application == nil { return nil }\n")
	source.WriteString("\treturn application.logger\n")
	source.WriteString("}\n\n")
	source.WriteString("// LoggingController returns the exact-scope runtime level controller.\n")
	source.WriteString("func (application *Application) LoggingController() *spicelogging.Controller {\n")
	source.WriteString("\tif application == nil || application.logger == nil { return nil }\n")
	source.WriteString("\treturn application.logger.Controller()\n")
	source.WriteString("}\n\n")
}
