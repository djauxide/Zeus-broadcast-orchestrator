#!/usr/bin/env bash
set -u
P=/home/user/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64
echo "etcd baked config check..."
"$P/etcd" --version 2>&1 | head -3
echo "--- launching etcd briefly to see runtime errors ---"
rm -rf /tmp/etcd-probe
timeout 8 "$P/etcd" --data-dir=/tmp/etcd-probe --name probe --listen-client-urls=http://127.0.0.1:2399 --advertise-client-urls=http://127.0.0.1:2399 --logger=zap 2>&1 | head -30
echo "etcd-probe-exit=$?"
