# 28 — Governance & Causal-Hypothesis Cohorts: Findings

> Status: FINDINGS / scoping notes (branch `v6`, 2026-06-21). Code-grounded map of why
> the governance and causal-hypothesis surfaces flood with thousands of items, the
> aggregation primitives that already exist, and a candidate abstraction. A build plan
> follows separately. No code changed.

## 1. One disease, two faces

Both surfaces are fed by the **same** firewalled candidate store
([`candidate/store.go:86`](../obsd/internal/candidate/store.go)). Causal hypotheses are
literally `KindCausalHypothesis` candidates; the governance queue is every *actionable*
candidate. So both inherit the same flaw:

> **Thousands of atomic candidate rows, each a discrete human decision, with no
> aggregation primitive at the data layer.**

The frontend barely mitigates it: governance groups by `kind` in collapsible accordions
([`governance.tsx:536`](../console/src/routes/governance.tsx)); causal is a flat dump
([`causal-hypotheses.tsx:122`](../console/src/routes/causal-hypotheses.tsx)). Every item
is still a DOM node and still asks for an individual promote/reject. Virtualization alone
would make rows scrollable but still demand N decisions — **the scarce resource is the
decision, not the row.**

## 2. Cardinality drivers (why thousands — it's structural, not a bug)

| Surface | Driver | ~Count | Source |
|---|---|---|---|
| Governance | CEI-fallback: ~1 node + edges **per stray metric** | ~650 | [`candidate/er.go:71`](../obsd/internal/candidate/er.go), [`main.go:3255`](../obsd/cmd/obsd/main.go) |
| Governance | dgx-agent: one edge **per (stray, entity)** pair | ~200–300 | [`dgx/agent.go:314`](../obsd/internal/dgx/agent.go) |
| Governance | phenomenon candidates: **one per signature** (already aggregated) | ~dozens | [`candidate/phenomenon.go`](../obsd/internal/candidate/phenomenon.go) |
| Causal | co-onset: **O(pairs of co-stepping streams)**; top-30/cycle but a *different* 30 each cycle; **no total cap, no expiry** | 171+ and climbing | [`cohypothesis/cohypothesis.go:48`](../obsd/internal/cohypothesis/cohypothesis.go), [`main.go:1898`](../obsd/cmd/obsd/main.go) |

Key contributing facts:
- Candidate identity is content-keyed (SHA256 of kind/subject/relation/identity-payload/
  evidence, [`store.go:516-563`](../obsd/internal/candidate/store.go)). Re-staging the
  same item **updates in place** — so dedup of *identical* items works. The flood is
  **distinct** items, not duplicates.
- Causal hypotheses have **no total cap and no TTL** — staged pairs accumulate in SQLite
  at `status=candidate` indefinitely; the top-30 cap is per-cycle only.
- The governance APIs **dump everything** — no pagination, filter, or grouping at the
  wire ([`api/governance.go:81`](../obsd/internal/api/governance.go),
  [`api/candidates.go:40`](../obsd/internal/api/candidates.go)).

## 3. Aggregation primitives that already exist (the seeds)

The system already has the right idea in two places — they're just not the default unit
of review:
- **`KindEquivGroup`** ([`candidate/equivgroup.go:12`](../obsd/internal/candidate/equivgroup.go))
  collapses a whole stray *family* into one promotion, and it is the **one** promotion
  that moves MEASURED coverage. It already computes how many other strays a pattern would
  also capture.
- **`KindPhenomenonCandidate`** aggregates recurring anomalies by **signature**
  (entity_kind + sorted metric_set) — one candidate per signature, not per observation.
- **`suppressedMetadata`** ([`api/governance.go:93`](../obsd/internal/api/governance.go))
  is an existing precedent for *honest aggregation*: object-metadata strays are counted
  but withheld from the review queue with a stated reason.

Causal already filters (not groups): same-entity / same-pod skip, co-onset window,
`r≥0.85` floor, 512-stream subset cap ([`cohypothesis.go:62-189`](../obsd/internal/cohypothesis/cohypothesis.go)).

## 4. Candidate abstraction: the **Review Cohort**

A deterministic, surfacing-layer rollup of candidates that share an *authored* grouping
key, shown as **one review unit** — a view over the immutable store (**join, never
fuse**). Members keep their own ID/provenance/evidence/status. The cohort carries
MEASURED-only aggregation: member count, distinct entities/metrics, evidence total,
**strongest** support (the existing lexicographic max), age range, status histogram, and
a `representative` (highest-support member as its face).

**Charter-safe bulk action (the load-bearing part):** a named human can **reject-as-group**
with one signed reason, or **promote-as-group** *only* where a single authored artifact
covers the members (the equiv-group already is exactly this). One gesture + one authored
note = N individual human decisions applied transactionally via the existing `cs.Decide`,
each recorded per-member. The system still never approves.

### Grouping keys (all deterministic — no learning)
- **Governance:** equiv-group (primary stray unit → collapses the ~650 bulk) → metric-family
  prefix (`mysql_*`, `kube_pod_*`) → target-entity (edges grouped by what they point at).
- **Causal:** co-onset *episode* (pairs in one disturbance window; ~1–3 episodes, labeled
  "co-occurrence in a window — not a shared cause") → connected-component (undirected
  cluster of co-stepping streams; direction still authored edge-by-edge) → entity-pair
  (collapse repeat observations + count).

## 5. Charter constraints any solution must hold

- **Join, never fuse** — cohort is a view; rollup is MEASURED counts/maxes, no
  stronger-class claim.
- **No learned grouping** — prefix / entity-extraction / connected-components only.
- **System never approves** — bulk action is a named human's per-member decision; the
  harness can still only block.
- **Determinism/replay** — cohorting is off-digest surfacing; golden-file testable; must
  not touch detection fingerprints.
- **MCP operator-parity (doc 23)** — cohorts get *read* tools; writes stay human.
- **Direction-free causal** — episodes/components stay undirected; no post-hoc fusion.

## 6. Where the rollup should live

The api surfacing layer (`obsd/internal/api`), not React — because of the MCP-parity rule
(the agent is "an operator", doc 23) and the golden-file test discipline: one source of
truth, every client and the MCP agent see the same cohorts. The existing `groupPending`
accordion generalizes into the frontend renderer.

## 7. Open items (pending the incoming plan)
- Total-cap + TTL/expiry for staged causal hypotheses (none today).
- Whether equiv-group becomes the *default* stray review unit (reduce-at-source) vs. only
  a cohort dimension at surfacing.
- Filter/search/sort + drill-down virtualization on both pages (absent today).
