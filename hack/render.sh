#!/usr/bin/env bash
# Renders every app in every environment into <outdir>/<env>/<app>.yaml as the next release would
# install it: the chart and deploy/apps/<app>.yaml from this tree, then deploy/envs/<env>/<app>.yaml.
# (Argo CD takes the chart and deploy/apps from the release each environment pins; see
# deploy/argocd/files/workloads.yaml.)
#   hack/render.sh <outdir> [deploy-dir]
set -euo pipefail
out=$1 deploy=${2:-deploy}
chart=$deploy/charts/go-service
rm -rf "$out"
for envdir in "$deploy"/envs/*/; do
  env=$(basename "$envdir")
  mkdir -p "$out/$env"
  for f in "$envdir"*.yaml; do
    app=$(basename "$f" .yaml)
    [[ $app == namespace || $app == version ]] && continue
    helm template "$app" "$chart" --namespace "wallet-$env" \
      -f "$deploy/apps/$app.yaml" -f "$f" > "$out/$env/$app.yaml"
  done
done
find "$out" -name '*.yaml' | sort
