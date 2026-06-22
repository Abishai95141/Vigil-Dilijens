# 33 — The coverage frontier + the governed inference agent (PROPOSE → VERIFY → PROMOTE → USE)

> Status: SCOPE / DESIGN. Defines (1) why "expose every signal" still leaves phenomena
> uncovered and what actually closes coverage, and (2) how we widen the agent harness to
> *infer the "why"* Vigil's deterministic path deliberately refuses to — as **governed,
> human-ratified proposals that, once authored, feed forward into future detection.**
> Companions: docs/20 (dynamic graph extension / candidate firewall), docs/21 (agentic
> retrieval), docs/22 (C3 direction-free causal hypotheses), docs/29/31 (lead-lag + PCMCI),
> docs/15 (flow + cascade), docs/01 (epistemic charter).

---

## 0. The reframe — there are TWO problems, not one

"We expose everything and it's still not covered" conflates two structurally different things:

- **A coverage problem** (closeable): some phenomena are dark for finite, enumerable reasons
  — a signal isn't emitted, or has no authored bar, or a required member is unobservable on
  this substrate. This is **deploy + author** work, and it can be driven to completion.
- **An inference problem** (charter-bounded): some questions ("which pod *caused* the CPU
  spike?", "is the PVC I/O *why* the pod restarted?") cannot be answered by the deterministic
  engine **by design** — answering them is *inventing direction*, which the charter forbids
  (doc 01). This is not a coverage hole to be plugged with more scraping; it is the job of a
  **governed inference layer** whose output is a *proposal a human ratifies*, never a system
  conclusion.

Everything below separates these and gives each its own closure path.

---

## 1. Coverage, defined: the five-stage pipeline

A phenomenon becomes a *full finding* only when all five stages hold:

```
scrape → EMIT → MAP (authored bar) → MEMBER-CONJUNCTION (every required member observable) → MATCH
```

The matcher requires a **conjunction of required members** (`detect/match.go:274-377`):
`Quality=full` ⇔ `RequiredUnobserved==0`; `degraded` if ≥1 member met but others unobservable;
a lone bar-crossing with no completed conjunction is **"loud"** — *not a finding*
(`match.go:31,359`). Degraded below `MinCompleteness` is hidden (anti-fatigue, doc 07 §3.6).

**Turning on every lane closes only EMIT** for the modalities our current tools produce. The
rest is four distinct gaps:

| Gap | Definition | Examples (this cluster) | Closes by | Deterministic? |
|---|---|---|---|---|
| **A — Emission** | no tool emits it, even all-lanes-on | CERT_EXPIRY (no cert `/metrics`); DNS_FAILURE (CoreDNS `/metrics` unscraped, `obtain.go:103`); APISERVER_OVERLOAD_APF (apiserver `/metrics` unscraped) | **deploy a source / scrape-lane / probe** | ✅ (deploy) |
| **B — Mapping / bar** | emitted, no authored bar (unbounded) or non-level bar | the 28 `unbounded` pairs; `eligibility.go:54` | **author a bar/overlay** (or customer SLO) | ✅ (author) |
| **C — Member completeness** | emitted + bar, but a required member is structurally unobservable on this substrate | PVC_FILLING (loud, conjunction incomplete); STORAGE_SATURATION (per-device disk-IO sub-var has no check, `detect-conditions-v3.yaml:25-42`) | **author the missing member check, OR re-scope the required-set as substrate-gated** | ✅ (author / re-scope) |
| **D — Attribution / why** | signal is node-wide / role-scoped; cannot be pinned to a culprit from the metric alone | THROTTLING_CASCADE rooting on node-exporter not the burn source; PVC-I/O ↔ restart | **NOT closeable deterministically — charter** | ❌ by design → inference agent |

**The coverage frontier** = for every dark phenomenon, the *exact* action that lights it,
derivable from `binding/report.go` (observability full/partial/none + MissingReasons) and
`binding/obtain.go` (out-of-scope reasons). A/B/C are a checklist; D is handed to §2.

---

## 2. The governed inference agent — scope

**Principle: the agent is a *why-proposer*, never a *why-author*.** "Never invents why" is
preserved by **where output lands**: every inference is a `status=candidate` **PROJECTED**
hint that surfaces on governance / the causal-hypotheses page, where a **named human authors
or rejects** it (`store.go:428-430`). Precedent: PCMCI already emits a direction *hint* a
human authors (`cohypothesis/discovery.go`) — the agent's why is another PROJECTED hint into
the **same human-authoring gate** (`/api/causal-hypotheses/author`, `main.go:2199-2253`).

### 2.1 Decisions (locked)
- **Inference engine = LLM-led synthesis** (DeepSeek, OpenAI-compatible via
  `NewOpenAICompatibleProvider`, `DGX_BASE_URL=https://api.deepseek.com`, key in env, never
  committed), still under the harness gates. The deterministic witnesses (lead-lag + co-onset
  + PCMCI) **gate** whether a direction may be suggested; the LLM **synthesizes** the
  attribution + rationale across signals.
- **Surface the gate-pending lanes now**, labeled PROJECTED (band-departure `departure.go:79`;
  cross-service / 2-hop cascade `flow/cascade.go`), using this cluster's live cascade captures
  as the backtest evidence the gate awaited (doc 11 §3.5). They become both operator-visible
  (PROJECTED tier) and agent input.
- **Authored causality must feed forward** ("once mapped, used in upcoming events") — the
  keystone, §3.

### 2.2 Scope IN
1. **Widen agent input** — new read-only retrieval tools mirroring the MCP surface so the
   agent can see: causal-hypothesis rows **with the lead-lag witness** (`leadlag.go:85`,
   `lagConsistentWithOnset`), right-sizing advisories, dependency/assoc edges, cross-service /
   root-cause chains, onsets, and the newly-surfaced pending cascades.
2. **Widen agent output** — a *suggested-why* candidate (attribution / cross-fault / directed
   causal hypothesis) carrying a suggested direction + LLM rationale, admissible **only when
   two independent MEASURED witnesses agree** (co-onset order ∧ detrended lead-lag sign;
   `lagConsistentWithOnset==true`; + PCMCI if present). The data must already support the
   direction *two ways* before the LLM may name it.
3. **Three inference products**, each a governance candidate:
   - **Attribution** (gap D): "stream-processor is the likely CPU-burn source — its
     `cpu_usage` onset led node CPU-PSI by N bins; lead-lag agrees."
   - **Cross-fault**: "datalake restarts are downstream of write-I/O page-cache OOM"
     (assoc `fs_reads~restarts` 0.978 + OOM event + lead-lag).
   - **Optimization rationale**: ties right-sizing `resize-up stream-processor` to the
     throttling incident.
4. **Surface pending lanes** in a PROJECTED tier (decision above).
5. **Coverage-frontier proposer** — the agent emits the A/B/C closers it can (bar_source for
   B, member for C, named *deploy* action for A) so the frontier is actionable, not just a list.

### 2.3 Scope OUT — charter lines that do NOT move
- No write tool; output is candidates only (`dgx/doc.go:12`).
- Agent output never feeds detection/forecast — the 3 firewalls (hash/import/wire) stay;
  replay byte-identical (`firewall_test.go`).
- The system never auto-authors a direction; promotion requires a **named human**.
- Provenance stays MEASURED/PROJECTED/AUTHORED; agent suggestions are PROJECTED, **discarded
  at promotion** (human writes the authored note).
- The structural causal guard stays for graph *edges* — a directional why lives only as a
  *hypothesis candidate*, never as a detection edge until a human authors it.

### 2.4 Safety gates on the LLM why-proposal
grounding floor (every cited ref must exist) · evidence floor · **dual-witness agreement**
(direction only when co-onset ∧ lead-lag agree) · structural causal guard (edges) ·
honest-verify (human ratifies; the harness can block, never approve).

---

## 3. The keystone — closing the loop (PROPOSE → VERIFY → PROMOTE → **USE**)

"Once causality is mapped it should be used in any upcoming events." The first three steps
exist; **USE** is the new requirement:

1. **PROPOSE** — LLM agent stages a directed causal candidate (gated by §2.4).
2. **VERIFY** — deterministic governance gate (grounding/evidence/structural) pre-filters.
3. **PROMOTE** — a named human authors the direction → **must write a `phenomenon_relation`
   edge** (upstream-degradation → downstream-impact), not merely an authored note, so it is
   the *kind of artifact the cascade detector reads*.
4. **USE** — on release, the cascade / cross-service lanes (`detect/cascade.go`,
   `flow/cascade.go`) read AUTHORED `phenomenon_relation` edges; the next occurrence of the
   same cause→effect lights up as a **recognized authored chain**, not an unexplained
   co-incidence.

**The work in P5 is to guarantee step 3 emits a `phenomenon_relation` overlay** (today the
causal-hypothesis author path writes an authored overlay; we must ensure its TYPE is the
relation the cascade detector consumes) **and prove step 4 with a re-injection test.**

---

## 4. Phasing + tests

| Phase | Deliverable | Test |
|---|---|---|
| **P1** | Coverage-frontier surface: per dark phenomenon, the exact A/B/C closer (from report.go/obtain.go reasons) | every `none`/`partial` phenomenon shows a concrete closer; A/B/C/D classification matches §1 |
| **P2** | Surface gate-pending lanes (band-departure, cross-service/2-hop cascade) in a PROJECTED tier | the live captures from docs-32 session render as PROJECTED; replay digest unchanged |
| **P3** | Widen agent input (retrieval tools: causal-hyp+lead-lag, right-sizing, dependency, cascades, onsets) | agent context includes the new sources; grounding refs resolve |
| **P4** | Widen agent output (LLM-led suggested-why candidate; attribution/cross-fault/optimization proposers; governance + causal-hyp page render the suggestion beside the witnesses) | DeepSeek proposes the THROTTLING attribution + PVC-I/O↔restart; dual-witness gate rejects unsupported directions; firewall tests pass |
| **P5** | Close the USE loop — see status below | persistence test + the cascade USE proof |

### P5 status (implemented 2026-06-23)
The USE loop has **two halves, and both are real today**:
- **Phenomenon-level (cascade) — WORKS.** Authored `phenomenon_relation` edges (upstream-degradation → downstream-impact) are read by the cross-service / transitive cascade and drive recognition on *future* occurrences. Demonstrated live: the historian-outage incident lit `asset-api PHEN_APP_DATA_STALENESS → operations-dashboard` over the MEASURED flow edge + the AUTHORED relation. This is "authored causality used in an upcoming event," already shipped.
- **Series-level (causal-hypothesis) — PERSISTS.** A named operator authoring a direction (`/api/causal-hypotheses/author`) promotes the candidate; `store.Put`'s `ON CONFLICT` updates only payload/evidence/lineage, **never status/decision**, so a future co-onset of the same pair re-stages without resetting it — the authored direction sticks and the pair is never re-surfaced as unexplained. Verified live (promoted:1, candidate count dropped) + pinned by `TestPromotionPersistsAcrossRestage`.

**Residual (honest):** the BRIDGE between the two — turning an agent-suggested *series* direction (P4) into a *phenomenon_relation* (cascade) — is a larger architectural step, because a causal hypothesis is between two metric SERIES while a `phenomenon_relation` is between two PHENOMENA over a workload flow edge. Authored series-directions persist and inform the causal surface; they do not yet auto-generate phenomenon_relations. That mapping (series-pair → phenomenon-pair) is the next build, not claimed done.

Charter discipline per phase: each new surface is off-digest until its backtest gate passes;
the deterministic path stays byte-identical; the agent authors nothing.

---

## 4b. Follow-ups shipped 2026-06-23 (post-review)

- **Emission win, acted on.** `--assert-capabilities CAP_CONFIG_PSI,CAP_CGROUP_V2,CAP_KUBELET_PSI_FG` — the operator asserts node caps obsd cannot API-derive (verified via the docs/32 preflight). Live: **5 phenomena flipped PARTIAL/NONE → FULL** (phenomenaFull 21→26, emission frontier 18→13), graph hash unchanged. NB: this moves **phenomena observability**, not the bar-coverage *vector* (watched pairs / resolvability) — they are distinct "coverage" notions.
- **Governance de-flooded.** A bare `cei-fallback` stray-NODE is a placeholder, not a human decision — the actionable item is the `associated-with` EDGE or the `equiv_group` (the agent proposes those). Stray nodes are now `Actionable=false`: suppressed from the review queue, still **counted** and still **fed to the agent's `get_strays`**. Live: pending **248 → 87** (162 suppressed). The agent IS the stray-mapper; the deterministic ER just feeds it + the silence ledger.
- **Agent not-causal.** The agent now suggests `a-to-b` | `b-to-a` | **`not-causal`**. Positive directions still need the dual-witness; `not-causal` (a negative judgement that never invents an edge) is admitted freely. Surfaced on the causal page + in the governance item (`suggestedDirection`).

## 4c. The series→phenomenon_relation bridge (next focused build — the P5 residual)

**Goal:** an authored causal direction automatically extends cascade detection, so the same cause→effect lights up as a *recognized authored chain* on recurrence.

**The gap:** a causal hypothesis is between two metric SERIES (mA on entity eX, mB on eY); the cascade reads AUTHORED `phenomenon_relation` edges (generic: degraded callee → impacted caller) over MEASURED flow edges. A series-direction is not yet a cascade-readable relation.

**Design (3 bounded steps, charter-clean):**
1. **Authoring emits an entity-causal overlay.** When a NAMED human authors a *cross-workload* direction (eX→eY) on a co-onset pair, render an AUTHORED entity-level causal-relation overlay `{from: eX, to: eY, basis: "operator-authored from cohyp:<id>", author, note}` (the committable artifact `authorCausalDirection` already returns, retyped to the cascade-readable shape). Released via the governance gate; provenance stays AUTHORED; off-digest until released → replay-identical.
2. **Cascade reads entity-causal edges** as a SPECIFIC authored relation alongside the generic one, so eX-degraded → eY-degraded over the flow edge surfaces as the AUTHORED chain (with the operator's note + provenance), not just a generic cascade.
3. **Recurrence test (your #2):** author eX→eY; re-inject an incident where eX + eY degrade over the flow edge; assert the cascade surfaces the AUTHORED eX→eY chain. This is the literal "fired when the same incident/entity happens" proof.

**Why it's its own build, not a tail-of-turn tweak:** step 2 touches `detect`/`flow` cascade recognition — the load-bearing detection path. It deserves the same gated discipline (off-digest until backtested, replay-identical, named-human authoring) the rest of the system holds. The authoring half (step 1) is contained; the cascade-read (step 2) + recurrence test (step 3) are the careful part.

## 5. Honest ceiling

After P1–P5: A/B/C coverage is a finite, working checklist; D is answered by *governed
inference* that gets *smarter over time* (every authored cause becomes future detection).
What remains structurally out of reach: signals no deployable tool emits (needs new
infrastructure), and any "why" a human declines to author (the system never forces it).
