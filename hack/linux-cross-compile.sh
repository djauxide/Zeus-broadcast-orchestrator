#!/usr/bin/env bash
# Cross-compiles the Linux/amd64 binaries the kernel image packages.
set -euo pipefail
cd "$(dirname "$0")/.."
for candidate in "$HOME/sdk/go1.26" /home/user/sdk/go1.26; do
  if [ -d "$candidate" ]; then
    export PATH="$candidate/bin:$PATH"
    break
  fi
done
mkdir -p bin/linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o bin/linux/manager ./cmd/manager
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o bin/linux/zeus-nap-validate ./cmd/zeus-nap-validate
ls -la bin/linux
