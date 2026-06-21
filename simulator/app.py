"""ABB Genix — Vigil Failure Simulation Console (Streamlit).

A self-serve chaos console for the abb-genix cluster. Every control states exactly what it
does to the cluster, the downstream effect, which Vigil capability it exercises, and the
expected phenomenon — and shows the precise kubectl command it runs. One-button scenarios
compose real faults to test Vigil end-to-end. A live Vigil pane shows how the system reacts.

Run:  just simulator        (or)   cd simulator && uv run streamlit run app.py
"""
from __future__ import annotations

import pandas as pd
import streamlit as st

import faults
import kube
import scenarios
import vigil

st.set_page_config(page_title="ABB Genix · Vigil Failure Simulator", page_icon="🧨", layout="wide")


# Cached reads (keyed on context+namespace) so repeated reruns don't re-shell kubectl on
# every click. Refresh buttons clear the cache for an immediate fresh pull.
@st.cache_data(ttl=8, show_spinner=False)
def _pods(_ctx, _ns):
    return kube.list_pods()


@st.cache_data(ttl=8, show_spinner=False)
def _snap(_base):
    return vigil.snapshot()


@st.cache_data(ttl=8, show_spinner=False)
def _ctl(dep, _ctx, _ns):
    return kube.ctl_state(dep)


def _refresh():
    st.cache_data.clear()
    st.rerun()

# --- sidebar: connection + status + global reset ----------------------------
with st.sidebar:
    st.title("🧨 Failure Simulator")
    st.caption("Self-serve chaos console for **abb-genix** → observed in **Vigil**.")

    st.subheader("Connection")
    kube.CFG.context = st.text_input("kube-context", kube.CFG.context)
    kube.CFG.namespace = st.text_input("namespace", kube.CFG.namespace)
    vigil.CFG.base = st.text_input("obsd URL", vigil.CFG.base)

    # live status
    pods = _pods(kube.CFG.context, kube.CFG.namespace)
    running = sum(1 for p in pods if p["phase"] == "Running")
    vready = vigil.ready()
    c1, c2 = st.columns(2)
    c1.metric("Pods running", f"{running}/{len(pods)}" if pods else "—")
    c2.metric("Vigil obsd", "● up" if vready else "○ down")
    st.metric("MCP tools", vigil.mcp_tool_count())

    st.divider()
    st.subheader("🚦 Steady traffic")
    st.caption("Keep a healthy baseline load flowing (the gateway forward rate — well under every "
               "SLO bar, so it NEVER triggers a failure) so the pipeline stays warm and cascade "
               "scenarios fire immediately without a warm-up. Heal / Reset returns here, not to idle.")
    _son = st.session_state.get("steady_on", False)
    _sr = st.session_state.get("steady_rate", 12)
    steady_on = st.toggle("Keep system warm", value=_son)
    steady_rate = st.slider("Baseline rate (polls/s)", faults.IDLE_RATE, faults.STEADY_RATE_MAX, _sr, 1,
                            help="× 5 assets = samples/s; stays well below the 200/s LOAD_SURGE bar",
                            disabled=not steady_on)
    if steady_on != _son or (steady_on and steady_rate != _sr):
        st.session_state["steady_on"], st.session_state["steady_rate"] = steady_on, steady_rate
        r = faults.set_steady_traffic(steady_rate) if steady_on else faults.clear_steady_traffic()
        msg = f"steady traffic ON → rate {faults.baseline_rate()}" if steady_on else "steady traffic OFF (idle rate 5)"
        (st.success if r.ok else st.error)(f"{msg} · {r.summary()[:40]}")

    st.divider()
    st.subheader("🩹 Safety")
    st.caption("Returns the namespace to a clean baseline: clears all sim flags, restores patched limits, "
               "removes disk ballast, scales everything back to 1.")
    if st.button("HEAL ALL / RESET", type="primary", use_container_width=True):
        with st.spinner("resetting…"):
            res = scenarios.global_reset()
        st.success(f"Reset issued ({len(res)} actions).")
        for label, r in res:
            (st.write if r.ok else st.error)(f"{'✓' if r.ok else '✗'} {label}: {r.summary()[:80]}")

    st.divider()
    st.caption("Vigil console: http://localhost:5173  ·  obsd /api + /mcp on :9095")


st.title("ABB Genix — Failure Simulation Console")
st.markdown(
    "Trigger **real, controlled** failures against the cluster and watch Vigil interpret them. "
    "Every control below is transparent: it shows *what* it does, the *downstream effect*, which "
    "*Vigil capability* it tests, the *expected phenomenon*, and the exact *kubectl command*. "
    "Nothing here is a mock — each action is a genuine `kubectl` operation."
)

tab_panel, tab_scen, tab_vigil, tab_cluster = st.tabs(
    ["🎛️  Control Panel", "🚀  Scenarios", "📊  Vigil Live", "🩺  Cluster"]
)


# ============================ CONTROL PANEL =================================
def render_fault(f: faults.Fault):
    with st.container(border=True):
        head, ctrl = st.columns([3, 2])
        with head:
            st.markdown(f"#### {f.icon} {f.name}")
            st.markdown(f"**What:** {f.what}")
            st.markdown(f"**Downstream effect:** {f.effect}")
            st.markdown(f"**Vigil capability tested:** {' · '.join(f.tests)}")
            st.markdown(f"**Expected in Vigil:** {f.expected}")
            if f.caveat:
                st.warning(f"⚠️ {f.caveat}", icon="⚠️")

        with ctrl:
            # target
            if f.target_locked or len(f.target_choices) <= 1:
                target = f.target_default
                st.text_input("target", target, disabled=True, key=f"t_{f.id}")
            else:
                target = st.selectbox("target", f.target_choices,
                                      index=f.target_choices.index(f.target_default), key=f"t_{f.id}")
            # param
            value = None
            if f.control == "slider":
                value = st.slider(f"{f.param_label} ({f.param_unit})", float(f.param_min), float(f.param_max),
                                  float(f.param_default), float(f.param_step), key=f"v_{f.id}")
            st.code(f.preview(target, value), language="bash")

            b1, b2 = st.columns(2)
            if b1.button("💉 Inject", key=f"inj_{f.id}", use_container_width=True):
                with st.spinner("injecting…"):
                    r = faults.inject(f.id, target, value)
                st.session_state[f"res_{f.id}"] = (r.ok, f"INJECT · {r.summary()[:140]}")
            heal_disabled = f.heal_fn is None
            if b2.button("🩹 Heal", key=f"heal_{f.id}", use_container_width=True, disabled=heal_disabled):
                with st.spinner("healing…"):
                    r = faults.heal(f.id, target)
                st.session_state[f"res_{f.id}"] = (r.ok, f"HEAL · {r.summary()[:140]}")

            if f"res_{f.id}" in st.session_state:
                ok, msg = st.session_state[f"res_{f.id}"]
                (st.success if ok else st.error)(msg)


with tab_panel:
    st.caption("Grouped by failure class. Pick a target, set the magnitude, then **Inject**. **Heal** reverses it.")
    for cat in faults.CATEGORIES:
        cat_faults = [f for f in faults.CATALOG if f.category == cat]
        if not cat_faults:
            continue
        st.subheader(cat)
        for f in cat_faults:
            render_fault(f)


# ============================ SCENARIOS =====================================
with tab_scen:
    st.caption("One button arms a multi-fault, production-like incident that exercises a span of Vigil capabilities. "
               "After launching, watch the **Vigil Live** tab. **Heal** (or sidebar HEAL ALL) reverses it.")
    for s in scenarios.SCENARIOS:
        with st.container(border=True):
            st.markdown(f"### {s.icon} {s.name}")
            st.markdown(s.story)
            st.markdown("**Capabilities exercised:** " + " · ".join(f"`{c}`" for c in s.capabilities))
            st.info(f"⏱️ **Timeline:** {s.timeline}")
            steps_txt = ", ".join(
                f"{st_.fault_id}→{st_.target}" + (f"({int(st_.value)})" if st_.value else "")
                for st_ in s.steps
            ) or (f"oscillate latency on {s.oscillate['target']}" if s.oscillate else "")
            st.caption(f"Steps: {steps_txt}")
            b1, b2, _ = st.columns([1, 1, 3])
            if b1.button("🚀 Launch", key=f"launch_{s.id}", type="primary", use_container_width=True):
                with st.spinner("launching scenario…"):
                    res = scenarios.launch(s.id)
                st.success(f"Launched **{s.name}** ({len(res)} immediate actions). Watch Vigil Live.")
                for label, r in res:
                    (st.write if r.ok else st.error)(f"{'✓' if r.ok else '✗'} {label}: {r.summary()[:80]}")
            if b2.button("🩹 Heal", key=f"sheal_{s.id}", use_container_width=True):
                with st.spinner("healing…"):
                    res = scenarios.heal(s.id)
                st.success(f"Healed **{s.name}**.")


# ============================ VIGIL LIVE ====================================
with tab_vigil:
    cols = st.columns([1, 3])
    if cols[0].button("🔄 Refresh", use_container_width=True):
        _refresh()
    cols[1].caption("How Vigil currently sees the cluster (reads obsd /api). Inject a fault, then refresh.")

    snap = _snap(vigil.CFG.base)
    if not snap["ready"]:
        st.error("obsd is not reachable — start it on the configured URL.")
    else:
        m = st.columns(4)
        m[0].metric("Active findings", len(snap["active_findings"]))
        m[1].metric("Forecast cards", len(snap["forecast_cards"]))
        m[2].metric("Cross-service cascade", "● active" if snap["cross_service_active"] else "○ idle")
        m[3].metric("Onsets (C2)", snap["onset_count"])

        st.subheader("🔬 Detection — active findings (MEASURED)")
        if snap["active_findings"]:
            st.dataframe(pd.DataFrame(snap["active_findings"]), use_container_width=True, hide_index=True)
        else:
            st.caption("none firing right now.")

        st.subheader("🔮 Forecast — early warnings (PROJECTED)")
        fc = snap["forecast_cards"]
        if fc:
            rows = [{
                "entity": c.get("entityCei", "")[-42:], "metric": c.get("metric"),
                "bar": c.get("barValue"), "crossAt": c.get("crossAt"),
                "leadSeconds": c.get("timeToCrossSeconds"), "band": c.get("confidence"),
                "precursor": ",".join(c.get("precursorPhenomena", [])),
            } for c in fc]
            st.dataframe(pd.DataFrame(rows), use_container_width=True, hide_index=True)
        else:
            st.caption("no early-warning card right now (a series must be approaching its bar).")

        st.subheader("🔗 Cross-service cascade (MEASURED ⋈ AUTHORED)")
        ch = snap["cross_service_chain"]
        if snap["cross_service_active"] and ch:
            st.markdown(f"**Most-upstream degraded node:** `{ch.get('most_upstream_degraded_node')}`")
            for link in ch.get("chain", []):
                st.markdown(f"- degraded **{link.get('degraded')}** → impacted **{link.get('impacted')}**  "
                            f"·  _{link.get('edge_class')}_")
                st.caption(f"  authored why ({link.get('why_class')}): {link.get('why','')[:160]}…")
        else:
            st.caption("no cross-service cascade firing.")

        st.subheader("🧾 Incident memory (recurrence)")
        inc = snap["incidents"]
        if inc:
            st.dataframe(pd.DataFrame([
                {"phenomenon": i.get("phenomenon"), "recurrence": i.get("recurrenceCount"),
                 "summary": i.get("summary", "")[:60]} for i in inc
            ]), use_container_width=True, hide_index=True)
        else:
            st.caption("no incidents recorded yet.")


# ============================ CLUSTER =======================================
with tab_cluster:
    if st.button("🔄 Refresh cluster", use_container_width=False):
        _refresh()
    st.subheader("Pods")
    if pods:
        st.dataframe(pd.DataFrame(pods), use_container_width=True, hide_index=True)
    else:
        st.error("could not list pods — check context/namespace.")

    st.subheader("Sim control state (armed faults per pod)")
    rows = []
    for dep in [faults.SIM_GATEWAY, faults.SIM_STREAM, faults.SIM_ANALYZER]:
        s = _ctl(dep, kube.CFG.context, kube.CFG.namespace)
        rows.append({"workload": dep, **{k: v for k, v in s.items() if not k.startswith("_")}})
    st.dataframe(pd.DataFrame(rows), use_container_width=True, hide_index=True)

    st.subheader("Dependency graph (for reference)")
    st.code(
        "smart-sensors → opcua-gateway → edgenius-broker → stream-processor → genix-historian → asset-api\n"
        "pdm-analyzer → genix-historian , asset-registry        asset-api → asset-registry , genix-historian\n"
        "operations-dashboard → asset-api , genix-historian",
        language="text",
    )
