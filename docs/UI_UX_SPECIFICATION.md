# Zeus Broadcast Orchestrator — Unified Operator Interface Specification

## Vision
A single, purchasable broadcast operations console that unifies routing, device control, patching, monitoring, and playout automation into one interface. Target operators coming from Cerebrum, NEP TFC, Ross Vision, and Grass Valley AMPP environments.

## Design Principles
1. **Operator-first**: One screen, one workflow. No CLI required for daily operation.
2. **Protocol-agnostic**: UDP, TCP, WebSocket, NMOS, Ember+, SW-P-08 all behind a common control surface.
3. **Environment-agnostic**: On-prem, cloud, and hybrid deployment with a single codebase.
4. **Real-time**: Sub-second status updates via WebSocket/SSE.
5. **Purchasable UI**: Polished, dark-theme broadcast console aesthetic.

## Screen Layout

```
+---------------------------------------------------------------+
|  [Zeus]  Env: [On-Prem]  12:45:32  [System: Healthy] [User] |
+---------------------------------------------------------------+
| [Router] [Rack] [Patch] [Monitor] [Devices] [NAP] [Tools]    |
+--------+-----------------------------------------------------+
|        |                                                     |
|  Module|          MAIN WORKSPACE (tabbed per module)         |
|  Nav   |                                                     |
|        |                                                     |
+--------+-----------------------------------------------------+
|  Status bar: SSE/WebSocket live | Alarms | Log tail          |
+---------------------------------------------------------------+
```

## Module 1 — Router Control (Crosspoint Grid)
- **Visual matrix**: Sources (rows) × Destinations (columns)
- **Cell states**: Free (dark), Active (green), Locked (red), Protect (amber)
- **Actions**: Click cell to take route, right-click to protect/lock, drag to multi-take
- **Protocols**: SW-P-08, Ember+, NMOS IS-05, custom UDP/TCP
- **Persistence**: Routes saved to runbook/NAP context

## Module 2 — Rack System (Virtual Equipment)
- **Rack units**: 1U–4U equipment slots in a 42U rack
- **Equipment types**: Camera, audio, replay, graphics, NAP, router, encoder, decoder
- **Each unit**: Power toggle, status LEDs, parameter knobs, patch points (rear)
- **Drag & drop**: Move equipment between racks
- **Patch points**: Rear-panel connectors that link to the Patch module

## Module 3 — Patch Bay
- **Sources panel** (left): All routable outputs
- **Destinations panel** (right): All routable inputs
- **Connection lines**: Visual patch cords between source and destination
- **Protocols**: UDP multicast/unicast, TCP, ST 2110, NMOS flow
- **Actions**: Click source → click destination to patch; double-click to unpatch
- **Labels**: User-definable patch point names

## Module 4 — Monitoring
- **Multiviewer grid**: Up to 16 configurable boxes
- **Audio meters**: PPM/VU per channel with loudness (LUFS)
- **Video status**: Lock, format, errors (CRC, ancillary)
- **Alarm panel**: Red/amber/green system health
- **Log tail**: Real-time operator log

## Module 5 — Device Control
- **Device tree**: NAPs, cameras, audio, replay, graphics, Cerebrum, AMPP, NEP
- **Per-device panel**: Transport controls, settings, metadata
- **Command queue**: Send/receive with acknowledgment

## Module 6 — NAP & Runbook
- **NAP list**: Certified application packages with status
- **Runbook timeline**: Event-based playout automation
- **Conformance**: NAP validation results

## Module 7 — Broadcast Tools
- **NAP conformance check**
- **Runbook validator**
- **NMOS IS-04 discovery**
- **Configuration export/import**

## Environment Switcher (Top Bar)
- **On-Prem**: Local kind/k8s cluster, direct device IPs
- **Cloud**: Remote k8s, AWS/Azure/GCP, SD-WAN or VPN
- **Hybrid**: Local + cloud mesh, latency indicators per site

## Protocol Abstraction Layer
```
Operator Action → Zeus UI → Protocol Adapter → Device/Network
                                  ↓
                          [UDP] [TCP] [WS] [NMOS] [Ember+] [SW-P-08]
```

## Data Model (Key Types)
- `Router`: id, name, type, inputs[], outputs[], crosspoints[][]
- `RackUnit`: id, name, type, uPosition, heightU, status, patchPoints[]
- `PatchConnection`: id, sourceId, destId, protocol, params, state
- `Device`: id, name, type, address, protocol, status, controls[]
- `Environment`: id, name, type (on-prem/cloud/hybrid), endpoints[]
