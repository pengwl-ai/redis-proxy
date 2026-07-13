package proxy

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/config"
	"github.com/example/redis-proxy/internal/resp"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func setupPool(t *testing.T, mr *miniredis.Miniredis) (*backend.Pool, func()) {
	t.Helper()
	logger := testLogger()
	entries := []config.BackendEntry{
		{Name: "primary", Addr: mr.Addr(), Role: "primary", PoolSize: 4, MaxPoolSize: 10},
	}
	pool, err := backend.NewPool(context.Background(), entries, logger.With("component", "pool"))
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	return pool, func() { pool.Close() }
}

// startServer creates a running proxy and returns it, its address, and a stop function.
func startServer(t *testing.T, pool *backend.Pool) (*Server, string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer(config.ProxyConfig{Listen: addr}, pool, testLogger())
	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	return srv, addr, func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}
}

func dialProxy(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, bufio.NewReaderSize(conn, 64*1024)
}

func readReply(t *testing.T, conn net.Conn, reader *bufio.Reader) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msg, err := resp.ReadMessage(context.Background(), reader)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return string(msg.Bytes())
}

func TestProxyPING(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)
	conn.Write([]byte("*1\r\n$4\r\nPING\r\n"))
	reply := readReply(t, conn, reader)
	if !strings.Contains(reply, "PONG") {
		t.Errorf("expected PONG, got %q", reply)
	}
}

func TestProxySETGET(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"))
	reply := readReply(t, conn, reader)
	if reply != "+OK\r\n" {
		t.Errorf("SET: expected +OK\\r\\n, got %q", reply)
	}

	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	reply = readReply(t, conn, reader)
	if reply != "$5\r\nvalue\r\n" {
		t.Errorf("GET: expected $5\\r\\nvalue\\r\\n, got %q", reply)
	}
}

func TestProxyDEL(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$4\r\ndelk\r\n$5\r\nhello\r\n"))
	readReply(t, conn, reader)

	conn.Write([]byte("*2\r\n$3\r\nDEL\r\n$4\r\ndelk\r\n"))
	reply := readReply(t, conn, reader)
	if reply != ":1\r\n" {
		t.Errorf("DEL: expected :1\\r\\n, got %q", reply)
	}
}

func TestProxyGETNonExistent(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$9\r\nnonexistk\r\n"))
	reply := readReply(t, conn, reader)
	if reply != "$-1\r\n" {
		t.Errorf("expected $-1\\r\\n (null), got %q", reply)
	}
}

func TestProxyNoPrimary(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	pool.Close()

	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	reply := readReply(t, conn, reader)
	if !strings.Contains(reply, "no primary backend") {
		t.Errorf("expected 'no primary backend' error, got %q", reply)
	}
}

func TestProxyUnsupportedCommand(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	conn.Write([]byte("*1\r\n$5\r\nHELLO\r\n"))
	reply := readReply(t, conn, reader)
	if !strings.HasPrefix(reply, "-ERR unsupported command") {
		t.Errorf("expected unsupported command error, got %q", reply)
	}
}

func TestProxyPipeline(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	for i := 0; i < 5; i++ {
		conn.Write([]byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"))
	}
	for i := 0; i < 5; i++ {
		conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	}

	for i := 0; i < 5; i++ {
		reply := readReply(t, conn, reader)
		if reply != "+OK\r\n" {
			t.Errorf("pipeline SET[%d]: expected +OK\\r\\n, got %q", i, reply)
		}
	}
	for i := 0; i < 5; i++ {
		reply := readReply(t, conn, reader)
		if reply != "$5\r\nvalue\r\n" {
			t.Errorf("pipeline GET[%d]: expected $5\\r\\nvalue\\r\\n, got %q", i, reply)
		}
	}
}

func TestProxyTransaction(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)

	conn.Write([]byte("*1\r\n$5\r\nMULTI\r\n"))
	reply := readReply(t, conn, reader)
	if reply != "+OK\r\n" {
		t.Errorf("MULTI: expected +OK\\r\\n, got %q", reply)
	}

	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"))
	reply = readReply(t, conn, reader)
	if reply != "+QUEUED\r\n" {
		t.Errorf("SET in transaction: expected +QUEUED\\r\\n, got %q", reply)
	}

	conn.Write([]byte("*1\r\n$4\r\nEXEC\r\n"))
	reply = readReply(t, conn, reader)
	if !strings.Contains(reply, "OK") {
		t.Errorf("EXEC: expected array with OK, got %q", reply)
	}
}

func TestProxyBatchForward(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()
	_, addr, stop := startServer(t, pool)
	defer stop()

	conn, reader := dialProxy(t, addr)
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	var buf []byte
	for i := 0; i < 20; i++ {
		buf = append(buf, []byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n")...)
	}
	conn.Write(buf)

	for i := 0; i < 20; i++ {
		msg, err := resp.ReadMessage(context.Background(), reader)
		if err != nil {
			t.Fatalf("read reply %d: %v", i, err)
		}
		if string(msg.Bytes()) != "+OK\r\n" {
			t.Errorf("batch SET[%d]: expected +OK\\r\\n, got %q", i, string(msg.Bytes()))
		}
	}
}

func TestProxyGracefulShutdown(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	pool, poolCleanup := setupPool(t, mr)
	defer poolCleanup()

	ctx, cancel := context.WithCancel(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := NewServer(config.ProxyConfig{Listen: addr}, pool, testLogger())
	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, _ := dialProxy(t, addr)

	cancel()
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	conn.Close()
}
