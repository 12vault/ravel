package mcp

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestContentLengthTransportReadsSplitUTF8FramesAndMirrorsFraming(t *testing.T) {
	first := []byte(`{"jsonrpc":"2.0","id":"α","method":"ping"}`)
	second := []byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	input := append(contentLengthFrame(first), contentLengthFrame(second)...)
	reader := &oneByteReader{data: input}
	var output bytes.Buffer
	transport := newTransport(reader, &output, 1<<20)

	gotFirst, err := transport.read()
	if err != nil || !bytes.Equal(gotFirst, first) {
		t.Fatalf("first frame = %q, %v", gotFirst, err)
	}
	gotSecond, err := transport.read()
	if err != nil || !bytes.Equal(gotSecond, second) {
		t.Fatalf("second frame = %q, %v", gotSecond, err)
	}
	response := []byte(`{"jsonrpc":"2.0","id":"α","result":{}}`)
	if err := transport.write(response); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(response))), response...)
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("framed response = %q, want %q", output.Bytes(), want)
	}
}

func TestNewlineTransportIgnoresBlankLinesAndMirrorsFraming(t *testing.T) {
	message := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	var output bytes.Buffer
	transport := newTransport(strings.NewReader("\r\n\n"+string(message)+"\r\n"), &output, 1<<20)
	got, err := transport.read()
	if err != nil || !bytes.Equal(got, message) {
		t.Fatalf("message = %q, %v", got, err)
	}
	if err := transport.write(message); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), string(message)+"\n"; got != want {
		t.Fatalf("newline response = %q, want %q", got, want)
	}
}

func TestContentLengthTransportRejectsInvalidFrames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
	}{
		{name: "missing", input: "Content-Type: application/json\r\n\r\n{}", max: 100},
		{name: "duplicate", input: "Content-Length: 2\r\ncontent-length: 2\r\n\r\n{}", max: 100},
		{name: "negative", input: "Content-Length: -1\r\n\r\n", max: 100},
		{name: "not integer", input: "Content-Length: nope\r\n\r\n", max: 100},
		{name: "oversized", input: "Content-Length: 101\r\n\r\n", max: 100},
		{name: "short body", input: "Content-Length: 4\r\n\r\n{}", max: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := newTransport(strings.NewReader(test.input), io.Discard, test.max)
			if _, err := transport.read(); err == nil {
				t.Fatal("read() error = nil")
			}
		})
	}
}

func contentLengthFrame(body []byte) []byte {
	header := fmt.Sprintf("Content-Length: %d\r\nContent-Type: application/json\r\n\r\n", len(body))
	return append([]byte(header), body...)
}

type oneByteReader struct {
	data []byte
}

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	buffer[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
