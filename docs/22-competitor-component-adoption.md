# 22 — Competitor Component Adoption Plan (branch `v6`)

**Status:** PLAN — written before any component dev, per the working agreement.
**Owner doc for:** the decision of *which* parts of the ABB competitor
(`GreaseMonkeyIT/ABB_Accelerator_Proto`, branch `mark-one`) are worth bringing
into Vigil, *how* they survive the charter (doc 01), and *how each will be proven
to actually work* — not merely to pass a unit test.

> Discipline for this whole arc (restating the engineering mandate, CLAUDE.md):
> **build robust and complete; never fall back to a shallow proxy to make a goal
> look met.** Every component below earns its place with a real experiment against
> real behaviour. **If a component proves valueless after honest dev, it is
> scrapped and recorded as scrapped** — not dressed up to look like it worked.

---

## 0. Where this comes from

Two inputs, both grounded in code (not docs or claims):

1. **An adversarial head-to-head** of Vigil vs the competitor (18-agent review +
   independent code reads). Net: Vigil wins on integrity/determinism/honesty;
   the competitor's one genuine edge is that it *infers* a root cause for faults
   nobody pre-authored — the thing Vigil's charter forbids.

2. **Empirical validation of the competitor's REAL engine.** Their
   `correlation/engine/*.py` + `service.py` were imported **unmodified** into an
   isolated venv (their exact numpy/scipy/networkx pins) and driven with
   physically-motivated inputs by a labelled harness (input generators only —
   the code under test is 100% theirs). Their 30-test suite passes (1.7s). Then a
   battery of happy-path **and** adversarial experiments:

| # | Probe (their real code) | Captured result | Read |
|---|---|---|---|
| E2 | determinism across `PYTHONHASHSEED` 0/1/42 | byte-identical SHA | ✅ real |
| E7 | S1 disk contention (writer→staller) | roots `cooling-monitor`, r=0.992 | ✅ works |
| E8 | CUSUM: 300× pure noise / 300× real step | **3.3%** false-onset, **100%** detect | ✅ robust |
| E9 | edge memory across a pod restart | survives, workload-keyed, renders vs new pod | ✅ real, portable |
| E5 | true aggressor **outside** the `STORAGE` allowlist | 0 edges — **aggressor invisible** | ⚠ real blind spot |
| E3b | direction at **true zero-lag** | draws a directed edge 170/200×, root split 137/33 — **invents direction with no physical basis** | ⚠ narrow, real |
| E4b | common-cause confounder (no A→B) | gate **accepts** r=0.999 pair; pipeline **invents `svc-a→svc-b`, roots svc-a** | ⚠ real |
| E6b | slow-leak baseline absorption | alarms ≥+0.002/cyc; only +0.001/cyc absorbed | ⚠ pathological-slow only |

**Honest correction to the earlier review:** three claimed weaknesses were
*narrower* than alleged. The direction "coin-flip" only bites at genuine
zero-lag (any ≥5 s real lead is stable); the baseline only absorbs a
pathologically slow leak. The one that is *worse* than credited: the
physical-coupling clause filters *unrelated* pods but **not confounded** ones —
two disk-sharing pods both stalled by a third driver get a fabricated directed
edge (E4b). That is exactly why we will **never** take their auto-direction as
truth.

---

## 1. The adoption thesis

Take **two** things; reframe both to fit the charter; refuse one; gate one on a value test.

- **(A) Churn-stable workload identity for forecasting.** E9 proves keying a
  series by stable *workload role* (not ephemeral pod) survives HPA/rollout/OOM
  churn — Vigil's known weak spot. **Vigil already has this** (`forecast/roleseries.go`);
  the competitor independently validates the *approach*. → validate under real
  churn, resolve an open bar-semantics question, decide on enabling. (Component **C1**.)

- **(B) A human-in-the-loop causal surface.** The competitor's value is proposing
  a root for an un-anticipated fault. Its *direction* is unreliable (E3b/E4b) and
  charter-forbidden as an asserted cause. The charter-clean form: surface the
  **direction-free association** ("A and B are coupled and both stepped within
  window W") and **let the operator author the direction**. Most of this already
  exists in Vigil (`KindCausalHypothesis`, `assoc`, `audit`, governance). → add a
  co-onset producer + a dedicated tab. (Components **C2**, **C3**.)

- **Refused:** their auto causal-direction inference. E4b shows it manufactures
  edges from confounders; the charter forbids co-occurrence→cause. Not ported in
  any form that asserts direction.

- **Value-gated:** the CUSUM onset detector (C2) is taken **only if** it adds
  real value as the timing input to (B) / sharpens the unexplained channel. If it
  doesn't beat Vigil's existing primitives in practice, it is scrapped.

---

## 2. Component plans

Each component states: **what's already there**, the **charter-clean design**,
the **exact integration points** (file:line, from a grounded code map), the
**real validation** (how we prove it works), and the **scrap criteria**.

### C1 — Forecast role-series (churn-stable identity) — *validate + resolve + enable*

**Already there (verified):** `obsd/internal/forecast/roleseries.go` —
`RoleSeriesReader` aggregates a deterministic per-bin **sum of live member pods**
of a workload role over `[now-Window, now]`. The "short-context" bug (parsing the
synthetic stream id on the *first* vs *last* `\x1f`) is **already fixed**
(`roleseries.go:191-202`) with a passing regression test
(`roleseries_test.go:111-161`; all 4 role-series tests green). Wired behind
`--forecast-role-series` (`main.go:127`, default **off**, byte-identical when
off), using Vigil's real OwnerReference-derived CEI identity
(`identity/cei.go`, `main.go:rollUpTargetsToRole / buildRoleMembers`) — **not** a
string hack. So this is not a bug-fix task.

**Bar-semantics risk — RESOLVED (the original SUM was unsound; fixed to worst-member).**
The bug was real and is **proven on the real code**: the role series was the per-bin
**SUM** of members, compared against a **per-pod** bar. A driving test through the actual
`RunCycle` showed three healthy replicas at ~210 bytes each summing to **622.8 > 486** →
`SilenceAlreadyCrossed`, i.e. the role forecast was *dead for every multi-pod workload* —
the exact case role-series exists for. A single real leaker (438, below its 486 limit)
summed to **752**, so even a true leak could not fire.

The fix (committed): role aggregation is now the **worst member toward the bar** — per-bin
`max` for an `above` bar, `min` for a `below` bar (`combineToBar`), so the series stays
comparable to the per-pod bar it is judged against. The churn-stable question becomes "is
**any** member about to cross **its own** bar." Option (b) from the original list, chosen
because **every authored bar is per-entity** (entity_scope ∈ Container/Pod/Node/PVC; none is
role/aggregate-scoped), so a sum has no valid bar in the ontology. Direction is plumbed from
the rolled-up targets into the `RoleSeriesReader`.

**Real validation (done, deterministic, driving the real `RunCycle`):**
- 3 healthy replicas (each ~210, bar 486) → worst-member **207.9** → **forecast fires** (was a dead `already-crossed`).
- single leaker among healthy → role tracks the leaker exactly at **438** (not the 752 sum) → fires.
- **churn rescue**: 4 short-lived pods (6 bins each, < MinContext) → per-pod is `short-context`-dead; the role worst-member series is **24 continuous points** → forecast fires. Role-series adds capability the per-pod path structurally cannot.
- full `go test -race ./...` green; `cmd/obsd` builds CGO-free; non-role path byte-identical (delegates unchanged).

**Remaining (honest):** this is unit-level proof against the real pipeline with a *scripted*
clock. The flag stays **opt-in / gate-pending** until a **live churn backtest** against real
TimesFM + a churning cluster certifies band coverage/accuracy (the standing forecast-class
gate) — not faked here. Enabling-by-default waits on that gate.

### C2 — CUSUM onset primitive (MEASURED timing) — *value-gated*

**Already there:** nothing. No changepoint/onset code exists in Vigil; the
"three primitives" (threshold, rate, co-occurrence) are a **closed, complete
set** by commitment (`observe/primitives.go:11-17`, `observe/doc.go:15`).

**Charter constraint (the crux):** adding a 4th primitive *on the deterministic
digest path* would amend the charter's closed-primitive commitment — **not doing
that**. The safe form is an **off-digest MEASURED producer** (mirroring how
`departure` is an off-digest PROJECTED producer): it computes a deterministic
arithmetic fact — "this series stepped at time *t*, with this sharpness" — never
touches `replay.Digest`, never feeds the detection digest, and is **not** an
"anomaly score" (it carries a timestamp + magnitude, not a novelty number).

**Why it might be worth it:** the onset *time* is exactly the input C3 needs to
say "A and B both stepped within window W" (temporal adjacency), and it sharpens
the unexplained channel's "loud since *t*." Implementation = **re-implement** the
CUSUM/EWMA math in Go (validated robust in E8) — we do **not** copy their Python;
we reproduce the algorithm and re-prove the robustness bar in Go.

**Real validation:** replicate the E8 battery in Go (≥300 pure-noise realisations
→ false-onset rate; ≥300 real-step → detection rate), require comparable numbers
(false-onset < 5%, detection > 95%), `-race`, injected clock, golden determinism.

**Scrap criteria:** if onset timing does not materially improve C3's hypotheses
or the unexplained "loud since" surface beyond what threshold/rate already give,
**scrap it** and record why. (It is a means to C3, not an end.)

### C3 — Direction-free CausalHypothesis tab — *the charter-clean reframe*

**Already there (~70%):** `candidate.KindCausalHypothesis` — "direction-free
co-occurrence, surfaced labelled, never a cause" (`candidate/store.go:17-28`,
`doc.go:30-33`); the `audit` lane already stages direction-free
"observed-adjacency" hypotheses (`audit/audit.go:263-309`); the `assoc` package
already emits MEASURED undirected `associated-with` edges (`assoc/assoc.go`,
surfaced at `/api/dependency`); a named-human authoring/promotion path already
exists (`api/governance.go`, `POST /api/governance/decide`, overlay YAML out).
Today these are surfaced **mixed** into the `/governance` tab.

**What we add:**
1. **A co-onset producer** — from a pair that is **coupled** (authored topology
   *or* an `assoc` `associated-with` edge) **and** whose members both register a
   C2 onset within a window, stage a `KindCausalHypothesis` (relation
   `co-occurrence`, **direction-free**). This is the charter-clean reframe of the
   competitor's engine: we surface the association, **not** a direction.
2. **`GET /api/causal-hypotheses`** — a dedicated view filtering just the
   direction-free hypotheses + their supporting associations, labelled as
   **PROJECTED / association** (`api/` new file, route in `api/server.go`,
   provider in `main.go`, mirroring the candidates/governance providers).
3. **A new console tab "Causal hypotheses"** (`console/src/`: `nav.tsx` entry,
   `router.tsx` route, `routes/causal-hypotheses.tsx`, `api/{client,types}.ts`) —
   shows projections + associations, and lets the operator **author the causal
   direction**, which flows through the existing governance decide path to become
   an **AUTHORED** edge.

**Charter invariants (non-negotiable):** the hypothesis is **always
direction-free**; it **never** feeds detection (the candidate firewall already
guarantees this — `candidate/firewall_test.go`); only a **named human** supplies
direction; PROJECTED model hints (if any) are discarded at promotion.

**Real validation:** end-to-end — stage a hypothesis from a genuinely coupled
co-onset pair, surface it in the tab, have the operator author a direction,
confirm it becomes an AUTHORED overlay **and** that the deterministic detection
digest is **byte-identical** before/after (the firewall holds; replay
determinism unbroken).

**Scrap criteria:** if the co-onset producer yields mostly spurious pairs (every
co-stalling pair on a shared node — the E4b/E5 failure mode), **tighten or scrap
the auto-producer** and keep only the manual `assoc`→author path. Better no
hypothesis than a noisy one.

---

## 3. How the pieces compose

```
C2 onset (WHEN each series stepped, MEASURED, off-digest)
        │
        ▼
coupling (WHICH pairs relate: authored topology  ⋃  assoc associated-with)   ← already in Vigil
        │
        ▼
C3 direction-free CausalHypothesis (staged candidate, firewalled)
        │
        ▼   operator authors DIRECTION (named human, governance)
        ▼
AUTHORED edge in the graph  →  may then light up as a normal cascade
```

`C1` (role-series) is **independent** — a forecast-path reliability fix, not part
of the causal pipeline.

---

## 4. Validation philosophy — no shallow proxies

For each component the bar is *behavioural*, not *test-passing*:

- **C1:** quantitative forecast accuracy + band coverage under simulated churn vs
  the per-pod baseline; a *meaningful* bar on the role series. Not "the unit test
  is green."
- **C2:** reproduce the E8 robustness numbers in Go on an adversarial noise/step
  battery; determinism under `-race` + injected clock.
- **C3:** a real end-to-end author-the-direction round-trip with a
  before/after **replay-digest byte-identity** check proving the firewall held.

Anything that can't clear its bar is scrapped and the scrapping is written down.

---

## 5. Tree state found on `v6` (must resolve before committing)

`v6` was branched from the `v5` tip carrying uncommitted KG work. The tree is
currently **red** on the graph release invariant (4 failing tests in
`obsd/internal/graph`), independent of any component above:

- `ontology/graph/k8s_signal_kg.json` is **modified**; a new overlay
  `overlays/init-container-failure-v1.yaml` is **untracked** (overlay count = 18,
  the pinning test expects 17).
- The loaded graph hashes to `sha256:a25a3e0f…`, which matches **no** release
  manifest. `v0.10.0` (tracked) pins `1caf5586…`; `v0.11.0/v0.12.0/v0.13.0` are
  **untracked** and pin three *other* hashes (`c5a3c064…`, `b04e22a8…`,
  `6c9e75be…`) — none equal to the current graph.

**Interpretation:** the init-container KG work is authored but its release was not
cut to a consistent green state. **The component packages (forecast/observe/api/
candidate/console) are orthogonal** to this — they test green independently — so
C1–C3 dev is not blocked. But committing on a red tree violates the "works as
intended" mandate, so the KG release must be finalised (or the WIP isolated)
first. This needs a one-line confirmation of release intent (next section) before
I cut anything in your KG history.

---

## 6. Sequencing

0. **Green the tree** (finalise the KG release per your confirmation) — commit the
   init-container work as one clean commit.
1. **C1** role-series: churn experiment → resolve bar-semantics → enable/gate decision.
2. **C2** CUSUM onset (off-digest, Go) → E8-equivalent validation → keep/scrap call.
3. **C3** direction-free hypothesis producer + `/api/causal-hypotheses` + console
   tab → end-to-end author-the-direction + replay-digest check.

Each lands as its own commit on `v6` with its validation evidence in the message.
