"""scenarios.py — one-button, multi-fault presets that each exercise a span of Vigil
capabilities, plus a global reset. Composed from the SAME catalog the manual panel uses,
so a scenario is just a named sequence of real faults — no separate, fake code path."""
from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field

import faults
import kube


@dataclass
class Step:
    fault_id: str
    target: str
    value: float | None = None
    delay: float = 0.0          # seconds to wait before firing (staggering)
    note: str = ""


@dataclass
class Scenario:
    id: str
    name: str
    icon: str
    story: str
    capabilities: list[str]
    timeline: str
    steps: list[Step] = field(default_factory=list)
    oscillate: dict | None = None   # {target, on_value, period, duration} for intermittent faults


SCENARIOS: list[Scenario] = [
    Scenario(
        id="full_cascade", name="Full Cascade — Plant Outage", icon="🌋",
        story="The flagship all-capability incident. The time-series historian is taken out (a DB outage) while, "
              "concurrently, the PdM analyzer leaks memory toward an OOM and the stream-processor backs up under "
              "latency + CPU burn. Three independent root faults across the dependency graph at once. "
              "(NB: the historian outage — not a gateway disconnect — is used so the broker keeps flowing and the "
              "stream-processor's queue still builds; a concurrent connectivity-loss would starve the queue.)",
        capabilities=["Forecasting", "Anomaly detection", "Multi-event chaining", "Dependency mapping",
                      "Root-cause analysis", "Multi-service propagation", "CARDINAL non-merge (independent faults)"],
        timeline="t+0 inject all · t+1–2m queue saturation + throttling fire · t+2m asset-api staleness + cross-service "
                 "cascade (historian's callers) · t+4–6m analyzer forecast warning → MEMORY_LEAK → OOM cascade. Watch the Vigil Live tab.",
        steps=[
            Step("latency_queue", faults.SIM_STREAM, 300, note="queue saturation (broker still flowing)"),
            Step("cpu_burn", faults.SIM_STREAM, note="CPU throttling"),
            Step("mem_leak", faults.SIM_ANALYZER, note="forecast + OOM cascade"),
            Step("db_outage", "genix-historian", note="TSDB outage → asset-api staleness + cross-service cascade"),
        ],
    ),
    Scenario(
        id="resource_exhaustion", name="Resource Exhaustion Sweep", icon="🔥",
        story="Exhaust all three local resources at once: memory (analyzer leak → OOM), CPU (stream burn → throttle), "
              "and disk (historian PVC ballast).",
        capabilities=["Resource exhaustion (CPU/memory/disk)", "Forecasting", "Anomaly detection", "Pod restarts"],
        timeline="t+0 inject all · t+1m CPU throttling · t+2–5m memory forecast → OOM · disk fills for real "
                 "(metric dark on kind — honest blind spot).",
        steps=[
            Step("mem_leak", faults.SIM_ANALYZER, note="memory → OOM"),
            Step("cpu_burn", faults.SIM_STREAM, note="CPU → throttle"),
            Step("disk_fill", "genix-historian", 2, note="disk fills (real; metric dark on kind)"),
        ],
    ),
    Scenario(
        id="dependency_breakdown", name="Dependency Breakdown (DB outage)", icon="🗄️",
        story="A single backbone fault with a wide blast radius: the time-series historian is taken out. Its callers "
              "(asset-api, pdm-analyzer, stream-processor writes) all lose their datastore — a true multi-service "
              "dependent failure from one root.",
        capabilities=["Dependency mapping", "Multi-service dependent failure", "Root-cause analysis",
                      "Propagation paths", "Service communication breakdown"],
        timeline="t+0 historian → 0 replicas · t+1–2m downstream errors/staleness · cross-service cascade lights up "
                 "from the impacted callers. The visible upstream is the scaled-down historian.",
        steps=[
            Step("db_outage", "genix-historian", note="TSDB outage → fan-out to callers"),
        ],
    ),
    Scenario(
        id="replica_instability", name="Replica Instability & Crashes", icon="🔁",
        story="Pod-lifecycle chaos: flap the stream-processor's replica count and kill the analyzer pod, forcing "
              "successions and restarts.",
        capabilities=["Replica instability", "Pod crashes/restarts", "Churn-stable identity (CEI)", "Recovery"],
        timeline="t+0 stream scaled to 3 + analyzer pod killed · pods churn · the role series stays continuous while "
                 "per-pod series come and go. Heal scales back to 1.",
        steps=[
            Step("replica_flap", "stream-processor", 3, note="replica churn"),
            Step("kill_pod", "pdm-analyzer", delay=4, note="crash + restart"),
        ],
    ),
    Scenario(
        id="intermittent_degradation", name="Intermittent Degradation", icon="〰️",
        story="A flapping fault, not a steady one: the stream-processor's latency oscillates on/off, producing "
              "intermittent queue build-ups and recoveries — the kind of noisy signal that defeats naive alerting.",
        capabilities=["Intermittent failures", "Anomaly detection", "Band departure", "Service degradation"],
        timeline="latency toggles 400ms ↔ 0 every ~45s for ~6 min. Each burst nudges the queue; watch onsets and the "
                 "unexplained channel rather than a single threshold.",
        steps=[],
        oscillate={"target": faults.SIM_STREAM, "on_value": 400, "period": 45, "duration": 360},
    ),
]

SCENARIOS_BY_ID = {s.id: s for s in SCENARIOS}

# active background oscillators, keyed by scenario id, with a stop Event
_OSC: dict[str, threading.Event] = {}


def launch(scenario_id: str) -> list[tuple[str, kube.Result]]:
    """Fire a scenario. Immediate steps run now (returned); delayed/oscillating steps run
    in a daemon thread. Returns (label, Result) for the steps fired synchronously."""
    s = SCENARIOS_BY_ID[scenario_id]
    out: list[tuple[str, kube.Result]] = []

    immediate = [st for st in s.steps if st.delay <= 0]
    delayed = [st for st in s.steps if st.delay > 0]

    for st in immediate:
        r = faults.inject(st.fault_id, st.target, st.value)
        out.append((f"{st.fault_id} → {st.target}", r))

    if delayed:
        def _run_delayed(steps):
            for st in steps:
                time.sleep(st.delay)
                faults.inject(st.fault_id, st.target, st.value)
        threading.Thread(target=_run_delayed, args=(delayed,), daemon=True).start()

    if s.oscillate:
        out.append((f"oscillator → {s.oscillate['target']}", _start_oscillator(s)))

    return out


def _start_oscillator(s: Scenario) -> kube.Result:
    cfg = s.oscillate
    stop = threading.Event()
    _OSC[s.id] = stop

    def _osc():
        end = time.time() + cfg["duration"]
        on = True
        while not stop.is_set() and time.time() < end:
            faults.inject("latency_queue", cfg["target"], cfg["on_value"] if on else 0)
            on = not on
            stop.wait(cfg["period"])
        # leave it healed
        faults.heal("latency_queue", cfg["target"])

    threading.Thread(target=_osc, daemon=True).start()
    return kube.Result(f"(oscillator started on {cfg['target']})", 0,
                       f"toggling every {cfg['period']}s for {cfg['duration']}s", "")


def heal(scenario_id: str) -> list[tuple[str, kube.Result]]:
    """Heal a scenario's faults (and stop its oscillator)."""
    s = SCENARIOS_BY_ID[scenario_id]
    out: list[tuple[str, kube.Result]] = []
    if s.id in _OSC:
        _OSC[s.id].set()
        del _OSC[s.id]
    for st in s.steps:
        out.append((f"heal {st.fault_id} → {st.target}", faults.heal(st.fault_id, st.target)))
    if s.oscillate:
        out.append(("heal oscillator", faults.heal("latency_queue", s.oscillate["target"])))
    return out


def global_reset() -> list[tuple[str, kube.Result]]:
    """Return the whole namespace to a clean baseline: stop oscillators, clear every sim
    control flag, restore patched limits, remove disk ballast, scale everything back to 1."""
    out: list[tuple[str, kube.Result]] = []
    for ev in list(_OSC.values()):
        ev.set()
    _OSC.clear()

    # clear all sim /ctl flags — the gateway returns to the current steady baseline (the
    # steady rate when "keep warm" is on, else idle), never below it, so HEAL ALL keeps the
    # pipeline warm rather than dropping it to idle.
    for dep, q in [
        (faults.SIM_GATEWAY, f"disconnect=0&rate={faults.baseline_rate()}"),
        (faults.SIM_STREAM, "slow=0&cpuburn=0"),
        (faults.SIM_ANALYZER, "leak=off"),
    ]:
        out.append((f"reset {dep}", kube.exec_ctl(dep, q)))

    # restore any patched limits
    for (target, resource), orig in list(faults._ORIG_LIMITS.items()):
        if orig:
            out.append((f"restore {target} {resource}", kube.patch_limit(target, resource, orig)))
    faults._ORIG_LIMITS.clear()

    # remove disk ballast from every backbone store
    for name in faults.BACKBONE:
        kube.disk_unfill(name)

    # scale everything back to 1
    for name in faults.APP_WORKLOADS + faults.BACKBONE:
        if kube.get_replicas(name) != 1:
            out.append((f"scale {name}=1", kube.scale(name, 1)))

    return out
