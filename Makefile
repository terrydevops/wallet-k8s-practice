SHELL := /bin/bash
.DEFAULT_GOAL := help

GO_DIR          := apps/go
GOLANGCI_VER    := v2.15.0
GITLEAKS_VER    := v8.30.1
KUBECONFORM_VER := v0.8.0
IMAGE_PREFIX    ?= wallet-k8s-practice
TAG             ?= dev
MOCK_ADDR       := 127.0.0.1:8081

# Cluster: every kubectl/helm call names this context explicitly, so other clusters are never touched.
CLUSTER         := wallet-k8s-practice
KCTX            := kind-$(CLUSTER)
KUBECTL         := kubectl --context $(KCTX)
HELM            := helm --kube-context $(KCTX)
# Platform chart versions are read from the GitOps values, so bootstrap and Argo CD never disagree.
KPS_VERSION     := $(shell sed -n 's/^kubePrometheusStackVersion: *//p' deploy/argocd/values.yaml)
ARGOCD_VERSION  := $(shell sed -n 's/^argoCDVersion: *//p' deploy/argocd/values.yaml)
# Git repo Argo CD follows: your own fork by default (origin), so a clone never deploys someone else's main.
REPO_URL        ?= $(shell git remote get-url origin 2>/dev/null | sed -E 's|^git@github.com:|https://github.com/|; s|\.git$$||')
# Released images and the OCI chart: ghcr.io/<owner>/<repo>, lower case (as release.yml pushes them).
REGISTRY        ?= $(shell echo '$(REPO_URL)' | sed -E 's|^https://github.com/|ghcr.io/|' | tr '[:upper:]' '[:lower:]')
# make dev deploys here: local builds, not managed by Argo CD (wallet-dev/staging/prod are).
LOCAL_NS        := wallet-local
DEV_TAG         := dev-$(shell date +%Y%m%d%H%M%S)
CHART           := deploy/charts/go-service
# Local ports for UIs; 3000 is often taken by another Grafana.
GRAFANA_PORT    ?= 3300

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: test
test: ## Go vet + tests with the race detector (same as CI)
	cd $(GO_DIR) && go vet ./... && go test -race -coverprofile=coverage.out ./...

.PHONY: lint
lint: ## golangci-lint (local binary if present, else the pinned container)
	@if command -v golangci-lint >/dev/null; then cd $(GO_DIR) && golangci-lint run; \
	else docker run --rm -v "$(CURDIR)/$(GO_DIR)":/app -w /app golangci/golangci-lint:$(GOLANGCI_VER) golangci-lint run; fi

.PHONY: secrets-scan
secrets-scan: ## gitleaks over the git history
	@if command -v gitleaks >/dev/null; then gitleaks git --redact .; \
	else docker run --rm -v "$(CURDIR)":/repo ghcr.io/gitleaks/gitleaks:$(GITLEAKS_VER) git --redact /repo; fi

.PHONY: hooks
hooks: ## Enable the repo's git hooks (pre-commit secret scan)
	git config core.hooksPath .githooks

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	cd $(GO_DIR) && go mod tidy -diff

.PHONY: run
run: ## Run quote-prober locally. MOCK=1 starts mock-quote-api and points the prober at it; else reads .env
ifeq ($(MOCK),1)
	@cd $(GO_DIR) && trap 'kill 0' EXIT; \
	LISTEN_ADDR=$(MOCK_ADDR) go run ./cmd/mock-quote-api & sleep 2; \
	AMBER_BASE_URL=http://$(MOCK_ADDR) AMBER_ACCESS_ID=mock-id AMBER_HMAC_SECRET=mock-secret \
	AMBER_RPS=1 PROBE_INTERVAL=10s PROBE_TIMEOUT=5s go run ./cmd/quote-prober
else
	@test -f .env || { echo ".env missing: cp .env.example .env and fill it in"; exit 1; }
	@set -a; source .env; set +a; cd $(GO_DIR) && go run ./cmd/quote-prober
endif

.PHONY: images
images: ## Build both images locally (TAG=dev)
	for c in quote-prober mock-quote-api; do \
	  docker build --build-arg CMD=$$c -t $(IMAGE_PREFIX)/$$c:$(TAG) $(GO_DIR) || exit 1; done

.PHONY: env-check
env-check: ## Environment files only set allowed keys; version files hold semver
	python3 hack/check-env-values.py deploy

.PHONY: render
render: ## Render every app in every environment into build/rendered/<env>/<app>.yaml
	@hack/render.sh build/rendered >/dev/null && echo "rendered into build/rendered/"

.PHONY: chart-check
chart-check: render ## helm lint + kubeconform (with CRD schemas) on every rendered environment and the dashboards
	helm lint --strict $(CHART) -f deploy/apps/quote-prober.yaml >&2
	helm lint --strict deploy/argocd --set repoURL=https://github.com/o/r,registry=ghcr.io/o/r >&2
	{ cat build/rendered/*/*.yaml; echo '---'; kubectl kustomize deploy/dashboards; echo '---'; \
	  helm template root deploy/argocd --set repoURL=https://github.com/o/r,registry=ghcr.io/o/r; } | docker run --rm -i ghcr.io/yannh/kubeconform:$(KUBECONFORM_VER) -strict -summary \
	  -schema-location default \
	  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' -

.PHONY: ci
ci: tidy-check test lint env-check chart-check secrets-scan ## Everything the ci.yml workflow runs, locally

## --- local cluster (inner loop) -----------------------------------------------------------------

.PHONY: cluster
cluster: ## Create the kind cluster (if missing) and the namespaces
	@kind get clusters 2>/dev/null | grep -qx $(CLUSTER) || kind create cluster --config deploy/bootstrap/kind-cluster.yaml
	for e in deploy/envs/*/namespace.yaml; do $(KUBECTL) apply -f $$e; done

.PHONY: platform
platform: cluster ## Bootstrap kube-prometheus-stack on a new cluster (afterwards Argo CD owns it; change via git)
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	helm repo update prometheus-community >/dev/null
	$(KUBECTL) create namespace monitoring --dry-run=client -o yaml | $(KUBECTL) apply -f - >/dev/null
	@$(KUBECTL) -n monitoring get secret grafana-admin >/dev/null 2>&1 || $(KUBECTL) -n monitoring create secret generic grafana-admin \
	  --from-literal=admin-user=admin --from-literal=admin-password="$$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-24)" >/dev/null
	$(HELM) upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
	  --version $(KPS_VERSION) -n monitoring --create-namespace \
	  -f deploy/platform-values/kube-prometheus-stack.yaml --wait --timeout 10m
	$(KUBECTL) apply -k deploy/dashboards

.PHONY: argocd
argocd: cluster ## Bootstrap Argo CD, or break-glass reinstall from the same values (then commit any fix)
	helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	helm repo update argo >/dev/null
	$(HELM) upgrade --install argocd argo/argo-cd --version $(ARGOCD_VERSION) \
	  -n argocd --create-namespace -f deploy/platform-values/argo-cd.yaml --wait --timeout 10m
	@echo "Argo CD installed; it will follow $(REPO_URL)"

.PHONY: gitops
gitops: argocd ## Hand dev/staging/prod to Argo CD: seed the API credentials, apply the root app-of-apps
	@if [ -f .env ]; then for ns in wallet-dev wallet-staging; do hack/secret.sh $(KCTX) $$ns real; done; \
	else echo "no .env: probers stay unready until 'hack/secret.sh $(KCTX) <namespace> real'"; fi
	sed -e 's|__REPO_URL__|$(REPO_URL)|g' -e 's|__REGISTRY__|$(REGISTRY)|g' deploy/bootstrap/root-app.yaml | $(KUBECTL) apply -f -

.PHONY: argocd-ui
argocd-ui: ## Port-forward Argo CD to localhost:8443 (user admin; password: see the command printed first)
	@echo "Argo CD admin password: $(KUBECTL) -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d"
	$(KUBECTL) -n argocd port-forward svc/argocd-server 8443:80

.PHONY: dev
dev: ## Inner loop: build, kind load, helm install mock + prober into wallet-local (mock API; REAL=1: real API)
	$(MAKE) images TAG=$(DEV_TAG)
	for c in quote-prober mock-quote-api; do kind load docker-image $(IMAGE_PREFIX)/$$c:$(DEV_TAG) --name $(CLUSTER); done
	$(KUBECTL) create namespace $(LOCAL_NS) --dry-run=client -o yaml | $(KUBECTL) apply -f - >/dev/null
	$(KUBECTL) label namespace $(LOCAL_NS) --overwrite pod-security.kubernetes.io/enforce=restricted \
	  pod-security.kubernetes.io/warn=restricted app.kubernetes.io/part-of=wallet-k8s-practice >/dev/null
	hack/secret.sh $(KCTX) $(LOCAL_NS) $(if $(REAL),real,mock)
	for app in mock-quote-api quote-prober; do \
	  $(HELM) upgrade --install $$app $(CHART) -n $(LOCAL_NS) -f deploy/apps/$$app.yaml --set image.tag=$(DEV_TAG) \
	    $$( [ $$app = quote-prober ] && echo --set env.AMBER_BASE_URL=$(if $(REAL),https://tws.trustwallet.com,http://mock-quote-api:8081) --set env.PROBE_INTERVAL=30s) \
	    --wait || exit 1; done

.PHONY: smoke
smoke: ## Assert quote-prober in NS (default wallet-local) reports successful quotes
	hack/smoke.sh $(KCTX) $(or $(NS),$(LOCAL_NS))

.PHONY: ui
ui: ## Port-forward Grafana to localhost:$(GRAFANA_PORT) (admin password: see the command printed first)
	@echo "Grafana admin password: $(KUBECTL) -n monitoring get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d | pbcopy"
	$(KUBECTL) -n monitoring port-forward svc/kube-prometheus-stack-grafana $(GRAFANA_PORT):80

.PHONY: down
down: ## Delete the kind cluster
	kind delete cluster --name $(CLUSTER)
