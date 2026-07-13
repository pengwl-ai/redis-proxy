package backend

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestBackendConnectSuccess(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if !b.IsConnected() {
		t.Fatal("expected connected")
	}
	b.Close()
}

func TestBackendConnectFailure(t *testing.T) {
	b := NewBackend("test", "127.0.0.1:19999", RolePrimary)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := b.Connect(ctx); err == nil {
		t.Fatal("expected connection failure")
	}
	if b.IsConnected() {
		t.Error("should not be connected after failed connect")
	}
}

func TestBackendForward(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	raw := []byte("*1\r\n$4\r\nPING\r\n")
	reply, err := b.Forward(ctx, raw)
	if err != nil {
		t.Fatalf("forward failed: %v", err)
	}
	if string(reply) != "+PONG\r\n" {
		t.Errorf("expected +PONG\\r\\n, got %q", string(reply))
	}
}

func TestBackendForwardSETGET(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	setRaw := []byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n")
	reply, err := b.Forward(ctx, setRaw)
	if err != nil {
		t.Fatalf("SET forward failed: %v", err)
	}
	if string(reply) != "+OK\r\n" {
		t.Errorf("expected +OK\\r\\n, got %q", string(reply))
	}

	getRaw := []byte("*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n")
	reply, err = b.Forward(ctx, getRaw)
	if err != nil {
		t.Fatalf("GET forward failed: %v", err)
	}
	if string(reply) != "$5\r\nvalue\r\n" {
		t.Errorf("expected $5\\r\\nvalue\\r\\n, got %q", string(reply))
	}
}

func TestBackendForwardDEL(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	setRaw := []byte("*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n")
	b.Forward(ctx, setRaw)

	delRaw := []byte("*2\r\n$3\r\nDEL\r\n$3\r\nkey\r\n")
	reply, err := b.Forward(ctx, delRaw)
	if err != nil {
		t.Fatalf("DEL forward failed: %v", err)
	}
	if string(reply) != ":1\r\n" {
		t.Errorf("expected :1\\r\\n, got %q", string(reply))
	}
}

func TestBackendForwardWhenNotConnected(t *testing.T) {
	b := NewBackend("test", "127.0.0.1:6379", RolePrimary)
	raw := []byte("*1\r\n$4\r\nPING\r\n")
	_, err := b.Forward(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestBackendReconnect(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	mr.Close()

	// Use a short timeout; reconnect should fail since miniredis is dead
	shortCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := b.Reconnect(shortCtx, testLogger())
	if err == nil {
		t.Log("reconnect succeeded unexpectedly")
	} else if err != context.DeadlineExceeded {
		t.Logf("reconnect error (expected): %v", err)
	}
}

func TestBackendClose(t *testing.T) {
	mr := miniredis.RunT(t)
	b := NewBackend("test", mr.Addr(), RolePrimary)
	ctx := context.Background()

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.IsConnected() {
		t.Error("expected not connected after close")
	}
}

func TestBackendSetRole(t *testing.T) {
	b := NewBackend("test", "127.0.0.1:6379", RolePrimary)
	b.SetRole(RoleStandby)
	if b.Role != RoleStandby {
		t.Errorf("expected standby, got %s", b.Role)
	}
}
