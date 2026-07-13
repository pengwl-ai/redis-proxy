package resp

import (
	"fmt"
	"io"
)

func WriteMessage(w io.Writer, msg Message) (int, error) {
	switch m := msg.(type) {
	case *SimpleString:
		return fmt.Fprintf(w, "+%s\r\n", m.Value)
	case *Error:
		return fmt.Fprintf(w, "-%s\r\n", m.Value)
	case *Integer:
		return fmt.Fprintf(w, ":%d\r\n", m.Value)
	case *BulkString:
		return fmt.Fprintf(w, "$%d\r\n%s\r\n", len(m.Value), m.Value)
	case *NullBulkString:
		return w.Write([]byte("$-1\r\n"))
	case *Array:
		n, err := fmt.Fprintf(w, "*%d\r\n", len(m.Items))
		if err != nil {
			return n, err
		}
		for _, item := range m.Items {
			nn, err := WriteMessage(w, item)
			n += nn
			if err != nil {
				return n, err
			}
		}
		return n, nil
	default:
		return 0, fmt.Errorf("resp: unknown message type %T", msg)
	}
}
