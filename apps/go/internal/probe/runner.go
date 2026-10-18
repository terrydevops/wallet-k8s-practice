// Package probe runs swap scenarios against an aggregator on a schedule and records the results as
// Prometheus metrics. It depends only on the aggregator.Quoter interface.
package probe

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
)

// Scenario is one swap to probe.
type Scenario struct {
	Name    string                  `yaml:"name"`
	Request aggregator.QuoteRequest `yaml:"request"`
	BuildTx bool                    `yaml:"buildTx"`
}

const (
	stepQuote   = "quote"
	stepBuildTx = "build_tx"
)

// Runner probes every scenario once per interval. Scenario i starts at offset i*interval/n, so
// requests are spread evenly instead of bursting at the top of the interval.
type Runner struct {
	q         aggregator.Quoter
	scenarios []Scenario
	interval  time.Duration
	timeout   time.Duration
	m         *Metrics
	log       *slog.Logger
	now       func() time.Time
}

func NewRunner(q aggregator.Quoter, sc []Scenario, interval, timeout time.Duration, m *Metrics, log *slog.Logger) *Runner {
	return &Runner{q: q, scenarios: sc, interval: interval, timeout: timeout, m: m, log: log, now: time.Now}
}

// Run blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, s := range r.scenarios {
		offset := time.Duration(i) * r.interval / time.Duration(len(r.scenarios))
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.loop(ctx, s, offset)
		}()
	}
	wg.Wait()
}

func (r *Runner) loop(ctx context.Context, s Scenario, offset time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(offset):
	}
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.probe(ctx, s)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Runner) probe(ctx context.Context, s Scenario) {
	agg := r.q.Name()
	quote, err := timed(ctx, r, s, stepQuote, func(ctx context.Context) (*aggregator.Quote, error) {
		return r.q.Quote(ctx, s.Request)
	})
	if err != nil {
		return
	}
	r.m.lastSuccess.WithLabelValues(agg, s.Name).Set(float64(r.now().Unix()))
	r.m.winner.WithLabelValues(agg, s.Name, quote.Provider).Inc()
	r.m.routes.WithLabelValues(agg, s.Name).Set(float64(quote.Routes))
	r.m.priceImpact.WithLabelValues(agg, s.Name).Set(quote.PriceImpactPct)
	if v, ok := parseAmount(quote.AmountOut); ok {
		r.m.amountOut.WithLabelValues(agg, s.Name).Set(v)
	}

	if s.BuildTx && len(quote.StepRefs) > 0 {
		_, _ = timed(ctx, r, s, stepBuildTx, func(ctx context.Context) (*aggregator.UnsignedTx, error) {
			return r.q.BuildTx(ctx, quote.StepRefs[0])
		})
	}
}

// timed runs one step with a timeout and records its result and latency.
func timed[T any](parent context.Context, r *Runner, s Scenario, step string, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	start := r.now()
	v, err := f(ctx)
	if parent.Err() != nil {
		// Shutting down: an interrupted probe says nothing about the aggregator, so record nothing.
		return v, err
	}
	result := Classify(err)
	agg := r.q.Name()
	r.m.duration.WithLabelValues(agg, s.Name, step).Observe(r.now().Sub(start).Seconds())
	r.m.probes.WithLabelValues(agg, s.Name, step, result).Inc()
	if err != nil {
		r.log.Warn("probe failed", "aggregator", agg, "scenario", s.Name, "step", step, "result", result, "err", err)
	}
	return v, err
}

// Classify maps an error to a fixed, low-cardinality result label.
func Classify(err error) string {
	var httpErr *aggregator.HTTPError
	var netErr net.Error
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, aggregator.ErrRateLimited):
		return "http_429"
	case errors.Is(err, aggregator.ErrThrottled):
		return "client_throttled"
	case errors.As(err, &httpErr) && httpErr.Status >= http.StatusInternalServerError:
		return "http_5xx"
	case errors.As(err, &httpErr):
		return "http_4xx"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, aggregator.ErrDecode):
		return "decode_error"
	case errors.Is(err, aggregator.ErrNoRoute):
		return "no_route"
	case errors.Is(err, aggregator.ErrUnsupported):
		return "unsupported"
	default:
		return "error"
	}
}
