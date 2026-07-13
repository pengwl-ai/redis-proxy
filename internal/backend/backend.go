package backend

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/redis-proxy/internal/resp"
)

var ErrNotConnected = errors.New("backend: not connected")

type Role string

const (
	RolePrimary Role = "primary"
	RoleStandby Role = "standby"
)

type Backend struct {
	Name string
	Addr string
	Role Role

	mu            sync.Mutex
	conn          net.Conn
	reader        *bufio.Reader
	onDisconnect  func()
	reconnecting  atomic.Bool
}

func NewBackend(name, addr string, role Role) *Backend {
	return &Backend{
		Name: name,
		Addr: addr,
		Role: role,
	}
}

func (b *Backend) SetOnDisconnect(fn func()) {
	b.onDisconnect = fn
}

func (b *Backend) tryReconnect() {
	if b.onDisconnect == nil {
		return
	}
	if b.reconnecting.CompareAndSwap(false, true) {
		go func() {
			b.onDisconnect()
			b.reconnecting.Store(false)
		}()
	}
}

func (b *Backend) Connect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn != nil {
		b.conn.Close()
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", b.Addr)
	if err != nil {
		return fmt.Errorf("connect %s (%s): %w", b.Name, b.Addr, err)
	}

	b.conn = conn
	b.reader = bufio.NewReaderSize(conn, 64*1024)
	return nil
}

// Forward sends raw command bytes to Redis and returns the raw reply bytes.
// Thread-safe via internal mutex.
func (b *Backend) Forward(ctx context.Context, raw []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn == nil {
		return nil, ErrNotConnected
	}

	deadline, ok := ctx.Deadline()
	if ok {
		b.conn.SetWriteDeadline(deadline)
		b.conn.SetReadDeadline(deadline)
	} else {
		b.conn.SetWriteDeadline(time.Time{})
		b.conn.SetReadDeadline(time.Time{})
	}

	if _, err := b.conn.Write(raw); err != nil {
		b.closeLocked()
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s write: %w", b.Name, err)
	}

	msg, err := resp.ReadMessage(ctx, b.reader)
	if err != nil {
		b.closeLocked()
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s read: %w", b.Name, err)
	}

	return msg.Bytes(), nil
}

func (b *Backend) IsConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil
}

func (b *Backend) SetRole(role Role) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Role = role
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeLocked()
}

func (b *Backend) closeLocked() error {
	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		b.reader = nil
		return err
	}
	return nil
}

// Reconnect attempts to reconnect with exponential backoff.
// Blocks until connected or ctx is cancelled.
func (b *Backend) Reconnect(ctx context.Context, logger *slog.Logger) error {
	backoff := 100 * time.Millisecond
	maxBackoff := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := b.Connect(ctx); err != nil {
			logger.Warn("reconnect failed", "name", b.Name, "addr", b.Addr, "err", err, "retry_in", backoff)
		} else {
			logger.Info("backend reconnected", "name", b.Name)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

var _ io.Closer = (*Backend)(nil)
