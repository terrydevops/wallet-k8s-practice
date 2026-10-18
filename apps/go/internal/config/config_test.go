package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/probe"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"AMBER_ACCESS_ID": "id", "AMBER_HMAC_SECRET": "s"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.AmberRPS != 0.5 || c.Interval != time.Minute || c.Timeout != 10*time.Second || c.ListenAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing credentials": {},
		"rps above free tier": {"AMBER_ACCESS_ID": "id", "AMBER_HMAC_SECRET": "s", "AMBER_RPS": "2"},
		"rps NaN":             {"AMBER_ACCESS_ID": "id", "AMBER_HMAC_SECRET": "s", "AMBER_RPS": "NaN"},
		"timeout >= interval": {"AMBER_ACCESS_ID": "id", "AMBER_HMAC_SECRET": "s", "PROBE_INTERVAL": "10s", "PROBE_TIMEOUT": "10s"},
	}
	for name, e := range cases {
		if _, err := Load(env(e)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadErrorDoesNotLeakSecret(t *testing.T) {
	_, err := Load(env(map[string]string{"AMBER_ACCESS_ID": "id", "AMBER_HMAC_SECRET": "top-secret", "AMBER_RPS": "x"}))
	if err == nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestShippedScenariosAreValid(t *testing.T) {
	sc, err := LoadScenarios(filepath.Join("..", "..", "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc) == 0 {
		t.Fatal("no scenarios")
	}
}

func TestValidateScenarios(t *testing.T) {
	ok := probe.Scenario{Name: "a", Request: aggregator.QuoteRequest{
		From: aggregator.Asset{Chain: "base", Token: "x"}, To: aggregator.Asset{Chain: "base", Token: "y"},
		Amount: "1", Address: "0x1",
	}}
	zero, dup := ok, ok
	zero.Name, zero.Request.Amount = "zero", "0"
	if err := ValidateScenarios([]probe.Scenario{ok, dup}); err == nil {
		t.Error("duplicate name accepted")
	}
	if err := ValidateScenarios([]probe.Scenario{zero}); err == nil {
		t.Error("zero amount accepted")
	}
	f := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(f, []byte("scenarios: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScenarios(f); err == nil {
		t.Error("empty file accepted")
	}
}
