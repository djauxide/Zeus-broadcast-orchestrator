#!/usr/bin/env bash
# Runs the Zeus build/verification gates on Linux (the supported build OS).
# controller-runtime v0.25.0 does not compile for windows (upstream bug in
# pkg/internal/testing/process), so gates are run here instead of natively.
#
# Prerequisites on the build machine:
#   - Go 1.26.x extracted at $HOME/sdk/go1.26 (or on PATH)
#   - GNU Make
set -euo pipefail
cd "$(dirname "$0")/.."
for candidate in "$HOME/sdk/go1.26" /home/user/sdk/go1.26; do
  if [ -d "$candidate" ]; then
    export PATH="$candidate/bin:$PATH"
    break
  fi
done
make "$@" LOCALBIN="$HOME/zeus-bin"
