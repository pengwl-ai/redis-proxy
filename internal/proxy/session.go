package proxy

import (
	"bufio"
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
	"GET":     true,
	"SET":     true,
	"SETEX":   true,
	"SETNX":   true,
	"GETSET":  true,
	"GETDEL":  true,
	"MGET":    true,
	"MSET":    true,
	"INCR":    true,
	"INCRBY":  true,
	"DECR":    true,
	"DECRBY":  true,
	"EXISTS":  true,
	"DEL":     true,
	"TTL":     true,
	"PTTL":    true,
	"EXPIRE":  true,
	"PEXPIRE": true,
	"EXPIREAT": true,
	"RENAME":  true,
	// Hash
	"HGET":    true,
	"HSET":    true,
	"HMSET":   true,
	"HMGET":   true,
	"HGETALL": true,
	"HDEL":    true,
	"HEXISTS": true,
	"HKEYS":   true,
	"HSETNX":  true,
	"HINCRBY": true,
	// List
	"RPUSH":  true,
	"LPUSH":  true,
	"LLEN":   true,
	"LRANGE": true,
	// Set
	"SADD":       true,
	"SREM":       true,
	"SMEMBERS":   true,
	"SISMEMBER":  true,
	"SCARD":      true,
	"SMISMEMBER": true,
	// Key / Scan
	"KEYS":         true,
	"SCAN":         true,
	"SSCAN":        true,
	"MEMORY USAGE": true,
	// Server
	"PING":   true,
	"SELECT": true,
	"AUTH":   true,
	// BitMap
	"SETBIT": true,
	"GETBIT": true,
	// Scripting
	"EVAL":        true,
	"SCRIPT LOAD": true,
	// Transaction
	"MULTI":   true,
	"EXEC":    true,
	"DISCARD":  true,
	"WATCH":   true,
	"UNWATCH": true,
}

// Commands forwarded to standby backends after primary succeeds.
// Data-modifying commands + AUTH/SELECT for standby sync.
var standbyForwardCommands = map[string]bool{
	// String writes
	"SET": true, "SETEX": true, "SETNX": true, "GETSET": true,
	"GETDEL": true, "MSET": true, "INCR": true, "INCRBY": true,
	"DECR": true, "DECRBY": true, "DEL": true,
	"EXPIRE": true, "PEXPIRE": true, "EXPIREAT": true, "RENAME": true,
	// Hash writes
	"HSET": true, "HMSET": true, "HSETNX": true, "HDEL": true, "HINCRBY": true,
	// List writes
	"RPUSH": true, "LPUSH": true,
	// Set writes
	"SADD": true, "SREM": true,
	// BitMap writes
	"SETBIT": true,
	// Scripting
	"EVAL": true, "SCRIPT LOAD": true,
	// Connection sync
	"AUTH": true, "SELECT": true,
}

type Session struct {
	id     uint64
	conn   net.Conn
	pool   *backend.Pool
	reader *bufio.Reader
	logger *slog.Logger
}

func (s *Session) Run(ctx context.Context) error {
	defer s.conn.Close()

	// Unblock reads when context is cancelled.
	go func() {
		<-ctx.Done()
		s.conn.SetReadDeadline(now())
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		cmd, raw, err := resp.ReadCommand(ctx, s.reader)
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

		s.logger.Debug("received command", "cmd", cmd)

		if !supportedCommands[cmd] {
			reply := fmt.Sprintf("-ERR unsupported command '%s'\r\n", cmd)
			s.conn.Write([]byte(reply))
			continue
		}

		primary := s.pool.Primary()
		if primary == nil {
			s.conn.Write([]byte("-ERR no primary backend available\r\n"))
			continue
		}

		reply, err := primary.Forward(ctx, raw)
		if err != nil {
			s.logger.Error("forward failed", "cmd", cmd, "err", err)
			reply := []byte(fmt.Sprintf("-ERR backend error: %v\r\n", err))
			s.conn.Write(reply)
			continue
		}

		// Best-effort forward to standby backends.
		if standbyForwardCommands[cmd] {
			s.forwardToStandbys(ctx, cmd, raw)
		}

		if _, err := s.conn.Write(reply); err != nil {
			s.logger.Error("write reply", "err", err)
			return err
		}
	}
}

func (s *Session) forwardToStandbys(ctx context.Context, cmd string, raw []byte) {
	standbys := s.pool.Standbys()
	for _, b := range standbys {
		if !b.IsConnected() {
			continue
		}
		b := b
		go func() {
			if _, err := b.Forward(ctx, raw); err != nil {
				s.logger.Warn("standby forward failed", "standby", b.Name, "cmd", cmd, "err", err)
			}
		}()
	}
}

var now = func() time.Time { return time.Now() }
