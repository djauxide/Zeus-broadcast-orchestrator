# Spec Errata — deviations found while materializing the Zeus MVP

The README is the specification. This file records every place where the
literal spec could not work as written, and what the repository does instead.
Each entry cites the spec section and the file that carries the fix.

## E-001 — `tools/split-doc.sh` did not match its own document
**Spec:** §0 / `tools/split-doc.sh`.
**Problem:** the extractor matched `^### File:` while the document uses bare
`File:` headers, so it produced zero files.
**Fix:** `tools/split-doc.sh` matches `^File: *(.+)$` and strips optional
`###`. All 33 files were materialized with it.

## E-002 — controller-runtime v0.25 webhook builder is not source-compatible
**Spec:** §4 (`zeusrunbook_webhook.go`) uses
`ctrl.NewWebhookManagedBy(mgr).For(&ZeusRunbook{})`.
**Problem:** v0.25 removed `.For()`; the builder now takes the object as its
second argument.
**Fix:** `cmd/manager/main.go` uses
`ctrl.NewWebhookManagedBy(mgr, &zeusv1.ZeusRunbook{}).WithValidator(...)`.

## E-003 — `float64` status field is rejected by controller-gen defaults
**Spec:** `ZeusApplicationPackageStatus` carries `lossPct` (`float64`).
**Problem:** controller-gen refuses float fields without an explicit opt-in.
**Fix:** the `Makefile` passes `crd:allowDangerousTypes=true`. `lossPct` is a
percentage, not money; the "dangerous" warning is about float precision.

## E-004 — `controller-gen` cannot use `paths="./..."`
**Spec:** Makefile target used `paths="./..."`.
**Problem:** on a checkout with the tool binary in `bin/`, controller-gen
reports "no Go files" for non-package directories.
**Fix:** the `Makefile` enumerates the actual package directories
(`./api/... ./cmd/... ./internal/...`) and pins `LOCALBIN`.

## E-005 — `make conversion-test` raced `make manifests`
**Spec:** gates 2 and 3 are independent targets.
**Problem:** conversion-test loads CRDs from `config/crd/bases`, which may not
exist on a clean checkout.
**Fix:** `conversion-test` depends on `manifests`.

## E-006 — Windows cannot build controller-runtime v0.25.0
**Spec:** no OS constraint stated.
**Problem:** `pkg/internal/testing/process` in controller-runtime v0.25.0
declares `signalProcess` twice and leaves `signalProcessImpl` undefined on
Windows (`GOOS=windows`), so `make build`/`make test` fail on the host.
**Fix:** gates 1–3 run on Linux (`hack/linux-build.sh`, driven from the local
Podman machine). This is an upstream bug, not a Zeus bug.

## E-007 — webhook TLS material is assumed, never created
**Spec:** §5 ships a ValidatingWebhookConfiguration with a `caBundle`
placeholder; §6 expects `make deploy` to work.
**Problem:** kind has no serving certificate; the API server rejects the
webhook until a CA is injected.
**Fix:** the chart exposes a `Service` on 9443 and annotates the
ValidatingWebhookConfiguration with `cert-manager.io/inject-ca-from`. Install
cert-manager first (`hack/webhook-tls.yaml` is the dev fallback).

## E-008 — `MxlReceiver.spec.flowID` must be a UUID
**Spec:** derived `flowID` as `<runbook>-<nap>`.
**Problem:** the mxl-k8s CRD validates a UUID pattern; the dashed name is
rejected by the API server.
**Fix:** `zeusFlowID()` derives a deterministic UUID-shaped value from the
runbook UID + NAP name (`internal/controller/zeusrunbook_controller.go`).

## E-009 — `MxlReceiver.spec.podRef.name` must be a concrete Pod
**Spec:** pointed `podRef` at the NAP Deployment name.
**Problem:** the receiver attaches to a Pod name; Deployments generate random
pod suffixes, so the receiver never binds.
**Fix:** the reconciler lists pods by `app=<nap>` and writes the live pod name,
re-syncing it on rollout (`findAppPod`, `fabric.EnsureReceiver`).

## E-010 — RBAC in the spec is incomplete for controller-runtime
**Spec:** §6 ClusterRole grants CRDs, deployments, secrets `get`.
**Problem:** observed denials, in order: `coordination.k8s.io/leases`
(leader election), cluster-wide `secrets` list/watch (cache), `pods`
list/watch (podRef sync), and `events` create (leader-election event).
**Fix:** `charts/zeus-kernel/templates/rbac.yaml` grants the minimum extra
verbs; comments explain each.

## E-011 — provider-validation DaemonSet has no image
**Spec:** §5 references a `zeus-provider-validate` image that the build never
produces.
**Problem:** the DaemonSet sits in `ImagePullBackOff` on every node.
**Fix:** it is gated behind `providerValidate.enabled` (default `false`); MVP
targets `tcp`, which needs no validation.

## E-012 — topology spread blocks the second replica on one node
**Spec:** `whenUnsatisfiable: DoNotSchedule` with `replicaCount: 2`.
**Problem:** a single-node kind cluster can only run one replica, so the
second stays `Pending` and `helm --wait` never completes.
**Fix:** for local clusters deploy with
`--set-json topologySpreadConstraints='[]'`. The production default is
unchanged.

## E-013 — NMOS "deploy" URL does not exist
**Spec:** §1 step 4 applies
`https://raw.githubusercontent.com/rhastie/easy-nmos/master/deploy/nmos-cpp.yaml`.
**Problem:** `rhastie/easy-nmos` ships a docker-compose stack; there is no
`deploy/` directory (HTTP 404).
**Fix:** `deploy/nmos-registry.yaml` reproduces the registry service from that
stack (`rhastie/nmos-cpp`, `RUN_NODE=FALSE`, `registry.json`). The registry is
optional for the gates because the kernel does not host one (D-005).

## E-014 — gate 8's `PathHealth[0].state: up` is not produced by the MVP
**Spec:** gate 8 expects `Live` **and** `PathHealth[0].state: up`.
**Problem:** `PathHealth` is a first-class status field (D-006) but the MVP
reconciler never writes it: fabric materialization feedback is Phase 2. The
gate as written cannot pass without fabricating status.
**Fix:** gate 8 is recorded as **partial** — `phase: Live`, receiver created,
podRef bound. `PathHealth` remains empty by design; filling it requires the
mxl-k8s receiver status to be projected, which is Phase 2 work.

## E-015 — gate 12 is destructive on a single-node cluster
**Spec:** `kubectl drain <node>` while one replica stays up.
**Problem:** the only node also hosts the API server, etcd, the kernel, and the
mxl-k8s control plane; draining it evicts everything, so "one replica stays up"
is physically impossible on this cluster shape.
**Fix:** the chart ships a PDB (`minAvailable: 1`). Gate 12 is verified with an
eviction/`--dry-run=server` demonstration against the PDB rather than a real
drain, and reported as such.

## E-016 — zeus_* metrics are invisible on the default registry
**Spec:** gate 10 greps `:8080/metrics` for `zeus_*`.
**Problem:** controller-runtime v0.25 serves `/metrics` from
`pkg/metrics.Registry`, not `prometheus.DefaultRegisterer`.
**Fix:** `internal/controller/metrics.go` registers on `crmetrics.Registry` and
seeds a zero-valued child so the family is exported before the first error.

## E-017 — spec example image is fictional
**Spec:** `examples/vm-nap.yaml` uses `registry.local/vm:test`.
**Problem:** no such registry in kind → `ImagePullBackOff`, so gate 8 cannot
reach `Live` with the example.
**Fix:** `hack/probe/` contains a locally built, dependency-free probe NAP used
for end-to-end verification. `examples/vm-nap.yaml` is retained unchanged as
the spec artifact.
