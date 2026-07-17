package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/example/redis-proxy/internal/config"
)

type Pool struct {
	mu       sync.RWMutex
	backends map[string]*Backend
	primary  atomic.Pointer[Backend]

	standbysSnapshot atomic.Pointer[[]*Backend]

	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
}

func NewPool(ctx context.Context, entries []config.BackendEntry, logger *slog.Logger) (*Pool, error) {
	ctx, cancel := context.WithCancel(ctx)

	p := &Pool{
		backends: make(map[string]*Backend, len(entries)),
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger,
	}

	for _, e := range entries {
		b := NewBackend(e.Name, e.Addr, Role(e.Role))

		poolSize := e.PoolSize
		if poolSize <= 0 {
			poolSize = 4
		}
		maxPool := e.MaxPoolSize
		if maxPool <= 0 {
			maxPool = 20
		}
		b.SetPoolConfig(poolSize, maxPool)

		b.SetOnDisconnect(func() {
			p.reconnectLoop(b)
		})

		if err := b.Connect(ctx); err != nil {
			if Role(e.Role) == RolePrimary {
				cancel()
				return nil, fmt.Errorf("connect primary %q: %w", e.Name, err)
			}
			logger.Warn("standby backend unreachable at startup", "name", e.Name, "addr", e.Addr, "err", err)
		}

		p.backends[e.Name] = b

		if Role(e.Role) == RolePrimary {
			p.primary.Store(b)
		}
	}

	if p.primary.Load() == nil {
		cancel()
		return nil, errors.New("no primary backend configured or reachable")
	}

	p.buildStandbysSnapshot()

	for _, b := range p.backends {
		if !b.IsConnected() {
			go p.reconnectLoop(b)
		}
	}

	return p, nil
}

func (p *Pool) Primary() *Backend {
	return p.primary.Load()
}

func (p *Pool) CachedStandbys() []*Backend {
	snapshot := p.standbysSnapshot.Load()
	if snapshot == nil {
		return nil
	}
	return *snapshot
}

func (p *Pool) buildStandbysSnapshot() {
	var standbys []*Backend
	for _, b := range p.backends {
		if b.Role == RoleStandby {
			standbys = append(standbys, b)
		}
	}
	p.standbysSnapshot.Store(&standbys)
}

func (p *Pool) Standbys() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*Backend
	for _, b := range p.backends {
		if b.Role == RoleStandby {
			result = append(result, b)
		}
	}
	return result
}

func (p *Pool) Get(name string) (*Backend, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	b := p.backends[name]
	if b == nil {
		return nil, fmt.Errorf("backend %q not found", name)
	}
	return b, nil
}

func (p *Pool) List() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		result = append(result, b)
	}
	return result
}

func (p *Pool) Promote(name string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	newPrimary := p.backends[name]
	if newPrimary == nil {
		return "", fmt.Errorf("backend %q not found", name)
	}

	oldPrimary := p.primary.Load()
	if oldPrimary == nil {
		return "", errors.New("no current primary")
	}

	if oldPrimary == newPrimary {
		return "", nil
	}

	if !newPrimary.IsConnected() {
		if err := newPrimary.Connect(p.ctx); err != nil {
			return "", fmt.Errorf("promote %q: connect: %w", name, err)
		}
	}

	p.primary.Store(newPrimary)
	oldPrimary.SetRole(RoleStandby)
	newPrimary.SetRole(RolePrimary)

	p.buildStandbysSnapshot()

	if !oldPrimary.IsConnected() {
		go p.reconnectLoop(oldPrimary)
	}

	p.logger.Info("promoted backend to primary", "name", name, "demoted", oldPrimary.Name)
	return oldPrimary.Name, nil
}

func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cancel()
	p.primary.Store(nil)

	var errs []error
	for _, b := range p.backends {
		if err := b.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Pool) reconnectLoop(b *Backend) {
	if err := b.Reconnect(p.ctx, p.logger); err != nil {
		if !errors.Is(err, context.Canceled) {
			p.logger.Error("reconnect loop exiting", "name", b.Name, "err", err)
		}
		return
	}

	if b.Role == RolePrimary {
		p.primary.Store(b)
	}
}
