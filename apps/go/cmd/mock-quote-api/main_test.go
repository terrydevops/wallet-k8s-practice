package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/sign"
)

func newHandler(mode string, slow time.Duration) *handler {
	return &handler{
		creds: sign.Credentials{AccessID: "id", Secret: "s"}, mode: mode, slowDelay: slow,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "x"}, []string{"route", "code"}),
		nonces:   map[string]time.Time{},
	}
}

func signedRequest(ctx context.Context, method, path string) *http.Request {
	r := httptest.NewRequestWithContext(ctx, method, path, nil)
	for k, v := range sign.New(sign.Credentials{AccessID: "id", Secret: "s"}).Headers(method, path, nil) {
		r.Header.Set(k, v)
	}
	return r
}

func TestModesDegradeOnlyQuoteEndpoints(t *testing.T) {
	for mode, want := range map[string]int{"rate_limited": http.StatusTooManyRequests, "server_error": http.StatusBadGateway} {
		h := newHandler(mode, 0)
		if got := h.respond(httptest.NewRecorder(), signedRequest(context.Background(), "GET", "/amber-api/v1/domains"), "domains.json", false); got != http.StatusOK {
			t.Errorf("%s: domains = %d, want 200", mode, got)
		}
		if got := h.respond(httptest.NewRecorder(), signedRequest(context.Background(), "POST", "/amber-api/v1/route"), "route.json", true); got != want {
			t.Errorf("%s: route = %d, want %d", mode, got, want)
		}
	}
}

func TestSlowModeHonoursClientCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	h := newHandler("slow", time.Minute)
	if got := h.respond(httptest.NewRecorder(), signedRequest(ctx, "POST", "/amber-api/v1/route"), "route.json", true); got != http.StatusRequestTimeout {
		t.Fatalf("code = %d", got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("slow mode ignored the request context")
	}
}

func TestRejectsReplayedNonce(t *testing.T) {
	h := newHandler("ok", 0)
	r := signedRequest(context.Background(), "GET", "/amber-api/v1/domains")
	if got := h.respond(httptest.NewRecorder(), r, "domains.json", false); got != http.StatusOK {
		t.Fatalf("first = %d", got)
	}
	if got := h.respond(httptest.NewRecorder(), r, "domains.json", false); got != http.StatusUnauthorized {
		t.Fatalf("replay = %d, want 401", got)
	}
}
