package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type probeFunc func(context.Context) error

func (p probeFunc) Check(ctx context.Context) error { return p(ctx) }

func TestLivenessNeverChecksDependencies(t *testing.T) {
	var called atomic.Int32
	h := NewHandler(probeFunc(func(context.Context) error { called.Add(1); return errors.New("sensitive failure") }), time.Second)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if r.Code != http.StatusOK || called.Load() != 0 {
		t.Fatalf("liveness=%d probes=%d", r.Code, called.Load())
	}
}

func TestReadinessIsFailClosedWithoutPublishingDependencyErrors(t *testing.T) {
	var healthy atomic.Bool
	h := NewHandler(probeFunc(func(context.Context) error {
		if !healthy.Load() {
			return errors.New("postgres://username:secret-canary@localhost/memx_test")
		}
		return nil
	}), 20*time.Millisecond)
	for _, tt := range []struct {
		ready bool
		code  int
		body  string
	}{
		{false, http.StatusServiceUnavailable, "not_ready"},
		{true, http.StatusOK, "ready"},
	} {
		healthy.Store(tt.ready)
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if r.Code != tt.code || !strings.Contains(r.Body.String(), tt.body) || strings.Contains(r.Body.String(), "secret-canary") {
			t.Fatalf("status=%d body=%q", r.Code, r.Body.String())
		}
	}
}

func TestReadinessTimesOut(t *testing.T) {
	h := NewHandler(probeFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), 5*time.Millisecond)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", r.Code)
	}
}

func TestNonCooperativeProbeCannotHangOrFanOutReadiness(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := NewHandler(probeFunc(func(context.Context) error {
		calls.Add(1)
		<-release // deliberately ignores cancellation
		return nil
	}), 10*time.Millisecond)
	for i := 0; i < 2; i++ {
		started := time.Now()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if r.Code != http.StatusServiceUnavailable || time.Since(started) > time.Second {
			t.Fatalf("probe blocked or reported ready: status=%d", r.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("started %d stalled probes; want at most one", calls.Load())
	}
}

func TestOnlyHealthRoutesAndMethods(t *testing.T) {
	h := NewHandler(probeFunc(func(context.Context) error { return nil }), time.Second)
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/facts", http.StatusNotFound},
		{http.MethodPost, "/livez", http.StatusMethodNotAllowed},
		{http.MethodPost, "/readyz", http.StatusMethodNotAllowed},
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(tt.method, tt.path, nil))
		if r.Code != tt.want {
			t.Errorf("%s %s: got %d", tt.method, tt.path, r.Code)
		}
	}
}
