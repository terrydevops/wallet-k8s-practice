// Command mock-quote-api fakes the aggregator's quote API for local runs and CI. It verifies the
// same signed headers the real API expects, rejects replayed nonces, and serves the fixtures.
//
// MOCK_MODE selects how the quote endpoints (route, route/step) behave, to exercise alerts:
// ok (default) | rate_limited | server_error | slow. The domains endpoint always answers normally, so a
// prober can start and the failure shows up in its probe metrics rather than in its startup.
// MOCK_SLOW_DELAY sets the delay of "slow" mode (default 15s, longer than the default probe timeout).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/fixtures"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/sign"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/httpserver"
)

const maxSkew = 5 * time.Minute

// route pattern -> fixture, and whether MOCK_MODE applies to it.
var routes = map[string]struct {
	file     string
	degraded bool
}{
	"GET /amber-api/v1/domains":     {"domains.json", false},
	"POST /amber-api/v1/route":      {"route.json", true},
	"POST /amber-api/v1/route/step": {"step.json", true},
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	creds := sign.Credentials{AccessID: getenv("MOCK_ACCESS_ID", "mock-id"), Secret: getenv("MOCK_HMAC_SECRET", "mock-secret")}
	mode := getenv("MOCK_MODE", "ok")
	slowDelay, err := time.ParseDuration(getenv("MOCK_SLOW_DELAY", "15s"))
	if err != nil {
		log.Error("bad MOCK_SLOW_DELAY", "err", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mock_quote_api_requests_total", Help: "Requests by route and response code.",
	}, []string{"route", "code"})
	reg.MustRegister(requests)

	h := &handler{creds: creds, mode: mode, slowDelay: slowDelay, log: log, requests: requests, nonces: map[string]time.Time{}}
	srv := httpserver.New(getenv("LISTEN_ADDR", ":8081"), reg, nil, func(mux *http.ServeMux) {
		for pattern, rt := range routes {
			mux.Handle(pattern, h.serve(pattern, rt.file, rt.degraded))
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("starting", "listen", srv.Addr, "mode", mode)
	if err := httpserver.Run(ctx, srv, 5*time.Second); err != nil {
		log.Error("mock-quote-api stopped", "err", err)
		os.Exit(1)
	}
}

type handler struct {
	creds     sign.Credentials
	mode      string
	slowDelay time.Duration
	log       *slog.Logger
	requests  *prometheus.CounterVec

	mu     sync.Mutex
	nonces map[string]time.Time // seen nonces, kept for maxSkew
}

func (h *handler) serve(route, file string, degraded bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := h.respond(w, r, file, degraded)
		h.requests.WithLabelValues(route, http.StatusText(code)).Inc()
	})
}

func (h *handler) respond(w http.ResponseWriter, r *http.Request, file string, degraded bool) int {
	now := time.Now()
	if err := sign.Verify(r.Header, r.Method, r.URL.Path, r.URL.Query(), h.creds, now, maxSkew); err != nil {
		h.log.Warn("rejected", "path", r.URL.Path, "err", err)
		return writeStatus(w, http.StatusUnauthorized)
	}
	if !h.firstUse(r.Header.Get(sign.HeaderNonce), now) {
		return writeStatus(w, http.StatusUnauthorized)
	}
	if degraded {
		switch h.mode {
		case "rate_limited":
			return writeStatus(w, http.StatusTooManyRequests)
		case "server_error":
			return writeStatus(w, http.StatusBadGateway)
		case "slow":
			select {
			case <-r.Context().Done(): // client gave up; nothing to write
				return http.StatusRequestTimeout
			case <-time.After(h.slowDelay):
			}
		}
	}
	b, err := fixtures.FS.ReadFile(file)
	if err != nil {
		return writeStatus(w, http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
	return http.StatusOK
}

// firstUse records the nonce and reports whether it was new. Old nonces expire after maxSkew,
// because a request older than that is rejected on its date anyway.
func (h *handler) firstUse(nonce string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for n, t := range h.nonces {
		if now.Sub(t) > maxSkew {
			delete(h.nonces, n)
		}
	}
	if _, seen := h.nonces[nonce]; seen {
		return false
	}
	h.nonces[nonce] = now
	return true
}

func writeStatus(w http.ResponseWriter, code int) int {
	w.WriteHeader(code)
	return code
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
