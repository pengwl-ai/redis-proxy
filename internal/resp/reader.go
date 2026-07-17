package resp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ReadMessage(ctx context.Context, r *bufio.Reader) (Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch b {
	case '+':
		return readSimpleString(r, b)
	case '-':
		return readError(r, b)
	case ':':
		return readInteger(r, b)
	case '$':
		return readBulkString(r, b)
	case '*':
		return readArray(ctx, r, b)
	default:
		return nil, fmt.Errorf("resp: unknown type byte %q", b)
	}
}

// readLine reads up to and including '\n', appending the line to buf and
// returning the appended bytes. Unlike ReadString it does not allocate:
// the line is copied straight from the bufio internal buffer into buf.
// The returned slice is only valid until buf grows or is reset.
func readLine(r *bufio.Reader, buf *bytes.Buffer) ([]byte, error) {
	start := buf.Len()
	for {
		frag, err := r.ReadSlice('\n')
		buf.Write(frag)
		if err == nil {
			return buf.Bytes()[start:], nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

// commandName uppercases s without allocating for already-uppercase input.
func commandName(s []byte) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return strings.ToUpper(string(s))
		}
	}
	return string(s)
}

// ReadCommand reads one client command, appending its raw bytes to buf.
// The returned raw slice references buf's storage and is valid only until
// buf is reset; callers that retain raw across a reset must copy it.
func ReadCommand(ctx context.Context, r *bufio.Reader, buf *bytes.Buffer) (cmd string, raw []byte, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	start := buf.Len()

	b, err := r.ReadByte()
	if err != nil {
		return "", nil, err
	}
	buf.WriteByte(b)

	if b != '*' {
		// Inline command: space-separated arguments terminated by \r\n.
		if _, err := readLine(r, buf); err != nil {
			return "", nil, err
		}
		raw = buf.Bytes()[start:]
		parts := strings.Fields(strings.TrimRight(string(raw), "\r\n"))
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("resp: empty inline command")
		}
		return strings.ToUpper(parts[0]), raw, nil
	}

	countLine, err := readLine(r, buf)
	if err != nil {
		return "", nil, err
	}

	count, err := strconv.Atoi(string(bytes.TrimSuffix(countLine, crlf)))
	if err != nil {
		return "", nil, fmt.Errorf("resp: invalid array count: %q", countLine)
	}
	if count <= 0 {
		return "", nil, fmt.Errorf("resp: empty or null array in command")
	}

	for i := 0; i < count; i++ {
		tag, err := r.ReadByte()
		if err != nil {
			return "", nil, err
		}
		buf.WriteByte(tag)

		if tag != '$' {
			return "", nil, fmt.Errorf("resp: expected bulk string in command, got %q", tag)
		}

		lenLine, err := readLine(r, buf)
		if err != nil {
			return "", nil, err
		}

		length, err := strconv.Atoi(string(bytes.TrimSuffix(lenLine, crlf)))
		if err != nil {
			return "", nil, fmt.Errorf("resp: invalid bulk string length: %q", lenLine)
		}
		if length < 0 {
			return "", nil, fmt.Errorf("resp: null bulk string in command at element %d", i)
		}

		// Read the payload directly into buf's spare capacity to avoid an
		// intermediate buffer. The self-append in buf.Write is a no-op copy.
		buf.Grow(length + 2)
		payload := buf.AvailableBuffer()[:length+2]
		if _, err := io.ReadFull(r, payload); err != nil {
			return "", nil, err
		}
		buf.Write(payload)

		if i == 0 {
			cmd = commandName(payload[:length])
		} else if i == 1 && (cmd == "MEMORY" || cmd == "SCRIPT") {
			cmd = cmd + " " + commandName(payload[:length])
		}
	}

	return cmd, buf.Bytes()[start:], nil
}

var crlf = []byte("\r\n")

func readSimpleString(r *bufio.Reader, first byte) (*SimpleString, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	raw := []byte{first}
	raw = append(raw, []byte(line)...)
	value := line[:len(line)-2]
	return &SimpleString{Value: value, raw: raw}, nil
}

func readError(r *bufio.Reader, first byte) (*Error, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	raw := []byte{first}
	raw = append(raw, []byte(line)...)
	value := line[:len(line)-2]
	return &Error{Value: value, raw: raw}, nil
}

func readInteger(r *bufio.Reader, first byte) (*Integer, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	raw := []byte{first}
	raw = append(raw, []byte(line)...)
	value, err := strconv.ParseInt(line[:len(line)-2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("resp: invalid integer: %q", line)
	}
	return &Integer{Value: value, raw: raw}, nil
}

func readBulkString(r *bufio.Reader, first byte) (Message, error) {
	lenLine, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}

	length, err := strconv.Atoi(strings.TrimSuffix(lenLine, "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("resp: invalid bulk string length: %q", lenLine)
	}

	if length == -1 {
		raw := []byte{first}
		raw = append(raw, []byte(lenLine)...)
		return &NullBulkString{raw: raw}, nil
	}

	payload := make([]byte, length+2)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	raw := []byte{first}
	raw = append(raw, []byte(lenLine)...)
	raw = append(raw, payload...)

	return &BulkString{Value: string(payload[:length]), raw: raw}, nil
}

// ReadRawReply reads the raw bytes of a complete Redis reply without allocating Message structs.
func ReadRawReply(r *bufio.Reader) ([]byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch b {
	case '+', '-', ':':
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		raw := make([]byte, 1+len(line))
		raw[0] = b
		copy(raw[1:], line)
		return raw, nil
	case '$':
		return readBulkStringRaw(r, b)
	case '*':
		return readArrayRaw(r, b)
	default:
		return nil, fmt.Errorf("resp: unknown type byte %q", b)
	}
}

func readBulkStringRaw(r *bufio.Reader, first byte) ([]byte, error) {
	lenLine, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	length, err := strconv.Atoi(strings.TrimSuffix(lenLine, "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("resp: invalid bulk string length: %q", lenLine)
	}

	if length == -1 {
		raw := make([]byte, 1+len(lenLine))
		raw[0] = first
		copy(raw[1:], lenLine)
		return raw, nil
	}

	payload := make([]byte, length+2)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	raw := make([]byte, 0, 1+len(lenLine)+len(payload))
	raw = append(raw, first)
	raw = append(raw, []byte(lenLine)...)
	raw = append(raw, payload...)
	return raw, nil
}

func readArrayRaw(r *bufio.Reader, first byte) ([]byte, error) {
	countLine, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSuffix(countLine, "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("resp: invalid array count: %q", countLine)
	}

	raw := make([]byte, 0, 128)
	raw = append(raw, first)
	raw = append(raw, []byte(countLine)...)

	if count == -1 {
		return raw, nil
	}

	for i := 0; i < count; i++ {
		elem, err := ReadRawReply(r)
		if err != nil {
			return nil, err
		}
		raw = append(raw, elem...)
	}
	return raw, nil
}

func readArray(ctx context.Context, r *bufio.Reader, first byte) (*Array, error) {
	countLine, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}

	count, err := strconv.Atoi(strings.TrimSuffix(countLine, "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("resp: invalid array count: %q", countLine)
	}

	raw := []byte{first}
	raw = append(raw, []byte(countLine)...)

	if count == -1 {
		return &Array{raw: raw}, nil
	}

	items := make([]Message, count)
	for i := 0; i < count; i++ {
		item, err := ReadMessage(ctx, r)
		if err != nil {
			return nil, err
		}
		items[i] = item
		raw = append(raw, item.Bytes()...)
	}

	return &Array{Items: items, raw: raw}, nil
}
