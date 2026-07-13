package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/redis-proxy/internal/api"
	"github.com/example/redis-proxy/internal/backend"
	"github.com/example/redis-proxy/internal/config"
	"github.com/example/redis-proxy/internal/proxy"
)

func main() {
	configPath := flag.String("config", "./config.yaml", "path to YAML config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "path", *configPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := backend.NewPool(ctx, cfg.Backends, logger.With("component", "pool"))
	if err != nil {
		logger.Error("failed to initialize backend pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	for _, b := range pool.List() {
		logger.Info("backend registered",
			"name", b.Name,
			"addr", b.Addr,
			"role", b.Role,
			"connected", b.IsConnected(),
		)
	}

	proxyServer := proxy.NewServer(cfg.Proxy, pool, logger.With("component", "proxy"))
	apiHandler := api.NewHandler(pool, logger.With("component", "api"))

	errCh := make(chan error, 2)

	go func() {
		errCh <- proxyServer.ListenAndServe(ctx)
	}()

	go func() {
		errCh <- apiHandler.Serve(ctx, cfg.Proxy.APIListen)
	}()

	logger.Info("redis-proxy started",
		"proxy_addr", cfg.Proxy.Listen,
		"api_addr", cfg.Proxy.APIListen,
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != nil {
			logger.Error("server error, shutting down", "err", err)
		}
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("proxy shutdown error", "err", err)
	}

	pool.Close()
	logger.Info("redis-proxy stopped")
}
