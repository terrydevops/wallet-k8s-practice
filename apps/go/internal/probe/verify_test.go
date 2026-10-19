package probe

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const exposition = `# HELP swap_probe_last_success_timestamp_seconds Unix time of the last successful quote.
# TYPE swap_probe_last_success_timestamp_seconds gauge
swap_probe_last_success_timestamp_seconds{aggregator="amber",scenario="base-eth-usdc"} 1.7609e+09
swap_probe_last_success_timestamp_seconds{scenario="ethereum-eth-usdc",aggregator="amber"} 1760899950.5
swap_probe_total{aggregator="amber",result="ok",scenario="solana-sol-usdc",step="quote"} 3
`

func TestLastSuccess(t *testing.T) {
	got, err := LastSuccess(strings.NewReader(exposition))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{
		"base-eth-usdc":     time.Unix(1760900000, 0),
		"ethereum-eth-usdc": time.Unix(1760899950, 5e8),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if !got[k].Equal(v) {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
}

func TestLastSuccessRejectsMalformedSample(t *testing.T) {
	if _, err := LastSuccess(strings.NewReader(`swap_probe_last_success_timestamp_seconds{aggregator="amber"} 1` + "\n")); err == nil {
		t.Fatal("want an error for a sample without a scenario label")
	}
}

func TestStale(t *testing.T) {
	now := time.Unix(1760900060, 0)
	last := map[string]time.Time{
		"fresh": now.Add(-time.Minute),
		"old":   now.Add(-10 * time.Minute),
	}
	scenarios := []Scenario{{Name: "fresh"}, {Name: "old"}, {Name: "never"}}
	got := Stale(last, scenarios, now, 3*time.Minute)
	if want := []string{"old", "never"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := Stale(last, scenarios[:1], now, 3*time.Minute); got != nil {
		t.Fatalf("got %v, want none", got)
	}
}
