package boundarygate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const benchmarkSchema = "spice.benchmarks/v1"

const benchmarkSampleCount = 5

var benchmarkNamePattern = regexp.MustCompile(`^Benchmark[A-Za-z0-9]+$`)

type benchmarkBudgetFile struct {
	Schema     string            `json:"schema"`
	Benchmarks []benchmarkBudget `json:"benchmarks"`
}

type benchmarkBudget struct {
	Name          string  `json:"name"`
	Package       string  `json:"package"`
	ReferenceNS   float64 `json:"reference_ns_per_op"`
	MaximumNS     float64 `json:"maximum_ns_per_op"`
	MaximumBytes  float64 `json:"maximum_bytes_per_op"`
	MaximumAllocs float64 `json:"maximum_allocs_per_op"`
	Rationale     string  `json:"rationale"`
}

type benchmarkMeasurement struct {
	NSPerOp     float64
	BytesPerOp  float64
	AllocsPerOp float64
}

func (gate verifier) performance(ctx context.Context) error {
	if err := gate.goVersion(ctx); err != nil {
		return err
	}
	return gate.benchmarkBudgets(ctx)
}

func (gate verifier) benchmarkBudgets(ctx context.Context) error {
	budgets, err := loadBenchmarkBudgets(gate.root)
	if err != nil {
		return err
	}
	for _, budget := range budgets {
		environment := map[string]string{
			"GOPROXY":     "off",
			"GOSUMDB":     "off",
			"GOTOOLCHAIN": "local",
			"GOWORK":      "off",
		}
		result, err := gate.capture(
			ctx,
			gate.root,
			environment,
			"go",
			"test",
			"-mod=vendor",
			"-run",
			"^$",
			"-bench",
			"^"+regexp.QuoteMeta(budget.Name)+"$",
			"-benchmem",
			"-benchtime",
			"250ms",
			"-count",
			strconv.Itoa(benchmarkSampleCount),
			budget.Package,
		)
		if err != nil {
			return fmt.Errorf("run benchmark %s: %w", budget.Name, err)
		}
		measurements, err := parseBenchmarkMeasurements(result, budget.Name)
		if err != nil {
			return err
		}
		median := medianMeasurement(measurements)
		if _, err := fmt.Fprintf(
			gate.output,
			"    %s median: %.0f ns/op, %.0f B/op, %.0f allocs/op\n",
			budget.Name,
			median.NSPerOp,
			median.BytesPerOp,
			median.AllocsPerOp,
		); err != nil {
			return fmt.Errorf("write benchmark result: %w", err)
		}
		if err := enforceBenchmarkBudget(budget, median); err != nil {
			return err
		}
	}
	return nil
}

func loadBenchmarkBudgets(root string) ([]benchmarkBudget, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open benchmark root: %w", err)
	}
	file, err := directory.Open("benchmarks/budgets.json")
	if err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var definition benchmarkBudgetFile
	decodeErr := decoder.Decode(&definition)
	if decodeErr == nil {
		var extra json.RawMessage
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			decodeErr = errors.New("document contains trailing JSON values")
		}
	}
	closeErr := errors.Join(file.Close(), directory.Close())
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return nil, fmt.Errorf("decode benchmark budgets: %w", err)
	}
	if definition.Schema != benchmarkSchema {
		return nil, fmt.Errorf(
			"decode benchmark budgets: schema %q, require %q",
			definition.Schema,
			benchmarkSchema,
		)
	}
	if len(definition.Benchmarks) == 0 {
		return nil, errors.New("decode benchmark budgets: no benchmarks")
	}
	seen := make(map[string]struct{}, len(definition.Benchmarks))
	for _, budget := range definition.Benchmarks {
		if !validBenchmarkBudget(budget) {
			return nil, fmt.Errorf(
				"decode benchmark budgets: invalid budget for %q",
				budget.Name,
			)
		}
		if _, found := seen[budget.Name]; found {
			return nil, fmt.Errorf(
				"decode benchmark budgets: duplicate %q",
				budget.Name,
			)
		}
		seen[budget.Name] = struct{}{}
	}
	return definition.Benchmarks, nil
}

func validBenchmarkBudget(budget benchmarkBudget) bool {
	return benchmarkNamePattern.MatchString(budget.Name) &&
		validBenchmarkPackage(budget.Package) &&
		budget.ReferenceNS > 0 &&
		budget.MaximumNS >= budget.ReferenceNS &&
		budget.MaximumBytes > 0 &&
		budget.MaximumAllocs > 0 &&
		strings.TrimSpace(budget.Rationale) != ""
}

func validBenchmarkPackage(packagePath string) bool {
	if !strings.HasPrefix(packagePath, "./") ||
		strings.ContainsAny(packagePath, " \t\r\n\\") {
		return false
	}
	relative := strings.TrimPrefix(packagePath, "./")
	return relative != "" &&
		relative != "." &&
		relative != ".." &&
		!strings.HasPrefix(relative, "../") &&
		path.Clean(relative) == relative
}

func parseBenchmarkMeasurements(
	result []byte,
	name string,
) ([]benchmarkMeasurement, error) {
	measurements := make([]benchmarkMeasurement, 0, benchmarkSampleCount)
	scanner := bufio.NewScanner(bytes.NewReader(result))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || !strings.HasPrefix(fields[0], name+"-") {
			continue
		}
		measurement, err := parseBenchmarkFields(fields)
		if err != nil {
			return nil, fmt.Errorf("parse benchmark %s: %w", name, err)
		}
		measurements = append(measurements, measurement)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse benchmark %s output: %w", name, err)
	}
	if len(measurements) != benchmarkSampleCount {
		return nil, fmt.Errorf(
			"parse benchmark %s: got %d measurement(s), require %d",
			name,
			len(measurements),
			benchmarkSampleCount,
		)
	}
	return measurements, nil
}

func parseBenchmarkFields(fields []string) (benchmarkMeasurement, error) {
	var measurement benchmarkMeasurement
	var foundNS, foundBytes, foundAllocs bool
	for index, field := range fields {
		switch field {
		case "ns/op":
			value, err := precedingMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.NSPerOp = value
			foundNS = true
		case "B/op":
			value, err := precedingMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.BytesPerOp = value
			foundBytes = true
		case "allocs/op":
			value, err := precedingMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.AllocsPerOp = value
			foundAllocs = true
		}
	}
	if !foundNS || !foundBytes || !foundAllocs ||
		measurement.NSPerOp <= 0 ||
		measurement.BytesPerOp < 0 ||
		measurement.AllocsPerOp < 0 {
		return benchmarkMeasurement{}, errors.New("required benchmark metrics are missing")
	}
	return measurement, nil
}

func precedingMetric(fields []string, index int) (float64, error) {
	if index == 0 {
		return 0, fmt.Errorf("metric %q has no value", fields[index])
	}
	value, err := strconv.ParseFloat(fields[index-1], 64)
	if err != nil {
		return 0, fmt.Errorf(
			"parse metric %q value %q: %w",
			fields[index],
			fields[index-1],
			err,
		)
	}
	return value, nil
}

func medianMeasurement(measurements []benchmarkMeasurement) benchmarkMeasurement {
	nsValues := make([]float64, len(measurements))
	byteValues := make([]float64, len(measurements))
	allocValues := make([]float64, len(measurements))
	for index, measurement := range measurements {
		nsValues[index] = measurement.NSPerOp
		byteValues[index] = measurement.BytesPerOp
		allocValues[index] = measurement.AllocsPerOp
	}
	slices.Sort(nsValues)
	slices.Sort(byteValues)
	slices.Sort(allocValues)
	middle := len(measurements) / 2
	return benchmarkMeasurement{
		NSPerOp:     nsValues[middle],
		BytesPerOp:  byteValues[middle],
		AllocsPerOp: allocValues[middle],
	}
}

func enforceBenchmarkBudget(
	budget benchmarkBudget,
	measurement benchmarkMeasurement,
) error {
	var violations strings.Builder
	if measurement.NSPerOp > budget.MaximumNS {
		writeBenchmarkViolation(&violations, "time", measurement.NSPerOp, budget.MaximumNS, "ns/op")
	}
	if measurement.BytesPerOp > budget.MaximumBytes {
		writeBenchmarkViolation(&violations, "memory", measurement.BytesPerOp, budget.MaximumBytes, "B/op")
	}
	if measurement.AllocsPerOp > budget.MaximumAllocs {
		writeBenchmarkViolation(&violations, "allocations", measurement.AllocsPerOp, budget.MaximumAllocs, "allocs/op")
	}
	if violations.Len() != 0 {
		return fmt.Errorf(
			"benchmark %s exceeded its release budget:%s improve the implementation or record reviewed evidence and rationale before changing benchmarks/budgets.json",
			budget.Name,
			violations.String(),
		)
	}
	return nil
}

func writeBenchmarkViolation(
	target *strings.Builder,
	label string,
	actual float64,
	maximum float64,
	unit string,
) {
	target.WriteByte(' ')
	target.WriteString(label)
	target.WriteByte(' ')
	target.WriteString(strconv.FormatFloat(actual, 'f', 0, 64))
	target.WriteString(" > ")
	target.WriteString(strconv.FormatFloat(maximum, 'f', 0, 64))
	target.WriteByte(' ')
	target.WriteString(unit)
	target.WriteByte(';')
}
