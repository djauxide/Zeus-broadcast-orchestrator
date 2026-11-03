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
if [ -d "$HOME/sdk/go1.26" ]; then
  export PATH="$HOME/sdk/go1.26/bin:$PATH"
fi
make "$@" LOCALBIN="$HOME/zeus-bin"
