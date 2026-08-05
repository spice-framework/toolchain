package schedule

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestCatalogCompilesFixedDelayJobs(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/jobs\n\ngo 1.26.0\n",
		"app/jobs.go": `package app

import "context"

type ContextAlias = context.Context
type ErrorAlias = error
type Worker struct{}

// @Bean
func NewWorker() *Worker {
	panic("provider bodies must not execute during analysis")
}

// @schedule.FixedDelay(continueOnError=true, initialDelay="250ms", delay="2s")
func (*Worker) Refresh(ContextAlias) ErrorAlias {
	panic("scheduled methods must not execute during analysis")
}

// @schedule.FixedDelay(delay="1m")
func (*Worker) Reconcile(context.Context) error {
	panic("scheduled methods must not execute during analysis")
}
`,
	})
	program, resolution, providers := loadCatalogs(t, root)
	catalog := Build(program, resolution, providers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	jobs := catalog.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("Jobs() = %#v", jobs)
	}
	if jobs[0].MethodID >= jobs[1].MethodID {
		t.Fatalf("jobs are not sorted by method identity: %#v", jobs)
	}
	refresh := jobByMethod(jobs, "Refresh")
	if refresh == nil ||
		refresh.Delay != 2*time.Second ||
		refresh.InitialDelay != 250*time.Millisecond ||
		!refresh.ContinueOnError ||
		refresh.ReceiverTypeID != "*example.com/jobs/app.Worker" ||
		refresh.ProviderID == "" {
		t.Fatalf("Refresh job = %#v", refresh)
	}
	reconcile := jobByMethod(jobs, "Reconcile")
	if reconcile == nil ||
		reconcile.Delay != time.Minute ||
		reconcile.InitialDelay != 0 ||
		reconcile.ContinueOnError {
		t.Fatalf("Reconcile job = %#v", reconcile)
	}
	first := catalog.Jobs()
	first[0].Delay = 0
	if catalog.Jobs()[0].Delay == 0 {
		t.Fatal("Jobs() returned mutable catalog storage")
	}
}

func TestCatalogRejectsInvalidFixedDelayJobsDeterministically(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/invalidjobs\n\ngo 1.26.0\n",
		"app/jobs.go": `package app

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
type ErrorLike interface{ Error() string }

// @schedule.FixedDelay(delay="1s")
func Function(context.Context) error { return nil }

type Repeated struct{}
// @Bean
func NewRepeated() Repeated { return Repeated{} }
// @schedule.FixedDelay(delay="1s")
// @schedule.FixedDelay(delay="2s")
func (Repeated) Run(context.Context) error { return nil }

type Missing struct{}
// @Bean
func NewMissing() Missing { return Missing{} }
// @schedule.FixedDelay
func (Missing) Run(context.Context) error { return nil }

type InvalidDuration struct{}
// @Bean
func NewInvalidDuration() InvalidDuration { return InvalidDuration{} }
// @schedule.FixedDelay(delay="later")
func (InvalidDuration) Run(context.Context) error { return nil }

type ZeroDelay struct{}
// @Bean
func NewZeroDelay() ZeroDelay { return ZeroDelay{} }
// @schedule.FixedDelay(delay="0s")
func (ZeroDelay) Run(context.Context) error { return nil }

type NegativeInitial struct{}
// @Bean
func NewNegativeInitial() NegativeInitial { return NegativeInitial{} }
// @schedule.FixedDelay(delay="1s", initialDelay="-1s")
func (NegativeInitial) Run(context.Context) error { return nil }

type Spaced struct{}
// @Bean
func NewSpaced() Spaced { return Spaced{} }
// @schedule.FixedDelay(delay=" 1s")
func (Spaced) Run(context.Context) error { return nil }

type WrongContext struct{}
// @Bean
func NewWrongContext() WrongContext { return WrongContext{} }
// @schedule.FixedDelay(delay="1s")
func (WrongContext) Run(ContextLike) error { return nil }

type WrongResult struct{}
// @Bean
func NewWrongResult() WrongResult { return WrongResult{} }
// @schedule.FixedDelay(delay="1s")
func (WrongResult) Run(context.Context) ErrorLike { return nil }

type NoProvider struct{}
// @schedule.FixedDelay(delay="1s")
func (NoProvider) Run(context.Context) error { return nil }

type Variadic struct{}
// @Bean
func NewVariadic() Variadic { return Variadic{} }
// @schedule.FixedDelay(delay="1s")
func (Variadic) Run(...context.Context) error { return nil }

type Box[T any] struct{}
// @Bean
func NewBox() Box[int] { return Box[int]{} }
// @schedule.FixedDelay(delay="1s")
func (Box[T]) Run(context.Context) error { return nil }
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
		if len(got) != 12 {
			t.Fatalf(
				"run %d diagnostic count = %d, want 12:\n%s",
				run,
				len(got),
				strings.Join(got, "\n"),
			)
		}
		if run == 0 {
			baseline = got
		} else if !slices.Equal(got, baseline) {
			t.Fatalf(
				"run %d diagnostics changed:\nfirst=%v\nnext=%v",
				run,
				baseline,
				got,
			)
		}
	}
	joined := strings.Join(baseline, "\n")
	for _, expected := range []string{
		"must target ordinary methods",
		"not repeatable",
		`required argument "delay" is missing`,
		"not a valid Go duration",
		`argument "delay" must be positive`,
		`argument "initialDelay" must not be negative`,
		"without surrounding whitespace",
		"exact loaded context.Context type",
		"exact predeclared error type",
		"no @Bean provider produces exact receiver type",
		"must be non-variadic",
		"receiver-generic scheduled methods are not supported",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogValidatesArgumentsWithoutRegistryPrevalidation(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		want       string
	}{
		{
			name:       "positional",
			annotation: `@schedule.FixedDelay("1s")`,
			want:       "accepts only named arguments",
		},
		{
			name:       "wrong delay type",
			annotation: `@schedule.FixedDelay(delay=true)`,
			want:       `argument "delay" requires a duration string`,
		},
		{
			name:       "wrong continue type",
			annotation: `@schedule.FixedDelay(delay="1s", continueOnError="yes")`,
			want:       `argument "continueOnError" requires boolean`,
		},
		{
			name:       "unknown",
			annotation: `@schedule.FixedDelay(delay="1s", zone="UTC")`,
			want:       `does not define argument "zone"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(t, map[string]string{
				"go.mod": "module example.com/arguments\n\ngo 1.26.0\n",
				"app/jobs.go": `package app

import "context"

type Worker struct{}

// @Bean
func NewWorker() Worker { return Worker{} }

// ` + test.annotation + `
func (Worker) Run(context.Context) error { return nil }
`,
			})
			program, resolution, providers := loadCatalogs(t, root)
			diagnostics := Build(
				program,
				resolution,
				providers,
			).Diagnostics()
			if len(diagnostics) != 1 ||
				!strings.Contains(diagnostics[0].Message, test.want) {
				t.Fatalf(
					"Diagnostics() = %v, want containing %q",
					diagnosticStrings(diagnostics),
					test.want,
				)
			}
		})
	}
	if diagnostics := Build(
		nil,
		resolve.Result{},
		provider.Catalog{},
	).Diagnostics(); len(diagnostics) != 1 ||
		diagnostics[0].Kind != "internal" {
		t.Fatalf("Build(nil) diagnostics = %#v", diagnostics)
	}
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
		t.Fatalf("resolve diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider diagnostics = %v", diagnostics)
	}
	return program, resolution, providers
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(filePath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			fullPath,
			[]byte(files[filePath]),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func jobByMethod(jobs []Job, name string) *Job {
	for index := range jobs {
		if jobs[index].Method.Name == name {
			return &jobs[index]
		}
	}
	return nil
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
