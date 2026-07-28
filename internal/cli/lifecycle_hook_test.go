package cli

import (
	"strings"
	"testing"
)

func TestRunVerifyAcceptsLifecycleHooks(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/hooks\n\ngo 1.26.0\n",
		"app/app.go": `package app
import (
    "context"
    life "github.com/StevenBuglione/spice/lifecycle"
)
type Server struct{}
type Worker struct{}
type ContextAlias = context.Context
type ErrorAlias = error
// @Bean
func ServerProvider() (*Server, life.Cleanup) { panic("provider and cleanup must not execute") }
// @OnStart
func (*Server) Boot(ContextAlias) ErrorAlias { panic("hook must not execute") }
// @OnStop
func (*Server) Halt(context.Context) error { panic("hook must not execute") }
// @Bean
func WorkerProvider() Worker { panic("provider must not execute") }
// @OnStart
func (Worker) Engage(context.Context) error { panic("hook must not execute") }
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "5 annotations") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunVerifyRejectsLifecycleHooks(t *testing.T) {
	root := writeModule(t, map[string]string{
		"app.go": `package sample
import (
    "context"
    "time"
)
type StopOnly struct{}
type PointerMismatch struct{}
type BadContext struct{}
type BadResult struct{}
type ContextLike interface {
    Deadline() (time.Time, bool)
    Done() <-chan struct{}
    Err() error
    Value(any) any
}
type ErrorLike interface { Error() string }
// @Bean
func StopOnlyProvider() StopOnly { panic("provider must not execute") }
// @OnStop
func (StopOnly) Halt(context.Context) error { panic("hook must not execute") }
// @Bean
func PointerMismatchProvider() PointerMismatch { panic("provider must not execute") }
// @OnStart
func (*PointerMismatch) Boot(context.Context) error { panic("hook must not execute") }
// @Bean
func BadContextProvider() BadContext { panic("provider must not execute") }
// @OnStart
func (BadContext) Boot(ContextLike) error { panic("hook must not execute") }
// @Bean
func BadResultProvider() BadResult { panic("provider must not execute") }
// @OnStart
func (BadResult) Boot(context.Context) ErrorLike { panic("hook must not execute") }
`,
	})
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"no corresponding @OnStart",
		"no @Bean provider produces exact receiver type *example.com/fixture.PointerMismatch",
		"parameter 0 must be the exact loaded context.Context type",
		"result 0 must be the exact predeclared error type",
		"4 lifecycle hook error(s)",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}
