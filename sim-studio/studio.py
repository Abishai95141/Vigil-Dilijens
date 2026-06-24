"""ABB Genix — Vigil Scenario Studio (Streamlit).

A scenario-first showcase console for the live abb-genix cluster. Each scenario is framed
around ONE operator question; it injects REAL, controlled faults (genuine kubectl ops), shows
which Vigil pages light up, and lets you ASK the MCP-connected agent the question and get a
grounded answer. Built to demonstrate Vigil end-to-end: detection, forecasting, cross-service
cascades, root-cause, association, right-sizing — and the charter discipline (it never invents
a cause; an association is a lead, only an authored relation is causal).

Run:  cd sim-studio && streamlit run studio.py
Env:  DGX_API_KEY (DeepSeek, for the agent pane) · OBSD_BASE (default http://localhost:9095)
      CLUSTER_KUBECONFIG (default /tmp/aws-k3s.yaml) · CLUSTER_CONTEXT (default "default")
"""
from __future__ import annotations

import pandas as pd
import streamlit as st

import agent
import cluster
import faults
import scenarios
import vigil

st.set_page_config(page_title="Vigil Scenario Studio", page_icon="🛰️", layout="wide")


@st.cache_data(ttl=8, show_spinner=False)
def _pods(_k, _c, _n):
    return cluster.list_pods()


@st.cache_data(ttl=8, show_spinner=False)
def _snap(_base):
    return vigil.snapshot()


def _refresh():
    st.cache_data.clear()
    st.rerun()


# ============================ SIDEBAR ======================================
with st.sidebar:
    st.title("🛰️ Vigil Scenario Studio")
    st.caption("Real, controlled failures on **abb-genix** → seen in **Vigil** → asked of the **agent**.")

    st.subheader("Connection")
    cluster.CFG.kubeconfig = st.text_input("kubeconfig", cluster.CFG.kubeconfig)
    cluster.CFG.context = st.text_input("context", cluster.CFG.context)
    cluster.CFG.namespace = st.text_input("namespace", cluster.CFG.namespace)
    vigil.OBSD_BASE = st.text_input("obsd URL", vigil.OBSD_BASE)

    pods = _pods(cluster.CFG.kubeconfig, cluster.CFG.context, cluster.CFG.namespace)
    running = sum(1 for p in pods if p["phase"] == "Running")
    c1, c2 = st.columns(2)
    c1.metric("Pods running", f"{running}/{len(pods)}" if pods else "—")
    c2.metric("obsd", "● up" if vigil.ready() else "○ down")
    c3, c4 = st.columns(2)
    c3.metric("MCP tools", vigil.mcp_tool_count())
    c4.metric("Agent key", "● set" if agent.DGX_API_KEY else "○ unset")

    st.divider()
    st.subheader("🚦 Steady traffic")
    st.caption("Keep the pipeline warm (gateway rate 12 → 60 samples/s, well under every SLO bar) so "
               "cascade scenarios fire immediately. Never trips a failure.")
    if "steady" not in st.session_state:
        st.session_state["steady"] = False
    steady = st.toggle("Keep system warm", value=st.session_state["steady"])
    if steady != st.session_state["steady"]:
        st.session_state["steady"] = steady
        r = faults.set_steady_traffic(steady)
        (st.success if r.ok else st.error)(f"steady={'on' if steady else 'off'} · {r.summary()[:50]}")

    st.divider()
    st.subheader("🩹 Safety")
    st.caption("Clears every sim flag, restores patched limits, removes PVC ballast, scales the backbone back to 1.")
    if st.button("HEAL ALL / RESET", type="primary", use_container_width=True):
        with st.spinner("resetting…"):
            res = faults.global_reset()
        st.success(f"Reset issued ({len(res)} actions).")
        for label, r in res:
            (st.write if r.ok else st.error)(f"{'✓' if r.ok else '✗'} {label}")

    st.divider()
    st.caption("Console: http://localhost:5173 · obsd /api + /mcp on :9095")


st.title("Vigil Scenario Studio — see it, then ask about it")

tab_principle, tab_scen, tab_live, tab_agent, tab_cluster = st.tabs(
    ["🧭  Working Principle", "🎬  Scenarios", "📊  Vigil Live", "🤖  Ask the Agent", "🩺  Cluster"]
)


# ============================ WORKING PRINCIPLE ============================
with tab_principle:
    st.markdown("""
### How this studio works — and how Vigil reasons

Every scenario here injects a **real, controlled fault** into the live `abb-genix` cluster
(a genuine `kubectl` operation — `exec`, `patch`, `scale`, `dd`, `delete` — nothing mocked).
Vigil then *sees* the result through its normal lanes, and you can **ask the MCP-connected
agent** the operator question and get an answer grounded only in what Vigil measured.

#### Provenance is the whole point
Vigil labels every fact with a provenance class, and never blurs them:

| Class | Meaning | Example surface |
|---|---|---|
| **MEASURED** | directly observed | findings, onsets, dependency edges, PVC fill, CPU usage |
| **PROJECTED** | a forecast with a mandatory uncertainty band — never a certainty | early-warning cards |
| **AUTHORED** | a human-curated causal relation — the **only** basis for a *cause* | root-cause orientation |
| **ASSOCIATION** | series that move together — a *lead*, **not** a cause | dependency / causal-hypotheses |
| **ADVISORY** | a recommendation a human applies — the system never acts | right-sizing |

#### The discipline you'll see on every scenario
- **Association ≠ cause.** Two services' CPU moving together is surfaced as a *direction-free
  hypothesis*. Vigil shows which one *stepped first* but **refuses to name the aggressor** — a
  named operator authors the arrow.
- **Causes come only from authored relations.** The root-cause chain is oriented by an authored
  `UPSTREAM_DEGRADATION → DOWNSTREAM_IMPACT` relation + observed-flow edges, never by timing.
- **It admits what it can't see.** The Coverage, Silence-ledger and Blindspots surfaces state
  the honest floor: e.g. Vigil names the throttled *container*, not the process inside it.
- **Independent faults never merge.** Two unrelated incidents stay two chains (cardinal rule).

#### The six showcase questions
""")
    for s in scenarios.SCENARIOS:
        st.markdown(f"- {s.icon} **{s.question}**  → _{s.name}_")
    st.info("Open **Scenarios** to inject one, watch **Vigil Live**, then **Ask the Agent** the matching question.")


# ============================ SCENARIOS ===================================
def _render_scenario(s: scenarios.Scenario):
    with st.container(border=True):
        st.markdown(f"### {s.icon} {s.name}")
        st.markdown(f"**Operator question:** _{s.question}_")
        st.markdown(f"**How Vigil answers it:** {s.principle}")
        st.info(f"⏱️ {s.timeline}")
        cols = st.columns([3, 2])
        with cols[0]:
            st.markdown("**Vigil pages it lights up:** " + " · ".join(f"`{p}`" for p in s.pages))
            st.markdown("**MCP tools that ground the answer:** " + " · ".join(f"`{t}`" for t in s.mcp_tools))
            st.markdown(f"**Expected:** {s.expected}")
        with cols[1]:
            if s.mode == "readonly":
                st.success("Always-on — no fault needed. See the table below / ask the agent.")
                rs = vigil.right_sizing()
                rows = rs.get("advisories", []) if isinstance(rs, dict) else []
                summ = rs.get("summary", {}) if isinstance(rs, dict) else {}
                if summ:
                    st.caption(f"analyzed {summ.get('analyzed')} · reclaim {summ.get('reclaim')} · "
                               f"resizeUp {summ.get('resizeUp')} · unstable {summ.get('unstable')}")
                reclaim = [r for r in rows if r.get("action") == "reclaim"][:8]
                if reclaim:
                    st.dataframe(pd.DataFrame([
                        {"workload": r.get("name"), "res": r.get("resource"),
                         "p95": round(r.get("p95", 0), 2), "request": r.get("request"),
                         "→ recommend": r.get("recommended")} for r in reclaim
                    ]), use_container_width=True, hide_index=True)
            elif s.mode == "choice":
                st.caption("Pick ONE lead-time variant (mutually exclusive — same pod):")
                for lv in s.levers:
                    f = faults.CATALOG[lv.fault_id]
                    st.code(f.preview_fn() if f.preview_fn else lv.fault_id, language="bash")
                    if st.button(f"💉 {lv.label}", key=f"lv_{s.id}_{lv.fault_id}", use_container_width=True):
                        with st.spinner("injecting…"):
                            r = faults.inject(lv.fault_id)
                        st.session_state[f"res_{s.id}"] = (r.ok, f"{lv.label} · {r.summary()[:80]}")
                if st.button("🩹 Heal (leak off)", key=f"heal_{s.id}", use_container_width=True):
                    r = faults.heal(s.levers[0].fault_id)
                    st.session_state[f"res_{s.id}"] = (r.ok, f"healed · {r.summary()[:60]}")
            else:  # sequence
                for lv in s.levers:
                    f = faults.CATALOG[lv.fault_id]
                    tag = f" (+{int(lv.delay)}s)" if lv.delay else ""
                    st.code((f.preview_fn() if f.preview_fn else lv.fault_id) + tag, language="bash")
                b1, b2 = st.columns(2)
                if b1.button("🚀 Launch", key=f"launch_{s.id}", type="primary", use_container_width=True):
                    with st.spinner("launching…"):
                        res = scenarios.launch_sequence(s.id)
                    msgs = " | ".join(f"{'✓' if r.ok else '✗'} {lbl}" for lbl, r in res)
                    st.session_state[f"res_{s.id}"] = (all(r.ok for _, r in res), msgs or "launched")
                if b2.button("🩹 Heal", key=f"heal_{s.id}", use_container_width=True):
                    with st.spinner("healing…"):
                        res = scenarios.heal_scenario(s.id)
                    st.session_state[f"res_{s.id}"] = (all(r.ok for _, r in res), "healed")
            if f"res_{s.id}" in st.session_state:
                ok, msg = st.session_state[f"res_{s.id}"]
                (st.success if ok else st.error)(msg)


with tab_scen:
    st.caption("Each card answers one question with REAL faults. Launch it, watch **Vigil Live**, "
               "then **Ask the Agent** the same question. Heal per-card or **HEAL ALL** in the sidebar.")
    for s in scenarios.SCENARIOS:
        _render_scenario(s)


# ============================ VIGIL LIVE ==================================
with tab_live:
    c = st.columns([1, 4])
    if c[0].button("🔄 Refresh", use_container_width=True):
        _refresh()
    c[1].caption("How Vigil currently sees the cluster (reads obsd /api). Inject a fault, wait an eval tick (~15s), refresh.")

    snap = _snap(vigil.OBSD_BASE)
    if not snap["ready"]:
        st.error("obsd is not reachable on the configured URL.")
    else:
        m = st.columns(5)
        m[0].metric("Active findings", len(snap["active_findings"]))
        m[1].metric("Forecast cards", len(snap["forecast_cards"]))
        m[2].metric("Cross-service", "● active" if snap["cross_service_active"] else "○ idle")
        m[3].metric("Root-cause chain", "● active" if snap["root_cause_active"] else "○ idle")
        m[4].metric("Onsets", snap["onset_count"])

        st.subheader("🔬 Detection — active findings (MEASURED)")
        if snap["active_findings"]:
            st.dataframe(pd.DataFrame(snap["active_findings"]), use_container_width=True, hide_index=True)
        else:
            st.caption("none firing right now (clean baseline).")

        st.subheader("🧭 Root cause (MEASURED ⋈ AUTHORED)")
        rc = snap["root_cause"]
        if snap["root_cause_active"]:
            st.markdown(f"**Most-upstream degraded node:** `{rc.get('mostUpstreamDegradedNode') or rc.get('most_upstream_degraded_node')}`")
            st.json(rc.get("path") or rc.get("chains") or rc, expanded=False)
        else:
            st.caption(rc.get("note", "no transitive chain this tick — independent faults stay separate."))

        st.subheader("🔗 Cross-service cascade")
        ch = snap["cross_service_chain"]
        if snap["cross_service_active"] and ch:
            for link in (ch if isinstance(ch, list) else ch.get("chain", [])):
                st.markdown(f"- degraded **{link.get('degraded')}** → impacted **{link.get('impacted')}** · _{link.get('edge_class') or link.get('edgeClass')}_")
        else:
            st.caption("no cross-service cascade firing.")

        st.subheader("🔮 Early warnings (PROJECTED)")
        fc = snap["forecast_cards"]
        if fc:
            st.dataframe(pd.DataFrame([{
                "entity": (c.get("entityCei") or "")[-40:], "metric": c.get("metric"),
                "bar": c.get("barValue"), "crossAt": c.get("crossAt"),
                "leadSeconds": c.get("timeToCrossSeconds"), "band": c.get("confidence"),
                "precursor": ",".join(c.get("precursorPhenomena", []) or []),
            } for c in fc]), use_container_width=True, hide_index=True)
        else:
            st.caption("no early-warning card (a series must be climbing toward its bar — run a leak).")

        st.subheader("🧮 Right-sizing (ADVISORY) — top reclaim candidates")
        rs = snap["rightsizing"]
        reclaim = [r for r in rs if r.get("action") == "reclaim"][:10]
        if reclaim:
            st.dataframe(pd.DataFrame([{
                "workload": r.get("name"), "res": r.get("resource"), "p95": round(r.get("p95", 0), 2),
                "request": r.get("request"), "→ recommend": r.get("recommended"), "stable": r.get("stable"),
            } for r in reclaim]), use_container_width=True, hide_index=True)
        else:
            st.caption("no reclaim rows right now.")


# ============================ ASK THE AGENT ===============================
with tab_agent:
    st.caption("The MCP-connected agent: it gathers GROUNDED facts from Vigil's MCP tools, then "
               "DeepSeek synthesises an answer — grounded only in those facts, provenance preserved, "
               "no invented causes. The raw grounding is shown so you can audit every claim.")
    if not agent.DGX_API_KEY:
        st.warning("`DGX_API_KEY` is not set — export the DeepSeek key (same one obsd uses) to enable synthesis. "
                   "The grounded MCP facts will still be shown below.")

    canned = list(agent.QUESTION_TOOLS.keys())
    q = st.selectbox("Pick a showcase question (or choose ✍️ to type your own):", canned + ["✍️ type my own"])
    if q == "✍️ type my own":
        q = st.text_input("Your question:", "What's the cause of a failure in our cluster?")

    if st.button("🤖 Ask the agent", type="primary"):
        with st.spinner("gathering grounded facts + synthesising…"):
            res = agent.answer(q)
        tools = res.get("tools", [])
        st.markdown(f"**Tools gathered:** " + " · ".join(f"`{t}`" for t in tools))
        if res.get("error"):
            st.error(f"agent synthesis error: {res['error']}")
        if res.get("answer"):
            st.markdown("#### Answer  \n*(AI-synthesised from grounded facts — verify against the grounding below)*")
            st.markdown(res["answer"])
        with st.expander("🔎 Raw grounding (the exact MCP facts the answer must be based on)"):
            for tool, val in res.get("grounded", {}).items():
                st.markdown(f"**{tool}**")
                st.json(val, expanded=False)


# ============================ CLUSTER =====================================
with tab_cluster:
    if st.button("🔄 Refresh cluster", use_container_width=False):
        _refresh()
    st.subheader("Pods")
    if pods:
        st.dataframe(pd.DataFrame(pods), use_container_width=True, hide_index=True)
    else:
        st.error("could not list pods — check kubeconfig/context/namespace.")

    st.subheader("Armed sim faults (per /ctl pod)")
    rows = []
    for dep in [faults.GATEWAY, faults.STREAM, faults.ANALYZER]:
        s = cluster.ctl_state(dep)
        rows.append({"workload": dep, **{k: v for k, v in s.items() if not k.startswith("_")}})
    st.dataframe(pd.DataFrame(rows), use_container_width=True, hide_index=True)

    st.subheader("Dependency graph (reference)")
    st.code(
        "smart-sensors → opcua-gateway → edgenius-broker → stream-processor → genix-historian → asset-api\n"
        "pdm-analyzer → genix-historian , asset-registry        asset-api → asset-registry , genix-historian\n"
        "operations-dashboard → asset-api , genix-historian",
        language="text",
    )
