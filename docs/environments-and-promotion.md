# Environments, versions and promotion

How this lab models dev / staging / prod, and why. Based on common Argo CD practice (sources at the end).

## Decisions

| Question | Choice | Why |
|---|---|---|
| Branches or folders for environments? | **One branch (`main`), one folder per environment** | Environment branches drift and cause merge conflicts; folders keep every environment visible in one commit |
| Umbrella chart, copied charts, or one shared chart? | **One shared chart, one release per service** | Services are few and alike. Umbrella charts add a dependency layer and tie release lifecycles together; copies drift |
| How are environments different? | **Layered values: app defaults → thin environment file → secrets via External Secrets** | Each layer owns one kind of setting |
| What is a version? | **One semver tag = one image digest + one chart version** | Never deploy an untested combination of chart and image |
| How does a version move? | **dev on every release (bot PR, merged once checks pass), staging by reviewed PR, prod by approval** — CI bot now, **Kargo** later | Promotion is a reviewed git change; prod never syncs something staging did not run |
| How do reviewers see the real result? | **Rendered diff in CI**, later Argo CD **Source Hydrator** | Layered values hide the final manifest; the diff shows it |

## Layout

```
deploy/
├── charts/go-service/            templates; packaged and pushed as an OCI chart on release
├── apps/
│   ├── quote-prober.yaml         app defaults shared by every environment
│   └── mock-quote-api.yaml       used by make dev (wallet-local) only; no environment runs the mock
├── envs/
│   ├── dev/                      every release automatically; real API
│   ├── staging/
│   │   ├── version.yaml          the release this environment runs (written by promotion)
│   │   └── quote-prober.yaml     only allowed keys: replicas, env, secret refs
│   └── prod/
│       ├── version.yaml
│       └── quote-prober.yaml
└── argocd/                       chart for the root app-of-apps: AppProject "wallet", namespaces, dashboards,
                                  ApplicationSet "workloads" = (envs with a version) x (app files in that env):
                                  chart from OCI at that version, valueFiles = apps/<app>.yaml + envs/<env>/<app>.yaml
```

## Release and promotion flow

```
git tag v1.4.2
  └─ release.yml: build images → scan → push → sign → bake digests into chart → push chart 1.4.2 (OCI)

Stage 1 (now): CI opens a PR per environment setting envs/<env>/version.yaml = 1.4.2 and approves its CI run
               dev: auto-merged once the checks pass → Argo CD syncs dev → PostSync post-deploy check
               staging: merged by a person → Argo CD syncs staging → PostSync post-deploy check
               (prod: same, by a reviewed PR and a manual sync; not deployed in this lab yet)

Stage 2 (later): Kargo (operator) does the same with CRDs:
  Warehouse  watches GHCR for new image + chart → creates Freight "1.4.2"
  Stage dev → Stage staging → Stage prod (each only accepts Freight verified upstream)
  Promotion  commits the version to git; Argo CD syncs; prod needs a manual approval
```

## Guardrails against configuration mess

1. Environment files may only set keys on an allow-list (CI fails otherwise). Probes, resources and
   security context belong to the chart version, not to an environment.
2. `values.schema.json` validates every rendered environment.
3. CI renders staging and prod for each PR and posts the diff against what is deployed.
4. Kyverno enforces runtime rules (signed images, non-root, limits) whatever the values say.
5. Auto-sync only outside prod; prod changes go through review.

## Sources

- Codefresh, Argo CD best practices — folders over environment branches
- learnk8s, How to promote releases between GitOps environments — `environments/<env>/values.yaml` + `version.yaml`
- Argo CD docs, Source Hydrator — rendered manifests (beta since v3.5)
- Kargo docs, Concepts — Warehouse, Freight, Stage, Promotion
- The New Stack, The super Helm chart: to deploy or not to deploy — shared vs per-service charts

## Which git revision each value comes from

| Values | Revision | Why |
|---|---|---|
| Chart (templates, schema, image digests) | OCI chart at the environment's version | A version is one tested, signed bundle |
| `deploy/apps/<app>.yaml` (app defaults) | git tag `v<version>` | Defaults are written for one chart version. Read from main, a new default reached environments still on an older chart, whose strict schema rejected it, and the Application could not render (staging on 0.2.0, after `postDeployCheck` was added for 0.3.0) |
| `deploy/envs/<env>/<app>.yaml` (overrides) | main | Per-environment settings apply without a release; `make env-check` limits them to keys every chart version accepts |

## Guardrails between git and a running pod

| Gate | Where | Stops |
|---|---|---|
| Ruleset on `main` | GitHub | Any change, bot included, that is not a pull request with the four CI checks green; force pushes and deletion |
| Release workflow | `release.yml` | Images built from a tag that is not on main; HIGH/CRITICAL CVEs (Trivy) |
| `wallet-images-from-release` | ValidatingAdmissionPolicy (built in) | Images from another registry, or not pinned by digest, in wallet-dev/staging/prod |
| `wallet-images-signed-by-release` | Kyverno ImageValidatingPolicy | Images from the registry without a cosign keyless signature by `release.yml` on a `vX.Y.Z` tag |
| Post-deploy check | PostSync hook Job (`quote-prober verify`) | A release that is Healthy to Kubernetes but cannot get a quote: the sync is marked failed |

The admission policies are fail-closed and do not cover wallet-local, where `make dev` runs local builds.
