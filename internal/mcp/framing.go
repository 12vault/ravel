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
	maxHeaderLineBytes     = 8 << 10
	maxHeaderBytes         = 64 << 10
	maxHeaderLines         = 100
	maxBlankLines          = 100
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
		blankBytes := 0
		for {
			first, err := t.reader.Peek(1)
			if err != nil {
				return nil, err
			}
			if first[0] == '\r' || first[0] == '\n' {
				_, _ = t.reader.ReadByte()
				blankBytes++
				if blankBytes > maxHeaderBytes {
					return nil, errors.New("too many blank bytes before MCP message")
				}
				continue
			}
			if first[0] == '{' || first[0] == '[' || first[0] == ' ' || first[0] == '\t' {
				t.mode = framingNewline
			} else {
				t.mode = framingContentLength
			}
			break
		}
	}
	if t.mode == framingNewline {
		return t.readNewline()
	}
	return t.readContentLength()
}

func (t *transport) readNewline() ([]byte, error) {
	for blankLines := 0; ; blankLines++ {
		if blankLines > maxBlankLines {
			return nil, errors.New("too many blank lines between MCP messages")
		}
		line, err := readBoundedLine(t.reader, t.maxBody+1)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read MCP newline message: %w", err)
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > t.maxBody {
			return nil, fmt.Errorf("MCP message exceeds %d bytes", t.maxBody)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}
		return line, nil
	}
}

func (t *transport) readContentLength() ([]byte, error) {
	contentLength := -1
	headerBytes := 0
	for lineNumber := 0; ; lineNumber++ {
		if lineNumber >= maxHeaderLines {
			return nil, fmt.Errorf("MCP headers exceed %d lines", maxHeaderLines)
		}
		lineBytes, err := readBoundedLine(t.reader, maxHeaderLineBytes)
		headerBytes += len(lineBytes)
		if headerBytes > maxHeaderBytes {
			return nil, fmt.Errorf("MCP headers exceed %d bytes", maxHeaderBytes)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(lineBytes) == 0 && contentLength < 0 {
				return nil, io.EOF
			}
			if !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("read MCP header: %w", err)
			}
			return nil, io.ErrUnexpectedEOF
		}
		line := string(lineBytes)
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

func readBoundedLine(reader *bufio.Reader, maximum int) ([]byte, error) {
	result := make([]byte, 0, min(maximum, 4<<10))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(result)+len(fragment) > maximum {
			return nil, fmt.Errorf("line exceeds %d bytes", maximum)
		}
		result = append(result, fragment...)
		switch {
		case err == nil:
			return result, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return result, err
		}
	}
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
