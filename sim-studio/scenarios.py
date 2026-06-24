"""scenarios.py — the six showcase scenarios, each framed around ONE operator question.

A scenario names the question, explains how Vigil answers it (the working principle), lists
the real faults it injects (composed from faults.py — the SAME catalog the manual panel runs),
the Vigil pages it lights up, the MCP tools that ground the answer, and the exact question to
ask the agent. Three modes:

  - "sequence"  : fire the levers in order (with staggering)        — cascades / PVC / noisy-neighbour
  - "choice"    : each lever is a mutually-exclusive one-click fault — forecast lead-time variants
  - "readonly"  : no fault; the answer is always-on                 — right-sizing / optimization
"""
from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field

import cluster
import faults


@dataclass
class Lever:
    fault_id: str
    label: str
    delay: float = 0.0   # seconds to wait before firing (staggering, sequence mode)


@dataclass
class Scenario:
    id: str
    name: str
    icon: str
    question: str            # the operator question this scenario answers (matches agent.py)
    principle: str           # how Vigil answers it — the working principle shown on the card
    timeline: str
    levers: list[Lever]
    pages: list[str]         # the Vigil console pages that light up
    mcp_tools: list[str]     # the MCP tools that ground the agent's answer
    expected: str            # what the operator should see / the answer
    mode: str = "sequence"   # sequence | choice | readonly


SCENARIOS: list[Scenario] = [
    Scenario(
        id="full_suite",
        name="The Grand Tour — full-suite incident (what · when · why · how)",
        icon="🎷",
        question="What is happening in our cluster right now — what, when, why, and how do I fix it?",
        principle=(
            "TWO independent real faults at once. Vigil sees both, keeps them SEPARATE (it never "
            "merges coincident-but-unrelated faults), and answers each operator question from a "
            "different lane: WHAT = a MEASURED finding; WHEN = a PROJECTED forecast with a lead "
            "time; WHY = an AUTHORED relation + the cross-service cascade; HOW = the agent's "
            "grounded remediation. The whole console lights up — and the agent, asked on the Ask "
            "page, returns one advisory covering both problems without conflating them."
        ),
        timeline=(
            "t+0  historian image-pull failure → genix-historian ready 0/1 (desired 1) → "
            "PHEN_WORKLOAD_UNAVAILABLE + a cross-service cascade to its callers (asset-api, "
            "pdm-analyzer, operations-dashboard) over observed-flow edges — the down-NOW root "
            "(WHAT + WHY are immediate).\n"
            "t+10s  a sustained memory leak is armed on pdm-analyzer → within ~1 window a CUSUM "
            "onset marks the step (the anomaly — WHEN it STARTED, immediately).\n"
            "t+10–15m  as the climb sustains and fills the forecast window, a PROJECTED early-warning "
            "card gives the lead time to the ~243Mi bar — the WHEN it WILL breach, before impact "
            "(forecasting needs history; arm this first and let it mature while you walk the other "
            "pages). The two incidents stay SEPARATE in the incident store (the anti-false-chain rule)."
        ),
        levers=[
            Lever("db_outage", "Historian image-pull failure (the down-NOW root)"),
            Lever("mem_leak_medium", "Arm the pdm-analyzer memory leak (the forecastable risk — matures into an early-warning card)", delay=10),
        ],
        pages=[
            "Findings (WORKLOAD_UNAVAILABLE)", "Early warnings (BandBar — the WHEN)",
            "Anomalies / Onsets", "Departures", "Root cause / Cross-service cascade (the WHY)",
            "Incidents (two, kept separate)", "Timeline", "Insights", "Coverage",
            "Silence ledger", "Right-sizing", "Cluster graph / Topology",
        ],
        mcp_tools=[
            "get_findings", "get_warnings", "get_onsets", "get_cross_service", "get_root_cause_chain",
            "get_incidents", "get_insights", "get_departures", "get_timeline", "get_rightsizing_advice",
        ],
        expected=(
            "TWO independent problems surfaced and NOT conflated: (1) genix-historian is DOWN NOW "
            "(image-pull) — PHEN_WORKLOAD_UNAVAILABLE, its callers impacted over observed-flow "
            "edges (the WHY/cross-service, immediate); (2) pdm-analyzer has a MEMORY LEAK — an onset "
            "marks WHEN it started right away, and as it sustains a PROJECTED early-warning card "
            "gives the lead time to the ~243Mi bar (the WHEN it will breach). On the Ask page, ask "
            "'what is happening, when, why and how do I fix it?' → the agent returns ONE advisory "
            "with WHAT / WHEN / WHY / HOW and a per-issue remediation, labelling each fact's "
            "provenance and refusing to merge the two."
        ),
        mode="sequence",
    ),
    Scenario(
        id="cascade_rootcause",
        name="Multi-level cascade → root cause",
        icon="🌋",
        question="What's the cause of a failure in our cluster?",
        principle=(
            "Vigil stitches MEASURED-degraded workloads into an ordered chain over OBSERVED-FLOW "
            "edges, oriented ONLY by an AUTHORED relation (never by timing). It names the "
            "most-upstream degraded node — the deepest degraded callee that calls no other degraded "
            "node — and refuses to merge coincident-but-unrelated faults."
        ),
        timeline="t+0 historian image-pull failure → ready 0/1 (desired 1) · PHEN_WORKLOAD_UNAVAILABLE fires on genix-historian · t+1–2m callers degrade · the workload itself is the MEASURED degraded root.",
        levers=[
            Lever("db_outage", "Make the historian (InfluxDB) unavailable — image-pull failure (the root)"),
            Lever("latency_queue", "Add queue saturation on stream-processor (intermediate hop)", delay=30),
        ],
        pages=["Findings (WORKLOAD_UNAVAILABLE)", "Root cause (chain)", "Cross-service cascade", "Insights", "Cluster graph / Topology", "Timeline"],
        mcp_tools=["get_root_cause_chain", "get_insights", "get_cross_service", "get_findings"],
        expected="PHEN_WORKLOAD_UNAVAILABLE (quality full) on abb-genix/genix-historian — the workload itself is now a MEASURED degraded node, so the agent names genix-historian as the cause instead of the nearest downstream symptom (asset-api). (Build 5: a scale-to-0 would be ignored as an intentional shutdown; this uses ready<desired.)",
        mode="sequence",
    ),
    Scenario(
        id="cpu_spike",
        name="Which pod is spiking CPU?",
        icon="📈",
        question="Which pod is causing unexpected CPU spikes?",
        principle=(
            "The off-digest CUSUM onset lane scans every hot CPU series and marks the exact TIME and "
            "DIRECTION a sustained step began, keyed by the container CEI — so it names the specific "
            "pod whose CPU stepped up. It asserts WHEN, not WHY: the step is a MEASURED magnitude, "
            "not a cause."
        ),
        timeline="t+0 cpu_burn on stream-processor (over its 250m limit) · within ~1 window PHEN_CPU_AGGRESSOR names the pod (over its OWN limit) + an onset marks the step + PHEN_THROTTLING_CASCADE fires.",
        levers=[Lever("cpu_burn", "Burn CPU on stream-processor (over its 250m limit)")],
        pages=["Findings (CPU_AGGRESSOR)", "Anomalies / Onsets", "Insights", "Right-sizing"],
        mcp_tools=["get_findings", "get_onsets", "get_unexplained", "get_dependency"],
        expected="PHEN_CPU_AGGRESSOR (quality full) names the stream-processor container as sustained AT/ABOVE its OWN declared 250m limit — the measured aggressor; an onset row + PHEN_THROTTLING_CASCADE corroborate. The agent names the pod.",
        mode="sequence",
    ),
    Scenario(
        id="noisy_neighbour",
        name="Services influencing each other's resources",
        icon="🔀",
        question="Are different services influencing each other's resource consumption?",
        principle=(
            "On a shared node, Vigil MEASURES which services' resource series move together (windowed "
            "Pearson → associated-with edges) and MEASURES which stepped first (co-onset order). It "
            "surfaces this as a DIRECTION-FREE hypothesis — an association is a lead, NOT a cause. It "
            "refuses to name the aggressor automatically; a named operator authors the arrow."
        ),
        timeline="t+0 cpu_burn on stream-processor (aggressor) · hold ~10m · victims (postgres/influxdb/minio/mosquitto) show rising CPU-pressure; co-onset + association surface on the Causal-hypotheses & Onsets pages.",
        levers=[Lever("cpu_burn", "Burn CPU on stream-processor (the noisy aggressor)")],
        pages=["Causal hypotheses", "Anomalies / Onsets", "Dependency (association)", "Right-sizing", "Blindspots"],
        mcp_tools=["get_dependency", "get_causal_hypotheses", "get_onsets", "get_cross_service"],
        expected="PHEN_CPU_AGGRESSOR names stream-processor as over its OWN cpu limit (the measured aggressor — a directional, mechanistic fact, NOT a correlation); co-located victims show CPU/PSI association edges + a co-onset, kept DIRECTION-FREE (a lead, not a cause) for the operator to author.",
        mode="sequence",
    ),
    Scenario(
        id="pvc_io_restart",
        name="PVC I/O patterns ↔ pod restarts",
        icon="💽",
        question="How are PVC I/O patterns linked to pod restarts?",
        principle=(
            "On this real EBS gp3 cluster the kind blind spot is CLOSED: Vigil MEASURES per-PVC I/O "
            "(container_fs_writes_bytes_total, container_pressure_io_*) and PVC fill "
            "(kubelet_volume_stats_used_bytes), and the discrete restart (KSM counter + OOMKilled "
            "event). When the I/O pattern and the restart counter co-move, the association lane links "
            "them — but the DIRECTION (I/O → restart) is operator-authored; Vigil never invents it."
        ),
        timeline="t+0 vigil-pvcfill fills its 10Gi PVC above the 0.85 bar and keeps RISING while crashlooping · t+2–3m PHEN_PVC_FILLING (rising, on the PVC) + PHEN_PROBE_FAILURE_RESTART (on the pod that mounts it) overlap · the AUTHORED PVC_FILLING→PROBE_FAILURE_RESTART cascade is recognized over the mounts edge.",
        levers=[
            Lever("pvc_fill_cascade", "Deploy the rising-fill + crashloop PVC workload"),
        ],
        pages=["Findings (PVC_FILLING / PROBE_FAILURE_RESTART)", "Root cause (cascade)", "Insights", "Events", "Coverage", "Dependency (association)"],
        mcp_tools=["get_findings", "get_root_cause_chain", "get_dependency", "get_causal_hypotheses", "get_events", "get_incidents"],
        expected="PHEN_PVC_FILLING (MEASURED, rising) on data-vigil-pvcfill-0 + PHEN_PROBE_FAILURE_RESTART on vigil-pvcfill-0 → the recognized ⛓ PVC_FILLING→PROBE_FAILURE_RESTART cascade (joined over the mounts edge). The agent now ANSWERS the PVC-fill ↔ restart link (vs 'no measured link' before build 5).",
        mode="sequence",
    ),
    Scenario(
        id="forecast_leadtime",
        name="Early warning — long vs short lead",
        icon="🔮",
        question="Is there any early warning, and how could I resolve it?",
        principle=(
            "Vigil forecasts each hot series toward its OWN declared bar and emits a PROJECTED "
            "early-warning card with a mandatory uncertainty band and a lead time (lead ≈ "
            "headroom / leak-rate). The SAME leak shows a long lead when gentle and a short lead when "
            "fast — same fault, lead inversely proportional to rate. The 'how to resolve it' is "
            "synthesised by the agent from the cited precursor + blast radius, not a hardcoded runbook."
        ),
        timeline="Pick a leak rate. Gentle (48 KiB) → HOURS of lead (may show beyond-horizon early). Medium (512) → tens of minutes. Fast (4096) → ~8 min tight band → OOM. Watch the Early-warnings card change.",
        levers=[
            Lever("mem_leak_gentle", "Gentle leak — long lead (hours)"),
            Lever("mem_leak_medium", "Medium leak — tens of minutes"),
            Lever("mem_leak_fast", "Fast leak — ~8 min, then OOM"),
        ],
        pages=["Early warnings (BandBar)", "Departures", "Insights", "Silence ledger", "Root cause (on OOM)"],
        mcp_tools=["get_warnings", "get_insights", "get_departures"],
        expected="A PROJECTED card on pdm-analyzer container_memory_working_set_bytes with crossAt, a humanized lead, the confidence cone, and precursorPhenomena=[MEMORY_LEAK]; the agent proposes a grounded remediation.",
        mode="choice",
    ),
    Scenario(
        id="rightsizing",
        name="Which workloads need optimization?",
        icon="🧮",
        question="Which workloads need optimization?",
        principle=(
            "Vigil compares each workload's SUSTAINED usage (p95 over the window) to its OWN declared "
            "request/limit: reclaim when p95 < 50% of request, resize-up when p95 > 85% of limit, with "
            "×1.3 headroom. Recommendations are ADVISORY — a human acts, the system never does. A churny "
            "or mid-onset workload yields NO number (honest 'unstable' silence), never a guess."
        ),
        timeline="Always-on — no fault needed. The right-sizing lane answers immediately from the live p95 window.",
        levers=[],
        pages=["Right-sizing", "Coverage"],
        mcp_tools=["get_rightsizing_advice"],
        expected="A per-workload table: reclaim candidates (over-provisioned → recommended N), resize-up candidates, and unstable rows shown honestly as no-recommendation.",
        mode="readonly",
    ),
]

SCENARIOS_BY_ID = {s.id: s for s in SCENARIOS}

_BG: list[threading.Thread] = []


def launch_sequence(scenario_id: str) -> list[tuple[str, cluster.Result]]:
    """Fire a sequence scenario. Immediate levers run now (returned); delayed ones run in a
    daemon thread."""
    s = SCENARIOS_BY_ID[scenario_id]
    out: list[tuple[str, cluster.Result]] = []
    immediate = [lv for lv in s.levers if lv.delay <= 0]
    delayed = [lv for lv in s.levers if lv.delay > 0]
    for lv in immediate:
        out.append((lv.label, faults.inject(lv.fault_id)))
    if delayed:
        def _run(levers):
            for lv in levers:
                time.sleep(lv.delay)
                faults.inject(lv.fault_id)
        t = threading.Thread(target=_run, args=(delayed,), daemon=True)
        t.start()
        _BG.append(t)
    return out


def inject_lever(fault_id: str) -> cluster.Result:
    return faults.inject(fault_id)


def heal_scenario(scenario_id: str) -> list[tuple[str, cluster.Result]]:
    s = SCENARIOS_BY_ID[scenario_id]
    out: list[tuple[str, cluster.Result]] = []
    seen = set()
    for lv in s.levers:
        if lv.fault_id in seen:
            continue
        seen.add(lv.fault_id)
        out.append((f"heal {lv.label}", faults.heal(lv.fault_id)))
    return out
