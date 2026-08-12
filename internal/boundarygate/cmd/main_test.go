package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimeoutForMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
		want time.Duration
	}{
		{name: "verify", mode: "verify", want: 30 * time.Minute},
		{name: "verify release", mode: "verify-release", want: 30 * time.Minute},
		{name: "fast", mode: "fast", want: 20 * time.Minute},
		{name: "check", mode: "check", want: 20 * time.Minute},
		{name: "benchmark", mode: "benchmark", want: 20 * time.Minute},
		{name: "tools bootstrap", mode: "tools-bootstrap", want: 20 * time.Minute},
		{name: "release artifacts", mode: "release-artifacts", want: 20 * time.Minute},
		{name: "empty", mode: "", want: 20 * time.Minute},
		{name: "unknown", mode: "unsupported", want: 20 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := timeoutForMode(test.mode); got != test.want {
				t.Fatalf("timeoutForMode(%q) = %s, want %s", test.mode, got, test.want)
			}
		})
	}
}

func TestVerificationContextFailsClosed(t *testing.T) {
	t.Parallel()

	minimumDeadline := time.Now().Add(fullVerificationTimeout)
	ctx, cancel := verificationContext(context.Background(), "verify")
	maximumDeadline := time.Now().Add(fullVerificationTimeout)
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(minimumDeadline) || deadline.After(maximumDeadline) {
		cancel()
		t.Fatalf("verification context deadline = %s, want between %s and %s", deadline, minimumDeadline, maximumDeadline)
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("canceled verification context error = %v, want context.Canceled", ctx.Err())
	}

	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	ctx, cancel = verificationContext(parent, "verify-release")
	defer cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("parent-canceled verification context error = %v, want context.Canceled", ctx.Err())
	}
}
