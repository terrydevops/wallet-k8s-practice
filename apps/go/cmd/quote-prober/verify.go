package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/config"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/probe"
)

// verify is the post-deploy check: it polls a running prober's /metrics until every scenario has
// a recent successful quote, or fails when the timeout expires. Same binary and image as the prober,
// so the check is released and signed together with it.
//
//	quote-prober verify -url http://quote-prober:8080/metrics -max-age 3m -timeout 4m
func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	url := fs.String("url", "http://quote-prober:8080/metrics", "metrics endpoint of the prober to check")
	maxAge := fs.Duration("max-age", 3*time.Minute, "a scenario passes with a successful quote at most this old")
	timeout := fs.Duration("timeout", 4*time.Minute, "give up after this long")
	every := fs.Duration("every", 5*time.Second, "poll interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	scenarios, err := config.LoadScenarios(orDefault(os.Getenv("SCENARIOS_FILE"), "scenarios.yaml"))
	if err != nil {
		return fmt.Errorf("scenarios: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	var stale []string
	for {
		last, err := scrape(ctx, client, *url)
		if err == nil {
			if stale = probe.Stale(last, scenarios, time.Now(), *maxAge); len(stale) == 0 {
				slog.Info("verify ok", "scenarios", len(scenarios), "max_age", maxAge.String())
				return nil
			}
			slog.Info("waiting for successful quotes", "stale", stale)
		} else {
			slog.Info("metrics not available yet", "err", err)
		}
		select {
		case <-ctx.Done():
			if stale == nil {
				return fmt.Errorf("no metrics from %s within %s", *url, timeout.String())
			}
			return fmt.Errorf("no successful quote within %s for %v", timeout.String(), stale)
		case <-time.After(*every):
		}
	}
}

func scrape(ctx context.Context, c *http.Client, url string) (map[string]time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req) //nolint:gosec // url is the operator's flag, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, errors.New(resp.Status)
	}
	return probe.LastSuccess(resp.Body)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
