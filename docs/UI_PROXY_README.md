# Zeus UI Proxy — Windows Quick Start

The Python proxy runs on Windows and serves the production dashboard directly,
bypassing the Podman WSL VM entirely. This makes the UI fast and responsive
even on memory-constrained VMs.

## Prerequisites

- Python 3.7+ installed on Windows
- `kubectl` working on Windows with `C:\Users\djaux\.kube\config`
- kind-control-plane running with port `127.0.0.1:44837` forwarded to `6443`

Verify the port is open:
```powershell
kubectl get nodes
```

## Start the Proxy

Double-click `start_ui.bat` or run in PowerShell:
```powershell
.\start_ui.ps1
```

Or manually:
```powershell
python ui_proxy.py
```

## Open the Dashboard

```
http://localhost:8082
```

## Architecture

```
Browser (Windows) ──► Python Proxy (localhost:8082) ──► kind API (127.0.0.1:44837)
                                                    ▲
                                            Windows port-forward
                                                    │
                                              kind-control-plane (Podman VM)
```

The proxy:
- Serves `cmd/zeus-ui/static/` (the SPA dashboard)
- Forwards `/api/*` to kind via the existing Windows port-forward
- Streams SSE `/api/v1/events` for real-time updates
- Falls back to `index.html` for client-side routing

## Troubleshooting

**Port 8082 already in use:**
```powershell
netstat -ano | findstr :8082
taskkill /PID <PID> /F
```

**kind port 44837 not reachable:**
```powershell
podman machine start
podman start kind-control-plane
kubectl get nodes
```

**API calls return 502:**
Ensure kind-control-plane is running and the port-forward is active:
```powershell
podman machine ssh 'podman port kind-control-plane'
# Should show: 6443/tcp -> 127.0.0.1:44837
```
