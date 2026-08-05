package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spice-framework/spice/compiler/load"
)

func TestLSPCommandServesProtocolOnlyOnStdout(t *testing.T) {
	t.Parallel()
	var input bytes.Buffer
	writeTestLSPMessage(t, &input, `{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{}
	}`)
	writeTestLSPMessage(t, &input, `{
		"jsonrpc":"2.0",
		"id":2,
		"method":"shutdown"
	}`)
	writeTestLSPMessage(t, &input, `{
		"jsonrpc":"2.0",
		"method":"exit"
	}`)
	var stdout, stderr bytes.Buffer
	code := lspCommandContext(
		context.Background(),
		nil,
		&input,
		&stdout,
		&stderr,
		load.Options{},
		load.Load,
	)
	if code != 0 ||
		stderr.String() != "" ||
		bytes.Count(stdout.Bytes(), []byte("Content-Length:")) != 2 ||
		!bytes.Contains(stdout.Bytes(), []byte(`"completionProvider"`)) {
		t.Fatalf(
			"lsp: code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if bytes.Contains(stdout.Bytes(), []byte("Spice lsp")) {
		t.Fatalf("stdout contains non-protocol text: %q", stdout.String())
	}
}

func TestLSPCommandRejectsArguments(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := lspCommandContext(
		context.Background(),
		[]string{"--listen"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		load.Options{},
		load.Load,
	)
	if code != 2 ||
		stdout.String() != "" ||
		!strings.Contains(stderr.String(), "does not accept arguments") {
		t.Fatalf(
			"lsp invalid: code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

func writeTestLSPMessage(
	t *testing.T,
	output *bytes.Buffer,
	content string,
) {
	t.Helper()
	content = strings.TrimSpace(content)
	if _, err := fmt.Fprintf(
		output,
		"Content-Length: %d\r\n\r\n%s",
		len(content),
		content,
	); err != nil {
		t.Fatalf("Fprintf() error = %v", err)
	}
}
