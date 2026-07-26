package async

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func TestCatalogCompilesTypedAsynchronousMethods(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/tasks\n\ngo 1.26.0\n",
		"app/tasks.go": `package app

import "context"

type ContextAlias = context.Context
type ErrorAlias = error
type Message struct{ ID string }
type Worker struct{}

// @Bean
func NewWorker() *Worker {
	panic("provider bodies must not execute during analysis")
}

// @async.Execute
func (*Worker) Reconcile(context.Context) error {
	panic("asynchronous methods must not execute during analysis")
}

// @async.Execute
func (*Worker) Send(
	_ ContextAlias,
	message Message,
	tags []string,
	lookup map[string]*Message,
) ErrorAlias {
	panic("asynchronous methods must not execute during analysis")
}
`,
	})
	program, resolution, providers := loadCatalogs(t, root)
	catalog := Build(program, resolution, providers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	tasks := catalog.Tasks()
	if len(tasks) != 2 || tasks[0].MethodID >= tasks[1].MethodID {
		t.Fatalf("Tasks() = %#v", tasks)
	}
	send := taskByMethod(tasks, "Send")
	if send == nil ||
		send.ProviderID == "" ||
		send.ReceiverTypeID != "*example.com/tasks/app.Worker" ||
		send.SubmitMethod != "SubmitWorkerSend" {
		t.Fatalf("Send task = %#v", send)
	}
	parameters := send.Parameters()
	if len(parameters) != 3 ||
		parameters[0].Index != 1 ||
		parameters[0].Name != "message" ||
		parameters[0].TypeID != "example.com/tasks/app.Message" ||
		parameters[1].TypeID != "[]string" ||
		parameters[2].TypeID != "map[string]*example.com/tasks/app.Message" {
		t.Fatalf("Send parameters = %#v", parameters)
	}
	parameters[0].TypeID = "mutated"
	if send.Parameters()[0].TypeID == "mutated" {
		t.Fatal("Parameters() returned mutable catalog storage")
	}
	for index := range tasks {
		if tasks[index].Method.Name == "Send" {
			tasks[index].parameters = nil
		}
	}
	freshSend := taskByMethod(catalog.Tasks(), "Send")
	if freshSend == nil || len(freshSend.Parameters()) != 3 {
		t.Fatal("Tasks() returned mutable catalog storage")
	}
}

func TestCatalogRejectsInvalidAsynchronousMethodsDeterministically(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/invalidtasks\n\ngo 1.26.0\n",
		"app/tasks.go": `package app

import (
	"context"
	"time"
)

type ContextLike interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}
type privateMessage struct{}

// @async.Execute
func Function(context.Context) error { return nil }

type Repeated struct{}
// @Bean
func NewRepeated() Repeated { return Repeated{} }
// @async.Execute
// @async.Execute
func (Repeated) Run(context.Context) error { return nil }

type Arguments struct{}
// @Bean
func NewArguments() Arguments { return Arguments{} }
// @async.Execute(queue="other")
func (Arguments) Run(context.Context) error { return nil }

type MissingContext struct{}
// @Bean
func NewMissingContext() MissingContext { return MissingContext{} }
// @async.Execute
func (MissingContext) Run() error { return nil }

type WrongContext struct{}
// @Bean
func NewWrongContext() WrongContext { return WrongContext{} }
// @async.Execute
func (WrongContext) Run(ContextLike) error { return nil }

type WrongResult struct{}
// @Bean
func NewWrongResult() WrongResult { return WrongResult{} }
// @async.Execute
func (WrongResult) Run(context.Context) string { return "" }

type NoProvider struct{}
// @async.Execute
func (NoProvider) Run(context.Context) error { return nil }

type Variadic struct{}
// @Bean
func NewVariadic() Variadic { return Variadic{} }
// @async.Execute
func (Variadic) Run(context.Context, ...string) error { return nil }

type Box[T any] struct{}
// @Bean
func NewBox() Box[int] { return Box[int]{} }
// @async.Execute
func (Box[T]) Run(context.Context) error { return nil }

type PrivateMethod struct{}
// @Bean
func NewPrivateMethod() PrivateMethod { return PrivateMethod{} }
// @async.Execute
func (PrivateMethod) run(context.Context) error { return nil }

type PrivateParameter struct{}
// @Bean
func NewPrivateParameter() PrivateParameter { return PrivateParameter{} }
// @async.Execute
func (PrivateParameter) Run(context.Context, privateMessage) error { return nil }
`,
	})
	program, resolution, providers := loadCatalogs(t, root)
	var baseline []string
	for run := range 4 {
		if run%2 == 1 {
			slices.Reverse(resolution.Occurrences)
		}
		diagnostics := Build(program, resolution, providers).Diagnostics()
		got := diagnosticStrings(diagnostics)
		if len(got) != 11 {
			t.Fatalf(
				"run %d diagnostic count = %d, want 11:\n%s",
				run,
				len(got),
				strings.Join(got, "\n"),
			)
		}
		if run == 0 {
			baseline = got
		} else if !slices.Equal(got, baseline) {
			t.Fatalf(
				"diagnostics changed at run %d:\nfirst=%v\nnow=%v",
				run,
				baseline,
				got,
			)
		}
	}
	for _, required := range []string{
		"ordinary method",
		"not repeatable",
		"does not accept annotation arguments",
		"require context.Context",
		"exact loaded context.Context",
		"predeclared error result",
		"no @Bean provider",
		"non-variadic",
		"receiver-generic",
		"method must be exported",
		"cannot be named safely",
	} {
		if !strings.Contains(strings.Join(baseline, "\n"), required) {
			t.Fatalf("diagnostics omit %q:\n%s", required, strings.Join(baseline, "\n"))
		}
	}
}

func TestCatalogRejectsGeneratedSubmitMethodCollisions(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/collision\n\ngo 1.26.0\n",
		"first/task.go": `package first
import "context"
type Worker struct{}
// @Bean
func NewWorker() *Worker { return &Worker{} }
// @async.Execute
func (*Worker) Run(context.Context) error { return nil }
`,
		"second/task.go": `package second
import "context"
type Worker struct{}
// @Bean
func NewWorker() *Worker { return &Worker{} }
// @async.Execute
func (*Worker) Run(context.Context) error { return nil }
`,
	})
	program, resolution, providers := loadCatalogs(t, root)
	diagnostics := Build(program, resolution, providers).Diagnostics()
	if len(diagnostics) != 2 {
		t.Fatalf("Diagnostics() = %v, want two collisions", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind != "submit-method-collision" ||
			!strings.Contains(
				diagnostic.Message,
				"Application.SubmitWorkerRun",
			) {
			t.Fatalf("collision diagnostic = %#v", diagnostic)
		}
	}
}

func TestCatalogRejectsNilProgram(t *testing.T) {
	t.Parallel()
	diagnostics := Build(nil, resolve.Result{}, provider.Catalog{}).Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Kind != "internal" {
		t.Fatalf("Diagnostics() = %#v", diagnostics)
	}
}

func taskByMethod(tasks []Task, name string) *Task {
	for index := range tasks {
		if tasks[index].Method.Name == name {
			return &tasks[index]
		}
	}
	return nil
}

func loadCatalogs(
	t *testing.T,
	root string,
) (*load.Program, resolve.Result, provider.Catalog) {
	t.Helper()
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./...",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider.Build() diagnostics = %v", diagnostics)
	}
	return program, resolution, providers
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(files))
	for file := range files {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	for _, file := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(files[file]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
