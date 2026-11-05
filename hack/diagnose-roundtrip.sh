#!/usr/bin/env bash
set -u
cd "$(dirname "$0")/.."
export PATH="$HOME/sdk/go1.26/bin:$PATH"
export KUBEBUILDER_ASSETS="$("$HOME/zeus-bin/setup-envtest" use 1.31.0 -p path)"
go test ./test/conversion/ -run TestRunbookRoundTrip -v -count=1 2>&1 | tail -40
