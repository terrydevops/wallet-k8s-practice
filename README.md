# wallet-k8s-practice

A platform practice lab for a self-custody DEX wallet backend: Go services, local Kubernetes (kind),
operators, Argo CD and GitHub Actions. See [PLAN.md](PLAN.md) for the full design and phases.

**Now (phase 0):** `quote-prober` probes a DEX aggregator's swap-quote API (Amber is the first
implementation) and exports Prometheus metrics; `mock-quote-api` fakes that API so everything runs
without credentials. CI runs from the first commit.

## Quickstart

```bash
make run MOCK=1          # mock API + prober, no credentials needed
curl -s localhost:8080/metrics | grep '^swap_'
```

Against the real API (read-only quotes; nothing is signed or sent on chain):

```bash
cp .env.example .env     # fill in AMBER_ACCESS_ID and AMBER_HMAC_SECRET; .env is git-ignored
make run
```

On a local kind cluster (Prometheus Operator, Grafana dashboard, mock API by default):

```bash
make platform            # kind cluster "wallet-k8s-practice" + kube-prometheus-stack (~2 min)
make dev                 # build, kind load, helm install mock-quote-api + quote-prober into wallet-dev
make smoke               # asserts successful quotes are being recorded
make ui                  # Grafana on localhost:3000, dashboard "Swap path — quote prober"
make dev REAL=1          # same, against the real API with the key from .env
make down                # delete the cluster
```

`make help` lists every target. `make ci` runs locally what the CI workflow runs. `make render` writes
the final manifests of every environment to `build/rendered/`. Environments, versions and promotion:
[docs/environments-and-promotion.md](docs/environments-and-promotion.md).

## Layout

```
apps/go/
  cmd/quote-prober/        wiring and lifecycle only
  cmd/mock-quote-api/      fake API: verifies signed headers, rejects replayed nonces; MOCK_MODE degrades the
                           quote endpoints (rate_limited, server_error, slow + MOCK_SLOW_DELAY)
  internal/aggregator/     vendor-neutral Quoter interface and types
  internal/aggregator/amber/        Amber client (go-libs HTTP client, rate limiter, error mapping)
  internal/aggregator/amber/sign/   HMAC-SHA256 request signing (pure, tested against openssl vectors)
  internal/aggregator/amber/fixtures/  response bodies shaped like the API docs
  internal/probe/          scheduler (staggered scenarios) and metrics
  internal/config/         env + scenarios.yaml, validated at startup
  internal/httpserver/     /metrics, /healthz, /readyz, graceful shutdown
  scenarios.yaml           what to probe
.github/workflows/ci.yml   tidy, vet, race tests, golangci-lint, image build + Trivy, helm lint + kubeconform,
                           gitleaks, Trivy fs
deploy/bootstrap/          kind cluster (node image pinned by digest)
deploy/platform-values/    kube-prometheus-stack values
deploy/charts/go-service/  one chart for both Go binaries: Deployment (restricted PSS), Service,
                           ServiceMonitor, scenarios ConfigMap; strict values schema
deploy/dashboards/         Grafana dashboard, installed once into monitoring; "environment" variable picks dev/staging/prod
deploy/apps/               per-app defaults shared by every environment
deploy/envs/{dev,staging,prod}/  namespace, version.yaml (staging/prod) and thin per-app overrides
hack/                      secret.sh (never prints values), smoke.sh, render.sh, check-env-values.py
```

## Metrics

| Metric | Labels | Meaning |
|---|---|---|
| `swap_probe_total` | aggregator, scenario, step, result | attempts; `result` is ok, http_4xx, http_429, http_5xx, client_throttled, timeout, decode_error, no_route, unsupported, error |
| `swap_probe_duration_seconds` | aggregator, scenario, step | latency, including the client rate-limit wait |
| `swap_probe_last_success_timestamp_seconds` | aggregator, scenario | for "probe stale" alerts |
| `swap_quote_winner_total` | aggregator, scenario, provider | which liquidity provider wins |
| `swap_quote_routes` | aggregator, scenario | routes offered |
| `swap_quote_amount_out` | aggregator, scenario | expected output, smallest units |
| `swap_quote_price_impact_ratio` | aggregator, scenario | USD out / USD in - 1, fees included; -0.007 = 0.7% lost |
| `http_client_*` | aggregator, url, method, name, status | from the go-libs HTTP client |

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `AMBER_ACCESS_ID`, `AMBER_HMAC_SECRET` | — | required; from the environment only |
| `AMBER_BASE_URL` | `https://tws.trustwallet.com` | |
| `AMBER_RPS` | `0.5` | must be ≤ 1 (free tier limit) |
| `PROBE_INTERVAL` / `PROBE_TIMEOUT` | `60s` / `10s` | timeout must be shorter than the interval |
| `SCENARIOS_FILE` | `scenarios.yaml` | |
| `LISTEN_ADDR` | `:8080` | |

## Notes

- `github.com/sirupsen/logrus` is required at v1.9.3 although go-libs pins v1.8.1: v1.8.1 has
  CVE-2025-65637 (HIGH), found by the Trivy step in CI.
- The API docs' example date "Thu, 27 Feb 2026" is a Friday; the signer tests use the correct weekday
  and vectors computed independently with `openssl dgst -sha256 -hmac`.
- `.gitleaks.toml` allows public EVM addresses (0x + 40 hex), which the generic API-key rule flags.
