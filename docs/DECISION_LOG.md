
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
