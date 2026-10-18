#!/usr/bin/env bash
# Creates the quote-prober credentials Secret. Values are never printed.
#   hack/secret.sh <kube-context> <namespace> mock   -> mock-quote-api's built-in credentials
#   hack/secret.sh <kube-context> <namespace> real   -> AMBER_ACCESS_ID / AMBER_HMAC_SECRET from .env
set -euo pipefail
ctx=$1 ns=$2 mode=$3
name=quote-prober-amber

if [[ $mode == real ]]; then
  [[ -f .env ]] || { echo ".env missing: cp .env.example .env and fill it in" >&2; exit 1; }
  set -a; source .env; set +a
  : "${AMBER_ACCESS_ID:?AMBER_ACCESS_ID is empty in .env}" "${AMBER_HMAC_SECRET:?AMBER_HMAC_SECRET is empty in .env}"
  id=$AMBER_ACCESS_ID secret=$AMBER_HMAC_SECRET
else
  id=mock-id secret=mock-secret
fi

kubectl --context "$ctx" -n "$ns" create secret generic "$name" \
  --from-literal=AMBER_ACCESS_ID="$id" --from-literal=AMBER_HMAC_SECRET="$secret" \
  --dry-run=client -o yaml | kubectl --context "$ctx" apply -f - >/dev/null
echo "secret/$name ($mode) applied in $ns"
