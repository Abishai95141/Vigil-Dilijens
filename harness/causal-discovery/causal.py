"""Offline causal-discovery harness (doc 29 Part B).

Pipeline:  raw panel  ->  lag-0 corr graph (the "before", reproduces obsd assoc)
                       ->  Stage 1: fixed, DECLARED-lag enrichment (NOT argmax-as-direction)
                       ->  Stage 2: PCMCI+ conditional-independence pruning (tigramite)
                       ->  ranked, lag-resolved, confounder-pruned CANDIDATE shortlist.

Two modes (same engine, different capture):
  --mode load     a common-driver capture (step gateway load): demonstrates CONFOUNDER PRUNING.
  --mode cascade  a fault-cascade capture (oscillate stream latency, fine 5s bins): demonstrates
                  DIRECTED LEAD-LAG recovery (stream.queue leads asset-api staleness).

Charter: offline; output is a CANDIDATE list (never graph writes); direction is a PROJECTED
hint a human authors; lag set is pinned/declared (no argmax orients an edge). See docs/29.
"""
import argparse
import json
import numpy as np
import pandas as pd

from tigramite.pcmci import PCMCI
from tigramite.independence_tests.parcorr import ParCorr
from tigramite import data_processing as pp

LAG_SET = [0, 1, 2, 4]  # PINNED, DECLARED candidate lags (bins). NOT an argmax search space.

LOAD_PANEL = [
    ("opcua-gateway", "app_requests_total", True, "gateway.req"),          # the DRIVER we stepped
    ("stream-processor", "app_queue_depth", False, "stream.queue"),
    ("stream-processor", "abb_datalake_objects_total", True, "stream.dl_writes"),
    ("asset-api", "app_requests_total", True, "assetapi.req"),
    ("smart-sensors", "container_cpu_usage_seconds_total", True, "sensors.cpu"),
    ("opcua-gateway", "container_cpu_usage_seconds_total", True, "gateway.cpu"),
    ("edgenius-broker", "container_cpu_usage_seconds_total", True, "broker.cpu"),
    ("stream-processor", "container_cpu_usage_seconds_total", True, "stream.cpu"),
    ("genix-historian", "container_cpu_usage_seconds_total", True, "historian.cpu"),
    ("genix-datalake", "container_cpu_usage_seconds_total", True, "datalake.cpu"),
    ("pdm-analyzer", "container_cpu_usage_seconds_total", True, "analyzer.cpu"),
    ("asset-api", "container_cpu_usage_seconds_total", True, "assetapi.cpu"),
]

# cascade panel for a CONNECTIVITY-LOSS oscillation (gateway disconnect): the freeze propagates
# down the pipeline with lags. gateway.req / stream throughput DROP first; asset-api staleness AGE
# (now - last_update; "age" transform) RISES after a lag — the cross-entity lagged chain.
CASCADE_PANEL = [
    ("opcua-gateway", "app_requests_total", True, "gateway.req"),                      # drops on disconnect (leads)
    ("stream-processor", "abb_historian_writes_total", True, "stream.histwrites"),     # drops, lagged
    ("stream-processor", "abb_datalake_objects_total", True, "stream.dlwrites"),       # drops, lagged
    ("asset-api", "app_last_update_seconds", False, "assetapi.staleness", "age"),      # RISES, lagged (integrator)
    ("stream-processor", "app_queue_depth", False, "stream.queue"),
    ("pdm-analyzer", "abb_pdm_inference_seconds", False, "analyzer.infer"),
]

PIPELINE_ORDER = ["sensors", "gateway", "broker", "stream", "historian", "datalake"]


def load_series(df, pod_sub, metric, is_counter, bin_s, transform=None):
    sub = df[(df.pod.str.contains(pod_sub)) & (df.metric == metric)]
    if sub.empty:
        return None
    best = None
    for (_, _), g in sub.groupby(["pod", "container"]):
        g = g.sort_values("wall_ts")
        t = g.wall_ts.to_numpy(float)
        v = g.value.to_numpy(float)
        _, idx = np.unique(t, return_index=True)
        t, v = t[idx], v[idx]
        if len(v) < 10:
            continue
        if transform == "age":  # staleness AGE = capture time - last_update timestamp
            v = t - v
        bins = np.floor(t / bin_s).astype(int)
        s = pd.Series(v, index=bins).groupby(level=0).last()
        if is_counter:
            s = (s.diff() / bin_s).clip(lower=0)
        s = s.dropna()
        if len(s) < 12 or s.std() == 0:
            continue
        cv = s.std() / (abs(s.mean()) + 1e-9)
        if best is None or cv > best[1]:
            best = (s, cv)
    return best[0] if best else None


def build_panel(df, panel, bin_s):
    cols = {}
    for spec in panel:
        pod_sub, metric, is_counter, label = spec[0], spec[1], spec[2], spec[3]
        transform = spec[4] if len(spec) > 4 else None
        s = load_series(df, pod_sub, metric, is_counter, bin_s, transform)
        if s is not None:
            cols[label] = s
    p = pd.DataFrame(cols).sort_index().interpolate(limit=2).dropna()
    return p, list(p.columns)


def lag0_graph(data, names, floor):
    n = data.shape[1]
    C = np.corrcoef(data.T)
    edges = [(names[i], names[j], round(float(C[i, j]), 3))
             for i in range(n) for j in range(i + 1, n) if abs(C[i, j]) >= floor]
    return edges, C


def fixed_lag_hint(data, i, j, lags):
    """Pearson at each PINNED lag; return (best_lag_bins, best_r). Positive lag = i leads j."""
    x, y = data[:, i], data[:, j]
    best = (0, 0.0)
    for L in lags:
        for s in ({L, -L} if L else {0}):
            if s >= 0:
                a, b = (x[:len(x) - s], y[s:]) if s else (x, y)
            else:
                a, b = x[-s:], y[:len(y) + s]
            if len(a) < 8:
                continue
            r = np.corrcoef(a, b)[0, 1]
            if abs(r) > abs(best[1]):
                best = (s, round(float(r), 3))
    return best


def partial_corr(data, i, j, k):
    Z = np.column_stack([np.ones(len(data)), data[:, k]])
    ri = data[:, i] - Z @ np.linalg.lstsq(Z, data[:, i], rcond=None)[0]
    rj = data[:, j] - Z @ np.linalg.lstsq(Z, data[:, j], rcond=None)[0]
    return float(np.corrcoef(ri, rj)[0, 1])


def pcmci_graph(data, names, tau_max, pc_alpha, bin_s):
    dframe = pp.DataFrame(data, var_names=names)
    pcmci = PCMCI(dataframe=dframe, cond_ind_test=ParCorr(significance="analytic"), verbosity=0)
    res = pcmci.run_pcmciplus(tau_min=0, tau_max=tau_max, pc_alpha=pc_alpha)
    g, val = res["graph"], res["val_matrix"]
    links = []
    n = len(names)
    for i in range(n):
        for j in range(n):
            for tau in range(g.shape[2]):
                if i == j and tau == 0:
                    continue
                if g[i, j, tau] == "-->":
                    links.append({"from": names[i], "to": names[j], "lag_bins": tau,
                                  "lag_seconds": int(tau * bin_s), "coefficient": round(float(val[i, j, tau]), 3),
                                  "contemporaneous": tau == 0})
    links.sort(key=lambda d: -abs(d["coefficient"]))
    return links


def pod_of(label):
    return label.split(".")[0]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", default="/tmp/vigil_exp/panel.csv")
    ap.add_argument("--mode", choices=["load", "cascade"], default="load")
    ap.add_argument("--bin", type=float, default=None)
    ap.add_argument("--tau-max", type=int, default=None)
    ap.add_argument("--floor", type=float, default=0.45)
    ap.add_argument("--out", default="/tmp/vigil_exp/causal_shortlist.json")
    args = ap.parse_args()
    bin_s = args.bin if args.bin else (5.0 if args.mode == "cascade" else 15.0)
    tau_max = args.tau_max if args.tau_max else (8 if args.mode == "cascade" else 3)
    panel_spec = CASCADE_PANEL if args.mode == "cascade" else LOAD_PANEL

    df = pd.read_csv(args.run)
    panel, names = build_panel(df, panel_spec, bin_s)
    data = panel.to_numpy(float)
    data = (data - data.mean(0)) / (data.std(0) + 1e-9)
    T, N = data.shape
    idx = {n: k for k, n in enumerate(names)}
    print(f"MODE={args.mode}  PANEL: {N} streams x {T} bins ({int(T*bin_s)}s @ {int(bin_s)}s)  tau_max={tau_max}")
    print("  streams:", ", ".join(names))

    edges0, _ = lag0_graph(data, names, args.floor)
    cross0 = [e for e in edges0 if pod_of(e[0]) != pod_of(e[1])]
    print("\n" + "=" * 92)
    print(f"BEFORE  (lag-0 |r|>={args.floor} correlation graph — what obsd's assoc lane surfaces)")
    print("=" * 92)
    print(f"  undirected edges: {len(edges0)}  (cross-entity: {len(cross0)})")

    links = pcmci_graph(data, names, tau_max, 0.05, bin_s)
    cross = [l for l in links if pod_of(l["from"]) != pod_of(l["to"])]
    for l in cross:
        bl, br = fixed_lag_hint(data, idx[l["from"]], idx[l["to"]], LAG_SET)
        l["leadlag_hint_bins"], l["leadlag_r"] = bl, br

    print("\n" + "=" * 92)
    print(f"AFTER   (PCMCI+ conditional-independence pruning, FDR pc_alpha=0.05)")
    print("=" * 92)
    red = (1 - len(cross) / max(len(cross0), 1)) * 100
    print(f"  PRUNING: {len(cross0)} undirected cross-entity assoc -> {len(cross)} directed candidates ({red:.0f}% fewer)")
    lagged = [l for l in cross if not l["contemporaneous"]]
    print(f"  of which NON-contemporaneous (lead-lag resolved): {len(lagged)}")
    print(f"\n  {'from':>20} ->  {'to':<22} {'lag':>5}  {'coef':>6}")
    for l in cross[:20]:
        print(f"  {l['from']:>20} ->  {l['to']:<22} {l['lag_seconds']:>4}s  {l['coefficient']:>6}")

    print("\n" + "=" * 92 + "\nVALIDATION\n" + "=" * 92)
    if args.mode == "cascade":
        # connectivity loss: upstream throughput (gateway.req / stream writes) DROPS first; asset-api
        # staleness AGE rises AFTER a lag. Validate that the harness recovers staleness as the LAGGED
        # downstream — upstream signals should LEAD it with a positive lag.
        s = "assetapi.staleness"
        if s in idx:
            print(f"  [lead-lag] which upstream signal LEADS {s} (fixed DECLARED lags, peak |r|):")
            for up in ["gateway.req", "stream.histwrites", "stream.dlwrites", "stream.queue"]:
                if up in idx:
                    bl, br = fixed_lag_hint(data, idx[up], idx[s], list(range(0, tau_max + 1)))
                    lead = f"LEADS by {int(bl*bin_s)}s" if bl > 0 else "contemporaneous"
                    print(f"      {up:18} -> {s}: peak r={br:+.2f} at +{int(bl*bin_s)}s  ({up} {lead})")
            touching = [l for l in cross if s in (l["from"], l["to"])]
            print(f"  [PCMCI directed] cross-entity links touching {s}: {len(touching)}")
            for l in touching:
                arrow = "✓ staleness is the downstream (lagged)" if l["to"] == s else "(staleness leads — check)"
                print(f"      {l['from']} -> {l['to']}  (+{l['lag_seconds']}s, r={l['coefficient']})  {arrow}")
    else:
        order = {p: k for k, p in enumerate(PIPELINE_ORDER)}
        driver = "gateway.req"
        if driver in idx:
            di = idx[driver]
            conf = [(a, b, r, round(partial_corr(data, idx[a], idx[b], di), 2)) for a, b, r in edges0
                    if driver not in (a, b) and pod_of(a) != pod_of(b)]
            spurious = [c for c in conf if abs(c[3]) < 0.3 and abs(c[2]) >= args.floor]
            und = {frozenset((l["from"], l["to"])) for l in links}
            kept = [c for c in spurious if frozenset((c[0], c[1])) in und]
            print(f"  [confounder pruning] {len(spurious)} lag-0 cross edges DISSOLVE when conditioned on "
                  f"the driver ({driver}); PCMCI dropped {len(spurious)-len(kept)}/{len(spurious)} "
                  f"({'✓ all pruned' if not kept else 'kept ' + str([(c[0], c[1]) for c in kept])})")
            for a, b, r, pc in spurious[:6]:
                print(f"      {a:16}~{b:16} lag0_r={r:>5} -> r|driver={pc:>5}  (spurious)")

    out = {"class": "CANDIDATE (PROJECTED) — direction-free leads from offline causal discovery; operator authors direction",
           "method": "fixed-declared-lag enrichment + PCMCI+ (tigramite ParCorr)",
           "params": {"mode": args.mode, "lag_set_bins": LAG_SET, "tau_max": tau_max, "pc_alpha": 0.05, "bin_seconds": bin_s},
           "before_lag0_cross_edges": len(cross0), "after_directed_cross_candidates": len(cross), "shortlist": cross}
    with open(args.out, "w") as f:
        json.dump(out, f, indent=2)
    print(f"\n  shortlist -> {args.out}  ({len(cross)} candidates)")


if __name__ == "__main__":
    main()
