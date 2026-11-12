#!/usr/bin/env python3
"""
Zeus Broadcast Orchestrator - Operator Console Proxy
====================================================

Serves the static operator SPA and exposes the Zeus REST/SSE surface by
querying a Kubernetes API server directly.

Why a proxy (rather than the Go kernel serving the SPA)?
  * Operators often run the console from an engineering laptop while the
    control plane lives in a cluster.
  * The proxy keeps cluster credentials off the browser and gives us one
    place to enforce CORS, security headers, and error handling.

Configuration (environment variables):
    PROXY_PORT            Listen port (default 8082)
    ZEUS_STATIC_DIR       Static asset directory (default cmd/zeus-ui/static)
    KUBECONFIG            Path to kubeconfig (default ~/.kube/config)
    KUBE_CONTEXT          Context name (default current-context)
    KUBE_SERVER           Full API server URL, overrides kubeconfig cluster
    KUBE_API_HOST         API host, overrides kubeconfig host
    KUBE_API_PORT         API port, overrides kubeconfig port
    KUBE_REWRITE_LOOPBACK Rewrite 127.0.0.1/localhost to KUBE_LOOPBACK_HOST.
                          Defaults to true inside a container.
    KUBE_LOOPBACK_HOST    Replacement host (default host.docker.internal)
    KUBE_TLS_INSECURE     Set to 1 only to skip certificate verification when
                          the kubeconfig has no CA data.

Usage:
    python ui_proxy.py

Then open: http://localhost:8082
"""

import atexit
import base64
import concurrent.futures
import datetime
import http.client
import http.server
import json
import os
import socket
import socketserver
import ssl
import sys
import tempfile
import threading
import time
from urllib.parse import urlparse

import yaml

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
STATIC_DIR = os.path.abspath(
    os.environ.get("ZEUS_STATIC_DIR", os.path.join(BASE_DIR, "cmd", "zeus-ui", "static"))
)
PROXY_PORT = int(os.environ.get("PROXY_PORT", "8082"))
KUBECONFIG_PATH = os.path.expanduser(
    os.environ.get("KUBECONFIG") or "~/.kube/config"
)

# Zeus API group/version
ZEUS_GROUP = "core.zeus.broadcast"
ZEUS_VERSION = "v1alpha1"

ANSI_RESET = "\033[0m"


def _env_bool(name, default=False):
    value = os.environ.get(name)
    if value is None:
        return default
    return value.strip().lower() in ("1", "true", "yes", "on")


def _write_temp_pem(data, suffix):
    handle = tempfile.NamedTemporaryFile(mode="wb", suffix=suffix, delete=False)
    try:
        handle.write(base64.b64decode(data))
    finally:
        handle.close()
    return handle.name


class KubeConfig:
    """Resolved connection details for the Kubernetes API server."""

    def __init__(self):
        self.server = ""
        self.host = "127.0.0.1"
        self.port = 6443
        self.ca_file = None
        self.client_cert = None
        self.client_key = None
        self.context = ""
        self.loopback_rewritten = False
        self.error = ""
        self._temp_files = []

    def cleanup(self):
        for path in self._temp_files:
            try:
                if path and os.path.exists(path):
                    os.unlink(path)
            except OSError:
                pass
        self._temp_files = []


def load_kube_config():
    cfg = KubeConfig()
    document = {}

    if os.path.exists(KUBECONFIG_PATH):
        try:
            with open(KUBECONFIG_PATH, "r", encoding="utf-8") as handle:
                document = yaml.safe_load(handle) or {}
        except (OSError, yaml.YAMLError) as exc:
            cfg.error = f"cannot read kubeconfig {KUBECONFIG_PATH}: {exc}"
    else:
        cfg.error = f"kubeconfig not found at {KUBECONFIG_PATH}"

    context_name = os.environ.get("KUBE_CONTEXT") or document.get("current-context", "")
    cfg.context = context_name

    cluster = {}
    user = {}
    if document:
        context = next(
            (c for c in document.get("contexts", []) if c.get("name") == context_name),
            {},
        )
        context_body = context.get("context", {})
        cluster = next(
            (
                c
                for c in document.get("clusters", [])
                if c.get("name") == context_body.get("cluster")
            ),
            {},
        ).get("cluster", {})
        user = next(
            (
                u
                for u in document.get("users", [])
                if u.get("name") == context_body.get("user")
            ),
            {},
        ).get("user", {})

    server = os.environ.get("KUBE_SERVER") or cluster.get("server", "")
    parsed = urlparse(server) if server else None
    host = os.environ.get("KUBE_API_HOST") or (parsed.hostname if parsed else "") or "127.0.0.1"
    env_port = os.environ.get("KUBE_API_PORT")
    port = int(env_port) if env_port else ((parsed.port if parsed else None) or 6443)
    scheme = (parsed.scheme if parsed else "") or "https"

    rewrite_default = os.path.exists("/.dockerenv") or os.path.exists("/run/.containerenv")
    if _env_bool("KUBE_REWRITE_LOOPBACK", rewrite_default) and host in (
        "127.0.0.1",
        "localhost",
        "::1",
        "0.0.0.0",
    ):
        host = os.environ.get("KUBE_LOOPBACK_HOST", "host.docker.internal")
        cfg.loopback_rewritten = True

    ca_data = cluster.get("certificate-authority-data")
    ca_path = cluster.get("certificate-authority")
    if ca_data:
        try:
            cfg.ca_file = _write_temp_pem(ca_data, ".ca.pem")
            cfg._temp_files.append(cfg.ca_file)
        except (ValueError, OSError) as exc:
            cfg.error = f"cannot decode cluster CA: {exc}"
    elif ca_path:
        candidate = os.path.expanduser(ca_path)
        if os.path.exists(candidate):
            cfg.ca_file = candidate

    cert_data = user.get("client-certificate-data")
    key_data = user.get("client-key-data")
    if cert_data and key_data:
        try:
            cfg.client_cert = _write_temp_pem(cert_data, ".crt.pem")
            cfg.client_key = _write_temp_pem(key_data, ".key.pem")
            cfg._temp_files.extend([cfg.client_cert, cfg.client_key])
        except (ValueError, OSError) as exc:
            cfg.error = f"cannot decode client credentials: {exc}"

    cfg.server = f"{scheme}://{host}:{port}"
    cfg.host = host
    cfg.port = port
    return cfg


KUBE = load_kube_config()
atexit.register(KUBE.cleanup)


def _build_ssl_context():
    if KUBE.ca_file:
        context = ssl.create_default_context(cafile=KUBE.ca_file)
        # A loopback rewrite (127.0.0.1 -> host.docker.internal) changes the
        # hostname the cluster certificate was issued for. Keep chain
        # verification, skip only the hostname comparison in that case.
        if KUBE.loopback_rewritten:
            context.check_hostname = False
        return context
    if _env_bool("KUBE_TLS_INSECURE"):
        return ssl._create_unverified_context()
    # No CA data: use the platform trust store. If the cluster uses a private
    # CA this fails loudly instead of silently disabling verification.
    return ssl.create_default_context()


SSL_CONTEXT = _build_ssl_context()

if KUBE.client_cert and KUBE.client_key:
    try:
        SSL_CONTEXT.load_cert_chain(KUBE.client_cert, KUBE.client_key)
    except ssl.SSLError as exc:
        print(f"KUBE_AUTH: cannot load client certificate: {exc}", file=sys.stderr, flush=True)
        KUBE.error = f"cannot load client certificate: {exc}"

if KUBE.error:
    print(f"KUBE_CONFIG: {KUBE.error}", file=sys.stderr, flush=True)
print(
    f"KUBE_CONFIG: context={KUBE.context or '(none)'} server={KUBE.server} "
    f"ca={'yes' if KUBE.ca_file else 'no'} client-cert={'yes' if KUBE.client_cert else 'no'}",
    file=sys.stderr,
    flush=True,
)


def kube_get(path, timeout=15):
    """GET a Kubernetes API path and return (parsed_json_or_None, status_code)."""
    if KUBE.error and not KUBE.server:
        return None, 503
    conn = http.client.HTTPSConnection(
        KUBE.host, KUBE.port, timeout=timeout, context=SSL_CONTEXT
    )
    try:
        conn.request(
            "GET",
            path,
            headers={
                "Accept": "application/json",
                "User-Agent": "zeus-ui-proxy/1.0",
            },
        )
        response = conn.getresponse()
        body = response.read()
        if response.status >= 400:
            try:
                detail = json.loads(body).get("message", "")
            except (ValueError, AttributeError):
                detail = body.decode("utf-8", "replace")[:300]
            print(
                f"KUBE_GET {path}: HTTP {response.status} {detail}",
                file=sys.stderr,
                flush=True,
            )
            return None, response.status
        return json.loads(body), response.status
    except ssl.SSLError as exc:
        print(f"KUBE_GET {path}: TLS error: {exc}", file=sys.stderr, flush=True)
        return None, 502
    except (OSError, http.client.HTTPException, ValueError) as exc:
        print(f"KUBE_GET {path}: {exc}", file=sys.stderr, flush=True)
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


def _phase(item):
    return (item.get("status") or {}).get("phase", "") or "Unknown"


def _condition_reason(item):
    conditions = (item.get("status") or {}).get("conditions") or []
    return conditions[0].get("reason", "") if conditions else ""


def extract_zeusrunbook(item):
    """Extract dashboard fields from a ZeusRunbook custom resource."""
    spec = item.get("spec", {})
    status = item.get("status", {})
    meta = item.get("metadata", {})
    return {
        "name": meta.get("name", ""),
        "namespace": meta.get("namespace", "default"),
        "phase": status.get("phase", "") or "Unknown",
        "eventClass": spec.get("eventClass", ""),
        "conditions": status.get("conditions", []),
        "pathHealth": status.get("pathHealth", []),
        "activeNAPs": status.get("activeNAPs", []),
        "suggestedAction": status.get("suggestedAction", ""),
        "creationTime": meta.get("creationTimestamp", ""),
        "conditionReason": _condition_reason(item),
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
        "phase": status.get("phase", "") or "Unknown",
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
        "phase": status.get("phase", "") or "Unknown",
        "napName": spec.get("napName", ""),
        "napVersion": spec.get("napVersion", ""),
        "napType": spec.get("napType", ""),
        "submitter": spec.get("submitter", ""),
        "approvedAt": status.get("approvedAt", ""),
        "rejectedAt": status.get("rejectedAt", ""),
        "creationTime": meta.get("creationTimestamp", ""),
    }


def _phase_counts(items):
    counts = {}
    for item in items:
        phase = _phase(item)
        counts[phase] = counts.get(phase, 0) + 1
    return counts


# ---------------------------------------------------------------------------
# Local mutable state for the routing / tally / patch surfaces.
# The kernel remains the source of truth for real deployments; these
# structures back the console's interactive endpoints in this standalone
# proxy. They are intentionally not persisted across restarts.
# ---------------------------------------------------------------------------

STATE_LOCK = threading.Lock()

ROUTERS = {
    "swp08-1": {
        "id": "swp08-1",
        "name": "SW-P-08 Router A",
        "type": "SW-P-08",
        "status": "online",
        "inputs": 64,
        "outputs": 32,
    },
    "ember-1": {
        "id": "ember-1",
        "name": "Ember+ Router B",
        "type": "Ember+",
        "status": "online",
        "inputs": 48,
        "outputs": 24,
    },
}

ROUTES = {
    "swp08-1": {
        1: {"input": 3, "state": "active"},
        2: {"input": 1, "state": "active"},
        5: {"input": 7, "state": "protected"},
    },
    "ember-1": {
        1: {"input": 2, "state": "active"},
        4: {"input": 6, "state": "locked"},
    },
}

TALLIES = {}
PATCHES = [
    {"id": "p1", "source": "s1", "destination": "d1", "transport": "ST 2110"},
]


class ZeusProxyHandler(http.server.SimpleHTTPRequestHandler):
    """Serves static assets and exposes the Zeus operator API."""

    server_version = "ZeusUIConsole/1.0"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=STATIC_DIR, **kwargs)

    def log_message(self, fmt, *args):
        # Keep the operator console quiet; errors are logged explicitly.
        pass

    # ------------------------------------------------------------------ HTTP

    def do_OPTIONS(self):
        self.send_response(204)
        self._send_common_headers()
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Accept")
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        path = urlparse(self.path).path
        if path.startswith("/api/"):
            self._handle_api_get(path)
            return

        # SPA fallback: unknown non-API paths render the app shell.
        static_path = self.translate_path(path)
        if not os.path.exists(static_path) or os.path.isdir(static_path):
            index_path = os.path.join(STATIC_DIR, "index.html")
            if not os.path.exists(index_path):
                self.send_error(404, "File not found")
                return
            self.path = "/index.html"
        super().do_GET()

    def do_POST(self):
        path = urlparse(self.path).path
        if not path.startswith("/api/"):
            self.send_error(404, "File not found")
            return
        body = self._read_json()
        if body is None:
            self._send_error(400, "invalid JSON body")
            return
        self._handle_api_post(path, body)

    # --------------------------------------------------------------- helpers

    def _send_common_headers(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header(
            "Content-Security-Policy",
            "default-src 'self'; img-src 'self' data:; "
            "style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; "
            "connect-src 'self'",
        )

    def _send_json(self, data, status=200):
        body = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self._send_common_headers()
        self.end_headers()
        self.wfile.write(body)

    def _send_error(self, status, message):
        self._send_json({"error": message, "status": status}, status=status)

    def _read_json(self):
        raw_length = self.headers.get("Content-Length", "0")
        try:
            length = int(raw_length)
        except ValueError:
            return None
        if length <= 0:
            return {}
        if length > 1 << 20:
            return None
        try:
            payload = self.rfile.read(length)
            return json.loads(payload or b"{}")
        except (ValueError, OSError):
            return None

    def _cr_or_error(self, plural, key):
        items, status = list_namespaced_resource(plural)
        if status != 200:
            self._send_error(
                502,
                f"Kubernetes API returned HTTP {status} while listing {plural}",
            )
            return None
        return {key: items, "count": len(items)}

    # ------------------------------------------------------------ API: GET

    def _handle_api_get(self, path):
        if path == "/api/v1/health":
            healthy = self._cluster_healthy()
            self._send_json(
                {
                    "service": "zeus-ui",
                    "status": "healthy" if healthy else "degraded",
                    "cluster": {
                        "server": KUBE.server,
                        "context": KUBE.context,
                        "reachable": healthy,
                        "error": None if healthy else KUBE.error or "API server unreachable",
                    },
                    "version": "1.0.0",
                }
            )
            return

        if path == "/api/v1/events":
            self._handle_sse()
            return

        if path == "/api/v1/stats":
            self._handle_stats()
            return

        if path == "/api/v1/zeusrunbooks":
            payload = self._cr_or_error("zeusrunbooks", "runbooks")
            if payload is not None:
                payload["runbooks"] = [extract_zeusrunbook(i) for i in payload.pop("items")]
                self._send_json(payload)
            return

        if path == "/api/v1/zeusapplicationpackages":
            payload = self._cr_or_error("zeusapplicationpackages", "naps")
            if payload is not None:
                payload["naps"] = [
                    extract_zeusapplicationpackage(i) for i in payload.pop("items")
                ]
                self._send_json(payload)
            return

        if path == "/api/v1/napregistry":
            payload = self._cr_or_error("napregistries", "registries")
            if payload is not None:
                payload["registries"] = [
                    extract_napregistry(i) for i in payload.pop("items")
                ]
                self._send_json(payload)
            return

        if path == "/api/v1/runbooktemplates":
            payload = self._cr_or_error("runbooktemplates", "templates")
            if payload is not None:
                payload["templates"] = [
                    {
                        "name": i.get("metadata", {}).get("name", ""),
                        "namespace": i.get("metadata", {}).get("namespace", "default"),
                        "eventClass": i.get("spec", {}).get("eventClass", ""),
                        "phase": _phase(i),
                    }
                    for i in payload.pop("items")
                ]
                self._send_json(payload)
            return

        if path == "/api/v1/devices":
            items, status = list_namespaced_resource("zeusrunbooks")
            if status != 200:
                self._send_error(502, f"Kubernetes API returned HTTP {status}")
                return
            devices, seen = [], set()
            for runbook in items:
                for nap in runbook.get("spec", {}).get("naps", []):
                    nap_name = nap.get("nap") or nap.get("name") or ""
                    if nap_name and nap_name not in seen:
                        seen.add(nap_name)
                        devices.append(
                            {
                                "id": nap_name,
                                "name": nap.get("name", nap_name),
                                "type": nap.get("kind", "device"),
                                "status": "online",
                            }
                        )
            self._send_json({"devices": devices, "count": len(devices)})
            return

        if path == "/api/v1/routing":
            with STATE_LOCK:
                routers = []
                for router in ROUTERS.values():
                    item = dict(router)
                    item["crosspoints"] = router["inputs"] * router["outputs"]
                    item["routes"] = [
                        {"output": out, **route}
                        for out, route in sorted(ROUTES.get(router["id"], {}).items())
                    ]
                    routers.append(item)
            self._send_json({"routers": routers, "count": len(routers)})
            return

        if path == "/api/v1/visionmixer":
            self._send_json(
                {
                    "mixers": [
                        {"id": "vm-1", "name": "Vision Mixer A", "program": "CAM-1",
                         "preview": "CAM-2", "transition": "mix", "meCount": 2, "status": "online"},
                        {"id": "vm-2", "name": "Vision Mixer B", "program": "GFX-1",
                         "preview": "CAM-3", "transition": "cut", "meCount": 1, "status": "online"},
                    ],
                    "count": 2,
                }
            )
            return

        if path == "/api/v1/servo":
            self._send_json(
                {
                    "servos": [
                        {"id": "servo-1", "name": "Camera Pan-Tilt 1", "type": "pan-tilt",
                         "position": "45°", "target": "45°", "speed": "12", "status": "online"},
                        {"id": "servo-2", "name": "Camera Zoom 1", "type": "zoom",
                         "position": "0%", "target": "0%", "speed": "8", "status": "online"},
                    ],
                    "count": 2,
                }
            )
            return

        if path == "/api/v1/shading":
            self._send_json(
                {
                    "multiviewers": [
                        {"id": "mv-1", "name": "Multiviewer A", "layout": "4x2",
                         "sources": 8, "tallies": 4, "safeZones": True, "status": "online"},
                        {"id": "mv-2", "name": "Multiviewer B", "layout": "2x2",
                         "sources": 4, "tallies": 2, "safeZones": True, "status": "online"},
                    ],
                    "count": 2,
                }
            )
            return

        if path == "/api/v1/monitoring":
            self._send_json(
                {
                    "monitors": [
                        {"id": "mon-1", "name": "Program Monitor", "source": "PGM",
                         "loudness": -23.4, "peak": -6.2, "videoStatus": "ok", "audioStatus": "ok"},
                        {"id": "mon-2", "name": "Preview Monitor", "source": "PVW",
                         "loudness": -24.1, "peak": -8.1, "videoStatus": "ok", "audioStatus": "ok"},
                    ],
                    "count": 2,
                }
            )
            return

        if path == "/api/v1/patch":
            with STATE_LOCK:
                patches = [dict(p) for p in PATCHES]
            self._send_json(
                {
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
                    "patches": patches,
                    "count": len(patches),
                }
            )
            return

        if path == "/api/v1/broadcast-tools":
            self._send_json(
                {
                    "tools": [
                        {"id": "tool-1", "name": "NAP Conformance Check", "category": "conformance",
                         "status": "idle", "lastRun": "—", "result": "—"},
                        {"id": "tool-2", "name": "Runbook Validator", "category": "validation",
                         "status": "idle", "lastRun": "—", "result": "—"},
                        {"id": "tool-3", "name": "NMOS IS-04 Discovery", "category": "discovery",
                         "status": "idle", "lastRun": "—", "result": "—"},
                    ],
                    "count": 3,
                }
            )
            return

        if path == "/api/v1/infrastructure/security/audit":
            self._send_json({"entries": [], "count": 0})
            return

        if path == "/api/v1/infrastructure/security/principals":
            self._send_json(
                {
                    "principals": [
                        {"id": "operator-1", "name": "Operator One", "roles": ["operator"], "method": "apikey"},
                        {"id": "engineer-1", "name": "Engineer One", "roles": ["engineer"], "method": "apikey"},
                        {"id": "admin-1", "name": "Admin", "roles": ["admin"], "method": "mtls"},
                    ],
                    "count": 3,
                }
            )
            return

        if path == "/api/v1/infrastructure/tally":
            with STATE_LOCK:
                tallies = [dict(t) for t in TALLIES.values()]
            self._send_json({"tallies": tallies, "count": len(tallies)})
            return

        if path == "/api/v1/infrastructure/fabric/clusters":
            self._send_json(
                {
                    "clusters": [
                        {"id": "on-prem", "name": "On-Premise Cluster", "environment": "on-prem",
                         "kubeConfig": KUBECONFIG_PATH, "context": KUBE.context,
                         "region": "local", "capacity": 100},
                    ],
                    "count": 1,
                }
            )
            return

        self._send_error(404, f"unknown API endpoint: {path}")

    # ----------------------------------------------------------- API: POST

    def _handle_api_post(self, path, body):
        if path == "/api/v1/routing/take":
            self._update_route(body, state="active")
            return
        if path == "/api/v1/routing/protect":
            self._update_route(body, state="protected")
            return
        if path == "/api/v1/routing/clear":
            self._update_route(body, state=None)
            return

        if path in (
            "/api/v1/infrastructure/tally/route",
            "/api/v1/infrastructure/tally/clear",
            "/api/v1/infrastructure/tally/protect",
        ):
            router = str(body.get("routerID", ""))
            dest = str(body.get("destinationID", ""))
            if not router or not dest:
                self._send_error(400, "routerID and destinationID are required")
                return
            key = f"{router}/{dest}"
            with STATE_LOCK:
                if path.endswith("/route"):
                    TALLIES[key] = {
                        "routerID": router,
                        "destinationID": dest,
                        "sourceID": str(body.get("sourceID", "")),
                        "state": "active",
                        "updatedAt": datetime.datetime.now(datetime.UTC).isoformat(),
                    }
                elif path.endswith("/protect"):
                    TALLIES[key] = {
                        "routerID": router,
                        "destinationID": dest,
                        "sourceID": TALLIES.get(key, {}).get("sourceID", ""),
                        "state": "protected",
                        "updatedAt": datetime.datetime.now(datetime.UTC).isoformat(),
                    }
                else:
                    TALLIES.pop(key, None)
            self._send_json({"status": "ok"})
            return

        if path in (
            "/api/v1/infrastructure/fabric/scale",
            "/api/v1/infrastructure/fabric/burst",
        ):
            # This standalone proxy has no authority to mutate a cluster's
            # workload scale. Report that explicitly instead of pretending.
            self._send_error(
                501,
                "fabric scale/burst is served by the Zeus kernel API in a full deployment",
            )
            return

        if path == "/api/v1/patch/connect":
            source = str(body.get("sourceID", ""))
            dest = str(body.get("destinationID", ""))
            if not source or not dest:
                self._send_error(400, "sourceID and destinationID are required")
                return
            with STATE_LOCK:
                PATCHES.append(
                    {
                        "id": f"p{len(PATCHES) + 1}",
                        "source": source,
                        "destination": dest,
                        "transport": body.get("transport", "UDP"),
                    }
                )
            self._send_json({"status": "ok"})
            return

        if path == "/api/v1/patch/disconnect":
            source = str(body.get("sourceID", ""))
            dest = str(body.get("destinationID", ""))
            with STATE_LOCK:
                PATCHES[:] = [
                    p for p in PATCHES
                    if not (p["source"] == source and p["destination"] == dest)
                ]
            self._send_json({"status": "ok"})
            return

        if path == "/api/v1/infrastructure/security/apikey":
            self._send_error(
                501,
                "API key issuance requires the Zeus kernel security subsystem",
            )
            return

        self._send_error(404, f"unknown API endpoint: {path}")

    def _update_route(self, body, state):
        router_id = str(body.get("routerID", ""))
        try:
            output = int(body.get("output", 0))
            input_number = int(body.get("input", 0))
        except (TypeError, ValueError):
            self._send_error(400, "input and output must be integers")
            return
        if router_id not in ROUTERS:
            self._send_error(404, f"unknown router: {router_id}")
            return
        if not 1 <= output <= ROUTERS[router_id]["outputs"]:
            self._send_error(400, "output out of range")
            return
        if state is not None and not 1 <= input_number <= ROUTERS[router_id]["inputs"]:
            self._send_error(400, "input out of range")
            return
        with STATE_LOCK:
            routes = ROUTES.setdefault(router_id, {})
            if state is None:
                routes.pop(output, None)
            else:
                routes[output] = {"input": input_number, "state": state}
        self._send_json({"status": "ok", "routerID": router_id, "output": output})

    # -------------------------------------------------------------- stats/SSE

    def _cluster_healthy(self):
        if not KUBE.server:
            return False
        _, status = kube_get("/version", timeout=3)
        return status == 200

    def _collect_stats(self, timeout=5):
        # The three list calls are independent; run them concurrently so a slow
        # or unreachable API server costs one timeout, not three.
        targets = (
            ("runbooks", "zeusrunbooks"),
            ("naps", "zeusapplicationpackages"),
            ("registry", "napregistries"),
        )
        with concurrent.futures.ThreadPoolExecutor(
            max_workers=len(targets)
        ) as pool:
            fetched = dict(
                zip(
                    (key for key, _ in targets),
                    pool.map(
                        lambda target: list_namespaced_resource(
                            target[1], timeout=timeout
                        ),
                        targets,
                    ),
                )
            )
        runbooks, rb_status = fetched["runbooks"]
        naps, nap_status = fetched["naps"]
        registry, reg_status = fetched["registry"]
        statuses = {"runbooks": rb_status, "naps": nap_status, "registry": reg_status}
        reachable = all(status == 200 for status in statuses.values())
        return (
            {
                "runbooks": {"total": len(runbooks), "phases": _phase_counts(runbooks)},
                "naps": {"total": len(naps), "phases": _phase_counts(naps)},
                "registry": {"total": len(registry), "phases": _phase_counts(registry)},
                "cluster": {
                    "reachable": reachable,
                    "server": KUBE.server,
                    "context": KUBE.context,
                    "errors": {
                        name: status
                        for name, status in statuses.items()
                        if status != 200
                    },
                },
                "networkProtocols": {
                    "bgp": {"state": "unknown", "sessions": 0, "disruptions": 0},
                    "igmp": {"state": "unknown", "groups": 0},
                    "srt": {"state": "unknown", "paths": 0, "rttMs": 0},
                    "rist": {"state": "unknown", "paths": 0},
                },
                "timestamp": datetime.datetime.now(datetime.UTC).isoformat(),
            },
            reachable,
        )

    def _handle_stats(self):
        stats, reachable = self._collect_stats()
        self._send_json(stats, status=200 if reachable else 502)

    def _handle_sse(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream; charset=utf-8")
        self.send_header("Cache-Control", "no-cache, no-transform")
        self.send_header("Connection", "keep-alive")
        self.send_header("X-Accel-Buffering", "no")
        self._send_common_headers()
        self.end_headers()

        try:
            self.wfile.write(b"retry: 5000\n\n")
            self.wfile.flush()
            while True:
                stats, reachable = self._collect_stats(timeout=5)
                payload = {"event": "stats", "reachable": reachable, **stats}
                message = f"data: {json.dumps(payload)}\n\n"
                self.wfile.write(message.encode("utf-8"))
                self.wfile.flush()
                time.sleep(5)
        except (BrokenPipeError, ConnectionResetError, OSError):
            return


class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    # SO_REUSEADDR on Windows lets a second process bind a port that is already
    # being served, so requests can silently land in a stale instance.
    allow_reuse_address = os.name != "nt"

    def server_bind(self):
        if os.name == "nt":
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        super().server_bind()


def main():
    os.chdir(BASE_DIR)
    if hasattr(sys.stdout, "reconfigure"):
        # Logs are usually redirected to a file; keep the banner visible.
        sys.stdout.reconfigure(line_buffering=True)
    try:
        server = ThreadingHTTPServer(("0.0.0.0", PROXY_PORT), ZeusProxyHandler)
    except OSError as exc:
        print(
            f"ERROR: cannot listen on port {PROXY_PORT}: {exc}\n"
            "Another operator console may already be running.",
            file=sys.stderr,
            flush=True,
        )
        raise SystemExit(1)
    print("=" * 66)
    print("  Zeus Broadcast Orchestrator - Operator Console")
    print("=" * 66)
    print(f"  Listening:   http://0.0.0.0:{PROXY_PORT}")
    print(f"  Static dir:  {STATIC_DIR}")
    print(f"  K8s API:     {KUBE.server}")
    print(f"  Context:     {KUBE.context or '(none)'}")
    print(f"  Kubeconfig:  {KUBECONFIG_PATH}")
    if KUBE.error:
        print(f"  Warning:     {KUBE.error}")
    print("=" * 66)
    print(f"  Open http://localhost:{PROXY_PORT}")
    print("=" * 66)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
