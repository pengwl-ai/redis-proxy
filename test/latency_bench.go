package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

var (
	setCmd = []byte("*3\r\n$3\r\nSET\r\n$4\r\nkey1\r\n$6\r\nvalue1\r\n")
	getCmd = []byte("*2\r\n$3\r\nGET\r\n$4\r\nkey1\r\n")
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "redis server address")
	flag.Parse()

	conn, err := net.DialTimeout("tcp", *addr, 3*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	const total = 10_000

	start := time.Now()
	for i := range total {
		var cmd []byte
		if i%2 == 0 {
			cmd = setCmd
		} else {
			cmd = getCmd
		}

		if _, err := conn.Write(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "write error at %d: %v\n", i, err)
			os.Exit(1)
		}
		if err := readReply(reader); err != nil {
			fmt.Fprintf(os.Stderr, "read error at %d: %v\n", i, err)
			os.Exit(1)
		}
	}
	elapsed := time.Since(start)

	fmt.Printf("target:       %s\n", *addr)
	fmt.Printf("requests:     %d\n", total)
	fmt.Printf("elapsed:      %v\n", elapsed)
	fmt.Printf("qps:          %.0f\n", float64(total)/elapsed.Seconds())
	fmt.Printf("avg latency:  %.3fms\n", float64(elapsed.Microseconds())/float64(total)/1000)
}

func readReply(r *bufio.Reader) error {
	b, err := r.ReadByte()
	if err != nil {
		return err
	}
	switch b {
	case '+', '-', ':':
		_, err := r.ReadBytes('\n')
		return err
	case '$':
		line, err := r.ReadBytes('\n')
		if err != nil {
			return err
		}
		// line is like "-1\r\n" for null, or "6\r\n" for length
		if len(line) < 3 {
			return io.ErrUnexpectedEOF
		}
		n, err := strconv.Atoi(string(line[:len(line)-2]))
		if err != nil {
			return err
		}
		if n < 0 {
			return nil // null bulk string
		}
		// read n bytes + trailing \r\n
		if _, err := r.Discard(n + 2); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unexpected response byte: %c", b)
	}
}
