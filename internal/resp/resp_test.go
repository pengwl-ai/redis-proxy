package resp

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func TestReadSimpleString(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("+OK\r\n"))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	ss, ok := msg.(*SimpleString)
	if !ok {
		t.Fatalf("expected SimpleString, got %T", msg)
	}
	if ss.Value != "OK" {
		t.Errorf("expected OK, got %q", ss.Value)
	}
	if string(ss.Bytes()) != "+OK\r\n" {
		t.Errorf("expected +OK\\r\\n, got %q", string(ss.Bytes()))
	}
}

func TestReadError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("-ERR unknown command\r\n"))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := msg.(*Error)
	if !ok {
		t.Fatalf("expected Error, got %T", msg)
	}
	if e.Value != "ERR unknown command" {
		t.Errorf("expected 'ERR unknown command', got %q", e.Value)
	}
	if string(e.Bytes()) != "-ERR unknown command\r\n" {
		t.Errorf("expected raw bytes, got %q", string(e.Bytes()))
	}
}

func TestReadInteger(t *testing.T) {
	tests := []struct {
		input string
		value int64
	}{
		{":0\r\n", 0},
		{":1000\r\n", 1000},
		{":-1\r\n", -1},
	}
	for _, tc := range tests {
		r := bufio.NewReader(strings.NewReader(tc.input))
		msg, err := ReadMessage(context.Background(), r)
		if err != nil {
			t.Fatalf("%q: %v", tc.input, err)
		}
		integer, ok := msg.(*Integer)
		if !ok {
			t.Fatalf("%q: expected Integer, got %T", tc.input, msg)
		}
		if integer.Value != tc.value {
			t.Errorf("%q: expected %d, got %d", tc.input, tc.value, integer.Value)
		}
		if string(integer.Bytes()) != tc.input {
			t.Errorf("%q: raw bytes mismatch: %q", tc.input, string(integer.Bytes()))
		}
	}
}

func TestReadBulkString(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$5\r\nhello\r\n"))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	bs, ok := msg.(*BulkString)
	if !ok {
		t.Fatalf("expected BulkString, got %T", msg)
	}
	if bs.Value != "hello" {
		t.Errorf("expected 'hello', got %q", bs.Value)
	}
	if string(bs.Bytes()) != "$5\r\nhello\r\n" {
		t.Errorf("expected raw bytes, got %q", string(bs.Bytes()))
	}
}

func TestReadEmptyBulkString(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$0\r\n\r\n"))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	bs, ok := msg.(*BulkString)
	if !ok {
		t.Fatalf("expected BulkString, got %T", msg)
	}
	if bs.Value != "" {
		t.Errorf("expected empty string, got %q", bs.Value)
	}
}

func TestReadNullBulkString(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$-1\r\n"))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := msg.(*NullBulkString)
	if !ok {
		t.Fatalf("expected NullBulkString, got %T", msg)
	}
	if string(msg.Bytes()) != "$-1\r\n" {
		t.Errorf("expected raw bytes, got %q", string(msg.Bytes()))
	}
}

func TestReadArray(t *testing.T) {
	input := "*2\r\n$5\r\nhello\r\n$5\r\nworld\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := msg.(*Array)
	if !ok {
		t.Fatalf("expected Array, got %T", msg)
	}
	if len(arr.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(arr.Items))
	}
	if string(arr.Bytes()) != input {
		t.Errorf("raw bytes mismatch: got %q", string(arr.Bytes()))
	}

	bs0, ok := arr.Items[0].(*BulkString)
	if !ok {
		t.Fatalf("expected BulkString, got %T", arr.Items[0])
	}
	if bs0.Value != "hello" {
		t.Errorf("expected 'hello', got %q", bs0.Value)
	}
}

func TestReadNestedArray(t *testing.T) {
	input := "*2\r\n*2\r\n:1\r\n:2\r\n*1\r\n:3\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := msg.(*Array)
	if !ok {
		t.Fatalf("expected Array, got %T", msg)
	}
	if len(arr.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(arr.Items))
	}
	inner1, ok := arr.Items[0].(*Array)
	if !ok {
		t.Fatalf("expected nested Array, got %T", arr.Items[0])
	}
	if len(inner1.Items) != 2 {
		t.Fatalf("expected 2 inner items, got %d", len(inner1.Items))
	}
}

func TestReadCommand(t *testing.T) {
	input := "*2\r\n$4\r\nLLEN\r\n$6\r\nmylist\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	cmd, raw, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "LLEN" {
		t.Errorf("expected LLEN, got %q", cmd)
	}
	if string(raw) != input {
		t.Errorf("raw bytes mismatch: got %q", string(raw))
	}
}

func TestReadCommandLowerCase(t *testing.T) {
	input := "*2\r\n$3\r\nget\r\n$3\r\nkey\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	cmd, _, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "GET" {
		t.Errorf("expected GET (uppercased), got %q", cmd)
	}
}

func TestReadCommandInline(t *testing.T) {
	// Inline PING (redis-benchmark PING_INLINE mode)
	r := bufio.NewReader(strings.NewReader("PING\r\n"))
	cmd, raw, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "PING" {
		t.Errorf("expected PING, got %q", cmd)
	}
	if string(raw) != "PING\r\n" {
		t.Errorf("expected 'PING\\r\\n', got %q", string(raw))
	}
}

func TestReadCommandInlineMultiWord(t *testing.T) {
	// Inline SET with arguments
	r := bufio.NewReader(strings.NewReader("SET key value\r\n"))
	cmd, raw, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "SET" {
		t.Errorf("expected SET, got %q", cmd)
	}
	if string(raw) != "SET key value\r\n" {
		t.Errorf("expected 'SET key value\\r\\n', got %q", string(raw))
	}
}

func TestReadCommandMemoryUsage(t *testing.T) {
	input := "*3\r\n$6\r\nMEMORY\r\n$5\r\nUSAGE\r\n$3\r\nkey\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	cmd, raw, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "MEMORY USAGE" {
		t.Errorf("expected 'MEMORY USAGE', got %q", cmd)
	}
	if string(raw) != input {
		t.Errorf("raw bytes mismatch: got %q", string(raw))
	}
}

func TestReadCommandScriptLoad(t *testing.T) {
	input := "*3\r\n$6\r\nSCRIPT\r\n$4\r\nLOAD\r\n$10\r\nreturn 123\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	cmd, _, err := ReadCommand(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "SCRIPT LOAD" {
		t.Errorf("expected 'SCRIPT LOAD', got %q", cmd)
	}
}

func TestWriteSimpleString(t *testing.T) {
	var buf strings.Builder
	msg := &SimpleString{Value: "OK"}
	n, err := WriteMessage(&buf, msg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes, got %d", n)
	}
	if buf.String() != "+OK\r\n" {
		t.Errorf("expected +OK\\r\\n, got %q", buf.String())
	}
}

func TestWriteError(t *testing.T) {
	var buf strings.Builder
	msg := &Error{Value: "ERR something"}
	WriteMessage(&buf, msg)
	if buf.String() != "-ERR something\r\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteInteger(t *testing.T) {
	var buf strings.Builder
	msg := &Integer{Value: 42}
	WriteMessage(&buf, msg)
	if buf.String() != ":42\r\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteBulkString(t *testing.T) {
	var buf strings.Builder
	msg := &BulkString{Value: "hello"}
	WriteMessage(&buf, msg)
	if buf.String() != "$5\r\nhello\r\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteNullBulkString(t *testing.T) {
	var buf strings.Builder
	msg := &NullBulkString{}
	WriteMessage(&buf, msg)
	if buf.String() != "$-1\r\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteArray(t *testing.T) {
	var buf strings.Builder
	msg := &Array{
		Items: []Message{
			&BulkString{Value: "hello"},
			&BulkString{Value: "world"},
		},
	}
	WriteMessage(&buf, msg)
	if buf.String() != "*2\r\n$5\r\nhello\r\n$5\r\nworld\r\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestRoundTrip(t *testing.T) {
	// Read a message, then write it, and verify bytes match original.
	input := "*2\r\n$5\r\nhello\r\n$5\r\nworld\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	msg, err := ReadMessage(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	_, err = WriteMessage(&buf, msg)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != input {
		t.Errorf("round-trip mismatch:\n  original: %q\n  written:  %q", input, buf.String())
	}
}

func TestMessageTypeTags(t *testing.T) {
	if (&SimpleString{}).TypeTag() != TypeSimpleString {
		t.Error("SimpleString TypeTag mismatch")
	}
	if (&Error{}).TypeTag() != TypeError {
		t.Error("Error TypeTag mismatch")
	}
	if (&Integer{}).TypeTag() != TypeInteger {
		t.Error("Integer TypeTag mismatch")
	}
	if (&BulkString{}).TypeTag() != TypeBulkString {
		t.Error("BulkString TypeTag mismatch")
	}
	if (&NullBulkString{}).TypeTag() != TypeBulkString {
		t.Error("NullBulkString TypeTag mismatch")
	}
	if (&Array{}).TypeTag() != TypeArray {
		t.Error("Array TypeTag mismatch")
	}
}
