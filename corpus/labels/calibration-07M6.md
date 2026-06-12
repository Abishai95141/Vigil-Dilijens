# Detection sensitivity calibration — 07 M6 (with 11 M4)

**Claim under test (doc 07 §3.6):** the shipped sensitivity defaults are
justified by precision/recall on the labeled replay corpus — never intuition.

**Machinery:** `harness/src/harness/sensitivity.py` re-evaluates recorded
bundles under a sensitivity grid via the replay binary's EVALUATION mode
(`replay -eval -eval-*` — re-derivation under an overridden regime, explicitly
never a verification) and scores each point event-level against ground-truth
labels. Definitions live in the module docstring; phenomena outside a label
file's scope are not scored — the calibration claims nothing about them.

**Corpus at this calibration:**

- `bundle-v1` (committed, synthetic-by-construction so the truth is exact;
  labels: `corpus/labels/bundle-v1.labels.json`): the leak trajectory into a
  cgroup OOM, the first-order throttling-cascade pair, the recognized
  leak→OOM cascade.
- Live chaos evidence (kind, graph v0.3.0, runs of 2026-06-12; bundles local —
  storage/anonymization policy is doc 14 A16): `cpu-cascade.yaml` (FULL
  first-order match at the victim, hogs/coredns suppressed, healthy cluster
  silent), `io-pressure.yaml` (second-order STORAGE_SATURATION degraded at the
  node, 387 findings byte-identical in replay), `leak-oom.yaml` (25
  MEMORY_LEAK findings + 50 blast-radius entries; zero false positives across
  every healthy window observed).

## Sweep (bundle-v1, 8 grid points)

| setting | precision | recall | cascades hit | cascade FPs |
|---|---|---|---|---|
| **pinned** (band 0.05 · floor 0 · cascade 10m) | **1.000** | **1.000** | **1/1** | **0** |
| band=0.02 | 1.000 | 1.000 | 1/1 | 0 |
| band=0.10 | 0.938 | 1.000 | 1/1 | 0 |
| min_completeness=0.25 | 1.000 | 0.667 | 0/1 | 0 |
| min_completeness=0.5 | 1.000 | 0.667 | 0/1 | 0 |
| min_completeness=0.9 | 1.000 | 0.333 | 0/1 | 0 |
| cascade_window=2m | 1.000 | 1.000 | 1/1 | 0 |
| cascade_window=30s | 1.000 | 1.000 | 1/1 | 0 |

Per-phenomenon at the pinned point: MEMORY_LEAK 1.0/1.0 (6 findings),
OOM_KILL_CGROUP 1.0/1.0 (2), THROTTLING_CASCADE 1.0/1.0 (7).

## Decisions (the shipped defaults, and why)

1. **`detection.min_completeness = 0`** — precision is already 1.0 at floor 0
   on every corpus item: the structural gates (anchor-evidence rule, the
   multi-signal conjunction, edge-validity) carry the false-positive defence.
   Any positive floor only costs recall on sparsely-checked phenomena — and at
   0.25 it already kills OOM_KILL_CGROUP (1/8 required members checkable on
   this signal set) **and with it the recognized leak→OOM cascade** (0/1).
   Suppressing honest degraded matches buys nothing here and silences real
   findings.

2. **`observation.at_threshold_band = 0.05`** (retained) — 0.10 admits
   premature claims (110Mi against a 121.6Mi bar scored "at-threshold":
   precision 0.938 against the tight truth window); 0.02 scores identically to
   0.05 on this corpus but narrows the early-warning approach zone for no
   measured precision gain. 0.05 is the widest setting with precision 1.0.
   (Regression: `test_band_widening_admits_premature_findings` pins the 0.10
   behaviour so a corpus change forces re-derivation.)

3. **`detection.cascade_window = 10m`** (explicit, formerly borrowed from the
   co-occurrence window) — windows down to 30s still pair the fixture's
   cascade because its leak fires until the OOM tick; the LIVE-shaped gap is
   different: a leak finding stops at the kill (the working set drops on
   restart) and the OOM evidence lands up to minutes later, so a short window
   would sever exactly the story the relation exists for. 10m bounds the
   pairing well above every observed kill→evidence gap while staying inside
   the co-occurrence regime. Tightening below the leak→OOM handoff time is the
   measurable failure to avoid; nothing measured argues for longer.

4. **Ordering strictness (T0− before T0)** — NOT yet a tunable: enforcing it
   needs member-state onset tracking (when a state first appeared), which the
   fingerprint pipeline does not carry. Deferred with the dependency named;
   the conjunction-only setting is the weakest honest default. (Doc 07 §8 /
   open-question 3 territory.)

**Standing caveats:** the corpus is small (one synthetic + three live chaos
scenarios on one cluster); per-class targets for the Phase-1 exit gate need
the fuller 11 M3 falsification corpus, and these defaults must be re-derived
as it grows. Re-run: `uv run pytest tests/test_sensitivity.py` (machinery
regression) + the grid in this file against any new labeled bundle.
