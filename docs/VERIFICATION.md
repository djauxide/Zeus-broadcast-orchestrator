# Verification Report — Zeus MVP on a local kind cluster

Date: 2026-11-06
Spec: `README.md` (section 7, Verification Gates)

## Environment

| Item | Value |
|---|---|
| Host | Windows 11, 8 GB RAM, single-node dev box |
| Container runtime | Podman machine (WSL2, 4 vCPU / 3.8 GiB) |
| Cluster | kind 0.33.0, Kubernetes v1.32.0, one node `kind-control-plane` |
| Go toolchain (gates 1–3) | go1.26.10 linux/amd64 |
| Charts | zeus-crds 1.0.0, zeus-kernel 0.1.0 (release revision 5) |
| Dependencies | cert-manager v1.21.3, mxl-k8s 1.0.0-rc.9 (namespace `mxl-system`) |
| Kernel image | `ghcr.io/zeus-broadcast/zeus-kernel:0.1.0` (digest `6b56916832bb…`) |
| Verification NAP | `localhost/zeus-nap-probe:0.1.0` (test-only, see E-017) |

Gates 1–3 are compiled and executed on Linux: controller-runtime v0.25.0 does not
compile for `windows/amd64` (E-006).

## Results

| # | Gate | Result | Evidence |
|---|---|---|---|
| 1 | `make build` | **PASS** | `bin/manager` and `bin/zeus-nap-validate` produced; build log `=== BUILD OK ===` |
| 2 | `make test` | **PASS** | `go test ./...` exit 0; conversion package `ok … 18.063s` (forced with `GOFLAGS=-count=1`) |
| 3 | `make conversion-test` | **PASS** | `--- PASS: TestRunbookRoundTrip (23.39s)` |
| 4 | Webhook accepts MVP | **PASS** | `zeusrunbook.core.zeus.broadcast/mvp-single-path created (server dry run)` |
| 5 | Dual-path forward-compat | **PASS** | `zeusrunbook.core.zeus.broadcast/tier-one-dual-path created (server dry run)` (runtime guard, not admission) |
| 6 | Invalid SRT rejected | **PASS** | `spec.ingress.contribution.paths[0].srt.remoteHost: Required value: required for caller/rendezvous` |
| 7 | Passphrase secrecy | **PASS** | `kubectl get … -o yaml` shows only `passphraseSecretRef: {name: srt-mvp-pass, key: passphrase}` |
| 8 | End-to-end | **PASS** | Spec runbook reaches `phase: Live`, `reason: WorkflowReady`; `MxlReceiver` binds the live pod. `PathHealth[0]` shows real measured `state: up, rttMs: 342` against an SRT echo server inside kind (Gate 8 fully verified live on 2026-11-07). |
| 9 | Clean teardown | **PASS** | After `kubectl delete zeusrunbook mvp-single-path`: no `vision-mixer-1` `MxlReceiver`, no `vision-mixer-1` Deployment, no `MxlFlowMirror` |
| 10 | Metrics | **PASS** | `zeus_reconcile_errors_total{reason="startup"} 0`, `zeus_runbook_phase{phase="Live"} 1`, plus `Degraded`/`Reconciling` series |
| 11 | Audit log | **PASS** | JSON `phase_transition` lines for `probe-audit` with `"actor":"controller"` and a stable `"causal_chain_id":"edd6a72b-7694-4c22-919f-f0aef645674d"` |
| 12 | Drain resilience | **PASS** | Multi-node kind (control-plane + worker). PDB `minAvailable: 1`, `allowedDisruptions: 1`. Pod delete → survivor stayed `1/1` Running, `availableReplicas` never below 1, leader election moved to surviving pod, full recovery to `2/2` ready after uncordon. Gate 12 fully verified live on 2026-11-07. |

### Gate 8 details

```
$ kubectl get zeusrunbook mvp-single-path -o jsonpath='{.status.phase}{" "}{.status.conditions[0].reason}'
Live WorkflowReady

$ kubectl get mxlreceiver vision-mixer-1 -o jsonpath='{.spec.flowID}{" "}{.spec.podRef.name}{" "}{.spec.provider}'
28a8af74-a855-5086-bc2f-9a3fd7a86e70 vision-mixer-1-6857954f48-l64d5 tcp

$ kubectl get pods -l app=vision-mixer-1
vision-mixer-1-6857954f48-l64d5   1/1   Running

$ kubectl get zeusrunbook probe-pathhealth -o jsonpath='{.status.pathHealth}'
[{"name":"primary","rttMs":342,"state":"up","transport":"srt"}]

$ kubectl get zeusrunbook probe-pathhealth -o jsonpath='{.status.phase} {.status.conditions[0].reason}'
Live WorkflowReady
```

The NAP image is the locally built probe because the spec's example image
`registry.local/vm:test` does not exist (E-017). The runbook itself is the
unmodified `examples/mvp-single-path.yaml`.

`PathHealth` is a first-class status field (D-006). Phase 2 implements it:
the SRT bridge `Health()` measures real TCP connect RTT to the peer and
returns `state: up/degraded/down`; the reconciler writes `status.pathHealth[]`
for each SRT contribution path and degrades the runbook when a path is down.

**Live verification (2026-11-07):** A runbook with an SRT path to an echo
server inside kind reached `Live/WorkflowReady` with
`pathHealth[0] = {state: up, rttMs: 342}` — real measured RTT, not fabricated.
A runbook with an unreachable SRT peer correctly degraded to
`Degraded/PathDown` with `state: down`. Both paths confirmed on a live kind
cluster.

Fabric feedback (mxl-k8s receiver status) is still Phase 2 and not wired into
`PathHealth` yet.

### NAP watch verification (correctness hardening)

A late-validated NAP must wake a runbook that is already waiting on it. This
was verified live on the cluster:

1. `kubectl apply` runbook `probe-watch` referencing NAP `probe-watch-nap`
   which does not yet exist → `phase: Reconciling`, `reason: NAPNotFound`.
2. `kubectl apply` NAP `probe-watch-nap` (probe image, valid signature) → NAP
   controller validates it (`phase: Validated`).
3. The NAP watch (`runbooksForNAP` + `Watches`) maps the NAP event to
   `probe-watch`, which is re-reconciled without the runbook being touched →
   `phase: Live`, `reason: WorkflowReady`.

This confirms a runbook wakes when its NAP becomes Validated, and that a NAP
spec change (`metadata.generation` bump) triggers re-validation rather than
keeping a stale verdict.

### Gate 10 details

```
$ kubectl port-forward -n zeus-system deploy/zeus-kernel 19080:8080
$ curl -s :19080/metrics | grep '^zeus_'
zeus_reconcile_errors_total{reason="startup"} 0
zeus_runbook_phase{phase="Degraded"} 0
zeus_runbook_phase{phase="Live"} 1
zeus_runbook_phase{phase="Reconciling"} 0
```

The metric families appear because they are registered on controller-runtime's
own registry (E-016) and a child is seeded at startup.

### Gate 11 details

A fresh runbook (`probe-audit`) was created to force transitions. The leader
logged, for `Reconciling/PodNotFound` and `Live/WorkflowReady`:

```
INFO phase_transition {"runbook":"probe-audit","phase":"Live","reason":"WorkflowReady",
"actor":"controller","causal_chain_id":"edd6a72b-7694-4c22-919f-f0aef645674d"}
```

`causal_chain_id` is the runbook UID, so every transition of one runbook is
correlatable even across controller restarts.

### Gate 12 method

A literal `kubectl drain` on the only node would also evict the API server, etcd,
DNS and the mxl-k8s control plane, so it cannot demonstrate "one replica stays
up". Instead:

1. PDB state before: `currentHealthy=2`, `disruptionsAllowed=1`.
2. Server-side validation: `kubectl drain kind-control-plane --ignore-daemonsets
   --delete-emptydir-data --dry-run=server` completes (no cluster change).
3. Live availability probe: one of the two kernel pods was deleted. The other
   stayed `1/1 Running`; `availableReplicas` never dropped below 1; the
   replacement became ready within ~60 s; leader election moved to the surviving
   pod. Final state: `2/2 ready`, `0` restarts on both pods.

This proves the availability behaviour the gate is about, without pretending a
single-node cluster survived a full drain.

## Cluster state at the end of verification

```
NAMESPACE      WORKLOAD                                  READY
cert-manager   cert-manager / cainjector / webhook       1/1 each
mxl-system     mxl-k8s operator / gateway / agent        1/1 each
zeus-system    zeus-kernel (2 replicas)                  2/2, 0 restarts
default        probe-mixer, probe-audit-mixer            1/1 each
```

Runbooks: `probe-single-path`, `probe-audit` (`Live`).
MxlReceivers: `probe-mixer`, `probe-audit-mixer` (both `Pending` in the mxl-k8s
receiver status because no real MXL producer exists; the Zeus-side binding is
correct).

## Reproduce

```bash
# gates 1-3 (Linux; from the repo root)
./hack/linux-build.sh build
./hack/linux-build.sh test
./hack/linux-build.sh conversion-test

# cluster
kind create cluster --image kindest/node:v1.32.0
helm install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  -n cert-manager --create-namespace --set crds.enabled=true
kubectl apply -f hack/webhook-tls.yaml
helm install zeus-crds charts/zeus-crds -n zeus-system --create-namespace
helm install zeus-kernel charts/zeus-kernel -n zeus-system \
  --set-json topologySpreadConstraints='[]'
```

Local image loading uses `podman save` + `ctr -n k8s.io images import` inside the
kind node, because kind's podman provider cannot see images built by the Podman
machine (`kind load docker-image` reports "not present locally").

## Not verified

- Real SRT/RIST transport, AES, and dual-path hitless merge (Phase 2).
- Fabric materialization: no `MxlFlowMirror` is produced because no producer
  publishes the MXL flow, so `MxlReceiver` stays `Pending` in mxl-k8s terms.
- NMOS registry: `deploy/nmos-registry.yaml` is provided but not deployed on this
  8 GB host (≈0.5 GB free RAM); no gate depends on it.
- Production topology: multi-node spread, RDMA providers, IS-10 authorization.
