// Package health serves process liveness and dependency readiness separately.
// It does not expose business routes or internal dependency errors.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Probe interface{ Check(context.Context) error }

type status struct {
	Status string `json:"status"`
}

func NewHandler(probe Probe, timeout time.Duration) http.Handler {
	// At most one probe may be in flight. If an adapter ignores cancellation,
	// readiness remains unavailable without spawning unbounded goroutines.
	permit := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK, "alive")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if probe == nil || timeout <= 0 {
			respond(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		select {
		case permit <- struct{}{}:
		default:
			respond(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			defer func() { <-permit }()
			result <- probe.Check(ctx)
		}()
		select {
		case err := <-result:
			if err == nil && ctx.Err() == nil {
				respond(w, http.StatusOK, "ready")
				return
			}
		case <-ctx.Done():
		}
		respond(w, http.StatusServiceUnavailable, "not_ready")
	})
	return mux
}

func respond(w http.ResponseWriter, code int, value string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(status{Status: value})
}
