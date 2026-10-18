// Command quote-prober probes a DEX aggregator's quote API and exposes Prometheus metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/config"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/httpserver"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/probe"
)

func main() {
	if err := run(); err != nil {
		slog.Error("quote-prober stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	scenarios, err := config.LoadScenarios(cfg.ScenariosFile)
	if err != nil {
		return fmt.Errorf("scenarios: %w", err)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	quoter, err := amber.New(amber.Config{
		BaseURL: cfg.AmberBaseURL, Credentials: cfg.AmberCreds, RPS: cfg.AmberRPS, Timeout: cfg.Timeout,
	}, reg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var ready atomic.Bool
	srv := httpserver.New(cfg.ListenAddr, reg, ready.Load, nil)
	runner := probe.NewRunner(quoter, scenarios, cfg.Interval, cfg.Timeout, probe.NewMetrics(reg), log)

	log.Info("starting", "aggregator", quoter.Name(), "scenarios", len(scenarios), "interval", cfg.Interval.String(), "listen", cfg.ListenAddr)
	var wg sync.WaitGroup
	var startErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Serve /healthz while checking chains, so a slow or failing API does not crash-loop the pod.
		if startErr = checkChains(ctx, quoter, scenarios, log); startErr != nil {
			stop()
			return
		}
		ready.Store(true)
		runner.Run(ctx)
	}()

	err = httpserver.Run(ctx, srv, 10*time.Second)
	stop()
	wg.Wait()
	if startErr != nil && !errors.Is(startErr, context.Canceled) { // cancelled = shut down during startup
		return startErr
	}
	return err
}

// checkChains verifies that the aggregator supports every chain in the scenarios. API errors are
// retried with backoff; only a definite "not supported" is fatal.
func checkChains(ctx context.Context, q aggregator.Quoter, scenarios []probe.Scenario, log *slog.Logger) error {
	backoff := 5 * time.Second
	for _, s := range scenarios {
		for _, chain := range []string{s.Request.From.Chain, s.Request.To.Chain} {
			for {
				ok, err := q.Supports(ctx, chain)
				if err == nil && !ok {
					return fmt.Errorf("scenario %q: chain %q not supported by %s", s.Name, chain, q.Name())
				}
				if err == nil {
					break
				}
				log.Warn("checking supported chains failed; retrying", "chain", chain, "err", err, "in", backoff.String())
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}
				backoff = min(2*backoff, time.Minute)
			}
		}
	}
	return nil
}
