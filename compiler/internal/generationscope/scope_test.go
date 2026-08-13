package generationscope

import (
	"context"
	"slices"
	"testing"
)

type testCarrier struct {
	Carrier
}

func TestScopeOwnsSortedDeduplicatedModuleIdentities(t *testing.T) {
	input := []string{"example.com/z", "example.com/a", "example.com/z"}
	scope := New(input)
	input[0] = "example.com/mutated-input"
	want := []string{"example.com/a", "example.com/z"}
	first := scope.ModuleIdentities()
	if !slices.Equal(first, want) {
		t.Fatalf("ModuleIdentities() = %v, want %v", first, want)
	}
	first[0] = "example.com/mutated-output"
	if second := scope.ModuleIdentities(); !slices.Equal(second, want) {
		t.Fatalf("ModuleIdentities() returned aliased storage: %v", second)
	}
}

func TestContextCarrierOwnsScopeWithoutAffectingCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	scope := New([]string{"example.com/z", "example.com/a"})
	ctx := WithContext(parent, scope)
	carried := testCarrier{Carrier: CarrierFromContext(ctx)}

	first := FromCarrier(&carried).ModuleIdentities()
	if want := []string{"example.com/a", "example.com/z"}; !slices.Equal(first, want) {
		t.Fatalf("carried identities = %v, want %v", first, want)
	}
	first[0] = "example.com/mutated-output"
	if second := FromCarrier(&carried).ModuleIdentities(); second[0] != "example.com/a" {
		t.Fatalf("carrier returned aliased scope: %v", second)
	}

	cancel()
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("context error = %v, want %v", err, context.Canceled)
	}
	if identities := FromCarrier(struct{}{}).ModuleIdentities(); identities != nil {
		t.Fatalf("foreign carrier identities = %v, want nil", identities)
	}
}
