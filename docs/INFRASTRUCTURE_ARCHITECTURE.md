# Zeus Broadcast Orchestrator — Infrastructure Architecture

## Executive Summary

Zeus is a **multi-vendor broadcast orchestration platform** that aggregates routing, device control, discovery, signal tallies, and security into a single control plane. It hosts virtualized, containerized production applications (NAPs) and can deploy them dynamically across on-premise, cloud, and hybrid environments.

## Architecture Layers

```
┌─────────────────────────────────────────────────────────────────┐
│                    OPERATOR CONSOLE (UI)                        │
│  Router · Rack · Patch · Monitor · Devices · NAP · Tools · Sec  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼ (REST + SSE + WebSocket)
┌─────────────────────────────────────────────────────────────────┐
│                      CONTROL PLANE (Zeus Kernel)                │
│  ┌─────────┐ ┌──────────────┐ ┌──────────┐ ┌─────────────┐   │
│  │ Runbook │ │   NAP CTRL   │ │ Fabric   │ │  Security   │   │
│  │ Reconcil│ │   Reconciler │ │ Client   │ │ (Auth/RBAC) │   │
│  └─────────┘ └──────────────┘ └──────────┘ └─────────────┘   │
│  ┌─────────┐ ┌──────────────┐ ┌──────────┐ ┌─────────────┐   │
│  │  Tally  │ │   Device     │ │   Routing│ │   Fabric    │   │
│  │ Tracker │ │   Adapters   │ │  Adapters│ │  Scaler     │   │
│  └─────────┘ └──────────────┘ └──────────┘ └─────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┴───────────────┐
              ▼                               ▼
    ┌───────────────────┐           ┌───────────────────┐
    │   DATA PLANE      │           │   DATA PLANE      │
    │  (On-Prem K8s)    │           │  (Cloud K8s)      │
    │  NAP Pods · MxR   │           │  NAP Pods · MxR   │
    │  Receivers · SDI  │           │  Receivers · SDI  │
    │  ST 2110 · SRT    │           │  ST 2110 · SRT    │
    └───────────────────┘           └───────────────────┘
```

## 1. Network Routing — Wired ✅

Zeus manages network routing through protocol-specific adapters:

| Protocol | Adapter | Status | Crosspoint Control |
|----------|---------|--------|-------------------|
| SW-P-08 | `internal/control/routing/swp08.go` | Implemented | TCP Route/Unroute |
| Ember+ | `internal/control/routing/emberplus.go` | Implemented | TCP JSON-RPC connect/disconnect |
| NMOS IS-05 | `internal/nmos/client.go` | Implemented | HTTP PATCH receiver staging |
| UDP/TCP | UI Patch Bay | Implemented | Select source → destination → connect |

**RoutingConfig** on `ZeusApplicationPackage` specifies provider, routerRef, sourceID, destinationID — the runbook reconciler applies routes at deployment time.

## 2. Device Control & Discovery — Wired ✅

**Device Control Adapters** (`internal/control/device/`):

| Adapter | Protocol | Status |
|---------|----------|--------|
| Camera | VISCA/TCP | Implemented |
| Audio | TCP/Ember+ | Implemented |
| Replay | TCP/ESCONTROL | Implemented |
| Graphics | TCP/HTTP | Implemented |
| GV AMPP | NMOS IS-04/IS-05 + iDCP | **Needs implementation** |
| NEP | REST/TCP | **Needs implementation** |
| EVS Cerebrum | REST/HTTP | **Needs implementation** |

All adapters implement the `DeviceControlAdapter` interface: `Connect`, `Disconnect`, `SendCommand`, `Status`.

**Discovery** — NMOS IS-04 client (`internal/nmos/client.go`):
- `ListNodes()` — discover all nodes on the registry
- `ListResources()` — discover all resources (senders, receivers, devices)
- `FindSender()` — match sender by media type/essence/sampling
- OAuth2 client-credentials token support for secure registries

## 3. Signal Tallies — **Needs Implementation**

Tallies track which signal is feeding which destination (program, preview, safe). Currently the UI shows tally status as static badges. A real tally system needs:

- `internal/tally/tally.go` — tally state machine (Program/Preview/Safe/None)
- `internal/tally/tracker.go` — propagate tallies through routing graph
- Tally updates pushed via SSE to operator console
- Tally linked to router crosspoint state

## 4. Security — **Needs Implementation**

Currently no authN/authZ. Required:
- `internal/security/auth.go` — API keys, JWT, mTLS client cert validation
- `internal/security/rbac.go` — role-based access control (operator, engineer, admin, viewer)
- HTTP middleware enforcing per-endpoint permissions
- Audit logging for all control operations (route takes, device commands, deployments)

## 5. Multi-Vendor Aggregator — Partially Implemented

Zeus natively hosts virtualized containerized production applications via the `ZeusApplicationPackage` CRD:

```go
type ZeusApplicationPackageSpec struct {
    Image          string        // Container image
    ImageSignature string        // Cosign/signature verification
    CRM            CRMResources  // CPU, memory, accelerators, media IO
    NMOS           NMOSSpec      // IS-04/IS-05/IS-10, transport, sender match
    Routing        RoutingConfig // Router provider, source, destination
    HealthPath     string        // HTTP health check path
}
```

**15 NAP-certified application packages** exist in `examples/naps/` (vision mixer, character generator, replay server, etc.).

**Multi-vendor device support** — device adapters abstract vendor protocols. To be a true multi-vendor aggregator, GV AMPP, NEP, and EVS Cerebrum adapters need to be implemented.

## 6. Compute-Agnostic Deployment — Partially Implemented

The **Fabric Client** (`internal/fabric/`) is the abstraction layer:

```go
type Client interface {
    EnsureReceiver(ctx, namespace, name, flowID, podName, provider, owner) error
    DeleteReceiver(ctx, namespace, name) error
}
```

- `RealClient` — creates MXL Receiver CRDs (Qvest MXL) on the local cluster
- `FakeClient` — test/mock implementation
- `RequireProvider` — validates node capabilities before scheduling

**Missing:** Multi-cluster support (deploy to remote on-prem or cloud clusters), cluster-agnostic kubeconfig management.

## 7. Control Plane vs Data Plane Separation — Architecturally Defined

**Control Plane** (Zeus Kernel, runs on management cluster):
- Runbook reconciler
- NAP reconciler
- Device/routing adapter registries
- Tally tracker
- Security enforcement
- Fabric client (issues deployment intentions)

**Data Plane** (NAP pods on workload clusters):
- Containerized production applications
- MXL receivers
- ST 2110 / SRT / RIST media endpoints
- Device control targets

The control plane never touches media — it only issues control intent (routes, device commands, deployments). The data plane carries the actual media.

## 8. Dynamic Scale On Demand — **Needs Implementation**

Required components:
- `internal/fabric/scaler.go` — HPA integration, cluster autoscaler hooks
- Fabric scaler spins up/down NAP replicas based on runbook load
- Multi-cluster burst capacity (burst to cloud when on-prem saturated)
- Pod disruption budgets + PDB-aware drain

## Security Model

| Layer | Mechanism | Status |
|-------|-----------|--------|
| Transport | mTLS (cert-manager) | Webhook TLS only; needs full mesh |
| Authentication | API keys / JWT / client certs | Not implemented |
| Authorization | RBAC (operator/engineer/admin/viewer) | Not implemented |
| Audit | Immutable log of all control ops | Not implemented |
| Image validation | Cosign signature on NAP image | CRD field exists; enforcement needed |

## Deployment Modes

| Mode | Control Plane | Data Plane | Use Case |
|------|--------------|------------|----------|
| On-Premise | Local k8s | Local k8s | Facility control room |
| Cloud | Remote k8s (EKS/AKS/GKE) | Remote k8s | Cloud production, disaster recovery |
| Hybrid | Local + Cloud | Local + Cloud | Burst to cloud, multi-site sync |

## Honest Status

| Capability | Status | Notes |
|------------|--------|-------|
| Network routing | ✅ Implemented | SW-P-08, Ember+, NMOS IS-05 adapters |
| Device control | ✅ Implemented | Camera/audio/replay/graphics adapters |
| Discovery | ✅ Implemented | NMOS IS-04 client |
| Containerized apps | ✅ Implemented | NAP CRD + 15 certified packages |
| Signal tallies | ❌ Not implemented | UI shows static badges only |
| Security | ❌ Not implemented | No authN/authZ yet |
| Multi-vendor (GV/NEP/Cerebrum) | ❌ Not implemented | Adapters need to be built |
| Compute-agnostic | ⚠️ Partial | Fabric client exists; multi-cluster missing |
| Control/data plane separation | ⚠️ Partial | Architecturally defined; not enforced |
| Dynamic scale on demand | ❌ Not implemented | No scaler/HPA integration |
