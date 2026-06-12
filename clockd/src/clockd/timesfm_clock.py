"""The TimesFM 2.5 reference adapter (doc 09 §3.1; doc 14 A15).

Pins BOTH the package version and the checkpoint revision — the PyPI package
version (2.0.x) deliberately differs from the checkpoint version (2.5), the
exact confusion A15 warns about. Validated 2026-06-12 on CPU (probe results in
artifacts/phase2-plan.md §6): ~290 ms/forecast at 240-pt context, horizon 64;
flat input → flat point with a non-collapsing band; quantile head emits
mean + deciles q10…q90 with crossing fixed.

Charter notes:
 - Input is a bare float series; nothing semantic reaches this module.
 - Requested quantiles outside [0.1, 0.9] are REFUSED, never extrapolated —
   the model has no tail estimate and this adapter will not fabricate one.
 - Covariates are refused until Phase 4 (doc 09 M6) admits them with rules.

Heavy deps (timesfm, torch) live behind the `model` extra; imports are lazy so
the base package never pays for them.
"""

from __future__ import annotations

from clockd.forecast import ForecastResult

# doc 14 A15: pin the package version AND the checkpoint revision. These are
# TWO DIFFERENT VERSION LINES, not a mismatch: the MODEL is TimesFM 2.5 (the
# checkpoint below, loaded via TimesFM_2p5_200M_torch); the PyPI package
# `timesfm` is the SDK that ships that class, and its own line tops out at
# 2.0.x (2.0.0, 2026-06-05, is the release that ADDED 2.5-model support —
# verified against the PyPI index 2026-06-12; no `timesfm==2.5` exists).
PACKAGE_PIN = "timesfm==2.0.1"
CHECKPOINT = "google/timesfm-2.5-200m-pytorch"
CHECKPOINT_REVISION = "1d952420fba87f3c6dee4f240de0f1a0fbc790e3"

# Compile-time envelope (validated in the probe). max_horizon bounds a single
# inference; the layer's emission horizon (doc 09 §3.7) is enforced upstream.
MAX_CONTEXT = 1024
MAX_HORIZON = 256

# The model's quantile head: column 0 is the mean, columns 1..9 are deciles.
_DECILES = [0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9]


class TimesFMClock:
    """TimesFM 2.5 behind the swap contract (same signature as StubClock)."""

    def __init__(self) -> None:
        self._model = None
        self._failed = False
        self._load()

    def _load(self) -> None:
        try:
            import timesfm

            model = timesfm.TimesFM_2p5_200M_torch.from_pretrained(
                CHECKPOINT, revision=CHECKPOINT_REVISION
            )
            model.compile(
                timesfm.ForecastConfig(
                    max_context=MAX_CONTEXT,
                    max_horizon=MAX_HORIZON,
                    normalize_inputs=True,
                    use_continuous_quantile_head=True,
                    force_flip_invariance=True,
                    infer_is_positive=True,
                    fix_quantile_crossing=True,
                )
            )
            self._model = model
        except Exception:
            # Health surfaces not-ready; the caller's Tier-B panel says
            # "forecasting degraded" (doc 14 A13). Detection is untouched.
            self._failed = True
            raise

    def ready(self) -> bool:
        return self._model is not None

    def forecast(
        self,
        series: list[float],
        horizon: int,
        quantiles: list[float],
        covariate_future: list[float] | None = None,
    ) -> ForecastResult:
        if self._model is None:
            raise RuntimeError("model not loaded")
        if covariate_future:
            # Phase 4 (doc 09 M6) admits known-future covariates with rules;
            # until then this adapter refuses rather than silently ignoring.
            raise NotImplementedError("covariates land in Phase 4 (doc 09 M6)")
        if horizon <= 0 or horizon > MAX_HORIZON:
            raise ValueError(f"horizon must be in (0, {MAX_HORIZON}], got {horizon}")
        for q in quantiles:
            if q < _DECILES[0] or q > _DECILES[-1]:
                raise ValueError(
                    f"quantile {q} outside the decile head [0.1, 0.9] — "
                    "refused, never extrapolated"
                )
        ctx = [float(v) for v in series[-MAX_CONTEXT:]]

        point_arr, quant_arr = self._model.forecast(horizon=horizon, inputs=[ctx])
        point = [float(v) for v in point_arr[0]]
        # quant_arr[0] has shape (horizon, 1 + len(_DECILES)): mean + q10..q90.
        rows = quant_arr[0]

        quantile_values: list[float] = []
        for q in quantiles:
            for h in range(horizon):
                quantile_values.append(_interp_decile(rows[h], q))
        return ForecastResult(
            point=point,
            quantile_values=quantile_values,
            num_quantiles=len(quantiles),
            horizon=horizon,
        )


def _interp_decile(row, q: float) -> float:
    """Linearly interpolate a requested quantile level onto the decile columns
    (row layout: [mean, q10, ..., q90]). Crossing is fixed by the model config,
    so the decile values are monotone and interpolation stays monotone."""
    for i in range(len(_DECILES) - 1):
        lo, hi = _DECILES[i], _DECILES[i + 1]
        if lo <= q <= hi:
            v_lo, v_hi = float(row[1 + i]), float(row[2 + i])
            if hi == lo:
                return v_lo
            t = (q - lo) / (hi - lo)
            return v_lo + t * (v_hi - v_lo)
    raise ValueError(f"quantile {q} outside [0.1, 0.9]")
