package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	defaultMaxMessageBytes = 4 << 20
	maxHeaderLineBytes      = 8 << 10
)

type framingMode uint8

const (
	framingAuto framingMode = iota
	framingContentLength
	framingNewline
)

type transport struct {
	reader  *bufio.Reader
	writer  *bufio.Writer
	mode    framingMode
	maxBody int
}

func newTransport(in io.Reader, out io.Writer, maxBody int) *transport {
	if maxBody <= 0 {
		maxBody = defaultMaxMessageBytes
	}
	return &transport{
		reader:  bufio.NewReaderSize(in, 64<<10),
		writer:  bufio.NewWriterSize(out, 64<<10),
		mode:    framingAuto,
		maxBody: maxBody,
	}
}

func (t *transport) read() ([]byte, error) {
	if t.mode == framingAuto {
		first, err := t.reader.Peek(1)
		if err != nil {
			return nil, err
		}
		if first[0] == '{' || first[0] == '[' {
			t.mode = framingNewline
		} else {
			t.mode = framingContentLength
		}
	}
	if t.mode == framingNewline {
		return t.readNewline()
	}
	return t.readContentLength()
}

func (t *transport) readNewline() ([]byte, error) {
	line, err := t.reader.ReadBytes('\n')
	if len(line) > t.maxBody+1 {
		return nil, fmt.Errorf("MCP message exceeds %d bytes", t.maxBody)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return nil, io.EOF
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 {
		return nil, errors.New("empty MCP message")
	}
	return line, nil
}

func (t *transport) readContentLength() ([]byte, error) {
	contentLength := -1
	for {
		line, err := t.reader.ReadString('\n')
		if len(line) > maxHeaderLineBytes {
			return nil, fmt.Errorf("MCP header line exceeds %d bytes", maxHeaderLineBytes)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" && contentLength < 0 {
				return nil, io.EOF
			}
			return nil, io.ErrUnexpectedEOF
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed MCP header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, errors.New("duplicate Content-Length header")
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
		}
		if parsed > t.maxBody {
			return nil, fmt.Errorf("MCP message exceeds %d bytes", t.maxBody)
		}
		contentLength = parsed
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(t.reader, body); err != nil {
		return nil, fmt.Errorf("read MCP message body: %w", err)
	}
	return body, nil
}

func (t *transport) write(body []byte) error {
	if t.mode == framingAuto {
		return errors.New("cannot write before MCP framing is detected")
	}
	if len(body) > t.maxBody {
		return fmt.Errorf("MCP response exceeds %d bytes", t.maxBody)
	}
	if t.mode == framingNewline {
		if _, err := t.writer.Write(body); err != nil {
			return err
		}
		if err := t.writer.WriteByte('\n'); err != nil {
			return err
		}
		return t.writer.Flush()
	}
	if _, err := fmt.Fprintf(t.writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := t.writer.Write(body); err != nil {
		return err
	}
	return t.writer.Flush()
}
