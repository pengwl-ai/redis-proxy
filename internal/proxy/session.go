package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/resp"
)

var supportedCommands = map[string]bool{
	// String
	"GET": true, "SET": true, "SETEX": true, "SETNX": true, "GETSET": true,
	"GETDEL": true, "MGET": true, "MSET": true, "INCR": true, "INCRBY": true,
	"DECR": true, "DECRBY": true, "EXISTS": true, "DEL": true,
	"TTL": true, "PTTL": true, "EXPIRE": true, "PEXPIRE": true, "EXPIREAT": true,
	"RENAME": true,
	// Hash
	"HGET": true, "HSET": true, "HMSET": true, "HMGET": true, "HGETALL": true,
	"HDEL": true, "HEXISTS": true, "HKEYS": true, "HSETNX": true, "HINCRBY": true,
	// List
	"RPUSH": true, "LPUSH": true, "LPOP": true, "RPOP": true, "LLEN": true, "LRANGE": true,
	// Set
	"SADD": true, "SREM": true, "SPOP": true, "SMEMBERS": true, "SISMEMBER": true,
	"SCARD": true, "SMISMEMBER": true,
	// ZSet
	"ZADD": true, "ZPOPMIN": true,
	// Key / Scan
	"KEYS": true, "SCAN": true, "SSCAN": true, "MEMORY USAGE": true,
	// Server
	"PING": true, "SELECT": true, "AUTH": true,
	// BitMap
	"SETBIT": true, "GETBIT": true,
	// Scripting
	"EVAL": true, "SCRIPT LOAD": true,
	// Transaction
	"MULTI": true, "EXEC": true, "DISCARD": true, "WATCH": true, "UNWATCH": true,
}

// Commands forwarded to standby backends after primary succeeds.
var standbyForwardCommands = map[string]bool{
	"SET": true, "SETEX": true, "SETNX": true, "GETSET": true,
	"GETDEL": true, "MSET": true, "INCR": true, "INCRBY": true,
	"DECR": true, "DECRBY": true, "DEL": true,
	"EXPIRE": true, "PEXPIRE": true, "EXPIREAT": true, "RENAME": true,
	"HSET": true, "HMSET": true, "HSETNX": true, "HDEL": true, "HINCRBY": true,
	"RPUSH": true, "LPUSH": true, "LPOP": true, "RPOP": true,
	"SADD": true, "SREM": true, "SPOP": true,
	"ZADD": true, "ZPOPMIN": true,
	"SETBIT": true,
	"EVAL":   true, "SCRIPT LOAD": true,
	"AUTH": true, "SELECT": true,
}

type Session struct {
	id     uint64
	conn   net.Conn
	pool   *backend.Pool
	reader *bufio.Reader
	writer *bufio.Writer
	logger *slog.Logger

	// cmdBuf backs the raw slices returned by resp.ReadCommand; reset at the
	// top of each Run iteration, so raw is only valid within one iteration.
	cmdBuf bytes.Buffer

	txConn    *backend.PinnedConn
	forwarder *standbyForwarder
}

// Commands larger than this are not worth keeping buffered per session.
const maxRetainedCmdBuf = 1 << 20

func (s *Session) Run(ctx context.Context) error {
	defer s.conn.Close()

	if s.txConn != nil {
		s.pool.Primary().Unpin(s.txConn)
		s.txConn = nil
	}

	go func() {
		<-ctx.Done()
		s.conn.SetReadDeadline(now())
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if s.cmdBuf.Cap() > maxRetainedCmdBuf {
			s.cmdBuf = bytes.Buffer{}
		}
		s.cmdBuf.Reset()

		cmd, raw, err := resp.ReadCommand(ctx, s.reader, &s.cmdBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.logger.Debug("client disconnected")
				return nil
			}
			if errors.Is(err, context.Canceled) {
				return nil
			}
			s.logger.Error("read command", "err", err)
			return err
		}

		primary := s.pool.Primary()
		if primary == nil {
			s.writer.Write([]byte("-ERR no primary backend available\r\n"))
			s.writer.Flush()
			continue
		}

		// Drain buffered commands for pipeline batching (best-effort).
		// Only batch when not in a transaction and the command doesn't start one.
		if s.txConn == nil && cmd != "MULTI" && cmd != "WATCH" && s.reader.Buffered() > 0 {
			cmds := []string{cmd}
			raws := [][]byte{raw}

			for s.reader.Buffered() > 0 {
				cmd2, raw2, err := resp.ReadCommand(ctx, s.reader, &s.cmdBuf)
				if err != nil {
					break
				}
				s.logger.Debug("received command", "cmd", cmd2)
				// Don't batch transaction-starting commands.
				if cmd2 == "MULTI" || cmd2 == "WATCH" {
					// Process what we have so far, then handle this one individually.
					if err := s.processBatch(ctx, primary, cmds, raws); err != nil {
						return err
					}
					if err := s.processCommand(ctx, primary, cmd2, raw2); err != nil {
						return err
					}
					goto next
				}
				cmds = append(cmds, cmd2)
				raws = append(raws, raw2)
			}

			if len(cmds) > 1 {
				s.logger.Debug("batch forwarding", "count", len(cmds))
				if err := s.processBatch(ctx, primary, cmds, raws); err != nil {
					return err
				}
			} else {
				if err := s.processCommand(ctx, primary, cmd, raw); err != nil {
					return err
				}
			}
		next:
			continue
		}

		if err := s.processCommand(ctx, primary, cmd, raw); err != nil {
			return err
		}
	}
}

func (s *Session) processCommand(ctx context.Context, primary *backend.Backend, cmd string, raw []byte) error {
	if !supportedCommands[cmd] {
		_, err := fmt.Fprintf(s.writer, "-ERR unsupported command '%s'\r\n", cmd)
		if err == nil {
			err = s.writer.Flush()
		}
		return err
	}

	var reply []byte
	var fwdErr error

	if s.txConn != nil {
		reply, fwdErr = primary.ForwardPinned(ctx, s.txConn, raw)
	} else if cmd == "MULTI" || cmd == "WATCH" {
		s.txConn, fwdErr = primary.Pin(ctx)
		if fwdErr == nil {
			reply, fwdErr = primary.ForwardPinned(ctx, s.txConn, raw)
		}
	} else {
		reply, fwdErr = primary.Forward(ctx, raw)
	}

	if fwdErr != nil {
		s.logger.Error("forward failed", "cmd", cmd, "err", fwdErr)
		if s.txConn != nil {
			primary.Unpin(s.txConn)
			s.txConn = nil
		}
		s.writer.Write([]byte(fmt.Sprintf("-ERR backend error: %v\r\n", fwdErr)))
		return s.writer.Flush()
	}

	if s.txConn != nil && (cmd == "EXEC" || cmd == "DISCARD" || cmd == "UNWATCH") {
		primary.Unpin(s.txConn)
		s.txConn = nil
	}

	if standbyForwardCommands[cmd] && s.txConn == nil {
		s.forwardToStandbys(raw)
	}

	_, err := s.writer.Write(reply)
	if err == nil {
		err = s.writer.Flush()
	}
	return err
}

func (s *Session) processBatch(ctx context.Context, primary *backend.Backend, cmds []string, raws [][]byte) error {
	// Verify all commands are supported.
	for _, cmd := range cmds {
		if !supportedCommands[cmd] {
			// Fall back to individual processing.
			for i, cmd := range cmds {
				if err := s.processCommand(ctx, primary, cmd, raws[i]); err != nil {
					return err
				}
			}
			return nil
		}
	}

	replies, err := primary.ForwardBatch(ctx, raws)
	if err != nil {
		s.logger.Error("batch forward failed", "err", err, "count", len(cmds))
		s.writer.Write([]byte(fmt.Sprintf("-ERR backend error: %v\r\n", err)))
		return s.writer.Flush()
	}

	for i, reply := range replies {
		if _, err := s.writer.Write(reply); err != nil {
			return err
		}
		if standbyForwardCommands[cmds[i]] {
			s.forwardToStandbys(raws[i])
		}
	}
	return s.writer.Flush()
}

func (s *Session) forwardToStandbys(raw []byte) {
	if s.forwarder == nil || !s.forwarder.HasStandbys() {
		return
	}
	s.forwarder.Submit(raw)
}

var now = func() time.Time { return time.Now() }
