// memx-api in personal-development mode is a loopback-only health prototype,
// NOT the production Task 1 service. It has no storage, auth or business API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AaYang0312/memX/internal/app"
	"github.com/AaYang0312/memX/internal/config"
	"github.com/AaYang0312/memX/internal/health"
)

type unavailableProbe struct{}

func (unavailableProbe) Check(context.Context) error {
	return errors.New("canonical dependency not implemented")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Parse(os.Environ())
	if err != nil {
		logger.Error("startup rejected", "reason_code", "invalid_config")
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("startup rejected", "reason_code", "listener_unavailable")
		os.Exit(1)
	}
	server := &http.Server{
		Handler:           health.NewHandler(unavailableProbe{}, 100*time.Millisecond),
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       10 * time.Second,
		WriteTimeout:      5 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("synthetic health-only listener started") // no address, DSN or secret
	if err := app.Serve(ctx, listener, server, 5*time.Second); err != nil {
		logger.Error("server stopped unexpectedly", "reason_code", "serve_failed")
		os.Exit(1)
	}
	logger.Info("listener stopped")
}
