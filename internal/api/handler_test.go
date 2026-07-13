package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"

	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/config"
)

func setupHandler(t *testing.T) (*Handler, *miniredis.Miniredis, func()) {
	t.Helper()

	mr1 := miniredis.RunT(t)
	mr2 := miniredis.RunT(t)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	entries := []config.BackendEntry{
		{Name: "dc1", Addr: mr1.Addr(), Role: "primary"},
		{Name: "dc2", Addr: mr2.Addr(), Role: "standby"},
	}

	pool, err := backend.NewPool(context.Background(), entries, logger)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	handler := NewHandler(pool, logger)

	cleanup := func() {
		pool.Close()
		mr1.Close()
		mr2.Close()
	}

	return handler, mr1, cleanup
}

func setupRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.SetupRoutes(r)
	return r
}

func TestHealthCheck(t *testing.T) {
	h, _, cleanup := setupHandler(t)
	defer cleanup()

	r := setupRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("expected ok, got %q", body["status"])
	}
}

func TestListBackends(t *testing.T) {
	h, _, cleanup := setupHandler(t)
	defer cleanup()

	r := setupRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/backends", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body struct {
		Backends []backendResponse `json:"backends"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(body.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(body.Backends))
	}

	if body.Backends[0].Name != "dc1" {
		t.Errorf("expected dc1, got %q", body.Backends[0].Name)
	}
	if body.Backends[0].Role != "primary" {
		t.Errorf("expected primary, got %q", body.Backends[0].Role)
	}
	if !body.Backends[0].Alive {
		t.Error("expected dc1 alive")
	}
}

func TestPromoteBackend(t *testing.T) {
	h, _, cleanup := setupHandler(t)
	defer cleanup()

	r := setupRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/backends/dc2/promote", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["promoted"] != "dc2" {
		t.Errorf("expected promoted=dc2, got %q", body["promoted"])
	}
	if body["demoted"] != "dc1" {
		t.Errorf("expected demoted=dc1, got %q", body["demoted"])
	}
}

func TestPromoteBackendInvalidName(t *testing.T) {
	h, _, cleanup := setupHandler(t)
	defer cleanup()

	r := setupRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/backends/nonexistent/promote", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
