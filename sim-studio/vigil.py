"""vigil.py — read-only client for the live obsd surfaces.

This is how the studio *observes* Vigil. It is strictly read-only: it pulls the same
``/api`` surfaces the console renders plus the ``/mcp`` JSON-RPC relay the MCP-connected
agent uses. It never writes to Vigil — the only thing that changes the cluster is the
fault engine (``cluster.py``); Vigil simply *sees* the result.

Every method returns plain dicts/lists so the Streamlit layer can render them directly.
Errors are surfaced (not hidden behind fake data) so the operator always knows what is real.
"""
from __future__ import annotations

import json
import os
import urllib.request

OBSD_BASE = os.environ.get("OBSD_BASE", "http://localhost:9095")


# --------------------------------------------------------------------------- #
# low-level transport
# --------------------------------------------------------------------------- #
def _get(path: str, timeout: int = 6) -> dict:
    try:
        with urllib.request.urlopen(OBSD_BASE + path, timeout=timeout) as r:
            return json.loads(r.read().decode())
    except Exception as e:  # honest: an error is reported, never masked with mock data
        return {"_error": f"{type(e).__name__}: {e}"}


def ready() -> bool:
    try:
        with urllib.request.urlopen(OBSD_BASE + "/readyz", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


# --------------------------------------------------------------------------- #
# /mcp JSON-RPC relay — the exact surface the MCP-connected agent calls.
# A tools/call result wraps the view JSON as a STRING in content[0].text; we
# double-parse it. A disabled lane returns {available:false, note}, not an error.
# --------------------------------------------------------------------------- #
_mcp_id = 0


def _mcp(method: str, params: dict | None = None, timeout: int = 12) -> dict:
    global _mcp_id
    _mcp_id += 1
    body = json.dumps({"jsonrpc": "2.0", "id": _mcp_id, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(OBSD_BASE + "/mcp", data=body, headers={"content-type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read().decode())
    except Exception as e:
        return {"error": {"message": f"{type(e).__name__}: {e}"}}


def mcp_list_tools() -> list[dict]:
    env = _mcp("tools/list")
    if env.get("error"):
        return []
    return env.get("result", {}).get("tools", [])


def mcp_call(tool: str, arguments: dict | None = None) -> dict | list:
    """Call one MCP tool and return its parsed view (or {"_error": ...})."""
    env = _mcp("tools/call", {"name": tool, "arguments": arguments or {}})
    if env.get("error"):
        return {"_error": env["error"].get("message", "mcp error")}
    try:
        text = env["result"]["content"][0]["text"]
        return json.loads(text)
    except Exception as e:
        return {"_error": f"unparseable MCP result: {e}"}


# --------------------------------------------------------------------------- #
# the specific /api surfaces the studio highlights
# --------------------------------------------------------------------------- #
def findings(active_only: bool = True) -> list[dict]:
    d = _get("/api/findings")
    rows = d.get("findings", []) if isinstance(d, dict) else []
    if active_only:
        rows = [r for r in rows if not r.get("stale")]
    return rows


def warnings() -> dict:
    return _get("/api/warnings")


def departures() -> dict:
    return _get("/api/departures")


def cross_service() -> dict:
    return _get("/api/cross-service")


def root_cause_chain() -> dict:
    return _get("/api/root-cause-chain")


def onsets() -> dict:
    return _get("/api/onsets")


def unexplained() -> dict:
    return _get("/api/unexplained")


def incidents() -> dict:
    return _get("/api/incidents")


def insights() -> dict:
    return _get("/api/insights")


def dependency() -> dict:
    return _get("/api/dependency")


def causal_hypotheses() -> dict:
    return _get("/api/causal-hypotheses")


def right_sizing() -> dict:
    return _get("/api/right-sizing")


def events() -> dict:
    return _get("/api/events")


def config() -> dict:
    return _get("/api/config")


def mcp_tool_count() -> int:
    return len(mcp_list_tools())


# --------------------------------------------------------------------------- #
# compact one-shot snapshot for the "Vigil Live" pane
# --------------------------------------------------------------------------- #
def snapshot() -> dict:
    w = warnings()
    xs = cross_service()
    rc = root_cause_chain()
    on = onsets()
    inc = incidents()
    f = findings()
    dep = dependency()
    rs = right_sizing()

    def _list(d: dict, *keys) -> list:
        if not isinstance(d, dict):
            return []
        for k in keys:
            v = d.get(k)
            if isinstance(v, list):
                return v
        return []

    return {
        "ready": ready(),
        "active_findings": [
            {
                "phenomenon": x.get("phenomenon"),
                "entity": (x.get("entityCei") or "")[-44:],
                "quality": x.get("quality"),
                "severity": x.get("severity"),
            }
            for x in f
        ],
        "forecast_cards": _list(w, "warnings", "cards"),
        "departures": _list(departures(), "departures", "open", "openCards"),
        "cross_service_active": bool(xs.get("active")) if isinstance(xs, dict) else False,
        "cross_service_chain": xs.get("chain") if isinstance(xs, dict) else None,
        "root_cause_active": bool(rc.get("active")) if isinstance(rc, dict) else False,
        "root_cause": rc if isinstance(rc, dict) else {},
        "onset_count": len(_list(on, "onsets")),
        "incidents": _list(inc, "incidents"),
        "dependency_edges": (dep.get("edges") if isinstance(dep, dict) else []) or [],
        "rightsizing": _list(rs, "advisories", "advice", "rows", "recommendations"),
    }
