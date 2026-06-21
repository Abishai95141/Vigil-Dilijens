"""Faithful Python port of Vigil's onset detector — obsd/internal/onset/onset.go.

EWMA-residual CUSUM with sustained-shift (MinZ) confirmation. Mirrors the Go line-for-line
so the bake-off compares the EXACT shipped logic, not an approximation.
Defaults: Alpha=0.10, K=0.5, H=4.0, Warmup=12, MinZ=3.0 (onset.go:74 DefaultParams).
"""
import math

MAX_STEP_Z = 99.9  # onset.go:54


def _median(x):
    if not x:
        return 0.0
    cp = sorted(x)
    n = len(cp)
    if n % 2 == 1:
        return cp[n // 2]
    return 0.5 * (cp[n // 2 - 1] + cp[n // 2])


def _robust_sigma(x):  # onset.go:200 — 1.4826*MAD, floored at 1e-9
    med = _median(x)
    dev = [abs(v - med) for v in x]
    return max(1.4826 * _median(dev), 1e-9)


def _ewma(x, alpha):  # onset.go:186
    out = [0.0] * len(x)
    if not x:
        return out
    out[0] = x[0]
    for i in range(1, len(x)):
        out[i] = alpha * x[i] + (1 - alpha) * out[i - 1]
    return out


def _sig_prefix_len(n, warmup):  # onset.go:172 — len/3 clamped to [2*warmup, 60]
    sigN = n // 3
    lo = 2 * warmup
    if sigN < lo:
        sigN = lo
    if sigN > 60:
        sigN = 60
    if sigN > n:
        sigN = n
    return sigN


def _residual_z(xs, alpha, sigN):  # onset.go:156
    base = _ewma(xs, alpha)
    resid = [0.0] * len(xs)
    for i in range(1, len(xs)):
        resid[i] = xs[i] - base[i - 1]  # predict x[i] from baseline through i-1
    sigma = _robust_sigma(resid[:sigN])
    return [r / sigma for r in resid]


def _window_median(xs, lo, hi):  # onset.go:223
    if lo < 0:
        lo = 0
    if hi > len(xs):
        hi = len(xs)
    if lo >= hi:
        return xs[lo] if lo < len(xs) else 0.0
    return _median(xs[lo:hi])


def _backtrack(z, alarm, sign):  # onset.go:242 — walk back to where the step began
    i = alarm
    while i > 0 and sign * z[i - 1] > 0.5:
        i -= 1
    return i


def detect(xs, times=None, alpha=0.10, K=0.5, H=4.0, warmup=12, min_z=3.0):
    """Return onsets as dicts {index, alarm_index, time, direction, stepZ}. xs = finite floats."""
    if H <= 0:
        return []
    n = len(xs)
    if n < warmup + 4:  # onset.go:85
        return []
    a = alpha if (0 < alpha <= 1) else 0.15
    sigN = _sig_prefix_len(n, warmup)
    z = _residual_z(xs, a, sigN)
    signal_sigma = _robust_sigma(xs[:sigN])
    out = []
    gp = gn = 0.0
    armed = warmup
    for i in range(warmup, len(z)):  # onset.go:99
        gp = max(0.0, gp + z[i] - K)
        gn = max(0.0, gn - z[i] - K)
        if gp > H and i >= armed:
            o = _confirm(xs, times, z, signal_sigma, i, +1, warmup, min_z)
            if o:
                out.append(o)
            gp = gn = 0.0
            armed = i + 3
        elif gn > H and i >= armed:
            o = _confirm(xs, times, z, signal_sigma, i, -1, warmup, min_z)
            if o:
                out.append(o)
            gp = gn = 0.0
            armed = i + 3
    return out


def _confirm(xs, times, z, signal_sigma, alarm, sign, warmup, min_z):  # onset.go:123
    start = _backtrack(z, alarm, sign)
    w = warmup // 2
    if w < 4:
        w = 4
    pre = _window_median(xs, start - w, start)
    post = _window_median(xs, alarm, alarm + w)
    shift = post - pre
    step_z = min(abs(shift) / signal_sigma, MAX_STEP_Z)
    if step_z < min_z:  # transient — settled back to baseline, dropped
        return None
    if (sign > 0) != (shift > 0):  # alarm side disagrees with realized shift
        return None
    direction = "up" if shift > 0 else "down"
    return {
        "index": start,
        "alarm_index": alarm,
        "time": (times[start] if times is not None else start),
        "direction": direction,
        "stepZ": round(step_z, 4),
    }


if __name__ == "__main__":
    # self-test: a clean +10 step at i=60 on N(0,1) baseline must fire 'up' near 60
    import random
    random.seed(1)
    xs = [random.gauss(0, 1) + (0.0 if i < 60 else 10.0) for i in range(120)]
    print("onsets:", detect(xs))
