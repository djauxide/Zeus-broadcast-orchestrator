
#!/usr/bin/env bash
set -euo pipefail
NS="${1:?usage: gen-passphrase.sh <namespace> <secret-name>}"
NAME="${2:?usage: gen-passphrase.sh <namespace> <secret-name>}"

PASS=$(openssl rand -base64 24)
kubectl create secret generic "$NAME" \
  --from-literal=passphrase="$PASS" \
  --namespace "$NS" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Secret $NS/$NAME created (${#PASS} chars)."
