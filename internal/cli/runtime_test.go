package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
)

func TestRuntimeClonesMutableLoadOptions(t *testing.T) {
	t.Parallel()

	options := load.Options{
		Env:               []string{"A=B"},
		BuildFlags:        []string{"-tags=first"},
		AuxiliaryPackages: []string{"example.com/auxiliary"},
		Overlay: map[string][]byte{
			"application.go": []byte("package application"),
		},
	}
	runtime := newRuntime(
		options,
		load.Load,
		executeGoBuild,
		executeGoTest,
	)
	options.Env[0] = "changed"
	options.BuildFlags[0] = "changed"
	options.AuxiliaryPackages[0] = "changed"
	inputOverlay := options.Overlay["application.go"]
	if len(inputOverlay) == 0 {
		t.Fatal("fixture overlay is empty")
	}
	inputOverlay[0] = 'X'
	overlay, found := runtime.options.Overlay["application.go"]
	if runtime.options.Env[0] != "A=B" ||
		runtime.options.BuildFlags[0] != "-tags=first" ||
		runtime.options.AuxiliaryPackages[0] != "example.com/auxiliary" ||
		!found ||
		string(overlay) != "package application" {
		t.Fatalf("runtime options retained caller mutation: %#v", runtime.options)
	}
}

func TestRuntimeValidationRejectsMissingSeams(t *testing.T) {
	t.Parallel()

	validLoader := func(
		context.Context,
		load.Options,
		...string,
	) (*load.Program, error) {
		return nil, errors.New("unused")
	}
	tests := []struct {
		name    string
		runtime *Runtime
		want    string
	}{
		{name: "nil", want: "spice CLI runtime is nil"},
		{
			name: "loader",
			runtime: &Runtime{
				builder: executeGoBuild,
				tester:  executeGoTest,
			},
			want: "spice CLI package loader is nil",
		},
		{
			name: "builder",
			runtime: &Runtime{
				loader: validLoader,
				tester: executeGoTest,
			},
			want: "spice CLI build executor is nil",
		},
		{
			name: "tester",
			runtime: &Runtime{
				loader:  validLoader,
				builder: executeGoBuild,
			},
			want: "spice CLI test executor is nil",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := test.runtime.validate(); err == nil ||
				err.Error() != test.want {
				t.Fatalf("validate() error = %v, want %q", err, test.want)
			}
		})
	}
	if err := NewRuntime().validate(); err != nil {
		t.Fatalf("NewRuntime().validate() error = %v", err)
	}
}
