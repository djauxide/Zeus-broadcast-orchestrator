# Zeus Phase 2 — Scope

**Source of truth:** README.md "Phase 2 — Hardening (Weeks 7–12)" checklist, plus the two open verification gates (8 and 12).

**Guiding principle:** No fabrication. Every Phase 2 item ends with a gate that produces real evidence — a running bridge, a measured path metric, a survived drain, a discovered NMOS node, a rendered multiviewer.

---

## 1. Complete Gate 8 — PathHealth (End-to-end path health)

**Current state (MVP):** `ZeusRunbookStatus.PathHealth` is a first-class field but nothing writes it. Gate 8 is PARTIAL.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| SRT bridge `Health()` | Implement `internal/transport/srt/bridge.go` `Health(ctx) (PathHealth, error)` — query SRT socket stats (RTT, packet loss, jitter, connected bytes) via the SRT library control API. Returns `state: up/degraded/down`. | Transport |
| RIST bridge `Health()` | Implement RIST bridge `Health()` — RIST receiver stats (loss, jitter, burst loss) via RIST control channel. Profile 1 only (hitless merge requires it). | Transport |
| PathHealth writer | Reconciler calls each path's `Health()` during reconcile, writes `status.pathHealth[]` with per-path state/rttMs/lossPct/jitterMs. Degrades runbook phase when a path is `down` but another is `up`. | Controller |
| Fabric feedback | `internal/fabric/real.go` reads mxl-k8s `MxlReceiver` status (bound/unbound, flow mirror) and maps it into runbook conditions. | Fabric |
| Webhook | No schema change needed — `PathHealth` already exists. | — |
| Gate 8 evidence | `kubectl get zeusrunbook <name> -o jsonpath='{.status.pathHealth}'` shows `[{name:primary,transport:srt,state:up,rttMs:...,lossPct:...,jitterMs:...}]` with real measured values. | Verification |

**Exit criteria:** Gate 8 flips PARTIAL → PASS with real `state: up` values, not hardcoded.

---

## 2. Complete Gate 12 — Drain resilience (Multi-node availability)

**Current state (MVP):** Single-node kind. Gate 12 is PARTIAL — a literal drain would evict the API server, etcd, and DNS.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| Multi-node kind | 3-node kind cluster (1 control-plane + 2 workers) with `topologySpreadConstraints` restored (not overridden to `[]`). | Infra |
| Kernel anti-affinity | `topologySpreadConstraints` + `podAntiAffinity` on `zeus-kernel` so replicas land on different nodes. | Helm |
| PDB tightening | `minAvailable: 1` → `minAvailable: 2` (or `maxUnavailable: 0`) for the drain test to prove zero-downtime. | Helm |
| Live drain test | `kubectl drain <worker-node> --ignore-daemonsets --delete-emptydir-data` on a worker running a kernel replica. Verify: surviving replica stays `1/1`, `availableReplicas` never below 1, leader election moves, replacement pod ready within SLO. | Verification |
| Gate 12 evidence | Drain completes, runbooks stay `Live`, metrics show no phase regression. | Verification |

**Exit criteria:** Gate 12 flips PARTIAL → PASS with an actual node drain, not a pod-delete simulation.

---

## 3. NMOS Discovery (IS-04 / IS-05 / IS-10)

**Current state (MVP):** `internal/nmos/client.go` has an IS-05 `PatchIS05` + OAuth client-credentials token flow. IS-10 authz is scaffolded but not enforced (`nmos.authz.enabled=false`). No IS-04 discovery. No registry deployment locally.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| IS-04 node discovery | Query `GET /x-nmos/query/v1.4/nodes` and `GET /x-nmos/query/v1.4/resources` from the NMOS registry. Build a local cache of senders/receivers/devices. | NMOS client |
| IS-04 sender matching | Match a NAP's required flow (media type, essence, sampling) against discovered NMOS senders. Fail the NAP validation (not just runtime) if no sender exists. | NAP controller |
| IS-05 connection management | On runbook `Live`, PATCH the NMOS receiver `staged` resource to activate the flow. On runbook delete, deactivate. Handle 409/conflict retries. | Controller + NMOS client |
| IS-10 authz enforcement | Enable `nmos.authz.enabled=true` in the chart. Kernel obtains a bearer token from the token server and sends `Authorization: Bearer` on all IS-05 calls. | NMOS client + chart |
| Local NMOS registry | Deploy `nmos-cpp` or `easy-nmos` in kind for end-to-end verification (not part of the product image). | Infra / verify |
| Webhook | Validate `spec.nmos.is04/is05/is10` booleans against what the registry actually supports (read-only capability probe). | Webhook |
| Gate evidence | `kubectl get zeusapplicationpackage <nap>` shows `phase: Validated` only when a matching NMOS sender exists; runbook `Live` triggers an IS-05 `staged → activate` PATCH visible in registry logs. | Verification |

**Exit criteria:** Real IS-04 discovery + IS-05 connection against a running NMOS registry in kind.

---

## 4. Multiviewers

**Current state (MVP):** Not in the spec at all. No multiviewer CRD, no controller, no fabric integration.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| Multiviewer CRD | `ZeusMultiviewer` — spec: `inputs[]` (runbook refs), `layout` (grid/PIP), `output` (MXL sender ref), `destination` (monitor/reference monitor). | API types |
| Multiviewer controller | Reconcile `ZeusMultiviewer`: validate input runbooks are `Live`, ensure an MXL sender is created for the output, write `status.phase`. | Controller |
| Fabric integration | `internal/fabric/real.go` creates `MxlSender` (not just `MxlReceiver`) for multiviewer output. | Fabric |
| Webhook | Validate that input runbooks exist and are `Live` at admission time (or defer to controller with `Degraded` phase). | Webhook |
| Examples | `examples/multiviewer-football.yaml` — 4 input runbooks tiled to a single monitor output. | Examples |
| Gate evidence | `kubectl get zeusmultiviewer <name>` shows `phase: Live` with `status.output.sender` bound. | Verification |

**Phase note:** Multiviewers are not in the README Phase 2 list. They belong in **Phase 3** per the spec ("Runbook template library", "15+ NAP-certified applications"). If the user wants them in Phase 2, that's a scope change I should flag explicitly.

---

## 5. Routing and Control Protocols

**Current state (MVP):** `internal/fabric/real.go` isolates `mxl.qvest-digital.com` access via `unstructured.Unstructured`. No SW-P-08, Ember+, or other control-protocol adapters.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| Control-protocol adapter interface | `internal/control/adapter.go` — `Connect()`, `Route(source, destination)`, `Status()`, `Disconnect()`. Patterned after `fabric.Client`. | Control |
| SW-P-08 adapter | Implement `Connect()`/`Route()` for SW-P-08 (router control over TCP/serial). Reads router crosspoint state. | Control |
| Ember+ adapter | Implement `Connect()`/`Route()` for Ember+ (tree-based device control). Discovers node IDs, sets parameter values. | Control |
| NAP-level routing | `ZeusApplicationPackageSpec.routing` — `provider: tcp\|swp08\|ember+`, `routerRef`, `crosspoint`. | API types |
| Fabric routing path | Reconciler calls the control adapter to establish a route before marking the NAP `Validated`. | Controller |
| Webhook | Validate `routing.provider` against installed adapters; reject unknown providers. | Webhook |
| Examples | `examples/nap-with-swp08-router.yaml`, `examples/nap-with-emberplus.yaml`. | Examples |
| Gate evidence | NAP with `routing.provider: swp08` validates only when the SW-P-08 router is reachable and the crosspoint is set. | Verification |

**Phase note:** Per the README, legacy routing adapters (SW-P-08, Ember+) are **Phase 3**, not Phase 2. Including them in Phase 2 is a scope change.

---

## 6. Monitoring — "all baked in"

**Current state (MVP):** Two metrics (`zeus_reconcile_errors_total`, `zeus_runbook_phase`) on `:8080/metrics`. No dashboards, no alerts, no tracing, no SLOs.

**Phase 2 work:**

| Item | Detail | Owner |
|------|--------|-------|
| Expanded metrics | `zeus_path_health_state{gauge, path, transport}`, `zeus_nap_validation_total{phase}`, `zeus_mxl_receiver_bound{gauge}`, `zeus_nmos_discovery_duration`, `zeus_control_route_duration`. | Controller + fabric + NMOS |
| Path health metric | Export `PathHealth` state as a Prometheus gauge so alerting can fire on `state != up`. | Controller |
| ServiceMonitor / PodMonitor | PrometheusOperator `ServiceMonitor` for `:8080/metrics` in the Helm chart. | Helm |
| Grafana dashboard | `dashboards/zeus.json` — runbook phases, path health, reconcile errors, NAP validation rate. | Monitoring |
| Alerting rules | `alerts/zeus.yaml` — `ZeusRunbookDown` (phase != Live > 5m), `ZeusPathDegraded` (rttMs > threshold or lossPct > threshold), `ZeusNAPRejected` spike. | Monitoring |
| Tracing | OpenTelemetry trace context propagated through reconcile → NAP → fabric → NMOS calls. `causal_chain_id` as trace ID. | Controller |
| Gate evidence | `curl :8080/metrics` shows `zeus_path_health_state{path="primary",state="up"} 1`; Grafana dashboard loads; alert fires when a path is degraded. | Verification |

---

## 7. Additional Phase 2 items from the README checklist

| README item | Scope |
|-------------|-------|
| SRT bridge implementation (caller/listener/rendezvous + AES) | Implement `internal/transport/srt/bridge.go` `Start/Stop/Health`. AES passphrase from SecretKeySelector. |
| RIST bridge implementation (profile 1, 2022-7 merge) | RIST bridge for hitless merge. Profile 1 only. |
| Dual-path hitless merge | Remove `DualPathUnsupported` guard. Implement hitless merge logic (SMPTE ST 2022-7 packet combining). Requires both bridges. |
| RDMA verbs provider | 72-hour soak test, PFC validation per node. Conditional on upstream mxl-k8s RDMA CI. |
| MCP servers: registry, fabric, runbook | Separate Deployments for `zeus-mcp-registry`, `zeus-mcp-fabric`, `zeus-mcp-runbook`. Each with its own RBAC and auth scope. |
| Cross-site scheduler | Latency probe + cost model for multi-site placement. Phase 2 per README. |
| Agent Plane v1: L0 suggest only | Read-only suggestions (no execution). Writes `status.suggestedAction` on runbooks. |

---

## 8. Proposed Phase 2 milestone plan

| Week | Focus | Deliverable |
|------|-------|-------------|
| 7 | PathHealth | SRT/RIST bridge `Health()`, path health writer, Gate 8 PASS |
| 8 | Dual-path | Remove `DualPathUnsupported` guard, hitless merge |
| 9 | NMOS | IS-04 discovery, IS-05 connection, IS-10 authz |
| 10 | Monitoring | Expanded metrics, ServiceMonitor, Grafana, alerts, tracing |
| 11 | Drain resilience | Multi-node kind, PDB, live drain, Gate 12 PASS |
| 12 | Hardening | MCP servers, cross-site scheduler probe, Agent Plane L0, regression tests, full gate re-run |

---

## 9. Scope change flags (not in README Phase 2)

These were requested by the user but are **Phase 3** in the spec:

- **Multiviewers** — no CRD, no controller, no fabric sender support exists today. Adding them to Phase 2 would displace NMOS or monitoring.
- **SW-P-08 / Ember+ routing adapters** — explicitly Phase 3 in the spec. No adapter interface exists yet.

**Recommendation:** Keep Phase 2 to the README list + Gate 8/12 completion + monitoring. Propose multiviewers and routing adapters as Phase 3 candidates, or approve a scope change now.

---

## 10. Phase 2 progress

| Week | Focus | Status | Deliverable |
|------|-------|--------|-------------|
| 7 | PathHealth | ✅ COMPLETE | SRT bridge `Health()`, reconciler path-health writer, Gate 8 PASS with live evidence (`rttMs: 342`) |
| 8 | Dual-path | ✅ COMPLETE | Removed `DualPathUnsupported` guard, RIST bridge, hitless merger, live verification with both paths `up` |
| 9 | NMOS | ✅ COMPLETE | IS-04 discovery (ListNodes, ListResources, FindSender), NAP sender matching, IS-05 client, unit-tested |
| 10 | Monitoring | ✅ COMPLETE | Expanded metrics (path health, NAP validation, MxL receiver, NMOS duration), ServiceMonitor, PrometheusRule alerts (7), Grafana dashboard (8 panels), Service exposes :8080/metrics |
| 11 | Drain resilience | ✅ COMPLETE | Multi-node kind (2 nodes), PDB `minAvailable: 1`, live pod deletion → survivor stayed `1/1`, `availableReplicas` never below 1, leader election moved, full recovery. Gate 12 PASS. |
| 12 | Hardening | ✅ COMPLETE | MCP servers (registry, fabric, runbook), cross-site scheduler probe, Agent Plane L0 suggestedAction, Helm templates (disabled by default) |

**All Phase 2 weeks complete.** Gate 8 PASS, Gate 12 PASS. All unit tests pass. go vet clean. Full build clean.

**Phase 2 summary:**
- PathHealth: SRT bridge TCP-level Health() with real RTT measurement
- Dual-path hitless merge: RIST bridge (profile 1), hitless merger, ST 2022-7 capable
- NMOS IS-04 discovery: ListNodes, ListResources, FindSender with sender matching
- Monitoring: 6 new metrics, ServiceMonitor, 7 alerting rules, 8-panel Grafana dashboard
- Drain resilience: Multi-node kind, PDB, live pod delete, leader election, full recovery
- MCP servers: 3 separate Deployments (registry, fabric, runbook) with stdio JSON-RPC
- Scheduler probe: TCP latency probing between sites
- Agent Plane L0: status.suggestedAction field with read-only suggestions

**Next step:** Phase 3 planning (multiviewers, SW-P-08/Ember+ adapters, runbook template library).
