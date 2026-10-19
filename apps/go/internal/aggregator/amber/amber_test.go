package amber

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/fixtures"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/sign"
)

var testCreds = sign.Credentials{AccessID: "id", Secret: "secret"}

// fakeAPI verifies every request's signature and serves the fixture for its path.
func fakeAPI(t *testing.T, status int) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sign.Verify(r.Header, r.Method, r.URL.Path, r.URL.Query(), testCreds, time.Now(), time.Minute); err != nil {
			t.Errorf("%s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				var m map[string]any
				if err := json.Unmarshal(b, &m); err != nil {
					t.Errorf("request body is not JSON: %v", err)
				}
				bodies = append(bodies, m)
			}
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		name := map[string]string{pathDomains: "domains.json", pathRoute: "route.json", pathStep: "step.json"}[r.URL.Path]
		b, err := fixtures.FS.ReadFile(name)
		if err != nil {
			t.Errorf("no fixture for %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: url, Credentials: testCreds, RPS: 1000, Timeout: 2 * time.Second}, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestQuote(t *testing.T) {
	srv, bodies := fakeAPI(t, http.StatusOK)
	c := newTestClient(t, srv.URL)

	q, err := c.Quote(context.Background(), aggregator.QuoteRequest{
		From:   aggregator.Asset{Chain: "base", Token: "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"},
		To:     aggregator.Asset{Chain: "base", Token: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
		Amount: "100000000000000000", Address: "0x0000000000000000000000000000000000000001", SlippagePct: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Provider != "1inch" || q.Routes != 2 || q.Steps != 1 || q.AmountOut != "234560000" || q.MinOut != "232214400" ||
		math.Abs(q.PriceImpactRatio-(-0.007189422017)) > 1e-12 || len(q.StepRefs) != 1 || q.StepRefs[0] != "step-1" || q.ExpiresAt.IsZero() {
		t.Fatalf("unexpected quote: %+v", q)
	}
	got := (*bodies)[0]
	for k, want := range map[string]any{"fromDomain": "base", "toDomain": "base", "amount": "100000000000000000", "slippage": "1", "sortBy": "outcome", "contractCall": false} {
		if got[k] != want {
			t.Errorf("request %s = %v, want %v", k, got[k], want)
		}
	}
}

func TestBuildTxKeepsPayloadRaw(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusOK)
	tx, err := newTestClient(t, srv.URL).BuildTx(context.Background(), "step-1")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Chain != "base" || !json.Valid(tx.Payload) {
		t.Fatalf("unexpected tx: chain=%q payload=%s", tx.Chain, tx.Payload)
	}
}

func TestSupportsCachesDomains(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusOK)
	c := newTestClient(t, srv.URL)
	for chain, want := range map[string]bool{"base": true, "ethereum": true, "tron": false} {
		got, err := c.Supports(context.Background(), chain)
		if err != nil || got != want {
			t.Errorf("Supports(%s) = %v, %v; want %v", chain, got, err, want)
		}
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusTooManyRequests, func(err error) bool { return errors.Is(err, aggregator.ErrRateLimited) }},
		{http.StatusInternalServerError, func(err error) bool {
			var h *aggregator.HTTPError
			return errors.As(err, &h) && h.Status == 500
		}},
	}
	for _, tc := range cases {
		srv, _ := fakeAPI(t, tc.status)
		_, err := newTestClient(t, srv.URL).Quote(context.Background(), aggregator.QuoteRequest{})
		if !tc.check(err) {
			t.Errorf("status %d: unexpected error %v", tc.status, err)
		}
	}
}

func TestNoRoute(t *testing.T) {
	if _, err := fromRouteResponse(routeResponse{}); !errors.Is(err, aggregator.ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
}

func TestOddExpirationDateDoesNotFailQuote(t *testing.T) {
	var resp routeResponse
	body := `{"routes":[{"id":"r","expirationDate":"","steps":[{"id":"s","to":{"amount":"1","minAmountOut":"1"},"provider":{"id":"p"}}]}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	q, err := fromRouteResponse(resp)
	if err != nil || !q.ExpiresAt.IsZero() || q.Provider != "p" {
		t.Fatalf("quote=%+v err=%v", q, err)
	}
}

func TestLimiterWaitBeyondDeadlineIsThrottled(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusOK)
	c, err := New(Config{BaseURL: srv.URL, Credentials: testCreds, RPS: 0.01, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Quote(ctx, aggregator.QuoteRequest{}); err != nil { // uses the only token
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := c.Quote(ctx, aggregator.QuoteRequest{}); !errors.Is(err, aggregator.ErrThrottled) {
		t.Fatalf("want ErrThrottled, got %v", err)
	}
}
