package backend

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/example/redis-proxy/internal/config"
)

func TestNewPoolValid(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "primary1", Addr: mr.Addr(), Role: "primary"},
		{Name: "standby1", Addr: "127.0.0.1:19999", Role: "standby"},
	}

	pool, err := NewPool(context.Background(), entries, logger)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	defer pool.Close()

	primary := pool.Primary()
	if primary == nil {
		t.Fatal("expected non-nil primary")
	}
	if primary.Name != "primary1" {
		t.Errorf("expected primary1, got %q", primary.Name)
	}

	b, err := pool.Get("primary1")
	if err != nil {
		t.Fatalf("Get primary1: %v", err)
	}
	if b.Role != RolePrimary {
		t.Errorf("expected primary role, got %s", b.Role)
	}
}

func TestNewPoolMissingPrimary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "s1", Addr: "127.0.0.1:6379", Role: "standby"},
	}

	_, err := NewPool(context.Background(), entries, logger)
	if err == nil {
		t.Fatal("expected error for missing primary")
	}
}

func TestNewPoolPrimaryUnreachable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "primary1", Addr: "127.0.0.1:19999", Role: "primary"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := NewPool(ctx, entries, logger)
	if err == nil {
		t.Fatal("expected error for unreachable primary")
	}
}

func TestPoolPromote(t *testing.T) {
	mr1 := miniredis.RunT(t)
	defer mr1.Close()
	mr2 := miniredis.RunT(t)
	defer mr2.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "dc1", Addr: mr1.Addr(), Role: "primary"},
		{Name: "dc2", Addr: mr2.Addr(), Role: "standby"},
	}

	pool, err := NewPool(context.Background(), entries, logger)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	defer pool.Close()

	primary := pool.Primary()
	if primary.Name != "dc1" {
		t.Fatalf("expected dc1 as primary, got %q", primary.Name)
	}

	demoted, err := pool.Promote("dc2")
	if err != nil {
		t.Fatalf("Promote failed: %v", err)
	}
	if demoted != "dc1" {
		t.Errorf("expected demoted=dc1, got %q", demoted)
	}

	newPrimary := pool.Primary()
	if newPrimary.Name != "dc2" {
		t.Errorf("expected dc2 as primary, got %q", newPrimary.Name)
	}
	if newPrimary.Role != RolePrimary {
		t.Errorf("expected dc2 role=primary, got %s", newPrimary.Role)
	}

	dc1, _ := pool.Get("dc1")
	if dc1.Role != RoleStandby {
		t.Errorf("expected dc1 role=standby, got %s", dc1.Role)
	}
}

func TestPoolPromoteInvalidName(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "dc1", Addr: mr.Addr(), Role: "primary"},
	}

	pool, err := NewPool(context.Background(), entries, logger)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	defer pool.Close()

	_, err = pool.Promote("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent backend")
	}
}

func TestPoolList(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "dc1", Addr: mr.Addr(), Role: "primary"},
	}

	pool, err := NewPool(context.Background(), entries, logger)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	defer pool.Close()

	list := pool.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(list))
	}
	if list[0].Name != "dc1" {
		t.Errorf("expected dc1, got %q", list[0].Name)
	}
}
