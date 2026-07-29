package main

import (
	"strings"
	"testing"
)

func TestParseBenchmarkMeasurementsAndMedian(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"goos: windows",
		"BenchmarkExample-24  100  50 ns/op  30 B/op  3 allocs/op",
		"BenchmarkExample-24  100  10 ns/op  50 B/op  1 allocs/op",
		"BenchmarkExample-24  100  30 ns/op  10 B/op  5 allocs/op",
		"BenchmarkExample-24  100  20 ns/op  40 B/op  2 allocs/op",
		"BenchmarkExample-24  100  40 ns/op  20 B/op  4 allocs/op",
	}, "\n")
	measurements, err := parseBenchmarkMeasurements(
		output,
		"BenchmarkExample",
	)
	if err != nil {
		t.Fatal(err)
	}
	median := medianMeasurement(measurements)
	if median.NSPerOp != 30 ||
		median.BytesPerOp != 30 ||
		median.AllocsPerOp != 3 {
		t.Fatalf("median = %#v", median)
	}
}

func TestParseBenchmarkMeasurementsRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		strings.Repeat(
			"BenchmarkExample-1  1  10 ns/op  1 B/op  1 allocs/op\n",
			4,
		),
		strings.Repeat(
			"BenchmarkExample-1  1  invalid ns/op  1 B/op  1 allocs/op\n",
			5,
		),
		strings.Repeat(
			"BenchmarkExample-1  1  10 ns/op\n",
			5,
		),
		strings.Repeat(
			"BenchmarkExample-1  1  10 ns/op  1 MB/s  2 widgets/op\n",
			5,
		),
	}
	for _, output := range tests {
		if _, err := parseBenchmarkMeasurements(
			output,
			"BenchmarkExample",
		); err == nil {
			t.Errorf("malformed output was accepted: %q", output)
		}
	}
}

func TestEnforceBenchmarkBudgetReportsEveryDimension(t *testing.T) {
	t.Parallel()
	budget := benchmarkBudget{
		Name:          "BenchmarkExample",
		MaximumNS:     10,
		MaximumBytes:  20,
		MaximumAllocs: 30,
	}
	if err := enforceBenchmarkBudget(budget, benchmarkMeasurement{
		NSPerOp:     10,
		BytesPerOp:  20,
		AllocsPerOp: 30,
	}); err != nil {
		t.Fatal(err)
	}
	err := enforceBenchmarkBudget(budget, benchmarkMeasurement{
		NSPerOp:     11,
		BytesPerOp:  21,
		AllocsPerOp: 31,
	})
	if err == nil ||
		!strings.Contains(err.Error(), "time") ||
		!strings.Contains(err.Error(), "memory") ||
		!strings.Contains(err.Error(), "allocations") {
		t.Fatalf("enforceBenchmarkBudget() error = %v", err)
	}
}

func TestLoadBenchmarkBudgetsRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	tests := []string{
		`{"schema":"wrong","benchmarks":[]}`,
		`{"schema":"spice.benchmarks/v1","benchmarks":[]}`,
		`{"schema":"spice.benchmarks/v1","benchmarks":[{"name":"B"}]}`,
		`{"schema":"spice.benchmarks/v1","benchmarks":[
			{"name":"B","package":"./p","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"x"},
			{"name":"B","package":"./p","reference_ns_per_op":1,"maximum_ns_per_op":2,"maximum_bytes_per_op":1,"maximum_allocs_per_op":1,"rationale":"x"}
		]}`,
	}
	for _, document := range tests {
		root := t.TempDir()
		writeTestFile(
			t,
			root,
			"benchmarks/budgets.json",
			document,
		)
		if _, err := loadBenchmarkBudgets(root); err == nil {
			t.Errorf("invalid budget document was accepted: %s", document)
		}
	}
}
