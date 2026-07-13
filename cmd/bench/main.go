package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/redis-proxy/internal/resp"
)

// ---------- RESP helpers ----------

func sendCommand(conn net.Conn, args ...string) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, a := range args {
		sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(a), a))
	}
	_, err := conn.Write([]byte(sb.String()))
	return err
}

func readReply(conn net.Conn, r *bufio.Reader, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	msg, err := resp.ReadMessage(context.Background(), r)
	if err != nil {
		return "", err
	}
	return string(msg.Bytes()), nil
}

// ---------- functional test ----------

type testCase struct {
	name string
	fn   func(conn net.Conn) error
}

func runFuncTests(addr string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 64*1024)

	tests := []testCase{
		// String
		{"SET/GET", func(c net.Conn) error {
			sendCommand(c, "SET", "bench_k", "v")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil {
				return err
			}
			if r != "+OK\r\n" {
				return fmt.Errorf("SET: %q", r)
			}
			sendCommand(c, "GET", "bench_k")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil {
				return err
			}
			if r != "$1\r\nv\r\n" {
				return fmt.Errorf("GET: %q", r)
			}
			return nil
		}},
		{"DEL", func(c net.Conn) error {
			sendCommand(c, "SET", "bench_del", "x")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "DEL", "bench_del")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil {
				return err
			}
			if r != ":1\r\n" {
				return fmt.Errorf("DEL: %q", r)
			}
			return nil
		}},
		{"SETEX/EXPIRE/TTL", func(c net.Conn) error {
			sendCommand(c, "SETEX", "bench_ttl", "10", "val")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("SETEX: %v %q", err, r)
			}
			sendCommand(c, "TTL", "bench_ttl")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.HasPrefix(r, ":") {
				return fmt.Errorf("TTL: %v %q", err, r)
			}
			sendCommand(c, "EXPIRE", "bench_ttl", "60")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("EXPIRE: %v %q", err, r)
			}
			return nil
		}},
		{"SETNX", func(c net.Conn) error {
			sendCommand(c, "DEL", "bench_nx")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "SETNX", "bench_nx", "val")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("SETNX first: %v %q", err, r)
			}
			sendCommand(c, "SETNX", "bench_nx", "val2")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":0\r\n" {
				return fmt.Errorf("SETNX second: %v %q", err, r)
			}
			return nil
		}},
		{"EXISTS", func(c net.Conn) error {
			sendCommand(c, "SET", "bench_ex", "1")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "EXISTS", "bench_ex")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("EXISTS yes: %v %q", err, r)
			}
			sendCommand(c, "EXISTS", "bench_nonexistent_xyz")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":0\r\n" {
				return fmt.Errorf("EXISTS no: %v %q", err, r)
			}
			return nil
		}},
		{"INCR/DECR", func(c net.Conn) error {
			sendCommand(c, "SET", "bench_ctr", "0")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "INCR", "bench_ctr")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("INCR: %v %q", err, r)
			}
			sendCommand(c, "INCRBY", "bench_ctr", "5")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":6\r\n" {
				return fmt.Errorf("INCRBY: %v %q", err, r)
			}
			sendCommand(c, "DECR", "bench_ctr")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":5\r\n" {
				return fmt.Errorf("DECR: %v %q", err, r)
			}
			sendCommand(c, "DECRBY", "bench_ctr", "3")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":2\r\n" {
				return fmt.Errorf("DECRBY: %v %q", err, r)
			}
			return nil
		}},
		{"MGET/MSET", func(c net.Conn) error {
			sendCommand(c, "MSET", "m1", "a", "m2", "b")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("MSET: %v %q", err, r)
			}
			sendCommand(c, "MGET", "m1", "m2")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "a") || !strings.Contains(r, "b") {
				return fmt.Errorf("MGET: %v %q", err, r)
			}
			return nil
		}},
		{"GETSET/GETDEL/RENAME", func(c net.Conn) error {
			sendCommand(c, "SET", "bench_gs", "old")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "GETSET", "bench_gs", "new")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "$3\r\nold\r\n" {
				return fmt.Errorf("GETSET: %v %q", err, r)
			}
			sendCommand(c, "GETDEL", "bench_gs")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != "$3\r\nnew\r\n" {
				return fmt.Errorf("GETDEL: %v %q", err, r)
			}
			sendCommand(c, "SET", "bench_rn", "val")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "RENAME", "bench_rn", "bench_rn2")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("RENAME: %v %q", err, r)
			}
			return nil
		}},
		// Hash
		{"HSET/HGET/HGETALL", func(c net.Conn) error {
			sendCommand(c, "HSET", "bh", "f1", "v1")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("HSET: %v %q", err, r)
			}
			sendCommand(c, "HGET", "bh", "f1")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != "$2\r\nv1\r\n" {
				return fmt.Errorf("HGET: %v %q", err, r)
			}
			sendCommand(c, "HGETALL", "bh")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "f1") {
				return fmt.Errorf("HGETALL: %v %q", err, r)
			}
			return nil
		}},
		{"HMSET/HMGET/HDEL/HEXISTS/HKEYS/HSETNX/HINCRBY", func(c net.Conn) error {
			sendCommand(c, "HMSET", "bh2", "a", "1", "b", "2")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("HMSET: %v %q", err, r)
			}
			sendCommand(c, "HMGET", "bh2", "a", "b")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "1") {
				return fmt.Errorf("HMGET: %v %q", err, r)
			}
			sendCommand(c, "HEXISTS", "bh2", "a")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("HEXISTS: %v %q", err, r)
			}
			sendCommand(c, "HKEYS", "bh2")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "a") {
				return fmt.Errorf("HKEYS: %v %q", err, r)
			}
			sendCommand(c, "HSETNX", "bh2", "c", "3")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("HSETNX: %v %q", err, r)
			}
			sendCommand(c, "HINCRBY", "bh2", "a", "10")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":11\r\n" {
				return fmt.Errorf("HINCRBY: %v %q", err, r)
			}
			sendCommand(c, "HDEL", "bh2", "c")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("HDEL: %v %q", err, r)
			}
			return nil
		}},
		// List
		{"LPUSH/RPUSH/LRANGE/LLEN", func(c net.Conn) error {
			sendCommand(c, "LPUSH", "bl", "b")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "LPUSH", "bl", "a")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "RPUSH", "bl", "c")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "LRANGE", "bl", "0", "-1")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "a") || !strings.Contains(r, "c") {
				return fmt.Errorf("LRANGE: %v %q", err, r)
			}
			sendCommand(c, "LLEN", "bl")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":3\r\n" {
				return fmt.Errorf("LLEN: %v %q", err, r)
			}
			return nil
		}},
		// Set
		{"SADD/SMEMBERS/SREM/SISMEMBER/SCARD/SMISMEMBER", func(c net.Conn) error {
			sendCommand(c, "SADD", "bs", "x", "y", "z")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":3\r\n" {
				return fmt.Errorf("SADD: %v %q", err, r)
			}
			sendCommand(c, "SMEMBERS", "bs")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "x") {
				return fmt.Errorf("SMEMBERS: %v %q", err, r)
			}
			sendCommand(c, "SISMEMBER", "bs", "x")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("SISMEMBER yes: %v %q", err, r)
			}
			sendCommand(c, "SISMEMBER", "bs", "nope")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":0\r\n" {
				return fmt.Errorf("SISMEMBER no: %v %q", err, r)
			}
			sendCommand(c, "SCARD", "bs")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":3\r\n" {
				return fmt.Errorf("SCARD: %v %q", err, r)
			}
			sendCommand(c, "SMISMEMBER", "bs", "x", "nope")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "1") || !strings.Contains(r, "0") {
				return fmt.Errorf("SMISMEMBER: %v %q", err, r)
			}
			sendCommand(c, "SREM", "bs", "z")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("SREM: %v %q", err, r)
			}
			return nil
		}},
		// BitMap
		{"SETBIT/GETBIT", func(c net.Conn) error {
			sendCommand(c, "SETBIT", "bmap", "7", "1")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != ":0\r\n" {
				return fmt.Errorf("SETBIT: %v %q", err, r)
			}
			sendCommand(c, "GETBIT", "bmap", "7")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":1\r\n" {
				return fmt.Errorf("GETBIT: %v %q", err, r)
			}
			sendCommand(c, "GETBIT", "bmap", "0")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != ":0\r\n" {
				return fmt.Errorf("GETBIT zero: %v %q", err, r)
			}
			return nil
		}},
		// Scripting
		{"EVAL", func(c net.Conn) error {
			sendCommand(c, "EVAL", "return redis.call('SET','eval_k','eval_v')", "0")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "OK") {
				return fmt.Errorf("EVAL: %v %q", err, r)
			}
			sendCommand(c, "GET", "eval_k")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != "$6\r\neval_v\r\n" {
				return fmt.Errorf("EVAL verify GET: %v %q", err, r)
			}
			return nil
		}},
		{"SCRIPT LOAD", func(c net.Conn) error {
			sendCommand(c, "SCRIPT", "LOAD", "return 1")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.HasPrefix(r, "$40") {
				return fmt.Errorf("SCRIPT LOAD: %v %q", err, r)
			}
			return nil
		}},
		// Key / Scan
		{"KEYS", func(c net.Conn) error {
			sendCommand(c, "SET", "keys_test_1", "1")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "KEYS", "keys_test_*")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "keys_test_1") {
				return fmt.Errorf("KEYS: %v %q", err, r)
			}
			return nil
		}},
		{"SCAN", func(c net.Conn) error {
			sendCommand(c, "SCAN", "0")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.HasPrefix(r, "*2") {
				return fmt.Errorf("SCAN: %v %q", err, r)
			}
			return nil
		}},
		{"MEMORY USAGE", func(c net.Conn) error {
			sendCommand(c, "SET", "mem_k", "v")
			readReply(c, br, 2*time.Second)
			sendCommand(c, "MEMORY", "USAGE", "mem_k")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.HasPrefix(r, ":") {
				return fmt.Errorf("MEMORY USAGE: %v %q", err, r)
			}
			return nil
		}},
		// Server
		{"PING", func(c net.Conn) error {
			sendCommand(c, "PING")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "PONG") {
				return fmt.Errorf("PING: %v %q", err, r)
			}
			return nil
		}},
		{"SELECT", func(c net.Conn) error {
			sendCommand(c, "SELECT", "0")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("SELECT: %v %q", err, r)
			}
			return nil
		}},
		// Transaction
		{"MULTI/EXEC", func(c net.Conn) error {
			sendCommand(c, "MULTI")
			r, err := readReply(c, br, 2*time.Second)
			if err != nil || r != "+OK\r\n" {
				return fmt.Errorf("MULTI: %v %q", err, r)
			}
			sendCommand(c, "SET", "tx_k", "tx_v")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || r != "+QUEUED\r\n" {
				return fmt.Errorf("SET in TX: %v %q", err, r)
			}
			sendCommand(c, "EXEC")
			r, err = readReply(c, br, 2*time.Second)
			if err != nil || !strings.Contains(r, "OK") {
				return fmt.Errorf("EXEC: %v %q", err, r)
			}
			return nil
		}},
		// Pipeline
		{"Pipeline", func(c net.Conn) error {
			// Send 10 SETs + 10 GETs without waiting
			for i := 0; i < 10; i++ {
				sendCommand(c, "SET", "pl_k", "pl_v")
			}
			for i := 0; i < 10; i++ {
				sendCommand(c, "GET", "pl_k")
			}
			// Read 20 replies
			for i := 0; i < 10; i++ {
				r, err := readReply(c, br, 2*time.Second)
				if err != nil || r != "+OK\r\n" {
					return fmt.Errorf("pipeline SET[%d]: %v %q", i, err, r)
				}
			}
			for i := 0; i < 10; i++ {
				r, err := readReply(c, br, 2*time.Second)
				if err != nil || r != "$4\r\npl_v\r\n" {
					return fmt.Errorf("pipeline GET[%d]: %v %q", i, err, r)
				}
			}
			return nil
		}},
	}

	passed, failed := 0, 0
	for _, tc := range tests {
		fmt.Printf("  %-30s ", tc.name)
		if err := tc.fn(conn); err != nil {
			fmt.Printf("FAIL  %v\n", err)
			failed++
		} else {
			fmt.Println("PASS")
			passed++
		}
	}
	fmt.Printf("\nResults: %d passed, %d failed\n", passed, failed)

	// Cleanup test keys
	cleanKeys := []string{"bench_k", "bench_del", "bench_ttl", "bench_nx", "bench_ex",
		"bench_ctr", "m1", "m2", "bench_gs", "bench_rn", "bench_rn2",
		"bh", "bh2", "bl", "bs", "bmap", "eval_k", "keys_test_1", "mem_k", "tx_k", "pl_k"}
	for _, k := range cleanKeys {
		sendCommand(conn, "DEL", k)
		readReply(conn, br, 2*time.Second)
	}
}

// ---------- performance test ----------

func runPerfTests(addr string, concurrency, numReqs, pipeline int, rwRatio float64) {
	var totalOps atomic.Int64
	var totalErrs atomic.Int64
	var wg sync.WaitGroup

	// Collect latencies (nanoseconds)
	latencies := make([]int64, concurrency*numReqs)
	var latencyIdx atomic.Int64

	start := time.Now()
	totalExpected := int64(concurrency * numReqs)

	// Progress reporter: prints every second.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		lastOps := int64(0)
		for {
			select {
			case <-done:
				return
			case t := <-ticker.C:
				ops := totalOps.Load()
				elapsed := t.Sub(start).Seconds()
				intervalOps := ops - lastOps
				lastOps = ops
				instantRPS := float64(intervalOps) / 1.0 // per-second window
				overallRPS := float64(ops) / elapsed

				n := int(latencyIdx.Load())
				var avgMs float64
				if n > 0 {
					var sum int64
					slice := latencies[:n]
					for _, v := range slice {
						sum += v
					}
					avgMs = float64(sum/int64(n)) / 1e6
				}

				fmt.Printf("rps=%.1f (overall: %.1f) avg_msec=%.3f (overall)\r",
					instantRPS, overallRPS, avgMs)
			}
		}
	}()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "worker %d dial: %v\n", workerID, err)
				totalErrs.Add(int64(numReqs))
				return
			}
			defer conn.Close()
			rd := bufio.NewReaderSize(conn, 64*1024)

			isWrite := func() bool {
				return float64(workerID%100)/100.0 < rwRatio
			}

			reqsPerWorker := numReqs
			for j := 0; j < reqsPerWorker; j += pipeline {
				batch := pipeline
				if j+batch > reqsPerWorker {
					batch = reqsPerWorker - j
				}

				// Send batch
				for k := 0; k < batch; k++ {
					if isWrite() {
						sendCommand(conn, "SET", "perf_k", "perf_v")
					} else {
						sendCommand(conn, "GET", "perf_k")
					}
				}

				// Read batch
				for k := 0; k < batch; k++ {
					t0 := time.Now()
					r, err := readReply(conn, rd, 10*time.Second)
					elapsed := time.Since(t0).Nanoseconds()

					idx := latencyIdx.Add(1) - 1
					if int(idx) < len(latencies) {
						latencies[idx] = elapsed
					}

					if err != nil {
						totalErrs.Add(1)
						if err != io.EOF {
							return
						}
					} else if strings.HasPrefix(r, "-") {
						totalErrs.Add(1)
					}
					totalOps.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	close(done)
	fmt.Print("\n") // newline after \r progress line

	elapsed := time.Since(start)
	ops := totalOps.Load()
	errs := totalErrs.Load()
	_ = totalExpected

	// Calculate percentiles from collected latencies
	n := int(latencyIdx.Load())
	if n > len(latencies) {
		n = len(latencies)
	}

	var p50, p95, p99 float64
	if n > 0 {
		slice := latencies[:n]
		sort.Slice(slice, func(i, j int) bool { return slice[i] < slice[j] })
		p50 = float64(slice[n*50/100]) / 1e6
		p95 = float64(slice[n*95/100]) / 1e6
		p99 = float64(slice[n*99/100]) / 1e6
	}

	fmt.Printf("\n========== Benchmark Results ==========\n")
	fmt.Printf("  Address:      %s\n", addr)
	fmt.Printf("  Concurrency:  %d\n", concurrency)
	fmt.Printf("  Requests:     %d/conn (%d total)\n", numReqs, concurrency*numReqs)
	fmt.Printf("  Pipeline:     %d\n", pipeline)
	fmt.Printf("  RW ratio:     %.1f\n", rwRatio)
	fmt.Printf("  Duration:     %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Total ops:    %d\n", ops)
	fmt.Printf("  Errors:       %d\n", errs)
	if elapsed.Seconds() > 0 {
		fmt.Printf("  QPS:          %.0f\n", float64(ops)/elapsed.Seconds())
	}
	fmt.Printf("  P50 latency:  %.2f ms\n", p50)
	fmt.Printf("  P95 latency:  %.2f ms\n", p95)
	fmt.Printf("  P99 latency:  %.2f ms\n", p99)
	fmt.Printf("========================================\n")
}

// ---------- main ----------

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "proxy address")
	mode := flag.String("mode", "func", "test mode: func or perf")
	concurrency := flag.Int("c", 50, "concurrent connections (perf mode)")
	numReqs := flag.Int("n", 10000, "requests per connection (perf mode)")
	pipeline := flag.Int("pipeline", 1, "pipeline batch size (perf mode)")
	rwRatio := flag.Float64("rw-ratio", 0.5, "write ratio 0=all read, 1=all write (perf mode)")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_ = logger

	switch *mode {
	case "func":
		fmt.Printf("Functional tests against %s\n\n", *addr)
		runFuncTests(*addr)
	case "perf":
		fmt.Printf("Performance benchmark against %s\n", *addr)
		runPerfTests(*addr, *concurrency, *numReqs, *pipeline, *rwRatio)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s (use func or perf)\n", *mode)
		os.Exit(1)
	}
}
