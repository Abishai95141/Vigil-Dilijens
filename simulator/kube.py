"""kube.py — thin, transparent kubectl wrappers for the failure simulator.

Every action shells out to `kubectl` using the operator's existing kubeconfig + context,
so nothing is hidden: each call returns the EXACT command it ran alongside the result,
which the UI surfaces verbatim. No bespoke RBAC, no in-cluster agent — the simulator only
does what an operator could type by hand.
"""
from __future__ import annotations

import json
import shlex
import subprocess
from dataclasses import dataclass


@dataclass
class Config:
    context: str = "kind-vigil-abb"
    namespace: str = "abb-genix"


CFG = Config()

# the stateful backbone (StatefulSets, pods named <name>-0); everything else is a Deployment
STATEFULSETS = {"edgenius-broker", "genix-historian", "genix-datalake", "asset-registry"}

# data mount path per stateful backbone (for the disk-fill ballast)
DATA_PATH = {
    "genix-historian": "/var/lib/influxdb2",
    "genix-datalake": "/data",
    "asset-registry": "/var/lib/postgresql/data",
    "edgenius-broker": "/mosquitto/data",
}


@dataclass
class Result:
    cmd: str
    rc: int
    out: str
    err: str

    @property
    def ok(self) -> bool:
        return self.rc == 0

    def summary(self) -> str:
        if self.ok:
            return self.out or "ok"
        return self.err or f"exit {self.rc}"


def _kubectl(args: list[str], timeout: int = 60, input_text: str | None = None) -> Result:
    base = ["--context", CFG.context, *args]
    pretty = "kubectl " + " ".join(shlex.quote(a) for a in base)
    try:
        p = subprocess.run(
            ["kubectl", *base], capture_output=True, text=True, timeout=timeout, input=input_text
        )
        return Result(pretty, p.returncode, p.stdout.strip(), p.stderr.strip())
    except subprocess.TimeoutExpired:
        return Result(pretty, 124, "", f"timed out after {timeout}s")
    except FileNotFoundError:
        return Result(pretty, 127, "", "kubectl not found on PATH")


def _ns() -> list[str]:
    return ["-n", CFG.namespace]


def resource_kind(name: str) -> str:
    return "statefulset" if name in STATEFULSETS else "deployment"


# --- reads ------------------------------------------------------------------

def cluster_reachable() -> Result:
    return _kubectl(["version", "-o", "json"], timeout=10)


def list_pods() -> list[dict]:
    r = _kubectl([*_ns(), "get", "pods", "-o", "json"], timeout=20)
    if not r.ok:
        return []
    try:
        items = json.loads(r.out)["items"]
    except Exception:
        return []
    pods = []
    for it in items:
        st = it.get("status", {})
        cs = (st.get("containerStatuses") or [{}])[0]
        last = (cs.get("lastState") or {}).get("terminated", {}) or {}
        pods.append(
            {
                "pod": it["metadata"]["name"],
                "phase": st.get("phase", "?"),
                "ready": bool(cs.get("ready", False)),
                "restarts": cs.get("restartCount", 0),
                "lastReason": last.get("reason", ""),
            }
        )
    return sorted(pods, key=lambda p: p["pod"])


def ctl_state(deploy: str) -> dict:
    """Read a sim pod's current /ctl control state (which faults are armed)."""
    r = exec_ctl(deploy, "")
    if not r.ok:
        return {"_error": r.summary()}
    txt = r.out
    if txt.startswith("ok "):
        txt = txt[3:]
    try:
        return json.loads(
            txt.replace("'", '"').replace("False", "false").replace("True", "true").replace("None", "null")
        )
    except Exception:
        return {"_raw": txt}


def get_container_limit(name: str, resource: str) -> str:
    """Current CPU/memory limit of a deployment/statefulset's first container ('' if unset)."""
    kind = resource_kind(name)
    path = "{.spec.template.spec.containers[0].resources.limits." + resource + "}"
    r = _kubectl([*_ns(), "get", kind, name, "-o", f"jsonpath={path}"], timeout=15)
    return r.out if r.ok else ""


def get_replicas(name: str) -> int:
    kind = resource_kind(name)
    r = _kubectl([*_ns(), "get", kind, name, "-o", "jsonpath={.spec.replicas}"], timeout=15)
    try:
        return int(r.out)
    except Exception:
        return -1


# --- actions ----------------------------------------------------------------

def exec_ctl(deploy: str, query: str, timeout: int = 15) -> Result:
    """Inject via a sim pod's /ctl control surface (served on the pod's own localhost:8080)."""
    py = (
        "import urllib.request as u;"
        f"print(u.urlopen('http://localhost:8080/ctl?{query}', timeout=6).read().decode())"
    )
    return _kubectl([*_ns(), "exec", f"deploy/{deploy}", "--", "python3", "-c", py], timeout=timeout)


def patch_limit(name: str, resource: str, value: str) -> Result:
    """Patch a CPU/memory limit (a real, declared-bar change → throttle/OOM pressure)."""
    kind = resource_kind(name)
    patch = json.dumps(
        {"spec": {"template": {"spec": {"containers": [{"name": name, "resources": {"limits": {resource: value}}}]}}}}
    )
    # the container name may differ from the workload name; resolve it first
    cn = _kubectl([*_ns(), "get", kind, name, "-o", "jsonpath={.spec.template.spec.containers[0].name}"], timeout=15)
    cname = cn.out if cn.ok and cn.out else name
    patch = json.dumps(
        {"spec": {"template": {"spec": {"containers": [{"name": cname, "resources": {"limits": {resource: value}}}]}}}}
    )
    return _kubectl([*_ns(), "patch", kind, name, "--type", "strategic", "-p", patch], timeout=30)


def scale(name: str, replicas: int) -> Result:
    kind = resource_kind(name)
    return _kubectl([*_ns(), "scale", kind, name, f"--replicas={replicas}"], timeout=30)


def delete_pod(pod: str) -> Result:
    return _kubectl([*_ns(), "delete", "pod", pod, "--wait=false"], timeout=30)


def delete_pod_of(deploy: str) -> Result:
    """Delete one running pod of a workload (it is recreated by its controller)."""
    r = _kubectl(
        [*_ns(), "get", "pods", "-l", f"app={deploy}", "-o", "jsonpath={.items[0].metadata.name}"], timeout=15
    )
    pod = r.out
    if not pod:
        # fall back to a name-prefix match
        for p in list_pods():
            if p["pod"].startswith(deploy):
                pod = p["pod"]
                break
    if not pod:
        return Result(f"(find pod for {deploy})", 1, "", f"no pod found for {deploy}")
    return delete_pod(pod)


def disk_fill(name: str, gigabytes: int) -> Result:
    """Write a ballast file into a stateful pod's data volume (fills the PVC for real)."""
    pod = f"{name}-0"
    path = DATA_PATH.get(name, "/data")
    mb = int(gigabytes * 1024)
    sh = f"dd if=/dev/zero of={path}/_vigil_ballast bs=1M count={mb} 2>&1 | tail -1"
    return _kubectl([*_ns(), "exec", pod, "--", "sh", "-c", sh], timeout=180)


def disk_unfill(name: str) -> Result:
    pod = f"{name}-0"
    path = DATA_PATH.get(name, "/data")
    return _kubectl([*_ns(), "exec", pod, "--", "sh", "-c", f"rm -f {path}/_vigil_ballast && echo removed"], timeout=60)
