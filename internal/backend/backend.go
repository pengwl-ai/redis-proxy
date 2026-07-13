package backend

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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

type pooledConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

// PinnedConn is a connection pinned to a session for the duration of a transaction.
// The caller must call Unpin to return it to the pool.
type PinnedConn struct {
	pc *pooledConn
	b  *Backend
}

type Backend struct {
	Name string
	Addr string
	Role Role

	pool    chan *pooledConn
	sem     chan struct{}
	poolSize int
	maxPool  int

	activeConns atomic.Int32
	closed      atomic.Bool

	onDisconnect func()
	reconnecting atomic.Bool
}

func NewBackend(name, addr string, role Role) *Backend {
	b := &Backend{
		Name:     name,
		Addr:     addr,
		Role:     role,
		poolSize: 4,
		maxPool:  20,
	}
	b.closed.Store(true)
	return b
}

func (b *Backend) SetPoolConfig(poolSize, maxPool int) {
	if poolSize > 0 {
		b.poolSize = poolSize
	}
	if maxPool > 0 {
		b.maxPool = maxPool
	}
}

func (b *Backend) SetOnDisconnect(fn func()) {
	b.onDisconnect = fn
}

func (b *Backend) dial(ctx context.Context) (*pooledConn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", b.Addr)
	if err != nil {
		return nil, err
	}
	return &pooledConn{
		conn:   conn,
		reader: bufio.NewReaderSize(conn, 64*1024),
		writer: bufio.NewWriterSize(conn, 64*1024),
	}, nil
}

func (b *Backend) acquire(ctx context.Context) (*pooledConn, error) {
	if b.closed.Load() {
		return nil, ErrNotConnected
	}

	select {
	case pc := <-b.pool:
		return pc, nil
	default:
	}

	select {
	case b.sem <- struct{}{}:
		if b.closed.Load() {
			<-b.sem
			return nil, ErrNotConnected
		}
		pc, err := b.dial(ctx)
		if err != nil {
			<-b.sem
			return nil, err
		}
		b.activeConns.Add(1)
		return pc, nil
	default:
	}

	select {
	case pc := <-b.pool:
		return pc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Backend) release(pc *pooledConn) {
	if b.closed.Load() {
		pc.conn.Close()
		return
	}
	select {
	case b.pool <- pc:
	default:
		pc.conn.Close()
	}
}

func (b *Backend) removeConn(pc *pooledConn) {
	pc.conn.Close()
	<-b.sem
	b.activeConns.Add(-1)
	go b.refillConn()
}

func (b *Backend) refillConn() {
	if b.closed.Load() {
		return
	}

	select {
	case b.sem <- struct{}{}:
	default:
		return
	}

	if b.closed.Load() {
		<-b.sem
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pc, err := b.dial(ctx)
	if err != nil {
		<-b.sem
		return
	}

	b.activeConns.Add(1)

	select {
	case b.pool <- pc:
	default:
		pc.conn.Close()
		<-b.sem
	}
}

func (b *Backend) tryReconnect() {
	if b.activeConns.Load() > 0 {
		return
	}
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
	if b.pool == nil {
		b.pool = make(chan *pooledConn, b.maxPool)
		b.sem = make(chan struct{}, b.maxPool)
	}

	// Drain old connections (may be zombies after server restart).
	b.closed.Store(true)
	for {
		select {
		case pc := <-b.pool:
			pc.conn.Close()
			b.activeConns.Add(-1)
			<-b.sem
		default:
			goto drained
		}
	}
drained:
	b.closed.Store(false)

	for b.activeConns.Load() < int32(b.poolSize) {
		select {
		case b.sem <- struct{}{}:
		default:
			return nil
		}

		pc, err := b.dial(ctx)
		if err != nil {
			<-b.sem
			if b.activeConns.Load() > 0 {
				return nil
			}
			return fmt.Errorf("connect %s (%s): %w", b.Name, b.Addr, err)
		}

		b.activeConns.Add(1)
		b.pool <- pc
	}

	return nil
}

// Forward sends raw command bytes to Redis and returns the raw reply bytes.
func (b *Backend) Forward(ctx context.Context, raw []byte) ([]byte, error) {
	pc, err := b.acquire(ctx)
	if err != nil {
		return nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		pc.conn.SetWriteDeadline(deadline)
	}

	if _, err := pc.writer.Write(raw); err != nil {
		b.removeConn(pc)
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s write: %w", b.Name, err)
	}
	if err := pc.writer.Flush(); err != nil {
		b.removeConn(pc)
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s flush: %w", b.Name, err)
	}

	msg, err := resp.ReadMessage(ctx, pc.reader)
	if err != nil {
		b.removeConn(pc)
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s read: %w", b.Name, err)
	}

	b.release(pc)
	return msg.Bytes(), nil
}

// ForwardBatch sends multiple raw command bytes in pipeline and returns replies.
func (b *Backend) ForwardBatch(ctx context.Context, raws [][]byte) ([][]byte, error) {
	pc, err := b.acquire(ctx)
	if err != nil {
		return nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		pc.conn.SetWriteDeadline(deadline)
	}

	for _, raw := range raws {
		if _, err := pc.writer.Write(raw); err != nil {
			b.removeConn(pc)
			b.tryReconnect()
			return nil, fmt.Errorf("backend %s batch write: %w", b.Name, err)
		}
	}
	if err := pc.writer.Flush(); err != nil {
		b.removeConn(pc)
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s batch flush: %w", b.Name, err)
	}

	replies := make([][]byte, len(raws))
	for i := range raws {
		msg, err := resp.ReadMessage(ctx, pc.reader)
		if err != nil {
			b.removeConn(pc)
			b.tryReconnect()
			return nil, fmt.Errorf("backend %s batch read: %w", b.Name, err)
		}
		replies[i] = msg.Bytes()
	}

	b.release(pc)
	return replies, nil
}

func (b *Backend) Pin(ctx context.Context) (*PinnedConn, error) {
	pc, err := b.acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &PinnedConn{pc: pc, b: b}, nil
}

func (b *Backend) Unpin(p *PinnedConn) {
	if p == nil {
		return
	}
	p.b.release(p.pc)
}

// ForwardPinned sends a command on an already-pinned connection.
func (b *Backend) ForwardPinned(ctx context.Context, p *PinnedConn, raw []byte) ([]byte, error) {
	pc := p.pc

	if deadline, ok := ctx.Deadline(); ok {
		pc.conn.SetWriteDeadline(deadline)
	}

	if _, err := pc.writer.Write(raw); err != nil {
		b.removeConn(pc)
		p.pc = nil
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s write: %w", b.Name, err)
	}
	if err := pc.writer.Flush(); err != nil {
		b.removeConn(pc)
		p.pc = nil
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s flush: %w", b.Name, err)
	}

	msg, err := resp.ReadMessage(ctx, pc.reader)
	if err != nil {
		b.removeConn(pc)
		p.pc = nil
		b.tryReconnect()
		return nil, fmt.Errorf("backend %s read: %w", b.Name, err)
	}

	return msg.Bytes(), nil
}

func (b *Backend) IsConnected() bool {
	return b.activeConns.Load() > 0
}

func (b *Backend) SetRole(role Role) {
	b.Role = role
}

func (b *Backend) Close() error {
	b.closed.Store(true)

	for {
		select {
		case pc := <-b.pool:
			pc.conn.Close()
			b.activeConns.Add(-1)
			<-b.sem
		default:
			return nil
		}
	}
}

// Reconnect attempts to reconnect with exponential backoff.
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
