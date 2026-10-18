# wallet-k8s-practice — plan

A platform practice lab for a self-custody DEX wallet backend: Go services on a local Kubernetes
(kind) cluster, managed by operators and Argo CD, delivered by GitHub Actions.

First module: watch the **swap path** end to end — quote from a DEX aggregator, build the transaction,
sign it the way a phone would, execute it on a local mainnet fork — and turn every step into metrics,
alerts and SLOs. Status: **plan, no code yet**.

## 1. Scope and rules

| Module | What | Phase |
|---|---|---|
| quote-prober | Synthetic probe of an aggregator's quote API; metrics per aggregator, scenario, provider | 0–2 |
| mock-quote-api | Local fake of the quote API (verifies auth headers, serves fixtures) for CI and keyless runs | 0 |
| tx-signer + anvil | Sign the built tx with a throwaway key, execute on a local fork, compare result vs. quote | 4 |

Rules:

- **No real funds, ever.** quote-prober is read-only and holds only the API key. Signing exists only in
  tx-signer, with a throwaway key never funded on mainnet, and only against a local fork; tx-signer
  refuses any RPC URL outside a local allow-list.
- **Secrets** come from the environment or a Kubernetes Secret. Never in git, logs, metric labels or CI.
  CI runs against mock-quote-api only.
- **Rate limit:** the first aggregator (Amber) allows 1 request/s on the free tier. A global limiter keeps
  the prober under 0.5 req/s; scenarios are staggered across the interval (budget below).
- **Pinned versions:** tools, charts, images (by digest in release), GitHub Actions (by commit SHA).

Aggregator facts used: Amber API docs (`setup.md`, `swap-quote.md` in the public agent-skills repo of its
provider), read 2026-10-18: base URL `https://tws.trustwallet.com`, HMAC-SHA256 auth, endpoints
`/amber-api/v1/{domains,route,route/step}`.

## 2. Repository layout

Three concerns, one repo: **apps/** (source), **deploy/** (desired cluster state, read by Argo CD),
**.github/** (CI/CD, the only writer of image tags into `deploy/`).

```
wallet-k8s-practice/
├── README.md                     quickstart, architecture picture, what each metric means
├── PLAN.md
├── Makefile                      single entry point (targets in section 7)
├── .tool-versions                go, node, kind, kubectl, helm, kubeconform, golangci-lint
├── .pre-commit-config.yaml       gofmt, golangci-lint, eslint, helm lint, gitleaks
├── .env.example                  AMBER_ACCESS_ID=  AMBER_HMAC_SECRET=  FORK_RPC_URL=   (empty)
├── .gitignore                    .env, bin/, *.kubeconfig
│
├── apps/
│   ├── go/                       ONE Go module, two binaries; they share the signer and fixtures
│   │   ├── go.mod / go.sum       module github.com/<me>/wallet-k8s-practice/apps/go
│   │   ├── cmd/
│   │   │   ├── quote-prober/main.go
│   │   │   └── mock-quote-api/main.go
│   │   ├── internal/
│   │   │   ├── aggregator/       Quoter interface + generic types (no vendor fields)
│   │   │   │   └── amber/        Amber implementation: wire types, client, mapping
│   │   │   │       ├── sign/     HMAC signer (pure)
│   │   │   │       └── fixtures/ route.json, step.json, domains.json (embedded; tests and mock)
│   │   │   ├── probe/            runner, scenarios, metrics
│   │   │   ├── e2e/              phase 4: tx-signer + fork client, result vs. quote
│   │   │   ├── config/
│   │   │   └── httpserver/       /metrics, /healthz, /readyz, graceful shutdown
│   │   ├── scenarios.yaml
│   │   ├── .golangci.yml
│   │   └── Dockerfile            ARG CMD=quote-prober → distroless/static:nonroot
│   │
│   └── tx-signer/                Node/TS (phase 4)
│       ├── package.json          @trustwallet/wallet-core pinned
│       ├── src/                  server.ts, wallet.ts, broadcast.ts, allowlist.ts
│       ├── test/                 wallet.test.ts (known vector), allowlist.test.ts
│       └── Dockerfile            node:22-slim, non-root
│
├── deploy/
│   ├── bootstrap/
│   │   ├── kind-cluster.yaml     1 control-plane + 2 workers, ingress ports 80/443
│   │   ├── argocd-values.yaml    Argo CD itself (Helm, installed once by `make up`)
│   │   └── root-app.yaml         app-of-apps → deploy/argocd/
│   ├── argocd/
│   │   ├── projects/             platform.yaml (CRDs, cluster scope) · apps.yaml (namespaces wallet-staging, wallet-prod only)
│   │   ├── platform/             one Application per operator, ordered by sync-wave (table in section 4)
│   │   └── apps/                 quote-prober.yaml · mock-quote-api.yaml · tx-signer.yaml · anvil.yaml
│   ├── platform-values/          values for each operator chart
│   ├── policies/                 Kyverno ClusterPolicies (section 4)
│   ├── charts/
│   │   ├── go-service/           ONE chart for both Go binaries (values pick the command)
│   │   │   ├── Chart.yaml, values.yaml, values.schema.json
│   │   │   ├── templates/        deployment, service, configmap, externalsecret, servicemonitor,
│   │   │   │                     prometheusrule, networkpolicy, pdb, _helpers.tpl
│   │   │   └── files/            rules.yaml
│   │   (deploy/dashboards/: Grafana dashboards, once per cluster, "environment" variable)
│   │   ├── tx-signer/
│   │   └── anvil/
│   ├── apps/                     per-app defaults shared by all environments
│   │   └── quote-prober.yaml, mock-quote-api.yaml (later tx-signer, anvil)
│   └── envs/                     one folder per environment; only allowed keys (hack/check-env-values.py)
│       ├── dev/                  inner loop (make dev), not managed by Argo CD
│       ├── staging/              version.yaml + thin per-app files; probes the mock API
│       └── prod/                 version.yaml + thin per-app files; real API
│           (each has namespace.yaml: Pod Security Admission enforce=restricted)
│
├── .github/
│   ├── workflows/                ci.yml · e2e.yml · release.yml · codeql.yml (section 6)
│   ├── dependabot.yml            gomod, npm, docker, github-actions, helm
│   └── CODEOWNERS
│
├── docs/
│   ├── architecture.md           diagram, trust boundaries, mapping to eth-docker-practice
│   ├── runbook.md                every alert: meaning, first checks, actions
│   └── adr/                      0001-monorepo-gitops · 0002-ci-bumps-tags · 0003-signer-isolation ·
│                                 0004-one-go-module · 0005-local-secrets
└── hack/                         bootstrap.sh · secret.sh (never echoes values) · smoke.sh
```

## 3. Go code design (`apps/go`)

Dependency direction (no cycles, nothing imports `cmd`):

```
cmd/quote-prober ─► config, httpserver, probe, aggregator/amber
probe            ─► aggregator            (interface only — knows no vendor, no HTTP, no signing)
aggregator/amber ─► aggregator, amber/sign, go-libs client
cmd/mock-quote-api ─► aggregator/amber/sign, amber/fixtures   (verifies the same headers it expects)
```

```go
// internal/aggregator — vendor-neutral contract
type Asset struct{ Chain, Token string }          // Chain is a lab key ("base", "ethereum", "solana")
type QuoteRequest struct{ From, To Asset; Amount, Address string; SlippagePct float64 }
type Quote struct {
    Provider        string        // winning route's first-step provider
    Routes          int
    AmountOut, MinOut string      // smallest units, as strings
    PriceImpactPct  float64
    Steps           int
    ExpiresAt       time.Time
    StepRefs        []string      // opaque, for BuildTx
}
type UnsignedTx struct{ Chain string; Payload json.RawMessage; Approve, Revoke json.RawMessage }
type Quoter interface {
    Name() string
    Supports(ctx context.Context, chain string) (bool, error)   // fail fast at startup
    Quote(ctx context.Context, r QuoteRequest) (*Quote, error)
    BuildTx(ctx context.Context, stepRef string) (*UnsignedTx, error)
}
var ErrRateLimited, ErrUnsupported = errors.New("rate limited"), errors.New("unsupported")
type HTTPError struct{ Status int }                  // classify() maps errors to result labels

// internal/aggregator/amber/sign — pure, clock and nonce injected
// plaintext = METHOD;PATH;SORTED_QUERY;ACCESS_ID;NONCE;DATE  (body not signed; raw query values, sorted by key)
type Credentials struct{ AccessID, Secret string }  // String()/LogValue() redact Secret
func New(c Credentials, opts ...Option) *Signer
func (s *Signer) Headers(method, path string, q url.Values) http.Header
func Verify(h http.Header, method, path string, q url.Values, c Credentials, maxSkew time.Duration) error // used by mock

// internal/aggregator/amber — implements aggregator.Quoter
type Client struct{ http client.Request; signer *sign.Signer; limiter *rate.Limiter; domains map[string]string }
func New(cfg Config, reg prometheus.Registerer) (*Client, error)
// wire types (routeRequest, routeResponse, stepResponse, domainsResponse) are unexported;
// toWire()/fromWire() map lab chain keys and generic types to Amber's domain IDs and fields.

// internal/probe
type Scenario struct{ Name string; Req aggregator.QuoteRequest; BuildTx bool }
type Runner struct{ q aggregator.Quoter; sc []Scenario; every, timeout time.Duration; m *Metrics; log *slog.Logger; now func() time.Time }
func (r *Runner) Run(ctx context.Context) error     // per-scenario offsets = i * every / len(sc)
func (r *Runner) probe(ctx context.Context, s Scenario)
func classify(err error) string                     // ok | http_4xx | http_429 | http_5xx | timeout | decode_error | unsupported
func NewMetrics(reg prometheus.Registerer) *Metrics // own registry, no globals

// internal/config — env + YAML, validated; secrets only from env
func Load() (Config, error)
func LoadScenarios(path string) ([]probe.Scenario, error)
```

Tests (`go test -race ./...`, `golangci-lint` with gosec and errorlint):

| Test | Pins |
|---|---|
| `sign` | plaintext against a hand-written vector from the docs (independent of `Verify`), query sorting, empty query, secret redaction |
| `amber` | `httptest.Server`: method, path, the four auth headers; fixture decoding; domain mapping; 429 → `ErrRateLimited` |
| `probe` | fake Quoter: ok / 429 / timeout / slow → counters, histograms, last-success gauge; offsets keep under the RPS budget |
| `config` | missing credentials fail fast; unknown chain or zero amount rejected |
| `mock-quote-api` | rejects bad signature, stale date, reused nonce; serves fixtures |

**Rate budget:** 3 scenarios × (1 quote + 1 build) per 60 s = 0.1 req/s, plus `domains` once an hour.

## 4. Platform on kind: operators

| Component | Kind | Role here | Sync-wave | Phase |
|---|---|---|---|---|
| Argo CD | Helm (bootstrap) | GitOps controller; app-of-apps from `deploy/argocd` | — | 3 |
| kube-prometheus-stack | operator | Prometheus, Alertmanager, Grafana; ServiceMonitor, PrometheusRule, dashboards via ConfigMap | -3 | 1 |
| Kyverno | operator | admission policies: verify cosign signatures of our images, require non-root, read-only rootfs, resource limits, no `latest` | -2 | 3 |
| Kargo | operator | promotion as CRDs: Warehouse watches GHCR, Freight = one release (image + chart), Stages dev → staging → prod, prod by approval. Replaces the CI tag-bump PRs | -1 | 3b |
| External Secrets | operator | `ExternalSecret` in the chart; locally backed by a `ClusterSecretStore` of kind `kubernetes` reading a seed Secret from `make secret`; swap the store for AWS Secrets Manager on a real cluster, chart unchanged | -2 | 3 |

Not used, on purpose: database operators (everything is stateless), service mesh (too heavy for kind),
ingress-nginx (the project is archived; UIs are reached with `kubectl port-forward`, a Gateway API
controller can come later).
Pod Security Admission `restricted` on the `wallet` namespace is built in, no operator needed.

**Two loops:**

- **Inner loop (phases 0–2):** `make dev` = build image → `kind load` → `helm upgrade` into namespace
  `wallet-dev`. Fast, no git push.
- **GitOps loop (phase 3+):** only Argo CD changes namespace `wallet`, from `main` on GitHub.

**Laptop budget:** kind + kube-prometheus-stack + Argo CD + Kyverno ≈ 5–6 GB RAM. Do not run
eth-docker-practice at the same time.

Environments, versioning and promotion: see [docs/environments-and-promotion.md](docs/environments-and-promotion.md)
(one branch, folder per environment, one shared chart, one version = image + chart, layered thin values).

## 5. Observability

Metrics (low cardinality: aggregator and scenario from config, provider from a small set):

| Metric | Type | Labels |
|---|---|---|
| `swap_probe_total` | counter | aggregator, scenario, step (quote / build_tx), result |
| `swap_probe_duration_seconds` | histogram (0.1–10 s) | aggregator, scenario, step |
| `swap_probe_last_success_timestamp_seconds` | gauge | aggregator, scenario |
| `swap_quote_winner_total` | counter | aggregator, scenario, provider |
| `swap_quote_routes` | gauge | aggregator, scenario |
| `swap_quote_amount_out` | gauge | aggregator, scenario |
| `swap_quote_price_impact_ratio` | gauge | aggregator, scenario |
| `swap_e2e_total` (phase 4) | counter | aggregator, scenario, result (executed / reverted / sign_error / expired) |
| `swap_e2e_out_vs_quote_ratio` (phase 4) | gauge | aggregator, scenario |
| go-libs HTTP client metrics | | as defined by the library |

SLO and alerts (PrometheusRule, each with a runbook entry):

- **Quote availability SLO 99% / 30 d** (`result="ok"` over all `step="quote"`): multi-window burn-rate
  alerts — page at 14.4× (1 h and 5 m), ticket at 6× (6 h and 30 m).
- **Quote latency:** p95 > 3 s for 15 min.
- **Probe stale:** no success for 10 min (prober down, key revoked, API down).
- **Rate limited:** any `http_429` for 5 min (our budget is wrong or the tier changed).
- **Single provider:** one provider wins 100% of a scenario for 6 h — informational (others may fail silently).
- **Execution gap (phase 4):** `swap_e2e_out_vs_quote_ratio` below `MinOut/AmountOut` → quote is not honest.

Grafana: one dashboard — SLO and error budget, latency heatmap, winners over time, quote vs. execution.
Alertmanager: a local webhook receiver that logs; no external notifications.

## 6. CI/CD

```
PR ──► ci.yml + e2e.yml ──► review ──► merge to main
   ──► release.yml: build (multi-arch) → SBOM → Trivy (fail on CRITICAL) → push GHCR → cosign sign
   ──► package chart <version> with the image digests baked in ──► push OCI chart
   ──► bot PR: deploy/envs/staging/version.yaml = <version> ──► merge (later: Kargo)
   ──► Argo CD syncs ──► Kyverno verifies signature at admission ──► Prometheus scrapes new pods
```

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | PR | path-filtered: Go (vet, golangci-lint, test -race, coverage) · TS (eslint, test) · Helm (lint, template → kubeconform with CRD schemas) · gitleaks · Trivy fs |
| `e2e.yml` | PR | kind in CI → helm install go-service (prober + mock-quote-api) and kube-prometheus-stack CRDs → `hack/smoke.sh` asserts `swap_probe_total{result="ok"}` grows. Helm, not Argo CD, for speed |
| `release.yml` | push to main | build, scan, push, keyless cosign (GitHub OIDC), open tag-bump PR |
| `codeql.yml` | PR, weekly | Go, JS/TS |

Rules: third-party actions pinned by commit SHA; `permissions: {}` by default, granted per job;
`id-token: write` only in the signing job; GHCR via `GITHUB_TOKEN`; no aggregator key in CI; images tagged by
SHA and referenced by digest, never `latest`.

## 7. Makefile

| Target | Does |
|---|---|
| `make test` / `make lint` | all apps, same commands as CI |
| `make run` | quote-prober on the laptop (`MOCK=1` starts mock-quote-api and points at it) |
| `make cluster` | kind cluster only |
| `make dev` | inner loop: build → kind load → helm upgrade into `wallet-dev` |
| `make up` | cluster + Argo CD + root app (GitOps loop) |
| `make secret` | seed Secret from `.env`, values never printed |
| `make ui` | prints Grafana / Argo CD URLs and initial passwords location |
| `make e2e` | the CI e2e, locally |
| `make down` | delete the cluster |

## 8. Phases

| Phase | Delivers | Done when | Effort | Needs |
|---|---|---|---|---|
| 0 | `apps/go`: signer, Amber client, probe, mock-quote-api, tests, **ci.yml** | `make ci` green; `make run MOCK=1` shows metrics; one real quote with a key | 2 h | key for the last check only |
| 1 | kind, kube-prometheus-stack, ingress, go-service chart, dashboard | `make dev`; dashboard shows live probes | 2 h | Docker |
| 2 | PrometheusRule, SLO, runbook, NetworkPolicy, PSA restricted | a forced failure (bad key on mock) fires the right alert | 1.5 h | |
| 3 | GitHub repo, CI/CD workflows, Argo CD app-of-apps, Kyverno, External Secrets | a merged PR reaches the cluster with no manual step; an unsigned image is rejected | 4 h | GitHub repo |
| 4 | tx-signer, anvil chart, e2e module, signer NetworkPolicy | one scenario executes on the fork; out-vs-quote ratio on the dashboard | 3 h | fork RPC URL |

## 9. Open points

- ~~go-libs `client` with Go 1.26~~: works. Its logrus v1.8.1 has CVE-2025-65637; overridden to v1.9.3.
- Whether kind's default CNI enforces NetworkPolicy in kind 0.31; otherwise install Calico for phase 2.
- Whether `/route/step` needs the address to hold a balance on mainnet (decides how much of phase 4 works).
- Quote expiry vs. fork age: fork at the latest block right before each phase-4 run.
- Repo visibility: public needs no Argo CD repo credentials and lets kind pull GHCR images without a pull secret.
