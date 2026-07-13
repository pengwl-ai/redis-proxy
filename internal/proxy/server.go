package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/config"
)

type Server struct {
	cfg    config.ProxyConfig
	pool   *backend.Pool
	logger *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup
	nextID   uint64
}

func NewServer(cfg config.ProxyConfig, pool *backend.Pool, logger *slog.Logger) *Server {
	return &Server{
		cfg:    cfg,
		pool:   pool,
		logger: logger,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Listen)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.logger.Info("proxy listening", "addr", s.cfg.Listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				if isTemporaryErr(err) {
					s.logger.Warn("accept error", "err", err)
					continue
				}
				return err
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()

	if ln != nil {
		ln.Close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("all sessions drained")
		return nil
	case <-ctx.Done():
		s.logger.Warn("shutdown timeout, forcing close")
		return ctx.Err()
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()

	logger := s.logger.With("session_id", id, "remote", conn.RemoteAddr())
	logger.Info("client connected")

	session := &Session{
		id:     id,
		conn:   conn,
		pool:   s.pool,
		reader: bufio.NewReaderSize(conn, 64*1024),
		writer: bufio.NewWriterSize(conn, 64*1024),
		logger: logger,
	}

	if err := session.Run(ctx); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			logger.Error("session error", "err", err)
		}
	}
	logger.Info("client disconnected")
}

func isTemporaryErr(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
