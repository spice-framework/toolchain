package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunReportsSuccess(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	code := run(func(ctx context.Context, root string, writer io.Writer) error {
		if ctx.Err() != nil || root != "." || writer != &output {
			t.Fatalf("acceptance invocation = (%v, %q, %T)", ctx.Err(), root, writer)
		}
		return nil
	}, &output, io.Discard)
	if code != 0 {
		t.Fatalf("run() = %d", code)
	}
}

func TestRunReportsFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("acceptance failed")
	var stderr bytes.Buffer
	code := run(func(context.Context, string, io.Writer) error {
		return sentinel
	}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("run() = %d", code)
	}
	if !strings.Contains(stderr.String(), sentinel.Error()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
