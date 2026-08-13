// Package generationscope carries compiler-validated identity-only generation
// inputs across compiler package boundaries without exposing them publicly.
package generationscope

import (
	"context"
	"slices"
	"sort"
)

type contextKey struct{}

// Carrier privately transports one immutable scope across compiler package
// boundaries. Its unexported method keeps the carrier out of public APIs.
type Carrier struct {
	scope Scope
}

type carrier interface {
	compilerGenerationScope() Scope
}

// Scope is the immutable identity-only context for one render operation.
type Scope struct {
	moduleIdentities []string
}

// New returns a scope with stable, deduplicated module identities.
func New(moduleIdentities []string) Scope {
	values := slices.Clone(moduleIdentities)
	sort.Strings(values)
	values = slices.Compact(values)
	return Scope{moduleIdentities: values}
}

// ModuleIdentities returns a defensive copy of the validated identity set.
func (scope Scope) ModuleIdentities() []string {
	return slices.Clone(scope.moduleIdentities)
}

// WithContext returns a child context carrying scope for one compiler load.
func WithContext(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, contextKey{}, scope)
}

// CarrierFromContext returns an immutable carrier for the context's scope.
func CarrierFromContext(ctx context.Context) Carrier {
	if ctx == nil {
		return Carrier{}
	}
	scope, found := ctx.Value(contextKey{}).(Scope)
	if !found {
		return Carrier{}
	}
	return Carrier{scope: New(scope.ModuleIdentities())}
}

// FromCarrier returns the immutable scope privately embedded by a compiler
// carrier. Values outside the compiler-owned carrier contract return zero.
func FromCarrier(value any) Scope {
	item, found := value.(carrier)
	if !found {
		return Scope{}
	}
	return New(item.compilerGenerationScope().ModuleIdentities())
}

func (carrier Carrier) compilerGenerationScope() Scope {
	return New(carrier.scope.ModuleIdentities())
}
