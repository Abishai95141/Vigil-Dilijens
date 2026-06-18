# Testing blindspot audit (2026-06-18, branch v4)

A first-principles re-audit of the Vigil test surface, going past `artifacts/task.md`
claims into the actual gate code, CI wiring, and justfile. Reported faithfully per the
CLAUDE.md engineering mandate (no shallow proxies; correct overstatements).

## What is GENUINELY enforced (corrected — better than the surface suggested)

An earlier pass claimed "the corpus gates are not CI-enforced." **That was wrong.**
Verified facts:

- `ci.yml` `go` job runs `go test -race ./...` → this includes EVERY Go unit test, the
  Go halves of all 14 gates, AND the `*FrozenConsistent` producer drift guards. Plus
  `go vet`, `gofmt`, `graphlint -strict`.
- `ci.yml` `harness` job runs `uv run pytest -q` → `harness/tests/test_*_gate.py` LOAD
  the real frozen corpus and `assert v.passed` for all 14 gate families. So the Python
  corpus-grading halves DO run in CI and assert the floors hold.
- `ci.yml` `clockd` + `proto` jobs cover the service + the wire contract.
- `integration.yml` (nightly 02:17 UTC + dispatch): kind identity/clock, live e2e
  (detection + restraint + replay determinism), forecast plumbing smoke (stub).

CI coverage is real and broad. The blindspots below are narrower than "no enforcement."

## Genuine remaining blindspots (verified, ranked)

1. **Scenario-list drift is unguarded (hermetic, fixable now).** Each gate hard-codes
   its scenario list in THREE places — the `justfile` recipe shell loop, the pytest
   `SCENARIOS` constant, and the `corpus/<gate>/label-*.json` files on disk. They are
   in sync today (verified app-slo=10, departure=8, etc.) but NOTHING fails if they
   drift. Add a corpus scenario and forget the runners → coverage silently shrinks,
   CI stays green. FIX: a meta-gate assertion that the corpus oracle count matches a
   declared registry count (forces a conscious bump when scenarios change).

2. **Real-model forecast gate (09 M3) never runs in CI (acknowledged, hard).** The
   gate of record needs multi-GB TimesFM weights; only the stub plumbing smoke runs in
   `integration.yml`, and a stub correctly returns INSUFFICIENT. So forecast SKILL
   calibration is only ever certified on-demand by a human. Structural, not cheaply
   fixable. Mitigation already present: the scorer arithmetic is unit-tested
   (`test_forecast_gate.py`).

3. **`crossservice` + `crossservice-projected` are `fixture-replay`, not producer-locked.**
   Curated/live-captured data graded by the scorer; the flow producer can change without
   regenerating them. Acknowledged in the meta-gate with a documented reason (may not be
   reproducible from synthetic inputs). Residual staleness risk.

4. **Forecast precision is structurally untested (the headline science gap).** Every
   forecast corpus ends in a real crossing, so "0 false warnings" is vacuously true —
   there is no near-miss workload (climbs toward the bar, then levels off) to exercise a
   false alarm. Recall is validated; precision is not. (`departure` gate DOES have a
   near-miss/decoy corpus for the band-anomaly producer — but the TimesFM crossing
   forecaster itself has none.) This is task #75 / 11 M3, still open.

5. **`just ci` ≠ what CI runs.** Local `just ci` = `lint gen-check test` (Go only). It
   skips clockd + harness pytest, so a developer can break a Python gate scorer and get
   a green local `just ci`. By-design per CLAUDE.md ("the full local Go gate") but there
   is no single local command that mirrors CI. Minor; documentable.

6. **`integration.yml` forecast smoke swallows exit codes** (`... || true`, greps for
   "GATE:"). Intentional plumbing smoke, but a non-crash partial failure that still
   prints "GATE:" would pass. Low severity.

## Second pass — future-proofing the framework (meta-gates)

The user's goal: the testing framework must FORCE any future (v5) feature to be tested —
no silent blind spot. Three agents mapped the full surface. The producer side (frozen
guards, corpus registry, scenario pins) was already strong; the blind spots were all on
the **surface/registration side**, where a new feature lands. Built these structural
guards (all hermetic, AST/source-introspecting, with proven teeth):

1. **Scenario-drift guard** — `harness/tests/test_gate_coverage.py`
   (`EXPECTED_SCENARIOS` + `test_gate_corpus_scenario_counts_are_pinned` +
   `test_scenario_modules_consume_every_corpus_oracle`). On first run it caught a REAL
   pre-existing drift: `IMAGE_PULL_FAILURE` was in the corpus + justfile but never in the
   pytest `SCENARIOS` list, so CI had been silently NOT grading it. Fixed.

2. **API route + charter-coverage meta-gate** — `obsd/internal/api/charter_coverage_test.go`.
   AST-parses `server.go`, discovers every mounted `/api/*` route, and requires each to
   carry an explicit charter disposition. Authored/projected surfaces must be `dispSwept`
   (a real payload run through the register audit) — this EXPANDED the charter sweep to
   cross-service, root-cause-chain, departures (previously never swept). A new endpoint
   fails the build until classified. Teeth verified (drop a disposition → fail).

3. **MCP tool-set completeness** — `obsd/internal/mcp/tool_coverage_test.go`. AST-discovers
   every `tool…` const, pins the set, and asserts tools/list advertises exactly it and
   every tool is dispatched. A new/removed tool fails until acknowledged. Teeth verified.

4. **`obsd/internal/meta` package** — two repo-structure guards:
   `TestGatePassedFlagsAreRegistered` (every `…GatePassed` operator-visibility flag must
   map to a real `just *-gate` recipe) and `TestEveryPackageHasTestsOrIsExempt` (every
   obsd package has a test or a documented `untestedAllowlist` entry). Teeth verified.

5. **Guarded-corpus honesty** — `test_guarded_status_is_backed_by_a_real_frozen_guard`
   asserts every corpus marked `"guarded"` names a `*FrozenConsistent` Go test that
   actually exists in the source tree. Cross-language teeth verified.

Verification: `go test -race ./...` exit 0 · harness 136 passed · gofmt clean ·
CGO-free build OK · go vet clean. Every guard runs in the EXISTING CI jobs (Go guards
under `go test -race ./...`; Python guards under the `harness` pytest job) — no new CI
wiring needed.

## Third pass — robust workarounds for the three "can't certify cheaply" gaps

A design + adversarial-refute workflow (6 agents) classified all three as
**testing-infrastructure** (not core/architectural) and designed hermetic workarounds, each
pressure-tested for shallow-proxy-ness. All three earned `implement-with-hardening`.

**Gap 1 — forecast precision (the false-positive boundary).** The live backtest only scores
crossing-terminating bundles, so "0 false warnings" was vacuous. Built the near-miss/recede
**decoy corpus** (`corpus/forecast-nearmiss/`) that folds the REAL `forecast.Project` over
recede/plateau/seductive-wide-band trajectories and asserts ZERO candidates with the EXACT
silence reason, while genuine crossings still emit (`just forecast-fp-gate`,
`harness.forecast_fp_gate`, `forecast.TestNearmissCorpusFrozenConsistent`). Hardened per the
refuter: enforced REQUIRED_SCENARIOS, exact-silence-reason oracle, params pinned in each
label, ill-formed (NaN) inputs, multiple emit controls, and an HONEST banner — it certifies
OUR decision code, NOT TimesFM's skill. Evidence: `corpus/labels/forecast-fp-gate.md`.

**Gap 2 — real-model gate in CI.** The gate of record needs multi-GB weights. Built the
hermetic **threshold-pin guard** (`test_gate_thresholds_are_pinned`) — all 12 `forecast_gate`
constants frozen to their evidence-backed literals + a completeness check (a new threshold
can't ship unpinned). This is the refuter's key hardening (the C+D+E run passes with slack,
so a loosened constant would otherwise stay green). Plus a **dispatch-gated real-model
loadability job** in `integration.yml` (manual trigger only — never a recurring multi-GB
download) that verifies the pinned TimesFM checkpoint still loads + runs. The full
recorded-real-trace re-grade is documented as the on-demand follow-up (needs a committed
real-capture bundle, which doesn't exist in-repo).

**Gap 3 — perf in CI.** The refuter judged the first design a SHALLOW PROXY: the benchmark's
`topo==nil` skips the blast-radius/spanned path where O(n²) lives. Built
`obsd/internal/detect/perf_guard_test.go` over the REAL-topology production path
(`TestMatchPerfGuard`): a machine-independent **alloc ceiling + scaling-flatness** guard
(teeth proven — an induced O(n²) shape fires both at 2.98×). Plus a store guard over
production-shaped findings, and FIXED the scale-test silent-pass hole (a missing misjoins
metric now FAILS). Honest limit documented: a constant-factor CPU regression with flat
allocs is not caught (the irreducible alloc-guard limit). Evidence:
`corpus/labels/perf-guard.md`.

All hermetic guards run in the EXISTING CI jobs (Go under `go test -race ./...`, Python under
the `harness` pytest job). Verified: `go test -race ./...` exit 0 · harness 144 passed ·
`just lint` OK · CGO-free build OK.

## Net effect on the v5 question

A v5 dev now CANNOT, without a test failing: add an `/api` endpoint (charter disposition
required), add/remove an MCP tool (pin required), add a gated feature flag (gate recipe
required), add a package with zero tests (allowlist entry required), add a gate corpus
(guard status + scenario pin required), or drift a scenario list. The remaining gaps are
the science/ops ones that can't be closed structurally: forecast precision (#4, near-miss
corpus needs a live model run), real-model forecast gate in CI (#2), and CI-enforced
performance thresholds (#3 in the ranked list).
