# clockd — forecasting clock service (doc 09)

Python/uv. The model-agnostic "clock": answers exactly one question — *when does this
number cross that number*. It sees ONLY bare float series; never labels, names, units,
entities, graph structure, or reasons (the charter, doc 01 §4). Everything that makes
a forecast mean something is authored graph knowledge, joined at surfacing, never here.

- `src/clockd/forecast.py` — the **StubClock**: pure-stdlib, contract-conformant.
  Honest behaviour: flat point forecast on random input; uncertainty band **never
  collapses to a line** (widens with horizon). This is the swap-test baseline
  (doc 09 M1): a stub must pass the same conformance suite as the real model.
- `src/clockd/server.py` — gRPC serving placeholder (Phase 2; stubs generated from
  `proto/vigil/clock/v1/clock.proto`).

**TimesFM 2.5** lands behind the `model` extra in Phase 2, pinned by BOTH the package
version and the HF checkpoint revision `google/timesfm-2.5-200m-pytorch` (doc 14 A15).
CPU is sufficient for dev.

Do: keep the base install heavy-dep-free so conformance tests run anywhere. Don't:
let any string/semantic field cross the clock boundary.

Test: `just clockd-test` (ruff + pytest). The TimesFM agent SKILL.md is vendored here
(`SKILL.md`) per techstack §2.3.
