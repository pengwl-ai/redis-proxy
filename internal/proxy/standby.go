package proxy

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/example/redis-proxy/internal/backend"
)

const defaultStandbyQueueSize = 10000

type standbyForwarder struct {
	tasks  chan *standbyTask
	pool   *backend.Pool
	logger *slog.Logger
	wg     sync.WaitGroup

	started atomic.Bool
	dropped atomic.Int64
}

type standbyTask struct {
	// raw is a task-owned buffer reused across pool cycles; Submit copies
	// the command into it because the source slice is invalidated when the
	// session resets its command buffer.
	raw []byte
}

// Buffers above this size are released instead of being retained in the pool.
const maxRetainedTaskBuf = 64 << 10

var standbyTaskPool = sync.Pool{
	New: func() any { return &standbyTask{} },
}

func putStandbyTask(task *standbyTask) {
	if cap(task.raw) > maxRetainedTaskBuf {
		task.raw = nil
	}
	standbyTaskPool.Put(task)
}

func newStandbyForwarder(pool *backend.Pool, logger *slog.Logger, queueSize int) *standbyForwarder {
	if queueSize <= 0 {
		queueSize = defaultStandbyQueueSize
	}
	return &standbyForwarder{
		tasks:  make(chan *standbyTask, queueSize),
		pool:   pool,
		logger: logger,
	}
}

func (sf *standbyForwarder) Start() {
	if !sf.started.CompareAndSwap(false, true) {
		return
	}
	sf.wg.Add(1)
	go sf.consume()
}

func (sf *standbyForwarder) consume() {
	defer sf.wg.Done()
	for task := range sf.tasks {
		for _, b := range sf.pool.CachedStandbys() {
			if !b.IsConnected() {
				continue
			}
			if _, err := b.Forward(context.Background(), task.raw); err != nil {
				sf.logger.Warn("standby forward failed", "standby", b.Name, "err", err)
			}
		}
		putStandbyTask(task)
	}
}

func (sf *standbyForwarder) HasStandbys() bool {
	for _, b := range sf.pool.CachedStandbys() {
		if b.IsConnected() {
			return true
		}
	}
	return false
}

func (sf *standbyForwarder) Submit(raw []byte) {
	if !sf.HasStandbys() {
		return
	}
	task := standbyTaskPool.Get().(*standbyTask)
	task.raw = append(task.raw[:0], raw...)

	select {
	case sf.tasks <- task:
	default:
		sf.dropped.Add(1)
		putStandbyTask(task)
	}
}

func (sf *standbyForwarder) Close() {
	close(sf.tasks)
	sf.wg.Wait()
}

func (sf *standbyForwarder) Dropped() int64 {
	return sf.dropped.Load()
}
