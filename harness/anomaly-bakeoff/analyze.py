"""Detector bake-off on the live capture: Vigil EWMA-CUSUM vs ADWIN vs Page-Hinkley,
with ruptures Binseg as the offline onset oracle. Scores live injection arms AND controlled
perturbations on real baseline noise. Prints tables + a determinism check.
"""
import sys
import time as _time
import numpy as np
import pandas as pd
import ruptures as rpt
from river import drift

import vigil_onset

RUN = "/tmp/vigil_exp/run.csv"
SCHED = "/tmp/vigil_exp/schedule.csv"
COUNTERS = ("_total",)  # metric suffixes that are cumulative counters -> rate


def is_counter(metric):
    return metric.endswith(COUNTERS)


def znorm(vals, k):
    """standardize a stream by its first-k baseline (mean/std) — realistic preprocessing for
    ADWIN/Page-Hinkley, which are NOT scale-invariant (CUSUM is, via its internal robust sigma)."""
    v = np.asarray(vals, dtype=float)
    k = max(8, min(k, len(v)))
    mu = v[:k].mean()
    sd = v[:k].std()
    if sd <= 0:
        sd = 1.0
    return (v - mu) / sd


def load():
    df = pd.read_csv(RUN)
    sch = pd.read_csv(SCHED)
    return df, sch


def series_for(df, pod_sub, metric):
    """Return (times, values) for the first pod matching pod_sub + metric, counters -> rate."""
    sub = df[(df.pod.str.contains(pod_sub)) & (df.metric == metric)]
    if sub.empty:
        return None, None
    sub = sub.sort_values("wall_ts")
    t = sub.wall_ts.to_numpy(dtype=float)
    v = sub.value.to_numpy(dtype=float)
    # dedupe identical timestamps
    _, idx = np.unique(t, return_index=True)
    t, v = t[idx], v[idx]
    if is_counter(metric):
        dt = np.diff(t)
        dv = np.diff(v)
        dt[dt == 0] = 1e9
        r = dv / dt
        return t[1:], np.clip(r, 0, None)
    return t, v


# ---------- detectors: each returns list of detection indices (0-based into values) ----------
def det_vigil(vals):
    on = vigil_onset.detect(list(map(float, vals)))
    return [o["index"] for o in on], on


def det_adwin(vals, delta=0.002):
    d = drift.ADWIN(delta=delta)
    hits = []
    for i, x in enumerate(vals):
        d.update(float(x))
        if d.drift_detected:
            hits.append(i)
    return hits


def det_ph(vals, delta=0.005, threshold=None):
    d = drift.PageHinkley(delta=delta) if threshold is None else drift.PageHinkley(delta=delta, threshold=threshold)
    hits = []
    for i, x in enumerate(vals):
        d.update(float(x))
        if d.drift_detected:
            hits.append(i)
    return hits


def oracle_cp(vals, lo, hi):
    """ruptures Binseg single-changepoint within [lo,hi] as the ground-truth onset index."""
    seg = np.asarray(vals[lo:hi], dtype=float).reshape(-1, 1)
    if len(seg) < 10:
        return None
    try:
        algo = rpt.Binseg(model="l2", min_size=3).fit(seg)
        bk = algo.predict(n_bkps=1)
        return lo + bk[0] if bk and bk[0] < len(seg) else None
    except Exception:
        return None


def idx_at_or_after(hits, t, ti):
    """first detection index whose TIME >= ti."""
    for h in hits:
        if t[h] >= ti:
            return h
    return None


def fmt_lat(h, t, t0):
    if h is None:
        return "MISS"
    return f"{t[h]-t0:+.0f}s"


def arm_window(t, t0, t1):
    return int(np.searchsorted(t, t0)), int(np.searchsorted(t, t1))


def run_live_arms(df, sch):
    print("\n" + "=" * 96)
    print("LIVE INJECTION ARMS  (real abb-genix series; ground truth = recorded /ctl inject time)")
    print("=" * 96)
    ev = {r.event + ":" + str(r.target): float(r.ts) for r in sch.itertuples()}
    rows = []

    def score(label, pod, metric, t0_key, end_key, expect):
        t, v = series_for(df, pod, metric)
        if t is None or len(v) < 20:
            rows.append((label, pod.split("-")[0] + "/" + metric[:22], "no-data", "", "", "", ""))
            return
        t0 = ev.get(t0_key)
        tend = ev.get(end_key, t[-1])
        if t0 is None:
            return
        nb = int(np.searchsorted(t, t0))  # baseline length (samples before inject)
        vn = znorm(v, nb)                  # normalized stream for ADWIN/PH (CUSUM stays raw, it's invariant)
        vg, _ = det_vigil(v)
        va = det_adwin(vn)
        vp = det_ph(vn)
        lo, hi = arm_window(t, t0, min(tend, t[-1]))
        orc = oracle_cp(v, max(0, lo - 6), min(len(v), hi + 6))
        hv = idx_at_or_after(vg, t, t0)
        ha = idx_at_or_after(va, t, t0)
        hp = idx_at_or_after(vp, t, t0)

        def loc(h):
            if h is None or orc is None:
                return ""
            return f"{h-orc:+d}smp"
        rows.append((label, f"{pod.split('-')[0]}/{metric.replace('container_','').replace('_bytes','')[:20]}",
                     f"CUSUM {fmt_lat(hv,t,t0)} {loc(hv)}",
                     f"ADWIN {fmt_lat(ha,t,t0)} {loc(ha)}",
                     f"PH {fmt_lat(hp,t,t0)} {loc(hp)}",
                     f"oracle@{'?' if orc is None else f'{t[orc]-t0:+.0f}s'}",
                     expect))

    score("GRADUAL-DRIFT", "pdm-analyzer", "container_memory_working_set_bytes",
          "inject:mem_leak", "heal:mem_leak", "both should fire; CUSUM sharper/earlier")
    score("ABRUPT-STEP", "stream-processor", "container_cpu_usage_seconds_total",
          "inject:cpu_burn", "heal:cpu_burn", "both should fire fast")
    score("ABRUPT-STEP", "stream-processor", "container_cpu_cfs_throttled_periods_total",
          "inject:cpu_burn", "heal:cpu_burn", "throttle step")
    score("TRANSIENT-TRAP", "stream-processor", "app_queue_depth",
          "inject:latency_transient", "heal:cpu_burn", "NEITHER should fire a SUSTAINED onset")
    score("GRADUAL-DRIFT", "pdm-analyzer", "container_memory_cache",
          "inject:mem_leak", "heal:mem_leak", "secondary leak corroboration")

    w = pd.DataFrame(rows, columns=["arm", "series", "Vigil-CUSUM", "ADWIN", "Page-Hinkley", "oracle", "expectation"])
    pd.set_option("display.width", 200, "display.max_colwidth", 40)
    print(w.to_string(index=False))


def run_fp_stationary(df, sch):
    print("\n" + "=" * 96)
    print("FALSE-POSITIVES on STATIONARY controls (untouched pods over the whole run)")
    print("  a detector is robust if it fires ~0 sustained onsets here")
    print("=" * 96)
    rows = []
    for pod in ["smart-sensors", "asset-api", "asset-registry", "edgenius-broker", "opcua-gateway"]:
        t, v = series_for(df, pod, "container_memory_working_set_bytes")
        if t is None or len(v) < 20:
            continue
        vg, _ = det_vigil(v)
        va = det_adwin(v)
        vp = det_ph(v)
        rows.append((pod, len(v), len(vg), len(va), len(vp)))
    print(pd.DataFrame(rows, columns=["control-pod", "samples", "CUSUM-fires", "ADWIN-fires", "PH-fires"]).to_string(index=False))


def run_controlled(df):
    print("\n" + "=" * 96)
    print("CONTROLLED PERTURBATIONS on a REAL baseline series (smart-sensors memory noise)")
    print("  isolates the spike-vs-drift distinction with a known ground-truth changepoint")
    print("=" * 96)
    # pick the QUIETEST real pod (fewest native onsets) as the clean baseline
    base, t = None, None
    best = 1e9
    for pod in ["edgenius-broker", "opcua-gateway", "genix-datalake", "smart-sensors", "asset-registry"]:
        tt, vv = series_for(df, pod, "container_memory_working_set_bytes")
        if vv is None or len(vv) < 50:
            continue
        native = len(det_vigil(vv)[0]) + len(det_ph(znorm(vv, len(vv) // 3)))
        if native < best:
            best, base, t = native, vv, tt
    if base is None:
        print("  (insufficient baseline data)")
        return
    print(f"  baseline pod chosen (quietest, {best} native onsets), {len(base)} samples")
    base = np.asarray(base, dtype=float)
    # standardize to mean 100, sd = real baseline sd (preserve real noise shape); add controlled signal
    mu, sd = base.mean(), max(base.std(), 1.0)
    z = (base - mu) / sd  # real noise, unit-ish
    n = len(z)
    cp = n // 2
    sd_real = float(np.std(z[:cp])) or 1.0

    def make(kind, amp):
        x = z.copy()
        if kind == "spike":          # single-sample transient at cp (returns to baseline)
            x[cp] += amp
        elif kind == "spike3":       # 3-sample transient then back
            x[cp:cp + 3] += amp
        elif kind == "step":         # sustained level shift from cp onward
            x[cp:] += amp
        elif kind == "ramp":         # gradual linear creep from cp
            x[cp:] += np.linspace(0, amp, n - cp)
        return x

    cases = [
        ("transient-spike(1smp,+8sd)", make("spike", 8 * sd_real), "NEITHER should fire (transient)"),
        ("transient-spike(3smp,+8sd)", make("spike3", 8 * sd_real), "NEITHER (settles back)"),
        ("sustained-step(+8sd)", make("step", 8 * sd_real), "BOTH fire; compare latency/localization"),
        ("sustained-step(+3sd)", make("step", 3 * sd_real), "small step: CUSUM edge expected"),
        ("gradual-ramp(+6sd)", make("ramp", 6 * sd_real), "creep: CUSUM rescue vs ADWIN lag"),
        ("pure-noise(no signal)", z.copy(), "BOTH ~0 false onsets"),
    ]
    rows = []
    for name, x, expect in cases:
        vg, _ = det_vigil(x)
        va = det_adwin(x)
        vp = det_ph(x)

        def first_after(hits):
            for h in hits:
                if h >= cp:
                    return h
            return None
        hv, ha, hp = first_after(vg), first_after(va), first_after(vp)

        def d(h):
            return "MISS" if h is None else f"+{h-cp}smp"
        # false fires BEFORE the changepoint (stationary part) = pure FP
        fp_v = len([h for h in vg if h < cp])
        fp_a = len([h for h in va if h < cp])
        fp_p = len([h for h in vp if h < cp])
        rows.append((name, d(hv), d(ha), d(hp), f"{fp_v}/{fp_a}/{fp_p}", expect))
    print(pd.DataFrame(rows, columns=["perturbation@cp", "CUSUM", "ADWIN", "Page-Hinkley", "FP(C/A/P) pre-cp", "expectation"]).to_string(index=False))


def run_determinism(df):
    t, v = series_for(df, "pdm-analyzer", "container_memory_working_set_bytes")
    if v is None:
        return
    a1, a2 = det_vigil(v)[0], det_vigil(v)[0]
    b1, b2 = det_adwin(v), det_adwin(v)
    print("\n" + "=" * 96)
    print("DETERMINISM (re-run on identical series ⇒ byte-identical detections — the replay surrogate)")
    print("=" * 96)
    print(f"  Vigil-CUSUM: run1==run2 -> {a1 == a2}   ADWIN: run1==run2 -> {b1 == b2}")


def run_compute(df):
    t, v = series_for(df, "pdm-analyzer", "container_memory_working_set_bytes")
    if v is None or len(v) < 20:
        return
    big = np.tile(v, 50)  # ~simulate a longer stream
    for name, fn in [("Vigil-CUSUM", lambda: det_vigil(big)), ("ADWIN", lambda: det_adwin(big)), ("Page-Hinkley", lambda: det_ph(big))]:
        s = _time.perf_counter()
        fn()
        print(f"  {name:14s} {len(big)} samples in {(_time.perf_counter()-s)*1000:.1f} ms")


def main():
    df, sch = load()
    print(f"loaded {len(df)} rows; {df.pod.nunique()} pods; metrics={sorted(df.metric.unique())}")
    print("\nschedule (ground truth):")
    print(sch.to_string(index=False))
    run_live_arms(df, sch)
    run_fp_stationary(df, sch)
    run_controlled(df)
    print("\n" + "=" * 96)
    print("COMPUTE COST")
    print("=" * 96)
    run_compute(df)
    run_determinism(df)


if __name__ == "__main__":
    main()
