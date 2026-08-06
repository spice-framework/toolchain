package boundarygate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestBenchmarkBudgetsExecutesDeclaredOrderAndReportsMedians(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "benchmarks/budgets.json", testBenchmarkBudgetDocument(
		`{"name":"BenchmarkFirst","package":"./first","reference_ns_per_op":10,"maximum_ns_per_op":100,"maximum_bytes_per_op":100,"maximum_allocs_per_op":10,"rationale":"first path"}`,
		`{"name":"BenchmarkSecond","package":"./second","reference_ns_per_op":10,"maximum_ns_per_op":100,"maximum_bytes_per_op":100,"maximum_allocs_per_op":10,"rationale":"second path"}`,
	))
	var packages []string
	var output bytes.Buffer
	gate := verifier{
		root:   root,
		output: &output,
		execute: func(_ context.Context, _ string, environment map[string]string, executable string, arguments ...string) ([]byte, error) {
			if executable != "go" || len(arguments) != 12 {
				t.Fatalf("command = %s %v", executable, arguments)
			}
			if environment["GOPROXY"] != "off" || environment["GOSUMDB"] != "off" ||
				environment["GOTOOLCHAIN"] != "local" || environment["GOWORK"] != "off" {
				t.Fatalf("benchmark environment = %#v", environment)
			}
			packages = append(packages, arguments[11])
			name := strings.TrimSuffix(strings.TrimPrefix(arguments[5], "^"), "$")
			return benchmarkOutput(name, []int{50, 10, 30, 20, 40}), nil
		},
	}
	if err := gate.benchmarkBudgets(context.Background()); err != nil {
		t.Fatalf("benchmarkBudgets() error = %v", err)
	}
	if got, want := strings.Join(packages, ","), "./first,./second"; got != want {
		t.Fatalf("package order = %q, want %q", got, want)
	}
	for _, name := range []string{"BenchmarkFirst", "BenchmarkSecond"} {
		if !strings.Contains(output.String(), name+" median: 30 ns/op, 20 B/op, 2 allocs/op") {
			t.Errorf("output does not contain %s median: %q", name, output.String())
		}
	}
}

func TestBenchmarkBudgetsRejectsMeasuredRegression(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "benchmarks/budgets.json", testBenchmarkBudgetDocument(
		`{"name":"BenchmarkSlow","package":"./slow","reference_ns_per_op":10,"maximum_ns_per_op":20,"maximum_bytes_per_op":10,"maximum_allocs_per_op":1,"rationale":"bounded path"}`,
	))
	gate := verifier{
		root:   root,
		output: io.Discard,
		execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			return benchmarkOutput("BenchmarkSlow", []int{31, 32, 33, 34, 35}), nil
		},
	}
	err := gate.benchmarkBudgets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "time 33 > 20 ns/op") ||
		!strings.Contains(err.Error(), "memory 20 > 10 B/op") ||
		!strings.Contains(err.Error(), "allocations 2 > 1 allocs/op") {
		t.Fatalf("benchmarkBudgets() error = %v", err)
	}
}

func TestParseBenchmarkMeasurementsAndMedian(t *testing.T) {
	t.Parallel()
	measurements, err := parseBenchmarkMeasurements(
		benchmarkOutput("BenchmarkExample", []int{50, 10, 30, 20, 40}),
		"BenchmarkExample",
	)
	if err != nil {
		t.Fatal(err)
	}
	median := medianMeasurement(measurements)
	if median.NSPerOp != 30 || median.BytesPerOp != 20 || median.AllocsPerOp != 2 {
		t.Fatalf("median = %#v", median)
	}
}

func TestParseBenchmarkMeasurementsRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	tests := [][]byte{
		nil,
		bytes.Repeat([]byte("BenchmarkExample-1 1 10 ns/op 1 B/op 1 allocs/op\n"), 4),
		bytes.Repeat([]byte("BenchmarkExample-1 1 invalid ns/op 1 B/op 1 allocs/op\n"), 5),
		bytes.Repeat([]byte("BenchmarkExample-1 1 10 ns/op\n"), 5),
		bytes.Repeat([]byte("BenchmarkExample-1 1 10 ns/op 1 MB/s 2 widgets/op\n"), 5),
	}
	for _, output := range tests {
		if _, err := parseBenchmarkMeasurements(output, "BenchmarkExample"); err == nil {
			t.Errorf("malformed output was accepted: %q", output)
		}
	}
}

func TestLoadBenchmarkBudgetsRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	valid := `{"name":"BenchmarkValid","package":"./compiler/valid","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"reviewed"}`
	tests := []string{
		`{"schema":"wrong","benchmarks":[]}`,
		`{"schema":"spice.benchmarks/v1","benchmarks":[]}`,
		testBenchmarkBudgetDocument(`{"name":"B"}`),
		testBenchmarkBudgetDocument(valid, valid),
		testBenchmarkBudgetDocument(`{"name":"BenchmarkBad","package":"../bad","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"bad path"}`),
		testBenchmarkBudgetDocument(`{"name":"BenchmarkBad","package":"./..","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"bad path"}`),
		testBenchmarkBudgetDocument(`{"name":"BenchmarkBad","package":"./compiler//bad","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"bad path"}`),
		testBenchmarkBudgetDocument(`{"name":"BenchmarkBad","package":"./bad","reference_ns_per_op":3,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"bad maximum"}`),
		`{"schema":"spice.benchmarks/v1","benchmarks":[],"unknown":true}`,
		testBenchmarkBudgetDocument(valid) + ` {}`,
	}
	for _, document := range tests {
		root := t.TempDir()
		writeGateFile(t, root, "benchmarks/budgets.json", document)
		if _, err := loadBenchmarkBudgets(root); err == nil {
			t.Errorf("invalid budget document was accepted: %s", document)
		}
	}
}

func TestPerformanceRequiresExactGoVersionBeforeBenchmarks(t *testing.T) {
	t.Parallel()
	benchmarked := false
	gate := verifier{
		root:   t.TempDir(),
		output: io.Discard,
		execute: func(_ context.Context, _ string, _ map[string]string, executable string, arguments ...string) ([]byte, error) {
			if executable == "go" && len(arguments) == 1 && arguments[0] == "version" {
				return []byte("go version go1.26.4 windows/amd64\n"), nil
			}
			benchmarked = true
			return nil, errors.New("unexpected command")
		},
	}
	if err := gate.performance(context.Background()); err == nil {
		t.Fatal("performance() succeeded with the wrong Go version")
	}
	if benchmarked {
		t.Fatal("performance() benchmarked after Go version failure")
	}
}

func benchmarkOutput(name string, latencies []int) []byte {
	var result strings.Builder
	for _, latency := range latencies {
		fmt.Fprintf(
			&result,
			"%s-24 100 %d ns/op 20 B/op 2 allocs/op\n",
			name,
			latency,
		)
	}
	return []byte(result.String())
}

func testBenchmarkBudgetDocument(budgets ...string) string {
	return `{"schema":"spice.benchmarks/v1","benchmarks":[` +
		strings.Join(budgets, ",") + `]}`
}
