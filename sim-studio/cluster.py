"""cluster.py — transparent kubectl wrappers for the showcase studio.

Everything here shells out to ``kubectl`` with the operator's own kubeconfig/context, so
nothing is hidden: each call returns the EXACT command it ran, which the UI surfaces
verbatim. There is no in-cluster agent and no bespoke RBAC — the studio only does what an
operator could type by hand. Every fault is a genuine kubectl operation; nothing is mocked.

Targets the live AWS k3s cluster by default (KUBECONFIG=/tmp/aws-k3s.yaml, context "default",
namespace abb-genix). Override via the sidebar or the CLUSTER_* environment variables.
"""
from __future__ import annotations

import json
import os
import shlex
import subprocess
from dataclasses import dataclass


@dataclass
class Config:
    # Default to the SSH-tunnel kubeconfig (server https://127.0.0.1:16443). The direct public-IP
    # path to the k3s API TLS-resets from this network even with the security group open, so the
    # tunnel (ssh -N -L 16443:127.0.0.1:6443 …, cert valid for 127.0.0.1) is the reliable route.
    kubeconfig: str = os.environ.get("CLUSTER_KUBECONFIG", "/tmp/aws-k3s-tunnel.yaml")
    context: str = os.environ.get("CLUSTER_CONTEXT", "default")
    namespace: str = os.environ.get("CLUSTER_NAMESPACE", "abb-genix")


CFG = Config()

# the stateful backbone (StatefulSets, pods named <name>-0); everything else is a Deployment
STATEFULSETS = {"edgenius-broker", "genix-historian", "genix-datalake", "asset-registry"}

# real data-mount path per stateful backbone (where the PVC is mounted) — used by the
# disk-fill ballast and the I/O burst. These ARE the PVC mount points.
DATA_PATH = {
    "genix-historian": "/var/lib/influxdb2",       # data-genix-historian-0 (10 GiB)
    "genix-datalake": "/data",                      # data-genix-datalake-0  (10 GiB)
    "asset-registry": "/var/lib/postgresql/data",   # data-asset-registry-0  (2 GiB)
    "edgenius-broker": "/mosquitto/data",           # data-edgenius-broker-0 (1 GiB)
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
        return (self.out or "ok") if self.ok else (self.err or f"exit {self.rc}")


def _kubectl(args: list[str], timeout: int = 60) -> Result:
    base = ["--kubeconfig", CFG.kubeconfig, "--context", CFG.context, *args]
    pretty = "kubectl " + " ".join(shlex.quote(a) for a in base)
    try:
        p = subprocess.run(["kubectl", *base], capture_output=True, text=True, timeout=timeout)
        return Result(pretty, p.returncode, p.stdout.strip(), p.stderr.strip())
    except subprocess.TimeoutExpired:
        return Result(pretty, 124, "", f"timed out after {timeout}s")
    except FileNotFoundError:
        return Result(pretty, 127, "", "kubectl not found on PATH")


def _ns() -> list[str]:
    return ["-n", CFG.namespace]


def resource_kind(name: str) -> str:
    return "statefulset" if name in STATEFULSETS else "deployment"


# --------------------------------------------------------------------------- #
# reads
# --------------------------------------------------------------------------- #
def reachable() -> bool:
    return _kubectl(["get", "ns", CFG.namespace, "-o", "name"], timeout=12).ok


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
        pods.append({
            "pod": it["metadata"]["name"],
            "phase": st.get("phase", "?"),
            "ready": bool(cs.get("ready", False)),
            "restarts": cs.get("restartCount", 0),
            "lastReason": last.get("reason", ""),
        })
    return sorted(pods, key=lambda p: p["pod"])


def get_replicas(name: str) -> int:
    r = _kubectl([*_ns(), "get", resource_kind(name), name, "-o", "jsonpath={.spec.replicas}"], timeout=15)
    try:
        return int(r.out)
    except Exception:
        return -1


def get_container_limit(name: str, resource: str) -> str:
    path = "{.spec.template.spec.containers[0].resources.limits." + resource + "}"
    r = _kubectl([*_ns(), "get", resource_kind(name), name, "-o", f"jsonpath={path}"], timeout=15)
    return r.out if r.ok else ""


def ctl_state(deploy: str) -> dict:
    """Read a sim pod's current /ctl control state (which faults are armed)."""
    r = exec_ctl(deploy, "")
    if not r.ok:
        return {"_error": r.summary()[:80]}
    txt = r.out[3:] if r.out.startswith("ok ") else r.out
    try:
        return json.loads(txt.replace("'", '"').replace("False", "false")
                          .replace("True", "true").replace("None", "null"))
    except Exception:
        return {"_raw": txt[:120]}


# --------------------------------------------------------------------------- #
# actions (every one a real kubectl operation)
# --------------------------------------------------------------------------- #
def exec_ctl(deploy: str, query: str, timeout: int = 15) -> Result:
    """Inject via a sim pod's /ctl control surface (served on the pod's own localhost:8080)."""
    py = ("import urllib.request as u;"
          f"print(u.urlopen('http://localhost:8080/ctl?{query}', timeout=6).read().decode())")
    return _kubectl([*_ns(), "exec", f"deploy/{deploy}", "--", "python3", "-c", py], timeout=timeout)


def patch_limit(name: str, resource: str, value: str) -> Result:
    """Patch a CPU/memory limit (a real declared-bar change → throttle/OOM pressure)."""
    kind = resource_kind(name)
    cn = _kubectl([*_ns(), "get", kind, name, "-o",
                   "jsonpath={.spec.template.spec.containers[0].name}"], timeout=15)
    cname = cn.out if cn.ok and cn.out else name
    patch = json.dumps({"spec": {"template": {"spec": {"containers": [
        {"name": cname, "resources": {"limits": {resource: value}}}]}}}})
    return _kubectl([*_ns(), "patch", kind, name, "--type", "strategic", "-p", patch], timeout=30)


def scale(name: str, replicas: int) -> Result:
    return _kubectl([*_ns(), "scale", resource_kind(name), name, f"--replicas={replicas}"], timeout=30)


def get_mem_spec(name: str) -> dict:
    """Return {'request': <str>, 'limit': <str>} for the first container's memory (''/missing → '')."""
    kind = resource_kind(name)
    req = _kubectl([*_ns(), "get", kind, name, "-o",
                    "jsonpath={.spec.template.spec.containers[0].resources.requests.memory}"], timeout=15)
    lim = _kubectl([*_ns(), "get", kind, name, "-o",
                    "jsonpath={.spec.template.spec.containers[0].resources.limits.memory}"], timeout=15)
    return {"request": req.out if req.ok else "", "limit": lim.out if lim.ok else ""}


def patch_mem(name: str, request: str, limit: str) -> Result:
    """Patch BOTH memory request and limit (a limit alone can't drop below the existing request,
    so an OOM-squeeze must move both). Resolves the real container name first."""
    kind = resource_kind(name)
    cn = _kubectl([*_ns(), "get", kind, name, "-o",
                   "jsonpath={.spec.template.spec.containers[0].name}"], timeout=15)
    cname = cn.out if cn.ok and cn.out else name
    patch = json.dumps({"spec": {"template": {"spec": {"containers": [
        {"name": cname, "resources": {"requests": {"memory": request}, "limits": {"memory": limit}}}]}}}})
    return _kubectl([*_ns(), "patch", kind, name, "--type", "strategic", "-p", patch], timeout=30)


def delete_pod_of(deploy: str) -> Result:
    """Delete one running pod of a workload (its controller recreates it with a new UID)."""
    r = _kubectl([*_ns(), "get", "pods", "-l", f"app={deploy}",
                  "-o", "jsonpath={.items[0].metadata.name}"], timeout=15)
    pod = r.out
    if not pod:
        for p in list_pods():
            if p["pod"].startswith(deploy):
                pod = p["pod"]
                break
    if not pod:
        return Result(f"(find pod for {deploy})", 1, "", f"no pod found for {deploy}")
    return _kubectl([*_ns(), "delete", "pod", pod, "--wait=false"], timeout=30)


def disk_fill(name: str, gigabytes: float) -> Result:
    """Write a ballast file into a stateful pod's PVC — fills the volume AND drives a real
    sustained write burst (container_fs_writes_bytes_total + kubelet_volume_stats_used_bytes
    both climb). Real EBS gp3 CSI, so both the FILL and the I/O are MEASURED here."""
    pod = f"{name}-0"
    path = DATA_PATH.get(name, "/data")
    mb = int(gigabytes * 1024)
    sh = f"dd if=/dev/zero of={path}/_vigil_ballast bs=1M count={mb} conv=fsync 2>&1 | tail -1"
    return _kubectl([*_ns(), "exec", pod, "--", "sh", "-c", sh], timeout=300)


def io_burst(name: str, megabytes: int = 512) -> Result:
    """One fsync'd write burst into the PVC — a deliberate I/O *pattern* spike with no net
    fill (the file is rewritten in place). Call repeatedly to sustain the pattern. Raises
    container_fs_writes_bytes_total and container_pressure_io_* on the PVC-backed pod."""
    pod = f"{name}-0"
    path = DATA_PATH.get(name, "/data")
    sh = (f"dd if=/dev/zero of={path}/_vigil_io bs=1M count={int(megabytes)} conv=fsync 2>&1 | tail -1; "
          f"sync")
    return _kubectl([*_ns(), "exec", pod, "--", "sh", "-c", sh], timeout=180)


def disk_unfill(name: str) -> Result:
    pod = f"{name}-0"
    path = DATA_PATH.get(name, "/data")
    sh = f"rm -f {path}/_vigil_ballast {path}/_vigil_io && echo removed"
    return _kubectl([*_ns(), "exec", pod, "--", "sh", "-c", sh], timeout=60)


def set_image(name: str, container: str, image: str, roll: bool = True) -> Result:
    """Set a container's image on a workload — a real declared-spec change. A non-pullable image
    -> ImagePullBackOff -> ready<desired -> PHEN_WORKLOAD_UNAVAILABLE; reverting restores it. When
    roll=True the pod is deleted so the controller applies the new image immediately; roll=False
    leaves the rollout to the controller (and is a true no-op when the image is unchanged)."""
    kind = resource_kind(name)
    r = _kubectl([*_ns(), "set", "image", f"{kind}/{name}", f"{container}={image}"], timeout=30)
    if roll:
        _kubectl([*_ns(), "delete", "pod", f"{name}-0", "--wait=false"], timeout=20)
    return r


def apply_manifest(yaml_str: str) -> Result:
    """kubectl apply -f - on an embedded manifest (a real resource, deployed exactly as typed)."""
    base = ["--kubeconfig", CFG.kubeconfig, "--context", CFG.context, *_ns(), "apply", "-f", "-"]
    pretty = "kubectl apply -f - (embedded manifest)"
    try:
        p = subprocess.run(["kubectl", *base], input=yaml_str, capture_output=True, text=True, timeout=60)
        return Result(pretty, p.returncode, p.stdout.strip(), p.stderr.strip())
    except subprocess.TimeoutExpired:
        return Result(pretty, 124, "", "timed out")
    except FileNotFoundError:
        return Result(pretty, 127, "", "kubectl not found on PATH")


def delete_resource(kind: str, name: str) -> Result:
    return _kubectl([*_ns(), "delete", kind, name, "--wait=false", "--ignore-not-found"], timeout=60)
