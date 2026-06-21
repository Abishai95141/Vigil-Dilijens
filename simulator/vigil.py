"""vigil.py — read-only client for the live obsd surfaces (the operator's view of how
Vigil is reacting). Pulls the same /api the console uses plus the /mcp relay. Strictly
read-only: this module never writes to Vigil (it only observes)."""
from __future__ import annotations

import json
import urllib.request


class Cfg:
    base = "http://localhost:9095"


CFG = Cfg()


def _get(path: str, timeout: int = 5) -> dict:
    try:
        with urllib.request.urlopen(CFG.base + path, timeout=timeout) as r:
            return json.loads(r.read().decode())
    except Exception as e:  # honest: surface the error rather than fake data
        return {"_error": f"{type(e).__name__}: {e}"}


def ready() -> bool:
    try:
        with urllib.request.urlopen(CFG.base + "/readyz", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


# --- the surfaces the simulator highlights ---------------------------------

def findings() -> list[dict]:
    d = _get("/api/findings")
    rows = d.get("findings", []) if isinstance(d, dict) else []
    return [r for r in rows if not r.get("stale")]


def all_findings() -> list[dict]:
    d = _get("/api/findings")
    return d.get("findings", []) if isinstance(d, dict) else []


def warnings() -> dict:
    return _get("/api/warnings")


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


def blindspots() -> dict:
    return _get("/api/blindspots")


def mcp_tool_count() -> int:
    try:
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}).encode()
        req = urllib.request.Request(
            CFG.base + "/mcp", data=body, headers={"content-type": "application/json"}
        )
        with urllib.request.urlopen(req, timeout=5) as r:
            return len(json.loads(r.read().decode())["result"]["tools"])
    except Exception:
        return -1


def snapshot() -> dict:
    """A compact one-shot of how Vigil currently sees the cluster (for the live view)."""
    w = warnings()
    xs = cross_service()
    rc = root_cause_chain()
    on = onsets()
    ux = unexplained()
    inc = incidents()
    f = findings()
    return {
        "ready": ready(),
        "active_findings": [
            {"phenomenon": x.get("phenomenon"), "on": x.get("name"), "quality": x.get("quality")} for x in f
        ],
        "forecast_cards": (w.get("warnings", []) if isinstance(w, dict) else []),
        "cross_service_active": bool(xs.get("active")) if isinstance(xs, dict) else False,
        "cross_service_chain": (xs.get("chain") if isinstance(xs, dict) else None),
        "root_cause_chains": (rc.get("chains", []) if isinstance(rc, dict) else []),
        "onset_count": (len(on.get("onsets", [])) if isinstance(on, dict) else 0),
        "unexplained": (ux.get("openCards") or ux.get("open_cards") or []) if isinstance(ux, dict) else [],
        "incidents": (inc.get("incidents", []) if isinstance(inc, dict) else []),
    }
