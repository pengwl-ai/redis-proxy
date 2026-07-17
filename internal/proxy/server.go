package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/config"
)

type Server struct {
	cfg    config.ProxyConfig
	pool   *backend.Pool
	logger *slog.Logger

	forwarder *standbyForwarder

	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup
	nextID   uint64
}

func NewServer(cfg config.ProxyConfig, pool *backend.Pool, logger *slog.Logger) *Server {
	fw := newStandbyForwarder(pool, logger, cfg.StandbyQueueSize)
	fw.Start()

	return &Server{
		cfg:       cfg,
		pool:      pool,
		logger:    logger,
		forwarder: fw,
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
	case <-ctx.Done():
		s.logger.Warn("shutdown timeout, forcing close")
	}

	if s.forwarder != nil {
		s.forwarder.Close()
	}

	return nil
}

var (
	readerPool = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 8*1024) }}
	writerPool = sync.Pool{New: func() any { return bufio.NewWriterSize(nil, 8*1024) }}
)

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()

	logger := s.logger.With("session_id", id, "remote", conn.RemoteAddr())
	logger.Info("client connected")

	reader := readerPool.Get().(*bufio.Reader)
	reader.Reset(conn)
	writer := writerPool.Get().(*bufio.Writer)
	writer.Reset(conn)

	session := &Session{
		id:        id,
		conn:      conn,
		pool:      s.pool,
		reader:    reader,
		writer:    writer,
		logger:    logger,
		forwarder: s.forwarder,
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	if err := session.Run(sessionCtx); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			logger.Error("session error", "err", err)
		}
	}

	reader.Reset(nil)
	readerPool.Put(reader)
	writer.Reset(nil)
	writerPool.Put(writer)

	logger.Info("client disconnected")
}

func isTemporaryErr(err error) bool {
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout()
	}
	return false
}
