package autoconfigure

import (
	"slices"
	"testing"

	"github.com/StevenBuglione/spice/internal/cli"
)

func TestDefaultCommandConstructsCommand(t *testing.T) {
	t.Parallel()

	runtime := DefaultRuntime()
	var handlers []cli.Handler
	for index, factory := range defaultHandlerFactories() {
		handler, err := factory(runtime)
		if err != nil {
			t.Fatalf("handler factory %d error = %v", index, err)
		}
		handlers = append(handlers, handler)
	}
	command, err := DefaultCommand(handlers)
	if err != nil {
		t.Fatalf("DefaultCommand() error = %v", err)
	}
	if command == nil ||
		!slices.Contains(command.Names(), "verify") ||
		!slices.Contains(command.Names(), "lsp") {
		t.Fatalf("DefaultCommand() = %#v, names=%#v", command, command.Names())
	}
}

func TestDescriptorDocumentsFallbackProvenance(t *testing.T) {
	t.Parallel()

	descriptor := SpiceAutoConfiguration()
	if descriptor.Review != "docs/dogfooding-readiness.md" ||
		len(descriptor.Beans) != 17 {
		t.Fatalf("SpiceAutoConfiguration() = %#v", descriptor)
	}
	if descriptor.Beans[0].Name != "runtime" ||
		descriptor.Beans[0].Factory == nil ||
		!descriptor.Beans[0].Fallback ||
		descriptor.Beans[len(descriptor.Beans)-1].Name != "command" ||
		descriptor.Beans[len(descriptor.Beans)-1].Factory == nil ||
		!descriptor.Beans[len(descriptor.Beans)-1].Fallback {
		t.Fatalf("SpiceAutoConfiguration() boundaries = %#v", descriptor.Beans)
	}
	if _, valid := descriptor.Beans[0].Factory.(func() *cli.Runtime); !valid {
		t.Fatalf(
			"runtime factory type = %T, want func() *cli.Runtime",
			descriptor.Beans[0].Factory,
		)
	}
	if _, valid := descriptor.Beans[1].Factory.(func(*cli.Runtime) (cli.Handler, error)); !valid {
		t.Fatalf(
			"handler factory type = %T",
			descriptor.Beans[1].Factory,
		)
	}
	if _, valid := descriptor.Beans[len(descriptor.Beans)-1].Factory.(func([]cli.Handler) (*cli.Command, error)); !valid {
		t.Fatalf(
			"command factory type = %T",
			descriptor.Beans[len(descriptor.Beans)-1].Factory,
		)
	}
	for index, bean := range descriptor.Beans[1:16] {
		if !bean.Fallback || bean.Order != int64(index*10) {
			t.Fatalf("handler bean %d = %#v", index, bean)
		}
	}
}

func defaultHandlerFactories() []func(*cli.Runtime) (cli.Handler, error) {
	return []func(*cli.Runtime) (cli.Handler, error){
		DefaultHelpHandler,
		DefaultVersionHandler,
		DefaultScaffoldHandler,
		DefaultAddHandler,
		DefaultVerifyHandler,
		DefaultAnnotationsHandler,
		DefaultModulesHandler,
		DefaultBeansHandler,
		DefaultGeneratedHandler,
		DefaultTestHandler,
		DefaultGenerateHandler,
		DefaultBuildHandler,
		DefaultRunHandler,
		DefaultDevHandler,
		DefaultLSPHandler,
	}
}
