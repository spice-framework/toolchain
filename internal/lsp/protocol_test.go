package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRPCReaderAndWriterUseBoundedContentLengthFrames(t *testing.T) {
	t.Parallel()
	request := frameJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	message, err := newRPCReader(bytes.NewReader(request)).read()
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if message.Method != "initialize" ||
		!message.request() ||
		string(message.ID) != "7" {
		t.Fatalf("read() = %+v", message)
	}

	var output bytes.Buffer
	writer := newRPCWriter(&output)
	if err := writer.response(message.ID, map[string]any{"ok": true}); err != nil {
		t.Fatalf("response() error = %v", err)
	}
	body := framedBody(t, &output)
	var response struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      int            `json:"id"`
		Result  map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("Unmarshal(response) error = %v", err)
	}
	if response.JSONRPC != "2.0" ||
		response.ID != 7 ||
		response.Result["ok"] != true {
		t.Fatalf("response = %+v", response)
	}
}

func TestRPCReaderRejectsMalformedAndOversizedFrames(t *testing.T) {
	t.Parallel()
	tests := [][]byte{
		[]byte("Other: value\r\n\r\n{}"),
		[]byte("Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}"),
		[]byte("Content-Length: nope\r\n\r\n{}"),
		[]byte(fmt.Sprintf(
			"Content-Length: %d\r\n\r\n",
			maximumMessageSize+1,
		)),
		[]byte("Content-Length: 1\r\n\r\n{"),
		[]byte("Content-Length: 2\r\n\r\n[]"),
		[]byte(
			"Content-Length: 33\r\n\r\n" +
				`{"jsonrpc":"1.0","method":"x"}`,
		),
	}
	for _, content := range tests {
		if _, err := newRPCReader(bytes.NewReader(content)).read(); err == nil {
			t.Errorf("read(%q) error = nil, want failure", content)
		}
	}
	longHeader := "X-" + strings.Repeat("a", maximumHeaderLine) + ": value\r\n"
	if _, err := newRPCReader(
		strings.NewReader(longHeader + "\r\n"),
	).read(); err == nil {
		t.Fatal("read(long header) error = nil, want failure")
	}
}

func FuzzRPCReader(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"exit"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 4_096 {
			body = body[:4_096]
		}
		frame := append(
			[]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))),
			body...,
		)
		message, err := newRPCReader(bytes.NewReader(frame)).read()
		if err != nil {
			return
		}
		if message.JSONRPC != "2.0" || message.Method == "" {
			t.Fatalf("read() accepted invalid message: %+v", message)
		}
	})
}

func frameJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return append(
		[]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(content))),
		content...,
	)
}

func framedBody(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	_, body, found := bytes.Cut(content, []byte("\r\n\r\n"))
	if !found {
		t.Fatalf("framed content has no header terminator: %q", content)
	}
	return body
}
