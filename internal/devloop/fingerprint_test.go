package devloop

import (
	"testing"
)

var fingerprintBenchmarkResult [32]byte

func TestStructuralFingerprintIgnoresFunctionBodies(t *testing.T) {
	t.Parallel()

	before := []byte(`package app

// @Service
func NewService(name string) string {
	return "before: " + name
}
`)
	after := []byte(`package app

// @Service
func NewService(name string) string {
	return "after: " + name
}
`)
	beforeHash, err := StructuralFingerprint("app.go", before)
	if err != nil {
		t.Fatalf("StructuralFingerprint(before) error = %v", err)
	}
	afterHash, err := StructuralFingerprint("app.go", after)
	if err != nil {
		t.Fatalf("StructuralFingerprint(after) error = %v", err)
	}
	if beforeHash != afterHash {
		t.Fatal("body-only edit changed the structural fingerprint")
	}
}

func TestStructuralFingerprintIncludesCompilerInputs(t *testing.T) {
	t.Parallel()

	base := []byte(`package app

// @Service
func NewService(name string) string {
	return name
}
`)
	tests := []struct {
		name    string
		content []byte
	}{
		{
			name: "annotation",
			content: []byte(`package app

// @Bean
func NewService(name string) string { return name }
`),
		},
		{
			name: "signature",
			content: []byte(`package app

// @Service
func NewService(name []byte) string { return string(name) }
`),
		},
		{
			name: "import",
			content: []byte(`package app

import "fmt"

// @Service
func NewService(name string) string { return fmt.Sprint(name) }
`),
		},
	}
	baseHash, err := StructuralFingerprint("app.go", base)
	if err != nil {
		t.Fatalf("StructuralFingerprint(base) error = %v", err)
	}
	for _, test := range tests {
		hash, err := StructuralFingerprint("app.go", test.content)
		if err != nil {
			t.Fatalf("StructuralFingerprint(%s) error = %v", test.name, err)
		}
		if hash == baseHash {
			t.Errorf("%s edit did not change the structural fingerprint", test.name)
		}
	}
}

func TestStructuralFingerprintRejectsInvalidGo(t *testing.T) {
	t.Parallel()

	if _, err := StructuralFingerprint("invalid.go", []byte("package")); err == nil {
		t.Fatal("StructuralFingerprint() accepted invalid Go")
	}
}

func BenchmarkStructuralFingerprint(b *testing.B) {
	source := []byte(`package app

import "context"

// @Service
func NewService(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "hello " + name, nil
}
`)
	b.ReportAllocs()
	for b.Loop() {
		result, err := StructuralFingerprint("service.go", source)
		if err != nil {
			b.Fatal(err)
		}
		fingerprintBenchmarkResult = result
	}
}
