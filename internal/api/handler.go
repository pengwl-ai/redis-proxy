package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/example/redis-proxy/internal/backend"
)

type Handler struct {
	pool   *backend.Pool
	logger *slog.Logger
}

func NewHandler(pool *backend.Pool, logger *slog.Logger) *Handler {
	return &Handler{pool: pool, logger: logger}
}

func (h *Handler) SetupRoutes(r *gin.Engine) {
	pprofGroup := r.Group("/debug/pprof")
	{
		pprofGroup.GET("/", gin.WrapH(http.HandlerFunc(pprof.Index)))
		pprofGroup.GET("/cmdline", gin.WrapH(http.HandlerFunc(pprof.Cmdline)))
		pprofGroup.GET("/profile", gin.WrapH(http.HandlerFunc(pprof.Profile)))
		pprofGroup.GET("/symbol", gin.WrapH(http.HandlerFunc(pprof.Symbol)))
		pprofGroup.GET("/trace", gin.WrapH(http.HandlerFunc(pprof.Trace)))
		pprofGroup.GET("/:name", gin.WrapH(pprof.Handler("")))
		pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}

	v1 := r.Group("/api/v1")
	{
		v1.GET("/backends", h.ListBackends)
		v1.PUT("/backends/:name/promote", h.PromoteBackend)
		v1.GET("/health", h.HealthCheck)
	}
}

type backendResponse struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"`
	Role  string `json:"role"`
	Alive bool   `json:"alive"`
}

func (h *Handler) ListBackends(c *gin.Context) {
	backends := h.pool.List()

	resp := make([]backendResponse, 0, len(backends))
	for _, b := range backends {
		resp = append(resp, backendResponse{
			Name:  b.Name,
			Addr:  b.Addr,
			Role:  string(b.Role),
			Alive: b.IsConnected(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"backends": resp})
}

func (h *Handler) PromoteBackend(c *gin.Context) {
	name := c.Param("name")
	demoted, err := h.pool.Promote(name)
	if err != nil {
		h.logger.Error("promote failed", "name", name, "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("backend promoted via API", "new_primary", name, "demoted", demoted)
	c.JSON(http.StatusOK, gin.H{
		"promoted": name,
		"demoted":  demoted,
	})
}

func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Serve(ctx context.Context, addr string) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h.SetupRoutes(r)

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	h.logger.Info("API server listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
