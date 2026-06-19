# 21 — The Intelligent Agent Harness: control surface, lifecycle & semantic coverage

> **Status:** forward dev track (2026-06-19). **Phase 1 (§5) and Phase 2 (§2.2–2.3) are
> SHIPPED** (branch v5, adversarially reviewed, `just ci` green, graph hash unchanged);
> Phases 3–5 are specified, not yet built. Every item graduates only through the existing
> governance gate (doc 12) and the firewall below.
>
> **Binding constraint:** this doc lives entirely under
> [`docs/01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md) and is the
> concrete, code-grounded build-out of the keystone in
> [`docs/20-dynamic-graph-extension.md`](20-dynamic-graph-extension.md) §1:
> **PROPOSE → VERIFY → PROMOTE. The model authors nothing.** Doc 20 states the principle;
> this doc defines the *control surface, the trigger lifecycle, the review UX, and the
> semantic-mapping path* that operationalise it — each grounded in the code that exists today.

---

## 0. Why this doc exists

The agent shipped today is far narrower than the system needs, and the gaps the operator
keeps hitting (a flat governance queue, "why doesn't promotion move coverage", "every
unseen metric is dead data") all trace back to the same three under-built dimensions:
the agent has **no new proposal kinds**, **no memory or tools**, and **no lifecycle**.

This doc answers four questions the operator raised and turns the answers into a phased
build. It is deliberately code-grounded: every "current state" claim cites the file that
makes it true, so the design is a build spec, not a vision.

---

## 1. Coverage is a JOIN, not an ontology property (the operator's confusion, resolved)

**A phenomenon's coverage is not determined by the ontology alone.** It is computed at
runtime by `binding.PhenomenonObservability` (`obsd/internal/binding/report.go:42`) as the
join of two layers:

- **the AUTHORED ceiling** — which signals are `role=required` members of the phenomenon
  (`participates_in` edges; `report.go:54` — only required members decide observability);
- **the RUNTIME actual** — whether those required signals are obtainable on *this* cluster
  (emitting tool deployed, signal scraped, bar resolvable).

```
coverage(phenomenon)  =  authored required-set   ∧   runtime signal availability
                         └── ontology ───────┘       └── this cluster ──────────┘
full    = every required member obtainable, none out-of-scope/indeterminate
partial = ≥1 required obtainable, but others out-of-scope or indeterminate
none    = zero required obtainable
```

So the ontology sets what *could* be seen; the cluster sets what *is* seen. **`partial`/`none`
is almost always the EMISSION gap**, not an ontology defect: ~335 of the 589 cataloged
signals come from exporters we don't run (apiserver, etcd, CoreDNS, kube-proxy, CRI, cert,
node-CNI), and some that *should* emit don't on this runtime (PHEN_DISK_FILLING is authored
+ forecast-proven yet dark on containerd/overlayfs — no per-container `container_fs_usage_bytes`).

### 1.1 Can a phenomenon be identified from only partial-coverage signals?

Not as a **confirmed MEASURED match** — a match requires its `required` members, and asserting
the phenomenon without them would be fabrication (forbidden, doc 01). But three charter-clean
escape hatches exist and are already in the codebase:

1. **corroborating members missing never blocks** — only `required` do (`report.go:54`);
2. a partial set can surface as a **DEGRADED finding with an honest blind-spot label**
   (the honest-partial-coverage discipline; `blindspots.go`) — a *suspected*, not confirmed, phenomenon;
3. it can **light up in a cascade** if a neighbouring phenomenon's required set IS present
   and the authored trigger→downstream edge fires.

Partial signals therefore raise a flag, corroborate, or join a chain — they do not *declare*.
That ceiling is deliberate.

### 1.2 Do we cover the operationally critical phenomena? (and the missing field)

**No, and there is no `severity`/`criticality` field on phenomena to even rank them** — itself
a gap. Today ~a dozen phenomena are wired with a `THR_` rule + members + span + checks, and
they are concentrated in **one family**: node/container resource exhaustion (OOM cgroup+system,
throttling cascade, eviction, storage saturation, disk/PID pressure, probe-failure-restart,
image-pull, volume-mount). The **dark-but-dangerous** set is the control-plane / DNS / cert /
networking family — `CERT_EXPIRY`, `DNS_FAILURE`, `ETCD_SLOW_PATH`, `NODE_NOT_READY`,
`APISERVER_OVERLOAD_APF`, `CNI_FAILURE`, `SCHEDULING_FAILURE` — all high-harm, all unwired,
nearly all blocked by the emission gap.

**Action (Phase 5):** author a `severity` field on phenomena so governance *and* the agent
prioritise the dark set by harm rather than by ease-of-wiring. This is the prioritisation
input every loop below depends on.

---

## 2. The expanded control surface

### 2.1 What the agent is today (grounded)

| Dimension | Current reality | Source |
|---|---|---|
| Trigger | dumb 5-min ticker, no event triggers, no gating | `main.go:1616,1751` |
| Context | one ~6000-char dump, single LLM call, no retrieval | `dgx/agent.go:54`, `dgx/prompt.go` |
| Memory | **fully stateless** every cycle | `dgx/agent.go:74` |
| Tools | **none** — not even function-calling; one JSON completion | `dgx/provider.go:18` |
| Can emit | `node`, structural `edge`, `member`, `bar_source`, `causal_hypothesis` | `candidate/store.go:20` |
| **Cannot** emit | a phenomenon, a causal edge, or any detection rule | `candidate/store.go:378` |
| Write boundary | nothing reaches the graph; all candidates, human-promoted, firewalled | `dgx/firewall_test.go` |

### 2.2 The three additions (all on the *propose* side; the write boundary never moves)

**(a) New candidate KINDS.** Two, both firewalled, both human-promoted, both must cite MEASURED
evidence:

- **`equiv_group`** (Phase 1, §5) — a proposed semantic mapping of an unmapped *operational*
  stray into an equivalence group (existing or new). On promotion it becomes a regex pattern
  the deterministic resolver absorbs — **the one promotion that actually moves MEASURED reality.**
- **`phenomenon_candidate`** (Phase 4) — a proposed *new failure mode*: a named cluster of
  co-occurring anomalies + a proposed `required`-member set + **a pointer to a declared-config
  bar, or an explicit `unbounded` / `Tier-B-ineligible` marker.** It carries **no learned
  threshold** — borrowed normativity is preserved. This is the *anomaly→candidate-phenomenon
  loop*, the single largest missing capability.

**(b) Tools — from one-shot dump to agentic retrieval.** Replace the 6000-char blind dump with
read-only function-calling over the deterministic stores — the *same surface the MCP server
already exposes* (`internal/mcp`, 15 tools): `query_series`, `get_topology`, `get_coverage_gap`,
`search_equivalence_groups`, `get_recent_anomalies`, `get_phenomenon`, `get_strays`. The agent
*gathers* grounded evidence instead of guessing. **Every tool is READ-only — there is no write
tool, ever.** This is why the MCP-harness track and the agent-expansion track converge on one
tool surface.

**(c) Memory — a proposal ledger.** At the start of each cycle the agent reads its own
firewalled candidate store (`candidate.Store.List`): what it already proposed, what was
promoted, what was *rejected and why*. This kills re-proposal spam (loop-until-dry) and feeds
rejected-with-reason back as in-prompt negative context. It is **not new persistent state** —
the store already records `status`/`decidedBy`/`note` (`candidate/store.go:83`); the agent
just reads it.

### 2.3 Context window & budget

The single 6000-char dump is the bottleneck. With retrieval tools, context becomes a *working
set*: a small triage prompt + tool-fetched evidence on demand. The real limit is the
provider's throughput quota (Groq free-tier 100k tokens/day → 429), already handled with a
10-min backoff (`main.go` rate-limit cooldown) and removable via the provider seam
(`DGX_BASE_URL`, `dgx/provider.go:53`). Define a per-cycle token budget + a daily budget; degrade
gracefully (skip a cycle, never fabricate to fit).

---

## 3. The agent lifecycle (when does it hunt?)

Today: a 5-min ticker with no event triggers and no prioritisation. The lifecycle below
replaces it.

### 3.1 Three trigger classes

1. **Scheduled sweep** (low cadence, ~hourly) — revisit the standing backlog: unmapped
   *operational* strays, partial-coverage phenomena, persistent silence rows. **Exponential
   backoff per unresolved gap** so the same thing is not re-proposed every cycle.
2. **Event-driven exploration** (the important one) — fires when the deterministic path emits
   **an anomaly that matches no phenomenon** (a `departure` band-exit or a capacity-crossing
   with no authored explanation), a **new recurring stray cluster**, or a **new k8s-event type**.
   This is *what event triggers exploration*.
3. **Threshold-gated proposal generation** — exploration only *emits* a proposal when evidence
   crosses a **deterministic support floor**: a co-occurrence recurs ≥N times across ≥M entities
   within a window. This is a *support count*, not a learned threshold — charter-clean, exactly
   like the assoc gate. Below the floor it stays a *watched gap*, not a card.

### 3.2 The task queue

```
gap-detected ──► exploring ──► evidence-gathered ──► proposal-staged ──► promoted | rejected
     ▲                                                                         │
     └──────────────────────── cooldown / backoff ◄───────────────────────────┘ (if rejected)
```

Each gap carries an `attempts` count + a `next_revisit` time — that is *how often it revisits
unresolved gaps*. A rejected proposal feeds its reason back as negative context (§2.2c) and the
gap's interval widens.

---

## 4. Governance UI/UX (how proposals surface)

The governance page (`console/src/routes/governance.tsx`) becomes a proper review surface.
Four additions, each charter-constrained:

- **Support score per card** — and the trap to avoid: it must be **deterministic MEASURED
  support** (recurrence count, # distinct entities, # evidence refs, time span), *never a model
  self-confidence* (that would launder PROJECTED→MEASURED). The `candidate.EvidenceRef` set is
  the input; ranking is a lexicographic function of agreed evidence, never a fitted number
  (`candidate/store.go:49` already states this). Rank cards by support; show the evidence.
- **Diff view** — extend the "ON PROMOTE →" block (already shipped) into a real graph diff:
  the overlay YAML that would be authored + a *re-classification preview* (what coverage /
  observability changes on promote, computed by re-running `binding.PhenomenonObservability`
  against the proposed graph). For an `equiv_group` card this is **"this pattern would also
  capture these N other strays — intended?"**
- **Grouping** — cluster cards by kind and by target entity/phenomenon so the operator reviews
  a *theme*, not 500 rows. The stray classifier already cut 589→31 (`candidate/classify.go`);
  this generalises it.
- **Decision trail** — promoted/rejected + named human + note + timestamp (exists,
  `candidate/store.go:287` `Decide`), *plus* the agent's own rejected-history ("proposed 3×,
  rejected 2×") so persistence is visible.

---

## 5. PHASE 1 — Semantic equivalence-group inference (building now)

### 5.1 The "dead data" problem, precisely

`binding.EquivalenceResolver.Resolve` (`obsd/internal/binding/equivalence.go:68`) regex-matches
each incoming metric against the **35** authored equivalence groups. **Zero matches → quarantine
→ stray** (`observe/scrape.go:370`, `observe/quarantine.go`). The set is *not* exhaustive over
observed metrics — that is the definition of a stray. So today an unseen metric is dead data:
it is counted, never mapped, never usable in detection/forecast/chain. ~545 strays exist; the
object-metadata classifier (`candidate/classify.go`) already filters the kube_* inventory noise,
leaving the *operational* remainder as the real worklist.

### 5.2 The path (PROPOSE → VERIFY → PROMOTE, and promotion actually moves coverage)

This is the highest-leverage *and* most charter-clean candidate kind, because an
equivalence-group pattern is a **deterministic identity rule, not a model belief**:

1. **Propose.** For an unmapped *operational* stray, the agent either maps it to an **existing**
   group (the metric name + labels + unit + canonical-OTel resemblance) or proposes a **new**
   group. It emits a `candidate.KindEquivGroup`:
   - `Subject = "stray:<metric>"`
   - `Payload = { group_id (existing) | proposed_group{id,label,canonical_otel}, pattern, capture_sample[] }`
   - `Evidence = [ measured-series refs proving recurrence + the stray's label shape ]`
2. **Verify (deterministic, before staging).** Grounding (every cited ref must exist in the
   cycle's observations — `dgx/agent.go` refIndex), the evidence floor, a structural check that
   the target group exists (existing) or that `label`+`canonical_otel`+`pattern` are present
   (new), and **the proposed pattern must compile as a regex** — a non-compiling pattern is an
   authoring defect, rejected (mirrors `equivalence.go:51`).
3. **Support score (deterministic).** `candidate.EquivGroupSupport(pattern, strays)` returns the
   **capture set**: which current strays the pattern would match. The score shown is the *count
   of captured strays + label-coordinate overlap* — pure MEASURED support, never a Fellegi-Sunter
   learned probability (a trap doc 20 already killed).
4. **Promote (named human).** Promotion renders a **real `equivalence_groups:` overlay block**
   (not the generic artifact) via `candidate.PromotedOverlayYAML`. A human commits it to
   `ontology/graph/overlays/`, bumps a release, `graphlint -strict` validates it, and on reload
   the deterministic resolver **absorbs the pattern** — the metric stops being a stray and
   becomes mappable. *This is the only promotion that changes MEASURED reality* (contrast the
   generic promote→apply gap noted in the backlog).

### 5.3 The deterministic absorb mechanism (the new overlay extension point)

`graph.overlayFile` (`obsd/internal/graph/overlay.go:209`) gains an `equivalence_groups` block:

```yaml
overlay: equiv-groups-vX
author: <named human>
status: <governance status>
equivalence_groups:
  # add a pattern to an EXISTING group (never redefines canonical):
  - id: EQG_WORKING_SET
    add_patterns: ["^myapp_working_set_bytes$"]
    rationale: "<author's falsifiable claim>"
  # OR define a NEW group:
  - id: EQG_REDIS_CONNECTED_CLIENTS
    label: "Redis connected clients"
    canonical_otel: "db.redis.connected_clients"
    patterns: ["^redis_connected_clients$"]
    rationale: "<...>"
```

Merge rules (in `applyOverlay`, applied with the other authored deltas):
- **add-to-existing**: `id` must already exist; `add_patterns` appended (dedup); `canonical_otel`
  may not change (overlays add, never redefine — mirrors the signal/phenomenon rule at
  `overlay.go:446,465`).
- **new group**: `id` must not exist; `label` + `canonical_otel` + ≥1 `patterns` required.
- **every pattern is `regexp.Compile`-checked at load** — fail loudly (mirrors `equivalence.go:51`).
- `graphlint` gains parity (parse + validate the block) so `-strict` stays honest.

**Graph-neutral until used:** Phase 1 ships the *capability*, not a specific mapping. No
production overlay is added, so the released hash stays `sha256:1f6790cd…` (v0.9.0). The block
is exercised by tests + future promotions only.

### 5.4 Why this closes the operator's loop

More mapped operational metrics → more `required` members obtainable → higher phenomenon
coverage (§1). The "every unseen metric is dead data" weakness becomes a *worklist the agent
triages into proposed groups, a human confirms, and the deterministic path absorbs* — escalation
before ontology, exactly as asked.

---

## 6. Charter boundaries (the invariants every phase must hold)

These are code-review rules, restated so the expansion cannot erode them:

1. **No write tool.** The agent's only output is `candidate.Store.Put` (status=candidate). The
   firewall test (`dgx/firewall_test.go`) must stay green: no detection package imports
   `internal/candidate`.
2. **No learned edge/weight/threshold.** A `phenomenon_candidate` carries a bar *pointer* or an
   honest `unbounded` marker — never a number. A support score is MEASURED support, never model belief.
3. **Co-occurrence ≠ cause.** Causal proposals stay `KindCausalHypothesis` (direction-free);
   structural edges stay the closed set (`candidate/store.go:43`).
4. **Promotion is a named human** through the deterministic governance recompile (doc 12) — the
   harness can block, never approve (`candidate/store.go:294`).
5. **Determinism unaffected.** Candidate-set membership is explicitly NOT part of any replay
   guarantee; detection produces identical results whether the agent ran or not.
6. **The clock wire stays string-free** (`clock.proto`; `clock/conformance_test.go`).

---

## 7. Phased roadmap

| Phase | What | Moves MEASURED coverage? | Status |
|---|---|---|---|
| **1** | `equiv_group` candidate + overlay absorb path + deterministic support | **Yes** (the absorb) | **SHIPPED** |
| **2** | Agent memory (proposal ledger) + read-only retrieval tools (MCP surface) | indirectly | **SHIPPED** |
| 3 | Lifecycle: event/threshold triggers + support-scored, diff/grouped governance UI | no (surfacing) | specified |
| 4 | `phenomenon_candidate` kind + the anomaly→phenomenon loop | yes (new wired phenomena) | specified |
| 5 | Author a `severity` field on phenomena (the prioritisation input) | no (ranking) | specified |

**Phase 2 as built (branch v5 @ `9c815df`):** the Provider gained `CompleteTools`
(OpenAI tool-calling, with `ErrToolsUnsupported` → single-shot fallback); a read-only
`ToolRegistry` of 6 tools (`get_strays`, `search_equivalence_groups`, `get_topology`,
`get_silence_ledger`, `get_coverage`, `get_unexplained`) whose CONTRACT lives in `dgx`
and whose IMPLEMENTATIONS live in `package main` (`dgxtools.go`) so the firewall holds;
a bounded multi-turn loop where tool-returned refs ACCUMULATE into the grounding index
(a proposal may cite only a row the model was shown — truncated rows never become
groundable); and the proposal-ledger memory (`buildLedger` → the prompt). `DGX_TOOLS=off`
is the rollback to single-shot.

Phase 1 de-risks the rest: it is the smallest slice that proves the full PROPOSE → VERIFY →
PROMOTE → *deterministic absorb* round trip end to end.
