
# Zeus — Decision Log

## D-001: Product name
"Zeus" with API group `zeus.broadcast`, module `github.com/zeus-broadcast/zeus-kernel`.
**Risk:** Heavy namespace collision. All identifiers derive from `hack/rename.sh`; a rename
after Milestone 1 requires a CRD migration plan. Revisit before external NAP publication.

## D-002: MXL CRD API group
Pin to `mxl.qvest-digital.com/v1alpha1`, access via `unstructured.Unstructured`. The kernel
isolates the string in `internal/fabric/real.go`.

## D-003: WAN passphrase handling
SRT/RIST passphrases are `corev1.SecretKeySelector` only. No plaintext field. Webhook rejects
empty name/key; reconciler rejects secrets under 20 chars.

## D-004: Dual-path merge deferred to Phase 2
Schema accepts `redundancy: hitless` today; MVP reconciler returns
`Degraded/DualPathUnsupported` at runtime. **Not rejected by webhook** so Phase 2 is an
implementation change, not a CRD bump.

## D-005: NMOS registry is external
Kernel consumes IS-04 and issues IS-05; it does not host a registry. Use `nmos-cpp` or
`easy-nmos` in development.

## D-006: PathHealth in status
`ZeusRunbookStatus.PathHealth` is first-class. Agent Plane cannot remediate what it cannot read.

## D-007: RDMA provider deferred
MVP targets `tcp` only. `verbs`/`efa` are Phase 2 with a 72-hour soak test. mxl-k8s routine
CI exercises `tcp`; RDMA paths are API-accepted but not CI-tested upstream.

## D-008: Microservice boundary
Kernel is one binary. MCP servers, agents, and NAPs are separate Deployments. Splitting
reconcilers breaks finalizer atomicity and status ownership.

## D-009: Local verification topology
Development verification runs on a single-node kind cluster driven through the local
Podman machine (`KIND_EXPERIMENTAL_PROVIDER=podman`). Cert-manager is installed for
webhook CA injection. This is a dev harness, not a production topology; any gate that
assumes multi-node placement (gate 12) is recorded as a demonstration, not a claim.

## D-010: Probe NAP for end-to-end verification
`examples/vm-nap.yaml` references a fictional image. Rather than weaken the example, a
tiny locally built probe (`hack/probe/`) is used to prove the reconcile → Deployment →
Pod → MxlReceiver chain. The probe is explicitly test-only and is not part of the
product.

## D-011: Metrics registry
Zeus metrics register on `sigs.k8s.io/controller-runtime/pkg/metrics.Registry`, the
registry the v0.25 metrics server actually serves. A startup child is seeded so
`zeus_reconcile_errors_total` exists before the first error.

## D-012: Probe tolerance on constrained nodes
Liveness/readiness keep the spec's paths and delays but use a 3 s timeout and a
liveness `failureThreshold` of 5. With a 500m CPU limit on a busy single-node dev
cluster, the 1 s default produced restart storms while the process was merely
descheduled; a restart does not make the controller healthier.

## D-013: Webhook auth is not exercised locally
The kernel issues IS-05 to the NMOS registry but the MVP does not enforce IS-10 OAuth
tokens on inbound calls. Local runs leave `nmos.authz.enabled=false`; enabling it
requires a token server that is out of scope for the MVP gates.

## D-014: envtest assets are resolved locally first
`setup-envtest use` refreshes a release index from GitHub on every call; on a
restricted or loaded network it fails with a TLS handshake timeout even when the
control-plane binaries are already installed, which made `make test` and
`make conversion-test` fail with an empty `KUBEBUILDER_ASSETS`. The Makefile now
prefers `$HOME/.local/share/kubebuilder-envtest/k8s/<version>-linux-amd64` and only
falls back to `setup-envtest`.

## D-015: Build and test serially on the dev host
The Windows host has 8 GB RAM and the Podman VM 3.8 GB. Running the kind cluster and
a full `go test ./...` envtest control plane at the same time exhausts the host and
causes API timeouts and probe-driven restarts. Gate runs 1–3 are executed with the
kind node stopped; cluster verification runs when no compiler is active.

## D-016: NAP watch + generation-aware revalidation
A runbook waiting on a NAP must not stall until the runbook itself is edited. The
`ZeusRunbookReconciler` watches `ZeusApplicationPackage` and maps events to
referencing runbooks via `runbooksForNAP`, so a NAP becoming `Validated` (or its
image/signature changing) re-wakes the runbook. The NAP reconciler re-validates
when `metadata.generation != status.observedGen` and records `observedGen` on both
acceptance and rejection. Verified live: a runbook referencing a not-yet-existent
NAP sits `Reconciling/NAPNotFound`, then transitions to `Live/WorkflowReady` when
the NAP is created and validated, with no change to the runbook.

## D-017: Webhook hitless/mergeMode consistency
The webhook's hitless rules are selected by `Contribution.MergeMode`, not
`Redundancy` (the spec example sets both to `hitless`). Test helpers must set both
fields explicitly; a helper that hardcodes `MergeMode: "switchover"` while setting
`Redundancy: "hitless"` would never exercise the hitless validation path.

## D-018: PathHealth via SRT bridge (Phase 2 Week 1)
PathHealth is measured, not fabricated. The SRT bridge opens a real TCP
socket to the peer's remoteHost:remotePort and times a probe round trip to
derive RTT. State is up when the peer echoes, degraded when reachable but
not echoing, down on connection failure. Without libsrt this is TCP-level
connectivity, not SRT ARQ statistics; loss/jitter are 0 until an SRT library
binding is integrated. The reconciler writes status.pathHealth[] for each
SRT path and degrades the runbook to Degraded/PathDown when any path is
down. Verified with unit tests against a real TCP echo listener; live cluster
verification pending.
