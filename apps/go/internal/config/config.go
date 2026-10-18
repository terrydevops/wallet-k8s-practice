// Package config loads quote-prober settings from the environment and scenarios from YAML.
// Secrets are read only from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/sign"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/probe"
)

type Config struct {
	AmberBaseURL  string
	AmberCreds    sign.Credentials
	AmberRPS      float64
	Interval      time.Duration
	Timeout       time.Duration
	ListenAddr    string
	ScenariosFile string
	LogLevel      slog.Level
}

// Load reads the environment. getenv is injected for tests; pass os.Getenv in production.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		AmberBaseURL:  orDefault(getenv("AMBER_BASE_URL"), "https://tws.trustwallet.com"),
		AmberCreds:    sign.Credentials{AccessID: getenv("AMBER_ACCESS_ID"), Secret: getenv("AMBER_HMAC_SECRET")},
		ListenAddr:    orDefault(getenv("LISTEN_ADDR"), ":8080"),
		ScenariosFile: orDefault(getenv("SCENARIOS_FILE"), "scenarios.yaml"),
	}
	var errs []error
	if c.AmberCreds.AccessID == "" || c.AmberCreds.Secret == "" {
		errs = append(errs, errors.New("AMBER_ACCESS_ID and AMBER_HMAC_SECRET are required"))
	}
	var err error
	if c.AmberRPS, err = strconv.ParseFloat(orDefault(getenv("AMBER_RPS"), "0.5"), 64); err != nil || !(c.AmberRPS > 0 && c.AmberRPS <= 1) { // written so NaN fails too
		errs = append(errs, errors.New("AMBER_RPS must be in (0, 1]: the free tier allows 1 request/s"))
	}
	if c.Interval, err = time.ParseDuration(orDefault(getenv("PROBE_INTERVAL"), "60s")); err != nil || c.Interval < 10*time.Second {
		errs = append(errs, errors.New("PROBE_INTERVAL must be a duration of at least 10s"))
	}
	if c.Timeout, err = time.ParseDuration(orDefault(getenv("PROBE_TIMEOUT"), "10s")); err != nil || c.Timeout <= 0 || c.Timeout >= c.Interval {
		errs = append(errs, errors.New("PROBE_TIMEOUT must be positive and shorter than PROBE_INTERVAL"))
	}
	if err := c.LogLevel.UnmarshalText([]byte(orDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	return c, errors.Join(errs...)
}

type scenarioFile struct {
	Scenarios []probe.Scenario `yaml:"scenarios"`
}

// LoadScenarios reads and validates the scenarios file.
func LoadScenarios(path string) ([]probe.Scenario, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from operator config, not user input
	if err != nil {
		return nil, err
	}
	var f scenarioFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Scenarios, ValidateScenarios(f.Scenarios)
}

func ValidateScenarios(sc []probe.Scenario) error {
	if len(sc) == 0 {
		return errors.New("no scenarios")
	}
	seen := map[string]bool{}
	var errs []error
	for i, s := range sc {
		r := s.Request
		switch {
		case s.Name == "":
			errs = append(errs, fmt.Errorf("scenario %d: name is required", i))
		case seen[s.Name]:
			errs = append(errs, fmt.Errorf("scenario %q: duplicate name", s.Name))
		case r.From.Chain == "" || r.To.Chain == "" || r.From.Token == "" || r.To.Token == "":
			errs = append(errs, fmt.Errorf("scenario %q: from/to chain and token are required", s.Name))
		case r.Address == "":
			errs = append(errs, fmt.Errorf("scenario %q: address is required", s.Name))
		case !positiveInt(r.Amount):
			errs = append(errs, fmt.Errorf("scenario %q: amount must be a positive integer in smallest units", s.Name))
		}
		seen[s.Name] = true
	}
	return errors.Join(errs...)
}

func positiveInt(s string) bool {
	n, ok := new(big.Int).SetString(s, 10)
	return ok && n.Sign() > 0
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
