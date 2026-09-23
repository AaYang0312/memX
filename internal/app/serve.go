// Package app manages the exploratory HTTP server's bounded shutdown.
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

var ErrShutdown = errors.New("server shutdown failed")
var ErrInvalidServer = errors.New("invalid server configuration")

// Serve stops accepting new requests on cancellation and lets in-flight
// requests finish within grace. Caller must supply a loopback listener.
func Serve(ctx context.Context, listener net.Listener, server *http.Server, grace time.Duration) error {
	if ctx == nil || listener == nil || server == nil || grace <= 0 {
		return ErrInvalidServer
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return ErrShutdown // never return a raw listener error with an address
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			<-finished
			return ErrShutdown
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return ErrShutdown
		}
		return nil
	}
}
