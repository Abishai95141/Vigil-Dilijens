# projected-transitive gate — evidence (doc 15 cap. D — forecast the ripple)

**Verdict: PASSED** (`just projected-transitive-gate`, exit 0) — 2026-06-16.

## What it certifies

The one-hop anticipatory cascade made **TRANSITIVE**: a single FORECAST root rippling to
its transitive callers over observed-flow edges, each downstream node inheriting the root's
forecast band **INHERITED and WIDENED per hop**. `flow.ProjectedTransitiveChains` is a pure
function of (flow topology, forecast roots, relation, window, now). The forecast is PROJECTED
by class, so the gate certifies the **JOIN**, not the tick-digest — but the producer is a
pure function, so the Go drift guard proves the frozen corpus reproduces byte-identically.

JOIN, never FUSE, with the weakest-input rule (doc 01): the forecast root and every downstream
impact are **PROJECTED** (with bands that never collapse); the edge stays **MEASURED**; the why
stays **AUTHORED** — each labelled, never fused into "X will cause Y". The clock is run ONCE, at
the root — never per hop (which would stack forecast error and manufacture a causal chain).

## Floors (all green)

| Floor | Result |
|---|---|
| **BAND-MONOTONICITY == 0** (ABSOLUTE-ZERO) | 0 — along every chain, no inherited band ever NARROWS downstream: `earliest_child ≤ earliest_parent` AND `latest_child ≥ latest_parent`, checked hop-by-hop from the root band outward. A downstream node tighter than its parent = instant fail (the producer guarantees it cannot happen) |
| **PROJECTED-CLASS** | OK — every band is class PROJECTED and the root symptom is PROJECTED; no MEASURED class on a forecast-derived node (no class laundering) |
| **ONE-ROOT-PER-CHAIN** | OK — each chain has exactly one forecast root (hop-0 root band); downstream nodes carry inherited bands (hops ≥ 1) |
| **CHAIN/ROOT/PATH-FIDELITY** | OK — emitted count/roots/path == the oracle |
| **CHARTER == 0** | 0 — no causal token in the scaffolding; for a PROJECTED cascade the denylist ALSO bans the propagation verbs (`propagates` / `cascades to` / `flows to`) — the system must never assert the ripple as fact. The authored `why` is excluded (verbatim curated text) |

## Scenarios (corpus/projected-transitive/, 6 total)

- `linear-2hop` — one forecast root (back, band +30..+50) ripples to mid then front; the band
  WIDENS each hop: root `[12:30..12:50]` → hop1 `[12:20..13:00]` → hop2 `[12:10..13:10]` (each
  strictly containing the last). **The headline.**
- `fan-out` — one root impacts TWO direct callers; both hop-1 bands widen equally vs the root.
- `two-roots` — two independently-warned roots → TWO chains (one forecast root per chain), never merged.
- `open-horizon` — a root whose far edge is open propagates an OPEN band (never a collapsed line).
- `no-caller` — a lone forecast root with no caller is a warning, not a cascade → 0 chains.
- `no-forecast` — flow edges but no forecast root → 0 chains, never invented.

## Machinery

- **Core:** `obsd/internal/flow/projected_transitive.go` — `ProjectedTransitiveChains` BFS-walks
  the transitive callers of each forecast root over `NeighboursInto(EdgeTypeFlow)`, deterministic
  (sorted, `SliceStable`, visited-set, `TransitiveMaxHops`). The widening per hop is DERIVED from
  the root's own band (half the root band's width on each side — not an invented constant), so it
  scales with the forecast's own uncertainty; `earliest` is clamped to `now`. `flow.Chain` gains a
  per-step `Band *ProjectedBand` + a chain-level `RootBand` (all omitempty — the MEASURED chains B/
  one-hop are byte-unchanged). The producer ASSERTS `bandContains(parent, child)` at construction.
- Regen: `REGEN_PROJTRANS_CORPUS=1 go test ./obsd/internal/flow -run RegenProjTransCorpus`.
- Always-on guards: `TestProjTransCorpusFrozenConsistent` (determinism) + 8 `ProjectedTransitive*`
  unit tests (two-hop widening, earliest-clamp, open-band, one-root-per-chain, no-caller, no-relation,
  maxHops, determinism).
- Scorer: `harness/src/harness/projected_transitive_gate.py`; 9 regression tests (good corpus passes,
  one mutation per floor incl. TWO band-narrowing mutations + a "ignores authored why" positive).
- Recipe: `just projected-transitive-gate` (Go -race + Python).

## LIVE-WIRED on kind-vigil (2026-06-16) — the gate-pending posture

`obsd --flow-enabled` against a live 3-service flow chain (`front→mid→back`):
- The **MEASURED** root-cause lane (cap. B) is active at `/api/root-cause-chain`:
  `traffic/back → traffic/mid → traffic/front`.
- The **multi-hop PROJECTED** lane (cap. D) is WIRED IN and computes every tick (off-digest),
  and correctly surfaces its honest **gate-pending** state: `projectedActive: false`,
  `projectedChains: []`, note = *"Multi-hop projected cascade is COMPUTED every tick but NOT yet
  operator-visible: a new PROJECTED class ships only after a real 2-hop lead+confirm capture
  validates it on the cluster."* This is exactly the doc 15 §4.D posture — the surface stays dark
  until a genuine 2-hop forecast lead+confirm is observed.
- **Non-gating**: the lane is a pure consequence of the warned forecast roots + the captured flow
  topology — zero bytes into the replay digest (off-digest like the one-hop Phase-E lane).

## Honest scope (the documented gate-flip step)

The DETERMINISTIC producer gate PASSES and the lane is wired + computing live. What remains is
the **live gate-flip** (task #128): a real 2-hop forecast lead+confirm capture — `back` leaking
container memory toward its declared limit (working_set is the forecast-eligible series), the
forecast warning it ~10 min early, the D lane producing the 2-hop projected cascade, the MEASURED
cascade later confirming. That capture flips `phaseDProjectedTransitiveGatePassed` to true and
makes the lane operator-visible. The one-hop forecast→cascade path this extends was already
live-proven (Phase E, `f3c24b2`); D inherits its warned-root seeding.
