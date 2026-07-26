package devloop

import "testing"

func TestPathFilterAppliesDefaultsAndCustomRules(t *testing.T) {
	t.Parallel()
	filter, err := NewPathFilter(PathRules{
		Include: []string{"assets/**", "schema/*.graphql"},
		Exclude: []string{"**/ignored/**", "config/local.yaml"},
	})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}
	tests := []struct {
		path string
		want bool
	}{
		{path: "main.go", want: true},
		{path: "go.mod", want: true},
		{path: "config/application.yaml", want: true},
		{path: "migrations/001_orders.sql", want: true},
		{path: "templates/order.tmpl", want: true},
		{path: "assets/logo.bin", want: true},
		{path: "schema/orders.graphql", want: true},
		{path: "README.md", want: false},
		{path: "config/local.yaml", want: false},
		{path: "module/ignored/service.go", want: false},
		{path: "module/.service.go.swp", want: false},
		{path: "examples/commerce/zz_spice_gen.go", want: false},
		{path: "examples/commerce/openapi.json", want: false},
		{path: ".spice/commerce.manifest.json", want: false},
	}
	for _, test := range tests {
		if got := filter.Match(test.path); got != test.want {
			t.Errorf("Match(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestPathFilterSkipsToolAndGeneratedDirectories(t *testing.T) {
	t.Parallel()
	filter, err := NewPathFilter(PathRules{})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}
	for _, directory := range []string{
		".git",
		"module/vendor",
		"module/node_modules/pkg",
		".spice/dev",
		".spice/build/candidate",
		"internal/spicegen/application",
	} {
		if !filter.SkipDirectory(directory) {
			t.Errorf("SkipDirectory(%q) = false, want true", directory)
		}
	}
	if filter.SkipDirectory("orders/internal") {
		t.Fatal("SkipDirectory(orders/internal) = true, want false")
	}
}

func TestPathFilterRejectsUnsafePatternsAndPaths(t *testing.T) {
	t.Parallel()
	for _, rules := range []PathRules{
		{Include: []string{"../outside/**"}},
		{Exclude: []string{"/absolute/**"}},
		{Include: []string{"["}},
	} {
		if _, err := NewPathFilter(rules); err == nil {
			t.Fatalf("NewPathFilter(%+v) error = nil, want failure", rules)
		}
	}
	filter, err := NewPathFilter(PathRules{})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}
	if filter.Match("../outside.go") {
		t.Fatal("Match(../outside.go) = true, want false")
	}
	if !matchPath("assets/**/logo.*", "assets/images/ui/logo.svg") {
		t.Fatal("matchPath() = false for recursive pattern, want true")
	}
}
