# Robustness & value audit — findings + fixes (2026-06-13)

A 6-dimension adversarial audit (`vigil-value-robustness-audit` workflow) asked the
question a cluster maintainer asks: *is this TRUSTWORTHY and ROBUST, or noisy?* It
probed the LIVE system (kind + boutique + the forecast lane on) read-only, refute-by-
default. Below: the REAL findings, what was fixed (all gates green + the 09 M5 gate
re-PASSES), and what is honestly deferred.

## Fixed this pass (commit `8eaac3d`)

### HIGH

- **Forecast FLICKER → debounced.** `Project()` is pure per-cycle (correct for the
  gate); the live surface had no debounce, so a marginal projection blinked on/off —
  a maintainer can't act on a blinking warning. Fix: `api.WarningDebouncer` (warm path,
  off the digest) holds a marginal warning ~3 cycles, labels it `aging` (the LAST real
  projection, never fabricated — mirrors the doc 08 §3.4 unexplained-card aging),
  reconciles it out of the silence list, evicts when quiet. **Crucially does NOT hold an
  `already-crossed` warning** — that is the PROJECTED→MEASURED handoff, released to
  detection at once (else the two lanes contradict each other for ~90s).

- **band-too-wide guardrail IMMINENCE BIAS → absolute test.** `project.go` silenced when
  `bandWidth > MaxBandRatio × time-to-cross`. With ttc in the denominator, the ratio
  EXPLODES as a crossing becomes imminent → the warning vanished exactly when it mattered
  most. Fix: an ABSOLUTE test — the actionable NEAR cone (earliest→point) vs the HORIZON,
  never vs ttc; the open far tail no longer silences a tight imminent crossing.
  `max_band_ratio` reinterpreted as a horizon fraction (default 0.5). **09 M5 re-gate:
  STILL PASSED** — band coverage 0.806→0.804, recall 1.00, in-band 93/93, 0 false,
  **band-too-wide silences 13→0, crossing-forecasts warned 87%→97%.**

- **STALE findings feed → serve-time freshness.** `store.ActiveFindings` returned the
  durable rows by `last_seen DESC` with NO recency filter, and `/api/findings` stamped a
  fresh `generatedAt` — so a RESOLVED leak read as currently firing (5 surfaces disagreed
  about the same entity). Fix: `FindingRow.MarkFreshness(asOf, staleAfter)` derives
  `stale` + `lastSeenAgoSeconds` at serve time (3 eval ticks), off the deterministic
  digest. A resolved/plateaued match now reads "last seen Xago", not firing-now.

- **Timeline note "ships in Phase 2" → conditional.** The hardcoded note showed even
  while the lane was ON. `BuildTimeline` now takes the lane state: OFF / ON-quiet /
  ON-with-bands.

### MEDIUM
- **Chat citation mislabel:** Chat.tsx hardcoded "cites (authored / measured)" for every
  answer regardless of class → neutral "references:" (no false class claim).
- **Silence list clarity:** 41 raw enum reason-codes → grouped by reason with a count and
  a plain-English glossary (collapsible).

## Live E2E verification (full build, mc32 — the previously-flickering config)

Watched the warning over a real creep (ws 130→304Mi, ~14 min). **The flicker is gone:**
the card appeared ONCE (flips=1) at 17:43 and held `card=1` for ~17 consecutive cycles
(~5.5 min) straight through the crossing — INCLUDING the imminent band-too-wide region
(ws 280→304) where the old guardrail silenced it. At the crossing (ws=304) and for 80s
after: `card=0` (cleared at once, not held stale) while the MEASURED leak finding fired —
the clean PROJECTED→MEASURED handoff. `aging=0` throughout (the band-too-wide fix made the
projection stable on its own; the debounce hold is the tested safety net). And live:
`/api/findings` carries `stale`/`lastSeenAgo`; `/api/timeline` reads "Forecasting is ON…",
not "Phase 2".

Gates after the pass: `just ci` (-race) ✓ · clockd 11 ✓ · harness 25 ✓ · web 6 ✓.

## Confirmed solid (audit-refuted as defects)
Non-gating is structurally airtight (detection isolated from clockd: separate goroutine,
frozen snapshot, microsecond gate hold); the chat register guard held every adversarial
probe; honest-silence/degradation/durability/gauge-reset handling are sound; render layers
keep the three classes labelled-adjacent, never fused.

## Honestly deferred (tracked, with reasons)
- **#82 Leak plateau blindness (HIGH).** `PHEN_MEMORY_LEAK` is slope-only (`expect=rising`),
  so a container pegged ABOVE its limit but no longer rising produces ZERO findings — an
  invisible maxed-out container. The robust fix is a SUSTAINED level-crossing check
  (`facet=level`, at-or-above the config bar) — an ONTOLOGY change that must go through
  GOVERNANCE (doc 12: author → new release → classify → re-bind → re-test), not a casual
  edit (`container_memory_rss` is observable, so it is an authoring gap, and "degraded"
  is itself honest). Also reconcile coverage `full` vs engine-`degraded`.
- **#83 surfacing/value (MED):** render the PROJECTED lane in Timeline.tsx (built, not
  drawn); a MEASURED criticality/severity ranking from DECLARED facts (config-bar vs
  default-bar, blast radius) so a payment-path crisis outranks a benign sidecar; numbers +
  drill-down on unexplained cards; decouple the clock-health chip from the serial forecast
  cycle (a slow-but-alive clockd can freeze the surface ~140s); rank default-bar loudness
  below config-bar in the unexplained channel; extend the charter battery to sweep chat +
  a populated timeline lane.
