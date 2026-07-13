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

func setupProxy(t *testing.T, mr *miniredis.Miniredis) (*Server, *backend.Pool, string, func()) {
	t.Helper()

	logger := testLogger()

	entries := []config.BackendEntry{
		{Name: "primary", Addr: mr.Addr(), Role: "primary"},
	}

	pool, err := backend.NewPool(context.Background(), entries, logger.With("component", "pool"))
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	cfg := config.ProxyConfig{
		Listen:    "127.0.0.1:0",
		APIListen: "127.0.0.1:0",
	}

	server := NewServer(cfg, pool, logger.With("component", "proxy"))

	return server, pool, cfg.APIListen, func() {
		pool.Close()
	}
}

func dialProxy(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readReply(t *testing.T, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	msg, err := resp.ReadMessage(context.Background(), reader)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return string(msg.Bytes())
}

func TestProxyPING(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// Patch the config
	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond) // wait for server to start

	conn := dialProxy(t, addr)

	// PING is now supported — forwarded to backend and returns +PONG.
	conn.Write([]byte("*1\r\n$4\r\nPING\r\n"))
	reply := readReply(t, conn)
	if !strings.Contains(reply, "PONG") {
		t.Errorf("expected PONG, got %q", reply)
	}
}

func TestProxySETGET(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// SET key value
	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"))
	reply := readReply(t, conn)
	if reply != "+OK\r\n" {
		t.Errorf("SET: expected +OK\\r\\n, got %q", reply)
	}

	// GET key
	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	reply = readReply(t, conn)
	if reply != "$5\r\nvalue\r\n" {
		t.Errorf("GET: expected $5\\r\\nvalue\\r\\n, got %q", reply)
	}
}

func TestProxyDEL(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// SET key value
	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$4\r\ndelk\r\n$5\r\nhello\r\n"))
	readReply(t, conn)

	// DEL key
	conn.Write([]byte("*2\r\n$3\r\nDEL\r\n$4\r\ndelk\r\n"))
	reply := readReply(t, conn)
	if reply != ":1\r\n" {
		t.Errorf("DEL: expected :1\\r\\n, got %q", reply)
	}
}

func TestProxyGETNonExistent(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$9\r\nnonexistk\r\n"))
	reply := readReply(t, conn)
	if reply != "$-1\r\n" {
		t.Errorf("expected $-1\\r\\n (null), got %q", reply)
	}
}

func TestProxyNoPrimary(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, pool, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// Promote a nonexistent backend to clear the primary (this is a bit hacky, but
	// we verify the no-primary error message is returned).
	// Instead: close the pool, which removes the primary.
	pool.Close()

	conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	reply := readReply(t, conn)
	if !strings.Contains(reply, "no primary backend") {
		t.Errorf("expected 'no primary backend' error, got %q", reply)
	}
}

func TestProxyUnsupportedCommand(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// ZADD is not in supportedCommands
	conn.Write([]byte("*3\r\n$4\r\nZADD\r\n$3\r\nkey\r\n$1\r\n1\r\n"))
	reply := readReply(t, conn)
	if !strings.HasPrefix(reply, "-ERR unsupported command") {
		t.Errorf("expected unsupported command error, got %q", reply)
	}
}

func TestProxyPipeline(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// Pipeline: send 5 SETs + 5 GETs without waiting for replies
	for i := 0; i < 5; i++ {
		conn.Write([]byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"))
	}
	for i := 0; i < 5; i++ {
		conn.Write([]byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"))
	}

	// Read 10 replies in order
	for i := 0; i < 5; i++ {
		reply := readReply(t, conn)
		if reply != "+OK\r\n" {
			t.Errorf("pipeline SET[%d]: expected +OK\\r\\n, got %q", i, reply)
		}
	}
	for i := 0; i < 5; i++ {
		reply := readReply(t, conn)
		if reply != "$5\r\nvalue\r\n" {
			t.Errorf("pipeline GET[%d]: expected $5\\r\\nvalue\\r\\n, got %q", i, reply)
		}
	}
}

func TestProxyTransaction(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	// MULTI
	conn.Write([]byte("*1\r\n$5\r\nMULTI\r\n"))
	reply := readReply(t, conn)
	if reply != "+OK\r\n" {
		t.Errorf("MULTI: expected +OK\\r\\n, got %q", reply)
	}

	// SET k v
	conn.Write([]byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"))
	reply = readReply(t, conn)
	if reply != "+QUEUED\r\n" {
		t.Errorf("SET in transaction: expected +QUEUED\\r\\n, got %q", reply)
	}

	// EXEC
	conn.Write([]byte("*1\r\n$4\r\nEXEC\r\n"))
	reply = readReply(t, conn)
	if !strings.Contains(reply, "OK") {
		t.Errorf("EXEC: expected array with OK, got %q", reply)
	}
}

func TestProxyGracefulShutdown(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	server, _, _, cleanup := setupProxy(t, mr)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	server = NewServer(config.ProxyConfig{Listen: addr}, server.pool, testLogger())

	go server.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn := dialProxy(t, addr)

	cancel()
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	conn.Close()
}
