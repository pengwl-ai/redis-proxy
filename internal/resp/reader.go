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

func ReadCommand(ctx context.Context, r *bufio.Reader) (cmd string, raw []byte, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	b, err := r.ReadByte()
	if err != nil {
		return "", nil, err
	}
	if b != '*' {
		// Inline command: space-separated arguments terminated by \r\n.
		line, err := r.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		raw := append([]byte{b}, []byte(line)...)
		parts := strings.Fields(strings.TrimRight(string(raw), "\r\n"))
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("resp: empty inline command")
		}
		return strings.ToUpper(parts[0]), raw, nil
	}

	var buf bytes.Buffer
	buf.WriteByte(b)

	countLine, err := r.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	buf.WriteString(countLine)

	countStr := strings.TrimSuffix(countLine, "\r\n")
	count, err := strconv.Atoi(countStr)
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

		lenLine, err := r.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		buf.WriteString(lenLine)

		length, err := strconv.Atoi(strings.TrimSuffix(lenLine, "\r\n"))
		if err != nil {
			return "", nil, fmt.Errorf("resp: invalid bulk string length: %q", lenLine)
		}
		if length < 0 {
			return "", nil, fmt.Errorf("resp: null bulk string in command at element %d", i)
		}

		payload := make([]byte, length+2)
		if _, err := io.ReadFull(r, payload); err != nil {
			return "", nil, err
		}
		buf.Write(payload)

		if i == 0 {
			cmd = strings.ToUpper(string(payload[:length]))
		} else if i == 1 && (cmd == "MEMORY" || cmd == "SCRIPT") {
			cmd = cmd + " " + strings.ToUpper(string(payload[:length]))
		}
	}

	return cmd, buf.Bytes(), nil
}

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
