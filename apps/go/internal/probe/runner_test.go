package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
)

type fakeQuoter struct {
	mu       sync.Mutex
	quoteErr error
	delay    time.Duration
	calls    []time.Time
	builds   int
}

func (f *fakeQuoter) Name() string                                   { return "fake" }
func (f *fakeQuoter) Supports(context.Context, string) (bool, error) { return true, nil }

func (f *fakeQuoter) Quote(ctx context.Context, _ aggregator.QuoteRequest) (*aggregator.Quote, error) {
	f.mu.Lock()
	f.calls = append(f.calls, time.Now())
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.quoteErr != nil {
		return nil, f.quoteErr
	}
	return &aggregator.Quote{Provider: "1inch", Routes: 2, AmountOut: "234560000", PriceImpactRatio: -0.007, StepRefs: []string{"s1"}}, nil
}

func (f *fakeQuoter) BuildTx(context.Context, string) (*aggregator.UnsignedTx, error) {
	f.mu.Lock()
	f.builds++
	f.mu.Unlock()
	return &aggregator.UnsignedTx{}, nil
}

func newRunner(q aggregator.Quoter, sc []Scenario, timeout time.Duration) (*Runner, *Metrics) {
	m := NewMetrics(prometheus.NewRegistry())
	return NewRunner(q, sc, time.Minute, timeout, m, slog.New(slog.NewTextHandler(io.Discard, nil))), m
}

func TestProbeRecordsResults(t *testing.T) {
	sc := Scenario{Name: "s", BuildTx: true}
	cases := []struct {
		name   string
		q      *fakeQuoter
		result string
		builds int
	}{
		{"ok", &fakeQuoter{}, "ok", 1},
		{"rate limited", &fakeQuoter{quoteErr: fmt.Errorf("%w: %w", aggregator.ErrRateLimited, &aggregator.HTTPError{Status: 429})}, "http_429", 0},
		{"server error", &fakeQuoter{quoteErr: &aggregator.HTTPError{Status: 502}}, "http_5xx", 0},
		{"timeout", &fakeQuoter{delay: time.Second}, "timeout", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, m := newRunner(tc.q, []Scenario{sc}, 50*time.Millisecond)
			r.probe(context.Background(), sc)

			if got := testutil.ToFloat64(m.probes.WithLabelValues("fake", "s", stepQuote, tc.result)); got != 1 {
				t.Fatalf("swap_probe_total{result=%q} = %v, want 1", tc.result, got)
			}
			if tc.q.builds != tc.builds {
				t.Fatalf("builds = %d, want %d", tc.q.builds, tc.builds)
			}
			wantWinner := 0.0
			if tc.result == "ok" {
				wantWinner = 1
			}
			if got := testutil.ToFloat64(m.winner.WithLabelValues("fake", "s", "1inch")); got != wantWinner {
				t.Fatalf("winner = %v, want %v", got, wantWinner)
			}
		})
	}
}

func TestRunStaggersScenarios(t *testing.T) {
	q := &fakeQuoter{}
	sc := []Scenario{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	r, _ := newRunner(q, sc, time.Second)
	r.interval = 300 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r.Run(ctx)

	if len(q.calls) != 3 {
		t.Fatalf("calls in first interval = %d, want 3 (one per scenario)", len(q.calls))
	}
	for i := 1; i < len(q.calls); i++ {
		if gap := q.calls[i].Sub(q.calls[i-1]); gap < 60*time.Millisecond {
			t.Errorf("gap between scenario starts = %v, want about 100ms", gap)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]error{
		"ok":               nil,
		"http_4xx":         &aggregator.HTTPError{Status: 401},
		"timeout":          context.DeadlineExceeded,
		"decode_error":     fmt.Errorf("%w: x", aggregator.ErrDecode),
		"no_route":         aggregator.ErrNoRoute,
		"client_throttled": fmt.Errorf("%w: wait", aggregator.ErrThrottled),
		"error":            errors.New("boom"),
	}
	for want, err := range cases {
		if got := Classify(err); got != want {
			t.Errorf("Classify(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestParseAmountHandlesBigIntegers(t *testing.T) {
	if v, ok := parseAmount("100000000000000000000000000000"); !ok || v < 1e29 {
		t.Fatalf("parseAmount = %v, %v", v, ok)
	}
	if _, ok := parseAmount("abc"); ok {
		t.Fatal("parseAmount accepted non-number")
	}
}

func TestShutdownMidProbeRecordsNothing(t *testing.T) {
	q := &fakeQuoter{delay: time.Second}
	sc := Scenario{Name: "s"}
	r, m := newRunner(q, []Scenario{sc}, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	r.probe(ctx, sc)
	if n := testutil.CollectAndCount(m.probes); n != 0 {
		t.Fatalf("recorded %d series during shutdown, want 0", n)
	}
}
