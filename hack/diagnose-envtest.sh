#!/usr/bin/env bash
# Diagnoses envtest assets for `make conversion-test`.
set -u
export PATH="$HOME/sdk/go1.26/bin:$PATH"
P=$("$HOME/zeus-bin/setup-envtest" use 1.31.0 -p path)
echo "assets=$P"
ls -la "$P"
"$P/kube-apiserver" --version 2>&1 | head -2
echo "--- try starting kube-apiserver briefly ---"
"$P/kube-apiserver" --version
echo "--- memory ---"
free -m | head -2
