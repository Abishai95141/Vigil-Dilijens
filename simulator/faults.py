"""faults.py — the transparent fault catalog + inject/heal engine.

Each Fault carries operator-facing metadata (WHAT it does to the cluster, the downstream
EFFECT, which Vigil CAPABILITY it exercises, the EXPECTED phenomenon, and any honest
CAVEAT) plus the real inject/heal actions (kube.py). The same catalog drives both the UI
(transparency) and the scenario engine (composition), so what you read is exactly what
runs. Nothing here is a shallow proxy: every action is a real kubectl operation.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable

import kube

# --- topology constants (from deploy/workloads/abb-genix) -------------------
SIM_GATEWAY = "opcua-gateway"      # /ctl: rate, disconnect
SIM_STREAM = "stream-processor"    # /ctl: slow, cpuburn
SIM_ANALYZER = "pdm-analyzer"      # /ctl: leak
APP_WORKLOADS = ["opcua-gateway", "stream-processor", "pdm-analyzer", "asset-api", "smart-sensors", "operations-dashboard"]
BACKBONE = ["edgenius-broker", "genix-historian", "genix-datalake", "asset-registry"]

# remember original limits so heal restores exactly what was there
_ORIG_LIMITS: dict[tuple[str, str], str] = {}


@dataclass
class Fault:
    id: str
    name: str
    icon: str
    category: str
    control: str                       # "toggle" | "slider" | "button"
    what: str
    effect: str
    tests: list[str]
    expected: str
    inject_fn: Callable[..., kube.Result]
    heal_fn: Callable[..., kube.Result] | None = None
    target_default: str = ""
    target_choices: list[str] = field(default_factory=list)
    target_locked: bool = False        # /ctl faults only respond on their role's pod
    param_label: str = ""
    param_min: float = 0
    param_max: float = 0
    param_default: float = 0
    param_step: float = 1
    param_unit: str = ""
    caveat: str = ""

    def preview(self, target: str, value=None) -> str:
        """The exact kubectl command this fault will run (for transparency)."""
        try:
            return self.inject_fn(target, value, _preview=True)  # type: ignore[call-arg]
        except TypeError:
            return f"(runs against {target})"


# --- inject/heal implementations -------------------------------------------
# Each takes (target, value) and returns a kube.Result. A _preview=True kw returns the
# command string instead of running it, so the UI can show exactly what will happen.

def _ctl_inject(query_fn):
    def f(target, value=None, _preview=False):
        q = query_fn(value)
        if _preview:
            return f"kubectl -n {kube.CFG.namespace} exec deploy/{target} -- python3 -c \"urlopen('http://localhost:8080/ctl?{q}')\""
        return kube.exec_ctl(target, q)
    return f


def _ctl_heal(query):
    def f(target, value=None):
        return kube.exec_ctl(target, query)
    return f


def _limit_inject(resource):
    def f(target, value=None, _preview=False):
        val = f"{int(value)}m" if resource == "cpu" else f"{int(value)}Mi"
        if _preview:
            return f"kubectl -n {kube.CFG.namespace} patch {kube.resource_kind(target)}/{target} (set {resource} limit -> {val})"
        key = (target, resource)
        if key not in _ORIG_LIMITS:
            _ORIG_LIMITS[key] = kube.get_container_limit(target, resource)
        return kube.patch_limit(target, resource, val)
    return f


def _limit_heal(resource):
    def f(target, value=None):
        orig = _ORIG_LIMITS.get((target, resource))
        if not orig:
            return kube.Result(f"(no saved {resource} limit for {target})", 0, "nothing to restore", "")
        return kube.patch_limit(target, resource, orig)
    return f


def _scale0_inject(target, value=None, _preview=False):
    if _preview:
        return f"kubectl -n {kube.CFG.namespace} scale {kube.resource_kind(target)}/{target} --replicas=0"
    return kube.scale(target, 0)


def _scale1_heal(target, value=None):
    return kube.scale(target, 1)


def _disk_inject(target, value=None, _preview=False):
    gb = int(value or 2)
    if _preview:
        path = kube.DATA_PATH.get(target, "/data")
        return f"kubectl -n {kube.CFG.namespace} exec {target}-0 -- dd if=/dev/zero of={path}/_vigil_ballast bs=1M count={gb*1024}"
    return kube.disk_fill(target, gb)


def _disk_heal(target, value=None):
    return kube.disk_unfill(target)


def _kill_inject(target, value=None, _preview=False):
    if _preview:
        return f"kubectl -n {kube.CFG.namespace} delete pod (one pod of {target})"
    return kube.delete_pod_of(target)


def _flap_inject(target, value=None, _preview=False):
    n = int(value or 3)
    if _preview:
        return f"kubectl -n {kube.CFG.namespace} scale deploy/{target} --replicas={n}  (then back to 1 on heal)"
    return kube.scale(target, n)


def _flap_heal(target, value=None):
    return kube.scale(target, 1)


# --- THE CATALOG ------------------------------------------------------------

CATALOG: list[Fault] = [
    # ===== Application faults (the sim /ctl control surface) =====
    Fault(
        id="mem_leak", name="Memory leak", icon="🧠", category="Application", control="toggle",
        target_default=SIM_ANALYZER, target_choices=[SIM_ANALYZER], target_locked=True,
        what="Arms a real heap leak in pdm-analyzer (/ctl?leak=on): it appends ~4 MiB every analyze cycle.",
        effect="Working set climbs steadily toward the 0.95×256Mi ≈ 243 Mi limit, then the kernel OOM-kills the container (exit 137) and it restarts.",
        tests=["Forecasting", "Anomaly detection", "Root-cause (authored cascade)", "Recurrence memory"],
        expected="A PROJECTED early-warning on container_memory_working_set_bytes (band + lead time), then PHEN_MEMORY_LEAK → PHEN_OOM_KILL_CGROUP cascade, OOMKilled event corroboration, incident recorded.",
        inject_fn=_ctl_inject(lambda v: "leak=on"), heal_fn=_ctl_heal("leak=off"),
    ),
    Fault(
        id="latency_queue", name="Latency / queue backlog", icon="🐌", category="Application", control="slider",
        target_default=SIM_STREAM, target_choices=[SIM_STREAM], target_locked=True,
        param_label="Injected per-message latency", param_min=0, param_max=1000, param_default=300, param_step=50, param_unit="ms",
        what="Injects per-message processing latency in stream-processor (/ctl?slow=<ms>).",
        effect="The drain rate falls below the ingest rate, so the internal queue builds. Past 500 it crosses the declared SLO; historian writes also slow.",
        tests=["Anomaly detection", "Service degradation", "Latency spikes", "Queue backlog"],
        expected="PHEN_APP_QUEUE_SATURATION on stream-processor (queue depth > 500 declared bar); C2 onset marks the step.",
        inject_fn=_ctl_inject(lambda v: f"slow={int(v or 300)}"), heal_fn=_ctl_heal("slow=0"),
    ),
    Fault(
        id="cpu_burn", name="CPU burn → throttle", icon="🔥", category="Application", control="toggle",
        target_default=SIM_STREAM, target_choices=[SIM_STREAM], target_locked=True,
        what="Starts a busy CPU spin in stream-processor (/ctl?cpuburn=1) that exceeds its 250m CPU limit.",
        effect="The cgroup is CFS-throttled; sustained throttling can starve the readiness probe.",
        tests=["Anomaly detection", "Resource exhaustion (CPU)", "Throttling cascade"],
        expected="PHEN_THROTTLING_CASCADE on stream-processor (CFS throttled ratio); may chain toward PROBE_FAILURE_RESTART if sustained.",
        inject_fn=_ctl_inject(lambda v: "cpuburn=1"), heal_fn=_ctl_heal("cpuburn=0"),
    ),
    Fault(
        id="load_surge", name="Load surge", icon="📈", category="Application", control="slider",
        target_default=SIM_GATEWAY, target_choices=[SIM_GATEWAY], target_locked=True,
        param_label="Forward rate", param_min=5, param_max=100, param_default=80, param_step=5, param_unit="polls/s",
        what="Raises the gateway forward rate (/ctl?rate=<n>); at 80 × 5 assets ≈ 400 samples/s.",
        effect="rate(app_requests_total) climbs above the 200 req/s declared SLO.",
        tests=["Anomaly detection", "Load surge", "Borrowed-normativity bar"],
        expected="PHEN_APP_LOAD_SURGE on opcua-gateway (a MEASURED rate crossing a declared bar, not a learned anomaly).",
        inject_fn=_ctl_inject(lambda v: f"rate={int(v or 80)}"), heal_fn=_ctl_heal("rate=5"),
    ),
    Fault(
        id="connectivity_loss", name="Field connectivity loss", icon="🔌", category="Application", control="toggle",
        target_default=SIM_GATEWAY, target_choices=[SIM_GATEWAY], target_locked=True,
        what="Cuts the gateway's field link (/ctl?disconnect=1) — a SILENT partial failure (no crash).",
        effect="No new telemetry reaches the broker → historian freezes → asset-api's freshness age crosses 120s.",
        tests=["Multi-event chain", "Dependency mapping", "Root-cause honesty", "Intermittent/silent failures"],
        expected="PHEN_APP_DATA_STALENESS on asset-api + a clean cross-service cascade asset-api → operations-dashboard. Vigil names the VISIBLE root and refuses to fabricate the silent gateway as cause.",
        inject_fn=_ctl_inject(lambda v: "disconnect=1"), heal_fn=_ctl_heal("disconnect=0"),
    ),

    # ===== Resource faults (real declared-bar changes) =====
    Fault(
        id="cpu_squeeze", name="CPU squeeze (throttle / DB bottleneck)", icon="🗜️", category="Resource", control="slider",
        target_default="genix-historian", target_choices=APP_WORKLOADS + BACKBONE,
        param_label="New CPU limit", param_min=20, param_max=500, param_default=50, param_step=10, param_unit="m",
        what="Patches the target's CPU limit down (real kubectl patch). On a backbone DB (historian/registry) this is a bottleneck.",
        effect="Under its normal load the container is throttled; queries/writes slow, propagating to callers.",
        tests=["Resource exhaustion (CPU)", "Database bottleneck", "Dependency mapping", "Root cause"],
        expected="THROTTLING on the target; if it is a depended-on DB, downstream services degrade (staleness / queue) over observed-flow edges.",
        inject_fn=_limit_inject("cpu"), heal_fn=_limit_heal("cpu"),
        caveat="Restored exactly to the pre-injection limit on heal.",
    ),
    Fault(
        id="mem_squeeze", name="Memory squeeze (force OOM)", icon="💥", category="Resource", control="slider",
        target_default="pdm-analyzer", target_choices=APP_WORKLOADS,
        param_label="New memory limit", param_min=16, param_max=256, param_default=32, param_step=8, param_unit="Mi",
        what="Patches the target's memory limit down so its normal footprint exceeds the cap.",
        effect="The pod is OOM-killed and restarts (CrashLoop if it re-OOMs immediately).",
        tests=["Resource exhaustion (memory)", "Pod crashes/restarts", "Replica instability"],
        expected="OOMKilled events + restarts; PHEN_OOM_KILL_CGROUP via the events lane; restart counter via KSM.",
        inject_fn=_limit_inject("memory"), heal_fn=_limit_heal("memory"),
        caveat="A too-low limit can hold the pod in CrashLoopBackOff until healed.",
    ),

    # ===== Storage =====
    Fault(
        id="disk_fill", name="PVC disk fill", icon="💾", category="Storage", control="slider",
        target_default="genix-historian", target_choices=BACKBONE,
        param_label="Ballast size", param_min=1, param_max=9, param_default=2, param_step=1, param_unit="GiB",
        what="Writes a ballast file into the stateful pod's PVC (real dd), consuming volume space.",
        effect="The volume fills toward capacity; once full, the database's writes start failing.",
        tests=["PVC / storage failure", "Resource exhaustion (disk)", "Database bottleneck"],
        expected="The disk genuinely fills + writes fail (observable as pod errors). NOTE: on kind the per-volume kubelet_volume_stats metric is NOT emitted, so Vigil's PHEN_DISK_FILLING stays gate-pending here — the fault is real, the metric is the honest blind spot.",
        inject_fn=_disk_inject, heal_fn=_disk_heal,
        caveat="kind's local-path provisioner does not emit per-volume stats → DISK_FILLING is dark on this cluster (needs a real CSI). The disk still fills for real.",
    ),

    # ===== Lifecycle =====
    Fault(
        id="kill_pod", name="Kill pod", icon="☠️", category="Lifecycle", control="button",
        target_default="pdm-analyzer", target_choices=APP_WORKLOADS + BACKBONE,
        what="Deletes one running pod of the target (real kubectl delete).",
        effect="The controller recreates it with a NEW pod UID; a brief outage during reschedule.",
        tests=["Pod crashes/restarts", "Recovery", "Churn-stable identity (CEI)"],
        expected="A restart/succession; Vigil's role-keyed identity survives the UID change (the role series is continuous across the churn).",
        inject_fn=_kill_inject, heal_fn=None,
    ),
    Fault(
        id="replica_flap", name="Replica instability", icon="🔁", category="Lifecycle", control="slider",
        target_default="stream-processor", target_choices=APP_WORKLOADS,
        param_label="Scale up to", param_min=2, param_max=5, param_default=3, param_step=1, param_unit="replicas",
        what="Scales the deployment up (real kubectl scale); heal scales back to 1 — flap it to create instability.",
        effect="Rapid pod churn: new pods spin up/down, identities succeed each other.",
        tests=["Replica instability", "Churn-stable identity", "HPA-like flapping"],
        expected="Multiple pod successions; the role series stays churn-stable while per-pod series come and go.",
        inject_fn=_flap_inject, heal_fn=_flap_heal,
    ),

    # ===== Communication / dependency outage =====
    Fault(
        id="broker_outage", name="Message-bus outage", icon="📡", category="Communication", control="button",
        target_default="edgenius-broker", target_choices=["edgenius-broker"], target_locked=True,
        what="Scales the MQTT broker (edgenius-broker) to 0 — the message bus between gateway and stream-processor.",
        effect="Gateway can't publish, stream-processor can't subscribe → telemetry flow stops → historian freezes → downstream staleness.",
        tests=["Service communication breakdown", "Multi-service propagation", "Dependency mapping", "Root cause"],
        expected="Downstream PHEN_APP_DATA_STALENESS on asset-api; the dependency chain shows the impacted services. (The broker being scaled to 0 is the visible upstream.)",
        inject_fn=_scale0_inject, heal_fn=_scale1_heal,
    ),
    Fault(
        id="db_outage", name="Database outage", icon="🗄️", category="Communication", control="button",
        target_default="genix-historian", target_choices=["genix-historian", "asset-registry"], target_locked=False,
        what="Scales a backbone datastore (historian TSDB or asset-registry Postgres) to 0.",
        effect="Its callers lose their data store: asset-api/pdm-analyzer queries fail → errors, staleness, queue buildup.",
        tests=["Database bottleneck/outage", "Multi-service dependent failure", "Dependency mapping", "Root cause"],
        expected="Downstream degradation on the DB's callers over observed-flow edges; a cross-service cascade from the impacted callers.",
        inject_fn=_scale0_inject, heal_fn=_scale1_heal,
    ),
]

CATALOG_BY_ID = {f.id: f for f in CATALOG}
CATEGORIES = ["Application", "Resource", "Storage", "Lifecycle", "Communication"]


def inject(fault_id: str, target: str, value=None) -> kube.Result:
    return CATALOG_BY_ID[fault_id].inject_fn(target, value)


def heal(fault_id: str, target: str) -> kube.Result:
    f = CATALOG_BY_ID[fault_id]
    if f.heal_fn is None:
        return kube.Result(f"({f.id} has no heal — self-recovering)", 0, "self-recovering (controller restores)", "")
    return f.heal_fn(target)
