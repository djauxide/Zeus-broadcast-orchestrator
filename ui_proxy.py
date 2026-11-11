#!/usr/bin/env python3
"""
Zeus Broadcast Orchestrator - UI Proxy Server
=============================================

Serves the static production dashboard and provides Zeus API endpoints by
querying the kind Kubernetes cluster directly via the Windows port-forward
(127.0.0.1:44837).

This runs on Windows and bypasses the Podman WSL VM entirely for data calls,
so the UI is fast and responsive even when the VM is memory-constrained.

The Go UI server inside the VM is NOT used for data - this proxy queries
kind's k8s API directly for custom resources (ZeusRunbook, ZeusApplicationPackage,
NAPRegistry) and returns them in the format the dashboard expects.

Usage:
    python ui_proxy.py

Then open: http://localhost:8082
"""

import base64
import datetime
import http.client
import http.server
import json
import os
import socketserver
import ssl
import sys
import tempfile
import threading
import time

import yaml

STATIC_DIR = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "cmd", "zeus-ui", "static"
)
KUBE_API_HOST = "127.0.0.1"
KUBE_API_PORT = 42625
PROXY_PORT = 8082

# Zeus API group/version
ZEUS_GROUP = "core.zeus.broadcast"
ZEUS_VERSION = "v1alpha1"

# Load kubeconfig for client cert authentication
KUBECONFIG_PATH = os.path.expanduser("~/.kube/config")
KUBE_CLIENT_CERT = None
KUBE_CLIENT_KEY = None

if os.path.exists(KUBECONFIG_PATH):
    with open(KUBECONFIG_PATH, "r") as f:
        kubeconfig = yaml.safe_load(f)
    users = kubeconfig.get("users", [])
    for user in users:
        user_data = user.get("user", {})
        cert_data = user_data.get("client-certificate-data")
        key_data = user_data.get("client-key-data")
        if cert_data and key_data:
            # Write cert and key to temp files for ssl context
            cert_file = tempfile.NamedTemporaryFile(
                mode="wb", suffix=".pem", delete=False
            )
            cert_file.write(base64.b64decode(cert_data))
            cert_file.close()
            KUBE_CLIENT_CERT = cert_file.name

            key_file = tempfile.NamedTemporaryFile(
                mode="wb", suffix=".pem", delete=False
            )
            key_file.write(base64.b64decode(key_data))
            key_file.close()
            KUBE_CLIENT_KEY = key_file.name
            break
    if KUBE_CLIENT_CERT:
        print(f"KUBE_AUTH: Loaded client cert from {KUBECONFIG_PATH}", file=sys.stderr, flush=True)
    else:
        print("KUBE_AUTH: No client cert found in kubeconfig, using anonymous auth", file=sys.stderr, flush=True)

# Cleanup temp files on exit
import atexit

def _cleanup_temp_files():
    for f in (KUBE_CLIENT_CERT, KUBE_CLIENT_KEY):
        if f and os.path.exists(f):
            try:
                os.unlink(f)
            except OSError:
                pass

atexit.register(_cleanup_temp_files)


def kube_get(path, timeout=15):
    """Make a GET request to the kind k8s API server and return parsed JSON."""
    context = ssl._create_unverified_context()
    if KUBE_CLIENT_CERT and KUBE_CLIENT_KEY:
        context.load_cert_chain(KUBE_CLIENT_CERT, KUBE_CLIENT_KEY)
    conn = http.client.HTTPSConnection(
        KUBE_API_HOST, KUBE_API_PORT, timeout=timeout, context=context
    )
    try:
        conn.request(
            "GET",
            path,
            headers={"Host": f"{KUBE_API_HOST}:{KUBE_API_PORT}", "Accept": "application/json"},
        )
        resp = conn.getresponse()
        if resp.status >= 400:
            return None, resp.status
        data = resp.read()
        return json.loads(data), resp.status
    except Exception as e:
        print(f"KUBE_GET ERROR: {path}: {e}", file=sys.stderr, flush=True)
        return None, 502
    finally:
        conn.close()


def list_namespaced_resource(plural, timeout=15):
    """List a namespaced custom resource from the Zeus API group."""
    path = f"/apis/{ZEUS_GROUP}/{ZEUS_VERSION}/{plural}"
    data, status = kube_get(path, timeout)
    if data is None:
        return [], status
    return data.get("items", []), status


def extract_zeusrunbook(item):
    """Extract dashboard fields from a ZeusRunbook custom resource."""
    spec = item.get("spec", {})
    status = item.get("status", {})
    meta = item.get("metadata", {})
    conditions = status.get("conditions", [])
    condition_reason = ""
    if conditions and len(conditions) > 0:
        condition_reason = conditions[0].get("reason", "")
    return {
        "name": meta.get("name", ""),
        "namespace": meta.get("namespace", "default"),
        "phase": status.get("phase", ""),
        "eventClass": spec.get("eventClass", ""),
        "conditions": conditions,
        "pathHealth": status.get("pathHealth", []),
        "activeNAPs": status.get("activeNAPs", []),
        "suggestedAction": status.get("suggestedAction", ""),
        "creationTime": meta.get("creationTimestamp", ""),
        "conditionReason": condition_reason,
    }


def extract_zeusapplicationpackage(item):
    """Extract dashboard fields from a ZeusApplicationPackage custom resource."""
    spec = item.get("spec", {})
    status = item.get("status", {})
    meta = item.get("metadata", {})
    crm = spec.get("crm", {})
    nmos = spec.get("nmos", {})
    return {
        "name": meta.get("name", ""),
        "namespace": meta.get("namespace", "default"),
        "phase": status.get("phase", ""),
        "image": spec.get("image", ""),
        "imageSignature": spec.get("imageSignature", ""),
        "observedGen": status.get("observedGen", 0),
        "cpu": crm.get("cpu", ""),
        "memory": crm.get("memory", ""),
        "domainRef": crm.get("mediaIO", {}).get("domainRef", ""),
        "transport": nmos.get("transport", ""),
        "creationTime": meta.get("creationTimestamp", ""),
    }


def extract_napregistry(item):
    """Extract dashboard fields from a NAPRegistry custom resource."""
    spec = item.get("spec", {})
    status = item.get("status", {})
    meta = item.get("metadata", {})
    return {
        "name": meta.get("name", ""),
        "namespace": meta.get("namespace", "default"),
        "phase": status.get("phase", ""),
        "napName": spec.get("napName", ""),
        "napVersion": spec.get("napVersion", ""),
        "napType": spec.get("napType", ""),
        "submitter": spec.get("submitter", ""),
        "approvedAt": status.get("approvedAt", ""),
        "rejectedAt": status.get("rejectedAt", ""),
        "creationTime": meta.get("creationTimestamp", ""),
    }


class ZeusProxyHandler(http.server.SimpleHTTPRequestHandler):
    """HTTP request handler that serves static files and provides Zeus API endpoints
    by querying kind's k8s API directly."""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=STATIC_DIR, **kwargs)

    def log_message(self, format, *args):
        # Suppress default logging to keep console clean
        pass

    def do_GET(self):
        path = self.path

        # ===== Zeus API endpoints (queried from kind k8s API directly) =====

        if path == "/api/v1/health":
            self._send_json({"service": "zeus-ui", "status": "healthy"})
            return

        # ===== SSE Events =====

        if path == "/api/v1/events":
            self._handle_sse()
            return

        if path == "/api/v1/stats":
            self._handle_stats()
            return

        if path == "/api/v1/zeusrunbooks":
            items, status = list_namespaced_resource("zeusrunbooks")
            runbooks = [extract_zeusrunbook(i) for i in items]
            self._send_json({"runbooks": runbooks, "count": len(runbooks)})
            return

        if path == "/api/v1/zeusapplicationpackages":
            items, status = list_namespaced_resource("zeusapplicationpackages")
            naps = [extract_zeusapplicationpackage(i) for i in items]
            self._send_json({"naps": naps, "count": len(naps)})
            return

        if path == "/api/v1/napregistry":
            items, status = list_namespaced_resource("napregistries")
            registry = [extract_napregistry(i) for i in items]
            self._send_json({"registries": registry, "count": len(registry)})
            return

        if path == "/api/v1/devices":
            # Derive devices from runbooks' NAP placements
            items, _ = list_namespaced_resource("zeusrunbooks")
            devices = []
            seen = set()
            for rb in items:
                for nap in rb.get("spec", {}).get("naps", []):
                    nap_name = nap.get("nap", nap.get("name", ""))
                    if nap_name and nap_name not in seen:
                        seen.add(nap_name)
                        devices.append({
                            "id": nap_name,
                            "name": nap.get("name", nap_name),
                            "type": "device",
                            "status": "online",
                        })
            self._send_json({"devices": devices, "count": len(devices)})
            return

        # ===== Mock broadcast tooling endpoints =====

        if path == "/api/v1/routing":
            self._send_json({
                "routers": [
                    {"id": "swp08-1", "name": "SW-P-08 Router A", "type": "SW-P-08",
                     "status": "online", "inputs": 64, "outputs": 32, "crosspoints": 2048},
                    {"id": "ember-1", "name": "Ember+ Router B", "type": "Ember+",
                     "status": "online", "inputs": 48, "outputs": 24, "crosspoints": 1152},
                ],
                "count": 2,
            })
            return

        if path == "/api/v1/visionmixer":
            self._send_json({
                "mixers": [
                    {"id": "vm-1", "name": "Vision Mixer A", "program": "CAM-1",
                     "preview": "CAM-2", "transition": "mix", "meCount": 2, "status": "online"},
                    {"id": "vm-2", "name": "Vision Mixer B", "program": "GFX-1",
                     "preview": "CAM-3", "transition": "cut", "meCount": 1, "status": "online"},
                ],
                "count": 2,
            })
            return

        if path == "/api/v1/servo":
            self._send_json({
                "servos": [
                    {"id": "servo-1", "name": "Camera Pan-Tilt 1", "type": "pan-tilt",
                     "position": "45°", "target": "45°", "speed": "12", "status": "online"},
                    {"id": "servo-2", "name": "Camera Zoom 1", "type": "zoom",
                     "position": "0%", "target": "0%", "speed": "8", "status": "online"},
                ],
                "count": 2,
            })
            return

        if path == "/api/v1/shading":
            self._send_json({
                "multiviewers": [
                    {"id": "mv-1", "name": "Multiviewer A", "layout": "4x2",
                     "sources": 8, "tallies": 4, "safeZones": True, "status": "online"},
                    {"id": "mv-2", "name": "Multiviewer B", "layout": "2x2",
                     "sources": 4, "tallies": 2, "safeZones": True, "status": "online"},
                ],
                "count": 2,
            })
            return

        if path == "/api/v1/monitoring":
            self._send_json({
                "monitors": [
                    {"id": "mon-1", "name": "Program Monitor", "source": "PGM",
                     "loudness": -23.4, "peak": -6.2, "videoStatus": "ok", "audioStatus": "ok"},
                    {"id": "mon-2", "name": "Preview Monitor", "source": "PVW",
                     "loudness": -24.1, "peak": -8.1, "videoStatus": "ok", "audioStatus": "ok"},
                ],
                "count": 2,
            })
            return

        if path == "/api/v1/patch":
            self._send_json({
                "sources": [
                    {"id": "s1", "name": "Camera 1 SDI Out", "type": "video"},
                    {"id": "s2", "name": "Camera 2 SDI Out", "type": "video"},
                    {"id": "s3", "name": "Graphics Out", "type": "video"},
                ],
                "dests": [
                    {"id": "d1", "name": "Vision Mixer In 1", "type": "video"},
                    {"id": "d2", "name": "Replay In", "type": "video"},
                    {"id": "d3", "name": "Multiviewer In", "type": "video"},
                ],
            })
            return

        if path == "/api/v1/broadcast-tools":
            self._send_json({
                "tools": [
                    {"id": "tool-1", "name": "NAP Conformance Check", "category": "conformance",
                     "status": "idle", "lastRun": "—", "result": "—"},
                    {"id": "tool-2", "name": "Runbook Validator", "category": "validation",
                     "status": "idle", "lastRun": "—", "result": "—"},
                    {"id": "tool-3", "name": "NMOS IS-04 Discovery", "category": "discovery",
                     "status": "idle", "lastRun": "—", "result": "—"},
                ],
                "count": 3,
            })
            return

        # ===== Infrastructure endpoints (mock) =====

        if path == "/api/v1/infrastructure/security/audit":
            self._send_json({"entries": [], "count": 0})
            return

        if path == "/api/v1/infrastructure/security/principals":
            self._send_json({
                "principals": [
                    {"id": "operator-1", "name": "Operator One", "roles": ["operator"], "method": "apikey"},
                    {"id": "engineer-1", "name": "Engineer One", "roles": ["engineer"], "method": "apikey"},
                    {"id": "admin-1", "name": "Admin", "roles": ["admin"], "method": "mtls"},
                ],
                "count": 3,
            })
            return

        if path == "/api/v1/infrastructure/tally":
            self._send_json({"tallies": [], "count": 0})
            return

        if path == "/api/v1/infrastructure/fabric/clusters":
            self._send_json({
                "clusters": [
                    {"id": "on-prem", "name": "On-Premise Kind", "environment": "on-prem",
                     "kubeConfig": "~/.kube/config", "context": "", "region": "local", "capacity": 100},
                ],
                "count": 1,
            })
            return

        # ===== Static files (SPA fallback to index.html) =====

        static_path = self.translate_path(path)
        if not os.path.exists(static_path) or os.path.isdir(static_path):
            index_path = os.path.join(STATIC_DIR, "index.html")
            if os.path.exists(index_path):
                self.path = "/index.html"
            else:
                self.send_error(404, "File not found")
                return

        super().do_GET()

    def do_POST(self):
        path = self.path

        # Tally controls (mock - just acknowledge)
        if path in ("/api/v1/infrastructure/tally/route",
                    "/api/v1/infrastructure/tally/clear",
                    "/api/v1/infrastructure/tally/protect"):
            self._send_json({"status": "ok"})
            return

        # Fabric scale/burst (mock)
        if path in ("/api/v1/infrastructure/fabric/scale",
                    "/api/v1/infrastructure/fabric/burst"):
            self._send_json({"status": "ok"})
            return

        # Security register API key (mock)
        if path == "/api/v1/infrastructure/security/apikey":
            self._send_json({"status": "registered"})
            return

        self.send_error(405, "Method Not Allowed")

    def _send_json(self, data, status=200):
        body = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body)

    def _handle_stats(self):
        """Aggregate stats from k8s custom resources."""
        runbooks, _ = list_namespaced_resource("zeusrunbooks")
        naps, _ = list_namespaced_resource("zeusapplicationpackages")
        registry, _ = list_namespaced_resource("napregistries")

        rb_phases = {}
        for rb in runbooks:
            p = rb.get("phase", "unknown")
            rb_phases[p] = rb_phases.get(p, 0) + 1

        nap_phases = {}
        for nap in naps:
            p = nap.get("phase", "unknown")
            nap_phases[p] = nap_phases.get(p, 0) + 1

        reg_phases = {}
        for reg in registry:
            p = reg.get("phase", "unknown")
            reg_phases[p] = reg_phases.get(p, 0) + 1

        self._send_json({
            "runbooks": {"total": len(runbooks), "phases": rb_phases},
            "naps": {"total": len(naps), "phases": nap_phases},
            "registry": {"total": len(registry), "phases": reg_phases},
            "networkProtocols": {
                "bgp": {"state": "up", "sessions": 2, "disruptions": 0},
                "igmp": {"state": "up", "groups": 4},
                "srt": {"state": "up", "paths": 2, "rttMs": 45},
                "rist": {"state": "up", "paths": 2},
            },
            "timestamp": datetime.datetime.now(datetime.UTC).isoformat() + "Z",
        })

    def _handle_sse(self):
        """SSE event stream for real-time dashboard updates."""
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "keep-alive")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()

        # Send initial connected event
        self.wfile.write(b"event: connected\ndata: {\"status\":\"connected\"}\n\n")
        self.wfile.flush()

        # Send periodic stats updates
        for i in range(120):  # Run for ~2 minutes max per connection
            try:
                time.sleep(5)
                runbooks, _ = list_namespaced_resource("zeusrunbooks", timeout=5)
                naps, _ = list_namespaced_resource("zeusapplicationpackages", timeout=5)
                registry, _ = list_namespaced_resource("napregistries", timeout=5)

                rb_phases = {}
                for rb in runbooks:
                    p = rb.get("phase", "unknown")
                    rb_phases[p] = rb_phases.get(p, 0) + 1
                nap_phases = {}
                for nap in naps:
                    p = nap.get("phase", "unknown")
                    nap_phases[p] = nap_phases.get(p, 0) + 1
                reg_phases = {}
                for reg in registry:
                    p = reg.get("phase", "unknown")
                    reg_phases[p] = reg_phases.get(p, 0) + 1

                stats = {
                    "runbooks": {"total": len(runbooks), "phases": rb_phases},
                    "naps": {"total": len(naps), "phases": nap_phases},
                    "registry": {"total": len(registry), "phases": reg_phases},
                    "networkProtocols": {
                        "bgp": {"state": "up", "sessions": 2, "disruptions": 0},
                        "igmp": {"state": "up", "groups": 4},
                        "srt": {"state": "up", "paths": 2, "rttMs": 45},
                        "rist": {"state": "up", "paths": 2},
                    },
                    "timestamp": datetime.datetime.now(datetime.UTC).isoformat() + "Z",
                }

                msg = f"event: stats\ndata: {json.dumps(stats)}\n\n"
                self.wfile.write(msg.encode("utf-8"))
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                break
            except Exception:
                continue


class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    server = ThreadingHTTPServer(("0.0.0.0", PROXY_PORT), ZeusProxyHandler)
    print("=" * 60)
    print("  Zeus Broadcast Orchestrator - UI Proxy Server")
    print("=" * 60)
    print(f"  Listening:   http://0.0.0.0:{PROXY_PORT}")
    print(f"  Static dir:  {STATIC_DIR}")
    print(f"  K8s API:     https://{KUBE_API_HOST}:{KUBE_API_PORT} (kind via port-forward)")
    print("=" * 60)
    print("  Open http://localhost:8082 in your browser")
    print("=" * 60)
    server.serve_forever()


if __name__ == "__main__":
    main()
