#!/usr/bin/env bash
# Asserts that quote-prober in the cluster reports successful quotes.
#   hack/smoke.sh <kube-context> <namespace> [timeout-seconds]
set -euo pipefail
ctx=$1 ns=$2 timeout=${3:-120}
port=18080

kubectl --context "$ctx" -n "$ns" port-forward svc/quote-prober "$port:8080" >/dev/null 2>&1 &
pf=$!
trap 'kill $pf 2>/dev/null' EXIT

deadline=$((SECONDS + timeout))
while (( SECONDS < deadline )); do
  # The port-forward may not be up yet: a failed curl just means "try again".
  metrics=$(curl -fsS "localhost:$port/metrics" 2>/dev/null || true)
  ok=$(awk '/^swap_probe_total\{/ && /step="quote"/ && /result="ok"/ {s += $NF} END {print s + 0}' <<<"$metrics")
  if (( ${ok%.*} > 0 )); then
    echo "smoke ok: $ok successful quotes"
    exit 0
  fi
  sleep 5
done
echo "smoke failed: no successful quote within ${timeout}s" >&2
exit 1
