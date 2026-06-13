# 12 — Graph Governance & Release Engineering: evidence log (M2–M6)

Doc 12 milestones M2–M6, built in `obsd/internal/governance` (+ `cmd/govern`,
`harness/governance.py`). M1 (immutable releases + pinning) landed in Phase 0b.
Every exercise below ran against the live `kind-vigil` cluster (Online Boutique +
node-exporter) with real binding + detection; deterministic cores are pinned by Go
unit tests (`*_test.go`) and the harness governance regression (`test_governance.py`).

## M2 — Change classes + review workflow

`Classify(from, to *graph.Graph)` diffs two real releases and derives the highest-
reaching blast-radius class (doc 12 §3.2): additive-low < behavioural-medium <
normative-high (the widest change sets the rigor). `LoadReleasePinned` was added so a
HISTORICAL release reconstructs byte-identically from just the overlays its manifest
names — governance must load arbitrary past releases to diff them (the existing
whole-dir loader only rebuilt the latest).

Validated on the real release history:
- `v0.1.0` first release → behavioural-medium · `v0.1.0→v0.2.0` → behavioural-medium ·
  `v0.2.0→v0.3.0` → behavioural-medium (correctly itemised: two-hop conditions,
  anchors, IO-PSI/OOM rules). Identical release → none.
- Synthetic boundaries pinned: new signal → additive; default/factor/kind change →
  normative; equivalence-group pattern-removal/canonical change → normative; a brand-
  NEW flagged default → behavioural (not a change to *existing* normativity).

Review workflow (`EvaluateProposal`) gates: authorship-required, class-verification
(declared ≥ derived, else MISCLASSIFICATION block naming the elements), evidence-
required-by-default for behavioural+, regression-ledger (M3), staged-rollout-mandatory
for normative. Verdict ∈ {blocked, ready-for-review, approved}; the tool NEVER
approves — a clean mechanical pass is ready-for-review until a named reviewer's
approve decision is present (doc 12 §3.3 "the harness can block; it cannot approve").

**Live CLI evidence:**
- `govern verify --proposal proposal-v0.3.0.yaml --ledger <real ledger>` → APPROVED
  (declared behavioural-medium matches the derived diff; 2 evidence refs; 4 gates pass;
  reviewer approve).
- A misclassified proposal (declares additive-low for the behavioural v0.3.0 change) →
  BLOCKED: "MISCLASSIFICATION: declared additive-low but the diff reaches
  behavioural-medium — detection check on PHEN_OOM_KILL_CGROUP … added …" + evidence
  block; exit 1.

## M3 — Harness-wired regression gates

`RequiredGates(class)` (Go) is mirrored by `harness/governance.py`'s `REQUIRED`
(cross-checked by `test_governance.py`). The harness RUNS each gate's real check and
emits the ledger the Go workflow consumes; a skipped/missing/failed required gate
BLOCKS (the harness blocks, never approves).

Gate → real command:
- authoring-lints → `graphlint -strict -release-latest ontology/releases`
- binding-qa → `go test ./obsd/internal/binding/`
- falsification → `pytest tests/test_sensitivity.py`
- replay-diff → `pytest tests/test_replay_determinism.py`
- shipped-class-backtest → `pytest tests/test_forecast_gate.py` (normative only)

**Live evidence:** `harness governance run-gates --proposal proposal-v0.3.0.yaml`
ran the four behavioural gates for real — graphlint immutability (release v0.3.0
matches its pin), `go test ./binding` (ok), sensitivity (4 passed), replay-determinism
(3 passed) — ALL PASSED; the ledger fed `govern verify` → APPROVED. Negative paths
(missing/failed/skipped gate → block) pinned by `regression_test.go`.

## M4 — Migration + binding diffs

`BindingDiff(from, to *binding.Result)` over two REAL re-binds of the same cluster
(same inventory, only the graph release differs): variables gained/lost, bars
re-resolved, validation shifts, + observability and Tier-A re-selection deltas. obsd
gained `--dump-bindings` to write its compiled `binding.Result` as JSON once, so the
exercise diffs the runtime's real output (never a mock).

**Live evidence (kind-vigil boutique):** a normative factor change 0.95→0.80 on the
container memory bar, dumped under each release and diffed:
`govern migrate` → "19 (entity, variable) bar(s) re-resolved under
THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT" — e.g. loadgenerator 510Mi→429.5Mi,
recommendationservice 298.8Mi→251.7Mi (every bar by the 0.80/0.95 ratio), operator-
readable per doc 12 §3.4.

## M5 — Staged rollout + rollback (🔒 Phase-2 EXIT GATE)

`EvaluateStage` checks live health signals (mis-join rate 03, binding-QA failures 04,
finding-volume diff 07) per stage; a tripped signal HOLDS the rollout and `RolloutState`
rolls back (re-pin prior release + re-bind). Deterministic core pinned by
`TestSeededBadReleaseRolledBack`.

**Live exercise — a seeded bad release caught at canary and rolled back:**
- Seeded bad release = container memory factor 0.95→0.05 (a normative default change;
  structurally valid, lints clean — passes the reference/regression stage).
- Live finding-volume measured on the cluster: GOOD release = **1** (0 findings + 1
  unexplained card; the healthy cluster is silent). Seeded-bad release = **19** (10
  MEMORY_LEAK findings + 9 unexplained loud cards; every container's working_set
  crossing its tiny bar).
- `govern rollout` over the real numbers: reference advances (mis-join 0, QA 0); at
  CANARY the finding-volume signal trips (baseline 1 → 19, ×19 vs max ×2) → held-and-
  rolled-back to v0.3.0; exit 1.
- Post-rollback (re-pinned to v0.3.0) finding-volume returns to **0** (silent/healthy);
  findings stamp graphVersion so post-rollback behaviour is cleanly attributable.

## M6 — Curation intake loop (closed end-to-end, live)

Three feeds (`FromUnexplainedCandidates` 08, `FromCoverageGaps` 04,
`FromFalsification` 11) → `Triage` (deduped, ranked: falsification > unexplained >
coverage). The loop closed on a REAL gap: MEMORY_LEAK requires a RISING slope, so a
container at/over its memory bar but STEADY is loud-but-unmatched (a distinct
saturation state). Under a conservative memory bar on the live cluster:

- **Candidate:** 9 recurring unexplained cards on `container_memory_working_set_bytes`
  across 7 Containers (steady, MEMORY_LEAK does not cover them).
- **Intake:** `govern intake` → "recurring unexplained loudness on
  {container_memory_working_set_bytes} across 7 Container → author candidate phenomenon".
- **Author:** a human adds `PHEN_MEMORY_SATURATION` (CorrelationGroup node + required
  container-memory member + entity-local span + a level/crossed condition) — the sole
  write path is the graph; the system PROPOSED, the human AUTHORED.
- **Release + matches:** re-running detection under the new release → **18
  PHEN_MEMORY_SATURATION matches** (coredns, kindnet, node-exporter, adservice,
  cartservice, …) and the **memory unexplained cards drop 9 → 0**. The pattern the
  system surfaced as unexplained now MATCHES authored knowledge (doc 12 §7 M6 exit).

## Adversarial review (6 dimensions, refute-by-default verification)

A multi-agent adversarial workflow reviewed the new governance/charter/chat/context
code (1 reviewer per dimension → refute-by-default verifier per finding). It confirmed
**13 real defects**; all were fixed + regression-tested, and the live workflow re-run:

- **BLOCKING** — a `--dry-run` ledger recorded gate status `"passed"`, so a release
  could be APPROVED with NO gate executed. FIX: dry-run results carry status
  `"dry-run"`; the Go verifier blocks them. (Live: dry-run ledger → BLOCKED; real
  ledger → APPROVED.)
- **Under-classification (the doc-12 §3.2 danger)** — `classifyRuleEdit` ignored a
  rule's `Signal` and `Eligibility` re-points (both change which bar binds where),
  deriving them as `ClassNone`. FIX: both now derive normative-high.
- **Ledger not bound** — `GateLedger.Class`/`Proposal` were loaded but never enforced;
  a weaker-class or wrong-proposal ledger could satisfy a release. FIX: both enforced
  (ledger-binding gate); the harness records the proposal's `release` field.
- **BindingDiff container collision** — the diff key omitted `Container`, collapsing
  two containers in one pod and masking a real per-container bar change. FIX: key
  includes Container.
- **Observability drop** — `AddObservabilityShift` ignored phenomena REMOVED by the
  upgrade. FIX: removed phenomena shift to "absent".
- **Rollout** — a stage with zero health signals was vacuously healthy; Step kept
  advancing after a rollback. FIX: empty-signals → unhealthy; Step terminal after
  rollback.
- **Chat/charter** — the snapshot mislabelled DOWNSTREAM blast-radius as "precursors";
  the causal denylist was narrow; the refusal message was ungrammatical; POST bodies
  were unbounded. FIX: blast-radius no longer fed to precursors; denylist broadened
  (with the inherent-incompleteness caveat stated); message reworded; 64KiB body cap.

Determinism re-confirmed with the patched binary: a 6-tick live capture (7464 streams)
replayed BYTE-IDENTICALLY twice with dump-bindings + chat + context all active — the
new code is off the deterministic path (non-gating preserved).

## Standing notes / limits

- The seeded-bad finding-volume explosion is severe enough that the falsification gate
  would also flag it at the reference stage (defense in depth); the canary health
  signal is the BACKSTOP for subtler faults that survive static regression — the
  exercise demonstrates that backstop machinery with real numbers.
- M6 used a conservative memory bar (factor 0.10) as the *scenario* under which
  sustained memory is loud; the curation change itself is the new phenomenon, not the
  bar. The temp graph + overlays live under /tmp (the committed KG/releases are
  untouched — governance never silently edits a release).
