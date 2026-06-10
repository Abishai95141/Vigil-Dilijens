# harness — validation & calibration (doc 11)

Python/uv. **Offline tooling, not product** — nothing here ships into a customer
cluster. The graph makes empirical claims (equivalences, temporal orders, spans); the
clock makes statistical claims (bands, crossings). This harness falsifies both BEFORE
operators see them, and its suite results are the **exit gates of every phase** (doc 13).

The four suites (built across Phases 0b–2): binding QA, phenomenon falsification,
detection sensitivity calibration, forecast backtesting — all on a deterministic
**replay substrate** (recorded readings + graph version + topology snapshot + bars +
params, re-run byte-identically through the real components).

Today: `src/harness/backtest.py` has the backtest primitives (band coverage,
time-to-cross error, doc 11 §3.5). numpy is in base; the columnar/plotting stack
(pyarrow, polars, scipy, matplotlib) is the `analytics` extra, added as suites need it.

The harness never edits the graph: discrepancies become curation items for humans
(via governance, doc 12). Test: `just harness-test`.
