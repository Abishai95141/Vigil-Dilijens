# SKILL: TimesFM clock (vendored, Phase 2)

Vendored agent guidance for the forecasting model (techstack §2.3, §9). Placeholder
until Phase 2 (doc 09 M1); fill in when the real adapter is built.

## Model
- **TimesFM 2.5**, checkpoint `google/timesfm-2.5-200m-pytorch` pinned by HF revision
  hash; the `timesfm` package pinned by exact version/commit. **Pin BOTH** — the PyPI
  package version numbering differs from the checkpoint's (doc 14 A15).
- Load via `TimesFM_2p5_200M_torch.from_pretrained`. `ForecastConfig` for our envelope:
  `max_context` up to 16,384, `max_horizon` ~1,024, `normalize_inputs=True` (the
  scale-independence the clock contract assumes), continuous quantile head on,
  quantile-crossing fix on.

## Runtime
- PyTorch, **CPU-sufficient for dev** (200M params); `torch_compile` on; GPU optional.
- Warm the model at process start; batch Tier-B targets per cycle within the
  invocation budget (doc 06 §3.3, `tier_b_budget_per_cycle`).

## Contract (do not violate)
- Input is bare floats only — no labels, names, units, or graph structure (doc 01 §4).
  The proto enforces no-strings; keep it that way.
- Output: point forecast + quantile band. The band **never collapses to a line**.
- Swap-readiness is a test: the StubClock must pass the same conformance suite as the
  real model (`clockd/src/clockd/forecast.py`).

## Later
- XReg covariate pathway (Phase 4); LoRA/PEFT examples exist if ever needed; ICF stays
  hard-gated (doc 13 Phase 5) on a confirmed loadable checkpoint for the deployed line.
