# 04 — Deep Audit Verdict (10-agent review of test-suite rigor)

> Produced by a multi-agent audit (6 dimension auditors → 3 adversarial verifiers → 1 synthesizer;
> 304 tool calls, ~23 min) that **read the actual test/gate/corpus source and perturbed it**
> (e.g. corrupted corpus files to confirm guards actually fire). v3 @ `50af2c2`. File paths/lines
> are v3 — re-locate in v4.
>
> The driving question: do the gates exercise the **real shipping engine**, or pass **by
> construction** (author writes both the input fixture AND the expected output → circular)?

## 1. Bottom line — **Partly business-grade**
The deterministic detection core (Vigil's actual contribution) is **genuinely, non-circularly
tested**. But the **headline forecasting clock (TimesFM) is never validated by any automated test**
— only a flat StubClock runs in CI, and the one "forecast quality" gate grades hand-authored
arrays, not model output. Plus: no automated live e2e, zero API auth, zero scale/soak, no
DB-migration story. **The "business solution" framing currently outruns what is validated.**

## 2. What's genuinely STRONG (real-engine, non-circular)
- **Replay determinism** — `obsd/internal/replay/replay_test.go`: two independent paths (in-memory
  rings vs disk segments) must yield byte-identical digests; `TestTamperedBundleIsDetected` (~:369)
  and `TestCorruptSegmentRefused` (~:406, flips a real byte → CRC fail); `TestFixtureBundleReplays`
  asserts real cascades fire (`PHEN_THROTTLING_CASCADE`, `MEMORY_LEAK→OOM_KILL_CGROUP`). **Strongest
  single piece.**
- **Clock charter conformance** — `obsd/internal/clock/conformance_test.go` (~:19-49): walks the
  proto descriptor by reflection, fails on any string/bytes. Impossible to pass by accident.
- **6 of 11 corpus gates are NOT circular** — `app-slo`, `validate-claim`, `event-detection`,
  `transitive-chain`, `projected-transitive`, `departure`. An **always-on Go `*FrozenConsistent`
  test** (no build tag, no skip) re-runs the real producer
  (`binding.Compile → observe.Materialize → detect.Matcher`, `eventdetect.Findings`,
  `departure.Detect`, `api.ValidateClaim`) and byte-locks the corpus; the Python gate then grades
  that engine output against an **independently hand-authored oracle**. Verifier confirmed by
  corrupting a corpus and watching the Go guard fail. *(This refutes the "are they circular?" worry
  for these six.)*
- **Identity mis-joins** — `obsd/internal/identity/normalize_test.go`: recreate-race, late sample
  binding to old uid, real cAdvisor/KSM dialect payloads, quarantine/drop negatives; live
  `integration_test.go` asserts zero mis-joins.
- **Pervasive must-NOT-fire** — every charter silent-failure mode has a negative test, incl. a
  structural honeypot that evades the denylist but is still caught (`api/validate_test.go` ~:102).
- **Determinism as first-class** — zero `time.Now()` in logic packages (grep-confirmed), injected
  clocks, shuffled-inventory invariance, meaningful `-race`.
- **Durability/restart** — `TestIncidentSurvivesRestart` actually closes+reopens SQLite;
  `TestRestartInvariance` proves fold-equivalence across restart.

## 3. What's WEAK or CIRCULAR (ranked by damage under a skeptical probe)
1. **Forecast-quality gate is circular by construction** — `harness/tests/test_forecast_gate.py`
   (~:50-72) authors **both** the bands (lo=v−20, hi=v+20) **and** the realized series, then checks
   the coverage arithmetic. Validates the *scorer's math*, not any model. `forecast_gate.py:5` claims
   it scores "the REAL forecast pipeline's output" — **it never invokes a forecaster.** A judge who
   opens this file sees the headline feature's quality gate grading a self-authored answer key.
2. **`xsvc-gate` / `xsvc-projected-gate` are fixture-only at run time** — `justfile` (~:177-190) run
   only the Python scorer over a captured corpus; no `go test`, no `replay -crossservice`, no Go
   drift guard pinning `corpus/crossservice*` to engine output. *(Mitigant: corpus is real captured
   data + separate cross-service unit tests exist.)*
3. **3 gates lack the always-on drift guard** — `events`, `mcp`, `incident`. Corpora regenerate via
   **skip-only `REGEN_*` tests**, so committed JSONL can silently drift. **Empirically proven:**
   regenerating `corpus/mcp/advisory-drafts.jsonl` yields a file differing from the committed one
   (row reordering) and CI never caught it. (Engine semantics ARE covered by independent unit tests
   → staleness, not full circularity — but TESTING.md oversells these as equal proof.)
4. **Frozen-consistency tests SKIP, not FAIL, when corpus is missing** — e.g.
   `api/validate_corpus_test.go` (~:120) and several harness tests. Delete a golden → the strongest
   check goes green-by-skip.
5. **Presentational over-selling** in the Go "frozen-consistent" comments (call themselves the
   "anti-shallow core"); read in isolation those tests only prove determinism — correctness lives in
   sibling hand-asserted files. Contained, but erodes trust on close reading.

## 4. Business-grade gaps (an ABB evaluator will probe these)
- **Forecasting unbacked (critical).** TimesFM never loads in CI (`ci.yml` runs base env; model deps
  behind an opt-in extra; both model tests double-gated + skipped). Even the opt-in run asserts only
  shape + "ramp goes up" — no band coverage, no recall, no time-to-cross error on real series. By the
  project's **own rule** (docs/09 §M3: no class ships until its gate passes on recorded series), the
  **PROJECTED class is currently unshippable by its own definition.** Pitch language about "minutes of
  warning" / "calibrated bands" is unsupported by repo evidence.
- **No automated live e2e detection.** Nothing boots `bin/obsd` against a faulted cluster and asserts
  the right phenomenon fires via `/api/findings`. The live pipeline (k8s scrape → identity → binding
  → real metrics → finding) is only checked by manual curl in TESTING.md Phase 3 (which isn't even
  runnable as written — see file 03). The integration tests run on `workflow_dispatch` only, **never
  on PR** — *(note: user has `.github/workflows/integration.yml` open — this is the file to change.)*
- **Security: disqualifying for enterprise.** Zero auth, zero authz, zero TLS on the served API +
  MCP — plain HTTP exposing full incident/topology/silence-ledger state. Not one auth test. (K8s RBAC
  in `deploy/` only governs what obsd can *read*, not who can hit obsd's own API.)
- **Scale uncharacterized.** No Go benchmarks, no load/churn/soak. SQLite is single-writer
  (`SetMaxOpenConns(1)`); per-tick cost is O(entities×variables); informer cache memory under
  thousands of pods unknown. For a "real-time pod discovery" pitch, the scaling envelope is *the*
  question and there's no answer. (S7 overhead is one manual `ps`, already known-violated.)
- **Persistence/upgrade has no story.** `CREATE TABLE IF NOT EXISTS` only; no `PRAGMA user_version`,
  no migration ladder. Findings PK embeds `graph_version`, so an ontology release silently orphans old
  rows; cross-version replay is *refused*, not migrated. No test that a new binary opens an old DB.

## 5. Prioritized roadmap: hackathon demo → defensible business solution
| # | Action | Effort |
|---|---|---|
| 1 | **Validate TimesFM for real.** Record boutique series, run the actual model via `replay -forecast`, feed real output through `forecast_gate.py` (band coverage / recall / time-to-cross) for ≥1 class (e.g. working-set→OOM). Wire `just forecast-gate` into a CI lane. *Highest priority — the difference between "we forecast" and "we have a forecasting architecture."* | ~1–2 wk |
| 2 | **One automated live e2e.** integration-tagged: kind/k3s up → inject a `corpus/chaos/` fault → boot obsd with `--kubeconfig` + `--referee-enabled` → assert the phenomenon fires via `/api/findings` + restraint holds on a non-modeled fault. Run on PR/nightly (edit `.github/workflows/integration.yml`). | ~3–5 d |
| 3 | **Minimum security + tests.** bearer/mTLS on `/api/*` + `/mcp`, deny-by-default; one test for unauth-rejected / auth-accepted. Document the threat model. | ~1 wk |
| 4 | **Close gate gaps.** Add `*FrozenConsistent` drift guards to `events`, `mcp`, `incident`; add a Go drift guard (or honest relabel) for `xsvc`/`xsvc-projected`; add a meta-test "every gate corpus has a live drift guard." | ~3–4 d |
| 5 | **Missing-corpus → FAIL, not SKIP.** `t.Skipf`→`t.Fatalf` in frozen-consistency tests; `pytest.skip`→fail in the harness. | ~½ d |
| 6 | **Scale benchmarks + churn/soak.** Go `Benchmark*` for per-tick path; hundreds/thousands of synthetic entities measuring memory + SQLite write-lock; churn (rapid create/delete) exercising identity GC + edge expiry + binding recompile. Automate the S7 overhead assertion against a doc-traceable bar. | ~1 wk |
| 7 | **DB migration story.** `PRAGMA user_version` + migration ladder; test that a new binary opens an old DB; define how incident memory survives an ontology upgrade. | ~3–5 d |

## 6. Honest framing for the pitch
Lead confidently with the **proven core**: provenance separation, join-never-fuse, replay
determinism, identity mis-join defense, must-NOT-fire restraint, dependency mapping. Present
**forecasting, enterprise scale, and security as a credible roadmap with the gates already
architected** — do **not** present the forecasting clock or enterprise scale/security as done.
Of the three pitch pillars (forecasting + real-time discovery + dependency mapping): **dependency
mapping and deterministic detection are proven; real-time discovery's *scaling* is unproven;
forecasting is unproven by the project's own gate.**
