"""faults.py — the transparent fault primitives.

Each Fault is a single, real cluster operation (a sim /ctl injection, a limit patch, a
scale, a PVC write, a pod delete) wrapped with operator-facing metadata: WHAT it does, the
downstream EFFECT, which Vigil SIGNAL it produces, and the exact command preview. Scenarios
(scenarios.py) compose these — so a scenario is just a named sequence of the SAME real
faults the manual panel runs. Nothing is a shallow proxy.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable

import cluster

# topology roles (deploy/workloads/abb-genix)
GATEWAY = "opcua-gateway"      # /ctl: rate, disconnect
STREAM = "stream-processor"    # /ctl: slow, cpuburn
ANALYZER = "pdm-analyzer"      # /ctl: leak, leak_kb
SENSORS = "smart-sensors"
ASSETAPI = "asset-api"
APP_WORKLOADS = [GATEWAY, STREAM, ANALYZER, ASSETAPI, SENSORS, "operations-dashboard"]
BACKBONE = ["edgenius-broker", "genix-historian", "genix-datalake", "asset-registry"]

IDLE_RATE = 5
STEADY_RATE = 12   # 12 × 5 assets = 60 samples/s — warm but well under the 200/s LOAD_SURGE bar

# remember patched limits so a heal restores exactly what was there
_ORIG_LIMITS: dict[tuple[str, str], str] = {}


@dataclass
class Fault:
    id: str
    name: str
    icon: str
    what: str
    effect: str
    signal: str
    inject_fn: Callable[..., cluster.Result]
    heal_fn: Callable[..., cluster.Result] | None = None
    preview_fn: Callable[..., str] | None = None
    caveat: str = ""


# --- primitive implementations ---------------------------------------------
def _leak(target: str, leak_kb: int) -> cluster.Result:
    return cluster.exec_ctl(target, f"leak=on&leak_kb={int(leak_kb)}")


def _leak_off(target: str) -> cluster.Result:
    return cluster.exec_ctl(target, "leak=off")


def _slow(target: str, ms: int) -> cluster.Result:
    return cluster.exec_ctl(target, f"slow={int(ms)}")


def _cpuburn(target: str, on: bool = True) -> cluster.Result:
    return cluster.exec_ctl(target, f"cpuburn={'1' if on else '0'}")


def _rate(target: str, n: int) -> cluster.Result:
    return cluster.exec_ctl(target, f"rate={int(n)}")


def _disconnect(target: str, on: bool = True) -> cluster.Result:
    return cluster.exec_ctl(target, f"disconnect={'1' if on else '0'}")


# original (request, limit) memory specs, saved on first squeeze so heal restores exactly
_ORIG_MEM: dict[str, dict] = {}


def _mem_squeeze(target: str, mi: int) -> cluster.Result:
    """Squeeze BOTH memory request and limit below the pod's working set → genuine OOM-restart.
    (A limit alone can't drop below the existing request, so both must move.)"""
    if target not in _ORIG_MEM:
        _ORIG_MEM[target] = cluster.get_mem_spec(target)
    return cluster.patch_mem(target, f"{int(mi)}Mi", f"{int(mi)}Mi")


def _mem_restore(target: str) -> cluster.Result:
    orig = _ORIG_MEM.get(target) or {"request": "128Mi", "limit": "256Mi"}
    r = cluster.patch_mem(target, orig.get("request") or "128Mi", orig.get("limit") or "256Mi")
    _ORIG_MEM.pop(target, None)
    return r


def _scale0(target: str) -> cluster.Result:
    return cluster.scale(target, 0)


def _scale1(target: str) -> cluster.Result:
    return cluster.scale(target, 1)


# The validated PVC-fill cascade workload (single container so there is exactly ONE restart-counter
# variable — a 2-container filler+crasher trips findRate's same-metric ambiguity guard). Each restart
# appends +40Mi (the volume keeps RISING → PHEN_PVC_FILLING) then exits 1 (restart counter climbs →
# PHEN_PROBE_FAILURE_RESTART). Base fast-fills just over the 0.85×10Gi=8.7Gi bar, then rises slowly.
PVC_CASCADE_MANIFEST = """\
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: vigil-pvcfill, namespace: abb-genix }
spec:
  serviceName: vigil-pvcfill
  replicas: 1
  selector: { matchLabels: { app: vigil-pvcfill } }
  template:
    metadata: { labels: { app: vigil-pvcfill } }
    spec:
      terminationGracePeriodSeconds: 2
      containers:
      - name: app
        image: busybox:1.36
        command: ["sh","-c","cur=$(du -m /data/base 2>/dev/null|cut -f1); cur=${cur:-0}; while [ $cur -lt 8750 ]; do dd if=/dev/zero of=/data/base bs=1M count=40 seek=$cur conv=notrunc 2>/dev/null||break; sync; cur=$((cur+40)); done; g=$(du -m /data/grow 2>/dev/null|cut -f1); g=${g:-0}; dd if=/dev/zero of=/data/grow bs=1M count=40 seek=$g conv=notrunc 2>/dev/null||true; sync; sleep 8; exit 1"]
        volumeMounts: [{ name: data, mountPath: /data }]
        resources: { requests: { cpu: "50m", memory: "32Mi" }, limits: { cpu: "600m", memory: "128Mi" } }
  volumeClaimTemplates:
  - metadata: { name: data }
    spec: { accessModes: ["ReadWriteOnce"], storageClassName: ebs-gp3, resources: { requests: { storage: "10Gi" } } }
"""


def _pvc_cascade_heal() -> cluster.Result:
    cluster.delete_resource("statefulset", "vigil-pvcfill")
    return cluster.delete_resource("pvc", "data-vigil-pvcfill-0")


# --- the catalog (each used by one or more scenarios) ----------------------
CATALOG: dict[str, Fault] = {
    "mem_leak_gentle": Fault(
        "mem_leak_gentle", "Memory leak — gentle (long forecast lead)", "🐢",
        what=f"Arms a slow heap leak in {ANALYZER} (/ctl?leak=on&leak_kb=48): 48 KiB appended each 10s analyze cycle.",
        effect="Working set creeps toward the 0.95×256Mi bar over a long time → hours of forecast lead.",
        signal="PROJECTED early-warning on container_memory_working_set_bytes — wide band, large timeToCrossSeconds.",
        inject_fn=lambda: _leak(ANALYZER, 48), heal_fn=lambda: _leak_off(ANALYZER),
        preview_fn=lambda: f"exec deploy/{ANALYZER} -- GET /ctl?leak=on&leak_kb=48",
    ),
    "mem_leak_medium": Fault(
        "mem_leak_medium", "Memory leak — medium lead", "🐇",
        what=f"Arms a medium heap leak in {ANALYZER} (/ctl?leak=on&leak_kb=512): ~52 KB/s.",
        effect="Working set climbs steadily → ~tens of minutes of forecast lead, moderate band.",
        signal="PROJECTED early-warning, moderate confidence, crossAt inside the 1h horizon.",
        inject_fn=lambda: _leak(ANALYZER, 512), heal_fn=lambda: _leak_off(ANALYZER),
        preview_fn=lambda: f"exec deploy/{ANALYZER} -- GET /ctl?leak=on&leak_kb=512",
    ),
    "mem_leak_fast": Fault(
        "mem_leak_fast", "Memory leak — fast (short lead → OOM)", "🔥",
        what=f"Arms a fast heap leak in {ANALYZER} (/ctl?leak=on&leak_kb=4096): ~420 KB/s.",
        effect="Working set climbs through the bar in minutes, then the kernel OOM-kills the cgroup.",
        signal="PROJECTED early-warning (tight band, ~8 min lead) → MEMORY_LEAK → OOM_KILL_CGROUP cascade.",
        inject_fn=lambda: _leak(ANALYZER, 4096), heal_fn=lambda: _leak_off(ANALYZER),
        preview_fn=lambda: f"exec deploy/{ANALYZER} -- GET /ctl?leak=on&leak_kb=4096",
    ),
    "cpu_burn": Fault(
        "cpu_burn", "CPU aggressor — over its own limit (noisy neighbour)", "🔥",
        what=f"Starts a busy CPU spin in {STREAM} (/ctl?cpuburn=1) that drives usage AT/ABOVE its own 250m CPU limit.",
        effect="The cgroup sits at/over its declared limit (CFS-pinned); on the shared single node, neighbours' I/O-wait / CPU-pressure rises.",
        signal="PHEN_CPU_AGGRESSOR (MEASURED — over its OWN limit, names the aggressor) + PHEN_THROTTLING_CASCADE; cross-workload PSI association edges.",
        inject_fn=lambda: _cpuburn(STREAM, True), heal_fn=lambda: _cpuburn(STREAM, False),
        preview_fn=lambda: f"exec deploy/{STREAM} -- GET /ctl?cpuburn=1",
    ),
    "latency_queue": Fault(
        "latency_queue", "Latency / queue backlog", "🐌",
        what=f"Injects per-message latency in {STREAM} (/ctl?slow=300).",
        effect="Drain rate falls below ingest → the internal queue builds toward the declared bar.",
        signal="PHEN_APP_QUEUE_SATURATION on stream-processor; a C2 onset marks the step.",
        inject_fn=lambda: _slow(STREAM, 300), heal_fn=lambda: _slow(STREAM, 0),
        preview_fn=lambda: f"exec deploy/{STREAM} -- GET /ctl?slow=300",
    ),
    "db_outage": Fault(
        "db_outage", "Historian unavailable — image-pull failure (the root)", "🗄️",
        what="Sets a non-pullable image on the historian (genix-historian, InfluxDB) so its pod cannot start — ready 0/1, desired 1 (NOT a scale-to-0, which is an intentional shutdown Vigil correctly ignores).",
        effect="The StatefulSet drops below its declared desired count → it becomes a MEASURED degraded workload node; its callers lose their datastore.",
        signal="PHEN_WORKLOAD_UNAVAILABLE (MEASURED, on the workload itself — the real upstream root) → the root-cause chain roots HERE, not on the nearest downstream symptom.",
        inject_fn=lambda: cluster.set_image("genix-historian", "influxdb", "influxdb:2.7-vigil-nonexistent"),
        heal_fn=lambda: cluster.set_image("genix-historian", "influxdb", "influxdb:2.7"),
        preview_fn=lambda: "set image statefulset/genix-historian influxdb=influxdb:2.7-vigil-nonexistent",
        caveat="Holds the historian in ImagePullBackOff until healed; heal restores influxdb:2.7.",
    ),
    "pvc_fill_cascade": Fault(
        "pvc_fill_cascade", "PVC fills (rising) → probe-failure restart cascade", "💽",
        what="Deploys vigil-pvcfill: a 10 GiB EBS PVC whose single container fills it ABOVE the 0.85×request bar and keeps RISING, then crashloops each cycle (appending more) — so the volume keeps filling AND the container keeps restarting on the SAME pod that mounts the PVC.",
        effect="kubelet_volume_stats_used_bytes rises over 0.85×request (a sustained fill TREND) while the restart counter climbs.",
        signal="PHEN_PVC_FILLING (PVC, rising) + PHEN_PROBE_FAILURE_RESTART (pod) → the AUTHORED ⛓ PVC_FILLING→PROBE_FAILURE_RESTART cascade recognized over the mounts edge.",
        inject_fn=lambda: cluster.apply_manifest(PVC_CASCADE_MANIFEST),
        heal_fn=lambda: _pvc_cascade_heal(),
        preview_fn=lambda: "apply statefulset/vigil-pvcfill (10Gi PVC: rising fill + crashloop, single container)",
        caveat="PVC_FILLING needs a sustained RISING fill (kubelet volume-stats update ~1×/min), so the pod rises slowly above the bar for ~15min; both endpoints overlap within ~2-3 min. Heal deletes the StatefulSet + its PVC.",
    ),
    "pvc_io_storm": Fault(
        "pvc_io_storm", "PVC I/O storm (asset-registry writes)", "💽",
        what="Drives a real fsync'd write burst into the asset-registry PVC (dd into /var/lib/postgresql/data on data-asset-registry-0) — the SAME PVC-backed pod that the squeeze restarts.",
        effect="container_fs_writes_bytes_total + container_pressure_io_* spike on the postgres pod; the volume fills toward its bar.",
        signal="MEASURED I/O pattern on the PVC-backed pod that then restarts — so the I/O series and the restart counter co-move on ONE entity (the association lane links them).",
        inject_fn=lambda: cluster.io_burst("asset-registry", 256),
        heal_fn=lambda: cluster.disk_unfill("asset-registry"),
        preview_fn=lambda: "exec asset-registry-0 -- dd if=/dev/zero of=/var/lib/postgresql/data/_vigil_io bs=1M count=256 conv=fsync",
    ),
    "pvc_fill": Fault(
        "pvc_fill", "PVC fill (toward ENOSPC)", "💾",
        what="Fills the historian PVC with ballast (dd 6 GiB into data-genix-historian-0, a 10 GiB volume).",
        effect="kubelet_volume_stats_used_bytes crosses 0.85×request → writes head toward ENOSPC.",
        signal="PHEN_PVC_FILLING MEASURED on the PVC CEI (the kind blind spot is CLOSED on this EBS cluster).",
        inject_fn=lambda: cluster.disk_fill("genix-historian", 6),
        heal_fn=lambda: cluster.disk_unfill("genix-historian"),
        preview_fn=lambda: "exec genix-historian-0 -- dd if=/dev/zero of=/var/lib/influxdb2/_vigil_ballast bs=1M count=6144 conv=fsync",
    ),
    "mem_squeeze_pg": Fault(
        "mem_squeeze_pg", "Memory squeeze → restart (asset-registry)", "💥",
        what="Patches asset-registry (Postgres) memory request+limit down to 24Mi — below its ~30Mi working set.",
        effect="Postgres is OOM-killed and restarts (CrashLoopBackOff) while it is doing PVC I/O — the restart fact.",
        signal="OOMKilled event + KSM restart-counter step on the PVC-backed pod (the restart half of the I/O↔restart link).",
        inject_fn=lambda: _mem_squeeze("asset-registry", 24), heal_fn=lambda: _mem_restore("asset-registry"),
        preview_fn=lambda: "patch statefulset/asset-registry (memory request+limit → 24Mi)",
        caveat="The too-low limit holds the pod in CrashLoopBackOff until healed — heal restores the original request+limit.",
    ),
    "mem_leak_analyzer_oom": Fault(
        "mem_leak_analyzer_oom", "Independent OOM (pdm-analyzer)", "🧠",
        what=f"Arms a fast leak in {ANALYZER} — an INDEPENDENT fault, to prove non-merge of unrelated incidents.",
        effect="pdm-analyzer climbs to OOM on its own branch of the graph.",
        signal="PHEN_OOM_KILL_CGROUP — stays a SEPARATE chain from the historian root (cardinal anti-false-chain rule).",
        inject_fn=lambda: _leak(ANALYZER, 4096), heal_fn=lambda: _leak_off(ANALYZER),
        preview_fn=lambda: f"exec deploy/{ANALYZER} -- GET /ctl?leak=on&leak_kb=4096",
    ),
}


def set_steady_traffic(on: bool) -> cluster.Result:
    return _rate(GATEWAY, STEADY_RATE if on else IDLE_RATE)


def inject(fault_id: str) -> cluster.Result:
    return CATALOG[fault_id].inject_fn()


def heal(fault_id: str) -> cluster.Result:
    f = CATALOG[fault_id]
    if f.heal_fn is None:
        return cluster.Result(f"({f.id} self-recovering)", 0, "controller restores", "")
    return f.heal_fn()


def global_reset() -> list[tuple[str, cluster.Result]]:
    """Return the namespace to a clean baseline: clear sim flags, restore limits, remove
    ballast, scale the backbone back to 1."""
    out: list[tuple[str, cluster.Result]] = []
    out.append(("clear analyzer leak", _leak_off(ANALYZER)))
    out.append(("clear stream slow", _slow(STREAM, 0)))
    out.append(("clear stream cpuburn", _cpuburn(STREAM, False)))
    out.append(("clear gateway disconnect", _disconnect(GATEWAY, False)))
    out.append(("gateway → idle rate", _rate(GATEWAY, IDLE_RATE)))
    for (target, resource), orig in list(_ORIG_LIMITS.items()):
        if orig:
            out.append((f"restore {target} {resource}", cluster.patch_limit(target, resource, orig)))
    _ORIG_LIMITS.clear()
    for target in list(_ORIG_MEM.keys()):
        out.append((f"restore {target} memory", _mem_restore(target)))
    for name in ("genix-historian", "genix-datalake", "asset-registry", "edgenius-broker"):
        out.append((f"unfill {name}", cluster.disk_unfill(name)))
    for name in ("genix-historian",):
        if cluster.get_replicas(name) == 0:
            out.append((f"scale {name}=1", cluster.scale(name, 1)))
    # restore the historian image (idempotent: kubectl set image is a no-op if unchanged; roll=False
    # so a healthy historian's pod is not needlessly recycled)
    out.append(("restore genix-historian image", cluster.set_image("genix-historian", "influxdb", "influxdb:2.7", roll=False)))
    # tear down the PVC-cascade workload + its volume
    out.append(("delete vigil-pvcfill", cluster.delete_resource("statefulset", "vigil-pvcfill")))
    out.append(("delete vigil-pvcfill PVC", cluster.delete_resource("pvc", "data-vigil-pvcfill-0")))
    return out
