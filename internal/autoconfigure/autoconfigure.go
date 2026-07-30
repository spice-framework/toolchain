// Package autoconfigure provides the explicitly imported default Spice command
// bean used by the production CLI application.
package autoconfigure

import "github.com/StevenBuglione/spice/starter"

// SpiceAutoConfiguration declares the default command bean. The descriptor is
// statically decoded during analysis and is never executed by the compiler.
func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{
		Review: "docs/dogfooding-readiness.md",
		Beans: []starter.AutoBean{
			{
				Factory:  DefaultRuntime,
				Name:     "runtime",
				Fallback: true,
			},
			{
				Factory:  DefaultHelpHandler,
				Name:     "helpHandler",
				Fallback: true,
				Order:    0,
			},
			{
				Factory:  DefaultVersionHandler,
				Name:     "versionHandler",
				Fallback: true,
				Order:    10,
			},
			{
				Factory:  DefaultVerifyHandler,
				Name:     "verifyHandler",
				Fallback: true,
				Order:    20,
			},
			{
				Factory:  DefaultAnnotationsHandler,
				Name:     "annotationsHandler",
				Fallback: true,
				Order:    30,
			},
			{
				Factory:  DefaultModulesHandler,
				Name:     "modulesHandler",
				Fallback: true,
				Order:    40,
			},
			{
				Factory:  DefaultBeansHandler,
				Name:     "beansHandler",
				Fallback: true,
				Order:    50,
			},
			{
				Factory:  DefaultGeneratedHandler,
				Name:     "generatedHandler",
				Fallback: true,
				Order:    60,
			},
			{
				Factory:  DefaultTestHandler,
				Name:     "testHandler",
				Fallback: true,
				Order:    70,
			},
			{
				Factory:  DefaultGenerateHandler,
				Name:     "generateHandler",
				Fallback: true,
				Order:    80,
			},
			{
				Factory:  DefaultBuildHandler,
				Name:     "buildHandler",
				Fallback: true,
				Order:    90,
			},
			{
				Factory:  DefaultRunHandler,
				Name:     "runHandler",
				Fallback: true,
				Order:    100,
			},
			{
				Factory:  DefaultDevHandler,
				Name:     "devHandler",
				Fallback: true,
				Order:    110,
			},
			{
				Factory:  DefaultLSPHandler,
				Name:     "lspHandler",
				Fallback: true,
				Order:    120,
			},
			{
				Factory:  DefaultCommand,
				Name:     "command",
				Fallback: true,
			},
		},
	}
}
