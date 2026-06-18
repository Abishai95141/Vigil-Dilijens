# 20 — Dynamic Graph eXtension (DGX), Coverage Modalities & the Causal-Reliability Roadmap

> **Status:** forward dev track (PROPOSED, 2026-06-18). Not shipped behaviour — every
> item graduates only through the phased roadmap (§6) and the existing governance gate.
> Authored from a multi-agent research + design pass, then re-checked against four
> adversarial charter critics (their accepted fixes are folded in and marked **[FIX]**;
> rejected-as-written designs are marked **[REJECTED-AS-WRITTEN → REPLACEMENT]**).
>
> **Binding constraint:** the whole design lives under
> [`docs/01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md). The
> spine in §1 is what makes an adaptive, self-extending system charter-clean: the agentic
> layer **proposes**, deterministic gates **verify**, a **named human promotes** — the
> model authors nothing, and the deterministic detection/forecast path never reads a
> candidate. Companion: the v4 whole-system gap analysis this plan answers.

---

*Lead systems architect deliverable. Every design below is constrained by docs/01 (the epistemic separation charter) and re-checked against the four adversarial critic verdicts. Where a critic accepted a fix, it is folded in and marked **[FIX]**. Where a critic rejected a design as written, the rejection and the replacement are stated explicitly **[REJECTED-AS-WRITTEN → REPLACEMENT]**.*

---

## 1. THE KEYSTONE PRINCIPLE

**Spine: PROPOSE → VERIFY → PROMOTE. The agentic layer authors nothing. It is a grounded hypothesis generator feeding a deterministic referee and a named-human promoter.**

This single discipline makes every later section charter-safe. It rests on three load-bearing decisions:

**(a) Provenance stays exactly three, immutable at birth: `MEASURED | PROJECTED | AUTHORED`.** We do *not* add a fourth provenance class. Instead we add an **orthogonal lifecycle status**: `status ∈ {CANDIDATE, PROMOTED, REJECTED, SHADOW}`. A datum's provenance class is what it *is*; its status is where it is in the promotion pipeline. The load-bearing distinction the critics repeatedly enforced: **a stray metric's readings are MEASURED; only its proposed join/membership/edge carries `status=CANDIDATE`.** A model-proposed edge is co-occurrence evidence, surfaced labelled, never a reason, never a cause.

**(b) The CANDIDATE space is physically firewalled from the deterministic path by mechanism, not convention — and the firewall is enforced by a test, not by reviewer vigilance.** Three critics independently caught the same prose error and one real mechanism gap, both folded in here:

- **[FIX — metadata + dynamic-graph critics] The hash-firewall claim is corrected.** The original prose ("candidate YAML contributes to no content hash because the hash is computed only over manifest-named overlays") is **factually wrong**. Verified against the code: `LoadRelease` → `LoadWithOverlays` → `overlayPaths(overlayDir)` **globs every `.yaml` in the `overlays/` root non-recursively** and hashes all of them; `VerifyRelease` then pins that hash. The manifest's `Overlays:` list is **not** used as a filter on the production path. The firewall holds for exactly one reason: **`overlayPaths` skips subdirectories** (`e.IsDir() → continue`). Therefore `overlays/candidate/` is excluded as a *subdirectory*, not by manifest filtering. **The real invariant, and the test we must add: `overlayPaths` never returns any path under `overlays/candidate/`**, so the released/pinned hash (`LoadReleasePinned`, what `detect`/`tierb`/matcher load) is byte-identical whether candidate files are present or absent.

- **[FIX] The candidate store must NOT ride the `LoadWithExtraOverlays` seam.** That seam (used by `--app-metrics`) *does* hash extra files into `g.Version` and flips the build to UNRELEASED via `IdentifyRelease`. If the candidate lane used it, candidates would (a) become visible in the matcher graph and (b) un-release the build. **The candidate store is a wholly separate `modernc.org/sqlite` store (`candidates.db`) outside the graph loader entirely.** The surfacing-only DGX lane may read candidate overlays under `--dgx-enabled` via a dedicated read path that produces a *distinct lane version string* — and that lane version is *allowed* to move with candidate content; the released hash is the invariant, asserted by test.

- **Three firewall layers, all mechanical and reviewer-independent:** (1) **hash firewall** — subdir exclusion, enforced by the `overlayPaths` test above; (2) **read firewall** — a grep-able `graphlint`/conformance test asserting **no detection package (`detect`, `selection/tierb`, `binding/report`, the matcher, `observe/fingerprint`) imports `internal/candidate`** (mirrors the verified-clean fact that they don't import `unexplained` today); (3) **wire firewall** — `clock.proto` stays string-AND-bytes-free, already locked by `conformance_test.go:35`.

**(c) The CANDIDATE space is never read by the deterministic path, and is never byte-identity-guaranteed.** Candidate-set membership (including any LLM stochasticity) is explicitly **NOT part of any replay guarantee**. This is safe precisely because detection never reads it and promotion still requires a deterministic `Classify` + named-human `approve` (`proposal.go:108` — *"the harness can block; it cannot approve"*).

**The promotion path is the existing governance gate, unchanged:** a verified candidate becomes a `governance.IntakeItem` (extend `intake.go` with a `SourceDGX`) → `EvaluateProposal` (authorship gate, `changeclass.Classify` diff where a new edge ⇒ `ClassBehaviouralMedium`, evidence-required) → **named human `approve`**. Promotion *is*: write authored YAML into a manifest overlay, add it to a new semver release, re-pin the hash over authored content only. **The model's text — including any proposed name — is discarded at promotion; the human authors the note, the name, and the version.** This is identical to how the flow-lane causal relation graduated into `cross-service-v0.yaml`.

---

## 2. DYNAMIC GRAPH + AGENTIC LAYER

The user's request — "dynamic node/edge generation plus an AI agentic layer that infers missing relationships" — is delivered **without** letting a model write an authoritative edge. The template for all three graphs: **discover structure (MEASURED) → stage as CANDIDATE → governance promotes *meaning* (AUTHORED) → detection stays neutral.**

### 2.1 The agent harness (`obsd/internal/dgx` + `dgxd/`)

An off-digest producer `obsd/internal/dgx` (injected clock, no `time.Now`) wraps a deliberately-replaceable Python LLM sidecar `dgxd/` (uv, like `clockd`, **never on the clock wire, never on the digest**).

**Propose (read-only).** Tools return already-classed MEASURED facts only: `get_unexplained`, `get_coverage`, `get_silence`, `get_blindspots`, `get_quarantine`, `get_topology`, `get_associations` (§2.3), `get_authored_relations`. **There is no write tool.** Typed outputs only:
- `CandidateNode{strayCEI, readings, provReason}`
- `CandidateEdge{from, to, kind ∈ {topology | associated-with | topo-adjacent}, evidence[], lineage}` — **structural kinds only; a causal `kind` is statically rejected by the schema enum.**
- `CandidateMember{phenomenon, signalNode, role}` and `CandidateBarSource{stream, declared-config-pointer}`.

**[FIX — dynamic-graph critic] Causal hypotheses get their OWN typed candidate, never the recurrence-shaped `IntakeItem`/`CandidateReport`.** The existing `unexplained.Candidates()` contract forbids causal vocabulary (`Rationale` "carries no causal vocabulary, only the recurrence fact"). A `change→phenomenon` or `template-precedes-phenomenon` hypothesis **is** causal vocabulary. Therefore we add a **separate** `CausalHypothesis{evidence: co-occurrence-only, proposedRelation, NO direction-asserting prose}` candidate type, surfaced strictly as *"observed co-occurrence, candidate only — not a causal claim."* It never merges into an `IntakeItem.Summary` until a human authors the relation. The agent may emit **no "reason" string and no causal direction**; free-text is discarded at promotion.

**Verify (deterministic, before staging — any fail ⇒ REJECTED, kept as SHADOW):**
1. **graphlint/SHACL** — schema + referential integrity. **[FIX] The "causal-vocabulary" rejection is a SCHEMA-LEVEL rejection of a causal `kind` enum (statically rejected), not a load-bearing prose grep.** The keyword lint is a non-load-bearing backstop only.
2. **Determinism-diff** — load `base+manifest` vs `+candidate`, recompute fingerprints/matches over a **golden replay using the INJECTED clock and the PINNED release load** (never `time.Now`). Must not move a single fingerprint. By construction it can't (off-manifest) — so this *re-proves the firewall every cycle*.
3. **Co-occurrence guard** — every temporal/topological match stages as `associated-with`, never `causes`.
4. **Evidence-sufficiency floor** — reject candidates citing `< k` MEASURED facts.
5. **Multi-vote + adversarial self-critique** — N proposer runs; advances only if support `≥ τ` and a challenge-critic fails to falsify against cited evidence. **This is a quality pre-filter, never authority; the strongest a model reaches is `verified`.**

**[FIX — all four critics] Every numeric cutoff is a DECLARED, versioned parameter in `internal/params`, never fit to data.** Vote `τ`, evidence-floor `k`, and any ER block score are versioned constants in the single parameters file, surfaced, and explicitly documented as gating **candidate quality only — never detection, and never derived from the data they are applied to.** A data-fit cutoff is a learned threshold and is rejected.

**[FIX — modality critic] VERIFY is defined honestly.** For a new *phenomenon binding* (a count-series member), VERIFY is a real deterministic backtest gate like every other forecastable class. **For a CAUSAL relation there is NO machine verifier** — VERIFY degrades to "human reviewer + co-occurrence evidence shown," and the promotion is justified *solely by the named human author*, never by the agent. We will not let the word VERIFY imply machine validation of causation.

**Promote.** Unchanged governance path (§1c).

### 2.2 Graph 1 — Infra topology (already MEASURED-dynamic)

`identity/edges.go` already discovers topology dynamically (validity-intersection windows). DGX only *proposes* a `CandidateNode` on CEI quarantine (§2.4); promotion mints an authored identity rule. **No core surgery.**

### 2.3 Graph 2 — Metrics dependency graph (`obsd/internal/assoc`) — HEAVILY CONSTRAINED BY CRITICS

A new deterministic off-digest producer computes windowed lead-lag / partial-correlation over `qss` series, emitting MEASURED edges labelled **`associated-with`, never `causes`.** Three critics (forecast HIGH, dynamic-graph HIGH, metadata) converged on the same dangerous move and I am adopting their fixes in full:

- **[REJECTED-AS-WRITTEN → REPLACEMENT] `assoc` MUST NOT feed `forecast/decompose.go`.** Routing a statistically-inferred (Granger/lead-lag) edge into the footprint-subtraction seam violates *"the graph-explained footprint must be AUTHORED"* and *"no learned edge anywhere."* **Footprint subtraction may ONLY consume AUTHORED relations + deterministic arithmetic boundaries** (gauge resets, operator-declared windows). `assoc` is **forbidden from decompose.go entirely.**

- **[REJECTED-AS-WRITTEN → REPLACEMENT] `assoc` MUST NOT feed live `tierb.go` selection ranking on fitted statistics.** A fitted lead-lag coefficient used to rank selection is a learned weight on the replay-load-bearing selection path (it changes which targets are forecast within the 50-budget). **Live selection ranking stays on AUTHORED graph attributes only** (blast-radius, cascade depth, authored severity — deterministic given graph version) **plus canonical order**, exactly as `tierb.go:46-55` does today. `assoc` may feed selection **only if** it first earns its own byte-identical replay gate (below) *and* is reduced to a fixed-window, fixed-lag, no-fitted-coefficient deterministic arithmetic consequence. Until then, `assoc` **only surfaces as a labelled candidate input and seeds CANDIDATEs.** It never relabels a series as a *precursor* — precursor-bearing is AUTHORED-only.

- **[FIX] `assoc` carries its OWN determinism golden-replay gate (`just assoc-gate`)** before any seam consumption: byte-identical edge set + scores over a fixed fixture under `go test -race`, with **explicitly pinned window alignment, gap/NaN policy, and fixed-order summation** (sorted + integer-scaled or Kahan). Window-boundary, gap-handling, and float-summation-order sensitivity are real replay risks and must be pinned, not asserted.

- **NOTEARS/PC/FCI/LiNGAM run OFFLINE only**, emitting `CandidateEdge`s into the candidate store — they perform near-random on microservice data and never touch the hot path.

### 2.4 CEI stray-metric fallback (`candidate/er.go`) — REJECTED Fellegi-Sunter scoring

`normalize.go:449` quarantines-never-guesses today (determinism by exclusion). We add an **off-digest second tier** reading the quarantine stream — but the critics rejected the scoring design:

- **[REJECTED-AS-WRITTEN → REPLACEMENT] No scalar confidence score, no Fellegi-Sunter m/u weights.** Fellegi-Sunter m/u probabilities are **fit from data — a learned weight by definition** — and any accept/rank cutoff on a score is a learned threshold. **Replacement: emit only the DISCRETE evidence set** (which labels agreed, exact-match booleans). Ranking, if any, is a **fixed lexicographic order over agreed-label COUNT** (mirroring `unexplained.Candidates`' windows-then-name order), with **no tunable threshold and no score.** Pure deterministic normalized-label-set intersection. Splink/Duke/Ditto/DeepMatcher all rejected.

- **[FIX — metadata critic] The PROVISIONAL node is explicitly excluded from the fingerprint digest**, exactly as a quarantined series is today. The `status=CANDIDATE` flag gates digest-exclusion. A golden-file replay test proves the per-tick digest is byte-identical with and without the provisional/candidate machinery enabled. A PROVISIONAL node **never flips an (entity,variable) pair to `Watched`** — it surfaces as a new honest silence-ledger state (`unmapped-provisional, candidate-link-proposed`), reusing `api/silence.go classifySilence`.

- **[FIX] The proposed link is a SEPARATE typed `candidate_edge` record**, never an attribute hung on the MEASURED provisional node. Node readings stay MEASURED-but-unmapped; the link stays CANDIDATE; they are joined only at the surface, each labelled.

- **[FIX] An LLM-proposed NAME is non-authoritative, lives only in the candidate store, is visibly marked "proposed, unauthored," and is NEVER copied to the graph.** The promoted authored node's name is (re)written by the named human. A candidate edge cannot exist without the agreed-label lineage that grounds it.

A human steward promotes → an authored CEI rule; non-winners survive as SHADOW (MDM survivorship).

### 2.5 Surfacing — join-never-fuse, single layer

**[FIX — dynamic-graph critic] A CANDIDATE proposed-edge is NEVER rendered in the same row/anchor as an AUTHORED cause edge for the same node pair.** Co-locating them is a fusion hazard ("proposed" reads as "cause-in-waiting"). Candidates live in a **visually separate "proposed / unverified" tray** with the method name and an explicit *"not a reason, not a cause"* tag. The AUTHORED-cause badge appears only after promotion. A surfacing-layer test asserts **a CANDIDATE row can never carry causal vocabulary.**

---

## 3. COVERAGE MODALITIES

Three new off-digest MEASURED producers, each mirroring `--events-enabled`: zero blast radius on detection, its own backtest gate, no string on the clock wire, fingerprint digest untouched.

### 3.1 LOGS — `internal/logtmpl` (regex → Drain3 → LLM-assist hybrid)

- **Layer 1 (AUTHORED, unchanged, sole authoritative layer):** operator-authored regex maps known lines → known `event-id`s. The *only* layer the detection path trusts for *named* events.
- **Layer 2 (MEASURED):** every unmapped line is mined by **Drain3** (MIT, pure-data deps, streaming). The mined `(template, cluster_id, params)` and its **per-template count series** are MEASURED — a deterministic arithmetic consequence of the byte stream, identical in class to a fingerprint. The count series joins the metric engine and is forecastable.
- **Layer 3 (PROPOSED):** an offline LLM may *name*, *severity-guess*, or *group* templates — output lands in the candidate store the detection path never reads. **[FIX] The LLM-proposed name is non-authoritative and discarded at promotion.**

**[FIX — modality critic, HIGH] Drain3 determinism is pinned, with a BLOCKING (not aspirational) replay gate.** Drain3's tree is **input-order-dependent**, and multi-pod log fan-in has no intrinsic total order. Required:
- A **deterministic total order before `add_log_message`**: sort by `(source-stream-id, source-timestamp, monotonic-seq)` within each fixed window.
- **Snapshot/restore the Drain3 tree state as part of the replay bundle** (cold-start vs checkpoint-resume otherwise yield different `cluster_id`s for the same lines).
- A **mandatory, gate-blocking golden-file replay test.** If a deterministic order cannot be guaranteed for a live stream, the count series is **marked replay-divergent and kept off any byte-identity path.**

**[REJECTED-AS-WRITTEN → REPLACEMENT — modality critic] No model-synthesized regex is ever promoted verbatim.** Promoting an LLM-generated match pattern makes model output an authoritative detection threshold in substance. **Replacement: bind detection to the MEASURED `cluster_id` as a stable authored signal node** (the general mechanism), OR require a **human to author the regex** (template shown as evidence only). `EvaluateProposal` rejects any proposal whose match-pattern field is flagged model-origin. The "promoted regex" path is the per-app regex zoo relocated — dropped.

### 3.2 TRACES — `internal/trace` (observed L7 call graph)

Reuses the flow-lane precedent verbatim. OTLP spans ingest via the OTel `servicegraph` connector + a CRISP critical-path extractor, off-digest (traces are sampled → census-incomplete → must stay off the digest, like conntrack flow edges).

- **MEASURED (born so):** the span-derived call graph (an observed A→B call, identical class to a conntrack edge — closes the L7 gap conntrack cannot see); per-edge latency + error-rate series from `servicegraph`; critical-path membership (a deterministic arithmetic consequence of span timing).
- **Borrowed normativity intact:** latency/error bars come ONLY from declared SLOs; undeclared edges stay unbounded / Tier-B-ineligible. **Never derive a bar from the observed latency distribution.**
- **[FIX — modality critic] A TraceRCA-style "abnormal traces traverse X" ranker may reference ONLY the customer's DECLARED SLO bar, never a distribution-derived cutoff** — even as a candidate input. A ranker keyed off a learned cutoff is rejected even as a proposal, because its evidence is itself a learned artifact.
- **[FIX] Sampled servicegraph series are flagged census-incomplete wherever surfaced and are NOT asserted byte-identical on replay.**
- The AUTHORED causal direction graduates separately via a governance overlay, exactly as conntrack's meaning was promoted into `cross-service-v0.yaml`. Sage/CIRCA/RCD/Eadro rejected as authoritative (learned edges).

### 3.3 AUDIT — `internal/audit` (change-to-incident as hypothesis)

The architectural twin of the events lane. A collector behind `--audit-enabled` parses kube-apiserver audit JSON into `ChangeEvent{verb, resource, namespace, name, user, decision, ts}` findings with a `RESOLVED`/dedup role, off the fingerprint digest.

- **MEASURED:** the change itself — a deterministic, source-timestamped fact. **No bar is invented** (a change is an occurrence, not a threshold-crossing).
- **The ONE permitted deterministic operation is the arrow-of-time prune:** a change *after* incident onset cannot be a candidate. **[FIX — modality critic] The prune keys strictly on the audit record's source `ts`, never receipt/delivery time** (webhook delivery is async; a wall-clock leak would make the prune non-deterministic).
- **"This change caused the incident" is a `CausalHypothesis` (PROPOSED), never AUTHORED**, and uses the **separate direction-free candidate type from §2.1** — never the recurrence-shaped `IntakeItem`. Adjacency stays co-occurrence (the ice-cream/drownings discipline). An unbound change surfaces as *"observed change, no authored causal link — candidate only,"* never silently dropped.

---

## 4. FORECASTING ROADMAP

The shipped forecasting code is **charter-clean and the critic could not break it** (string-and-bytes-free wire; `Project`/`Decompose`/`DetectRegimeShift` are pure MEASURED arithmetic with injected time; the band never collapses via `LatestBeyondHorizon`). The needs-fixes verdict is **entirely about the roadmap**, and I adopt every fix.

### 4.1 NOW (shipped, honest)
A narrow, non-gating early-warning estimator on the warm path (60s, parallel to the 15s deterministic tick). Horizon **1h** (`horizon_steps: 240`, *not* the doc's "4–8h" — corrected). Band from outer quantiles `[0.1, 0.5, 0.9]`; confidence is **structural** (`fullWidth/horizon` buckets), not calibrated. Selection is deterministic lexicographic with authored-precursor-first ranking, 50-budget, unbudgeted published never silent. Footprint = **boundary-TRIM** (`remainder := samples[spliceIdx:]`), not shape-model. Forecasts single-stream gauge-valued not-yet-crossed series with a declared bar; everything else silenced with a typed reason.

### 4.2 LIMITS (code-grounded)
1h reach only; footprint trims not models; **recombination absent**; per-pod churn (HPA/rollout) severs series with zero continuity handling; counters/ratios/histograms silenced (blocks p95/p99 latency); band structural not calibrated; selection tiebreak crude.

### 4.3 FUTURE — within the TWO permitted graph→model flows only

**Seam 1 — richer SELECTION (`tierb.go`).** **[FIX — forecast critic, HIGH]** Rank live selection ONLY on **AUTHORED graph attributes** (blast-radius, cascade depth, authored severity) as a **single authored, versioned total order in params — never a hand-tuned weighted sum.** Fitted Granger/lead-lag statistics are forbidden from the live rank (a learned number laundered onto the replay-load-bearing path). `assoc` edges may *inform a human's authoring* only, living in the CANDIDATE space the runner/`Project`/`Decompose`/`SelectTierB`/`detect` **never read.**

**Seam 2 — richer FOOTPRINT SUBTRACTION (`decompose.go`).** **[REJECTED-AS-WRITTEN → REPLACEMENT — forecast + modality critics, HIGH] "Subtract the explained component" is REMOVED from the roadmap.** That is shape-modelling — a fitted footprint shape fed to the clock as floats launders a learned quantity onto the inference path, and it is not what the code does. **Replacement: an AUTHORED `trigger→downstream` edge + a MEASURED event boundary may only pick a SPLICE INDEX (trim the head), never compute and subtract a fitted shape.** Trace/critical-path latency may serve as a splice boundary ONLY if the resulting splice **index is a deterministic timestamp pinned in the replay manifest and asserted byte-identical** (events-lane firewall precedent).

**Covariates.** **[FIX — forecast critic]** `covariate_future` may carry only **customer-DECLARED, genuinely-knowable, dimensionless future floats** (borrowed normativity), never a graph/model-inferred or fitted schedule. The current linear stub **stays disabled** until a real backtest gate and TimesFM covariate support exist; a covariate-bearing forecast without its own gate is an ungated PROJECTED claim.

**Phased forecasting work:**
- **NEAR:** (a) **calibrate the band** — **[FIX]** only as an **offline, harness-derived, customer-INVARIANT, versioned constant in `defaults.yaml`, cited to its gate, replay-pinned**; an online or per-cluster fit is REJECTED, and the operator surface must state which guarantee ("structural-width" vs "empirical-coverage") it is showing. (b) **churn-stable identity** — **[FIX — forecast critic]** bridge UID resets via **DETERMINISTIC authored succession from the identity layer (doc 03 tombstone/succession, keyed on OwnerReference lineage), NEVER series-shape similarity** (shape stitching is both non-deterministic and a false-continuity fabrication — the #1 identity-mis-join failure mode). (c) **histogram→quantile bucket-interpolation** at `scrape.go:277` — the single highest-leverage fix; one MEASURED, golden-tested change unlocks p95/p99 latency + counter-rate for *every* exporter.
- **MID:** authored-attribute selection (Seam 1) + splice-index footprint boundaries (Seam 2), fed by off-digest trace/audit lanes; agentic proposals land in CANDIDATE only.
- **FAR:** multi-horizon (>1h per class, each its own gate) + true recombination + live declared covariates.

**Permanently FORBIDDEN (named so a future implementer cannot drift):** any string on the clock wire; a learned edge/weight/threshold or model output written to the ontology; **a PROPOSED/CANDIDATE datum read by the deterministic detection OR forecast-input/selection path** (the runner, `Project`, `Decompose`, `SelectTierB`, `detect` are barred); co-occurrence (temporal/topological/Granger) restated as causation; an agentic natural-language rationale surfaced on a PROJECTED card as a "reason" or precursor (`Candidate.PrecursorPhenomena` cites only AUTHORED graph ids — as the code does today); an invented bar where none is declared.

---

## 5. METADATA & PARTIAL-COVERAGE

### 5.1 The cause taxonomy (why a (signal, phenomenon) pair is partial/none)
Every gap reduces to one of four fixable classes plus two inherent:
- **DATA (member-signal absent / dark bar / unresolved config):** architecturally fixable, **no core surgery** — deploy the exporter; the availability gate flips the pair to `Watched` on next compile (the KSM/node-exporter lanes prove the pattern). Dark bar and unresolved self-heal once stream/config arrives.
- **NORMALIZATION (histogram/summary skipped / won't normalize to CEI):** **the highest-leverage class.** Promote sub-variable/data_type normalization to a first-class stage: histogram→quantile interpolation at `scrape.go:277` (unlocks p95/p99 for every exporter at once); new `ksmDerivations`/`normalize.ksm` cases; recurring equivalence-resolver dialect sweep. **[FIX — metadata critic] The histogram→quantile fix is genuinely general; the per-dialect ksm/equivalence cases are flagged as ONGOING CURATION (MEASURED, golden-tested), not a closed architectural fix** — honest framing prevents normalizing a backlog of one-offs.
- **AUTHORING (no detect-condition / unresolved inline member / undeclared bar):** fixable ONLY through governance, never code. **[FIX] `CandidateBarSource` is a GENERAL mechanism** — propose a declared-config bar pointer for *any* unresolved-inline required member (`report.go RequiredTotal==0`), not a hand-fit patch for the single known dead row. Plus an **auto-derived detectable-vs-obtainable honesty pass**: for every obtainable signal with no binding phenomenon or no bar, emit a *visible* "obtainable-but-undetectable / unbounded" state into the silence ledger.
- **MODALITY (CERT_EXPIRY / DNS / ETCD):** needs a new off-digest lane (§3), not a binding fix.
- **Inherent:** undeclared bar (charter: borrowed normativity, `compile.go` refuses to invent) and eligibility-excluded (failure mode genuinely cannot occur) — surfaced honestly, never false gaps.

### 5.2 The stray-metric path
Replace the terminal quarantine drop with the three-layer cascade of §2.4 — Layer 1 AUTHORED regex unchanged; Layer 2 PROVISIONAL node (MEASURED readings, **digest-excluded**, **discrete-evidence ER, no score**); Layer 3 PROPOSED ranking/naming in the candidate store. Promotion via `governance/intake.go FromUnexplainedCandidates` → `EvaluateProposal` → named human. Non-winners kept as SHADOW.

---

## 6. UNIFIED PHASED ROADMAP

| Phase | Item | Charter risk | Reuses |
|---|---|---|---|
| **P0** | `internal/candidate` store (`candidates.db`, outside graph loader) + `overlays/candidate/` subdir + `--dgx-enabled` read-only surfacing path. **The `overlayPaths`-subdir-exclusion test + the import-firewall lint + the Gate-2 determinism-diff test (injected clock, pinned load).** *No agent.* | **LOW** — pure firewall; mechanically proven | `overlay.go` subdir-skip, `graphlint`, `conformance_test.go` pattern |
| **P0.5** | **histogram→quantile interpolation** (`scrape.go:277`) + golden test. *Highest single leverage; unblocks latency everywhere.* | **LOW** — MEASURED arithmetic, golden-tested | `observe/scrape.go`, equivalence resolver |
| **P1** | CEI stray-metric fallback (`candidate/er.go`): **discrete-evidence ER, NO score**, lexicographic-by-agreed-label-count; PROVISIONAL node digest-excluded; surface in `api/blindspots.go` + new silence state | **MEDIUM** — was the Fellegi-Sunter violation; now neutered | `normalize.go:449` quarantine, `silence.go classifySilence`, `blindspots.go` |
| **P2** | `internal/assoc` MEASURED dependency graph **with `just assoc-gate` (byte-identical, pinned windows/gaps/summation) BEFORE any seam.** Surface as candidate input + `/api/dependency`. **Does NOT feed decompose; feeds tierb only after gate + no-fit reduction** | **HIGH** — the central critic flag; gated hard | `qss` series, flow-lane precedent |
| **P3** | Agent harness (`dgx` + `dgxd`): read-only tools, typed outputs incl. separate `CausalHypothesis` type, 5-gate verify (params-declared cutoffs), `SourceDGX` → `EvaluateProposal` | **MEDIUM** — LLM off-digest/off-wire, behind deterministic gates | `governance/{intake,proposal,changeclass}`, `unexplained/candidate.go` |
| **P4** | Semantic lanes, in RCA-value order: `internal/trace` (servicegraph/CRISP) → `internal/logtmpl` (Drain3, **with the blocking determinism gate + state snapshot in replay bundle**) → `internal/audit` (source-`ts` arrow-of-time prune). Each behind its `--*-enabled` flag + own gate | **MEDIUM** — Drain3 order-determinism is the live risk | `--events-enabled` lane shape, `flow/relation.go` overlay promotion |
| **P5** | Forecast NEAR set: band calibration (offline/invariant/versioned constant) → churn-stable identity (authored succession, **no shape stitching**) | **MEDIUM** — calibration & identity-mis-join hazards, fixed | doc-03 succession, `forecast/project.go` |
| **P6** | Promotion UX: console intake queue, **separated proposed-tray vs authored-cause surfacing** (never same row), one-click "draft overlay" → `EvaluateProposal` | **LOW** — human-gated | console, governance CLI |
| **FAR** | Authored-attribute selection (Seam 1) + splice-index footprint (Seam 2); multi-horizon; declared covariates (gated) | **MEDIUM** — only if the named-forbidden list holds | `tierb.go`, `decompose.go`, `clock.proto` covariate field |

**Critical dependencies:** P0 firewall gates everything agentic (P1, P3, P4 causal lanes). P0.5 unblocks all latency forecasting independently — do it early, it has no agentic dependency. P2's `assoc-gate` gates any selection-seam consumption. P4's trace lane precedes the forecast splice-boundary work (Seam 2).

---

## 7. CHARTER-COMPLIANCE STATEMENT

- **CANDIDATE space:** does not violate provenance (status is orthogonal to the three immutable classes); does not violate join-never-fuse (proposed link is a separate typed record, surfaced in a separate tray); does not violate determinism (off-digest, outside the graph loader, released hash byte-identical with/without candidates — *tested*); the detection path's blindness is enforced by an import lint, not reviewer trust.
- **Agent harness (`dgx`/`dgxd`):** authors nothing; read-only tools; LLM off-wire (string-and-bytes-free, `conformance_test.go`) and off-digest; stochasticity confined to candidate-set membership which is explicitly outside the replay guarantee and never read by `detect`. Promotion needs deterministic `Classify` + named human (`proposal.go:108`).
- **`assoc`:** MEASURED `associated-with` only, never `causes`; forbidden from `decompose.go`; barred from live selection until its own byte-identical gate passes; never relabels a series as a precursor.
- **CEI fallback:** no learned weight (discrete evidence set, lexicographic rank, no score); PROVISIONAL node digest-excluded (replay byte-identical, tested); proposed name non-authoritative and discarded at promotion; pair stays not-`Watched`.
- **Logs/Traces/Audit:** off-digest MEASURED producers; no invented bar (borrowed normativity); causal claims are direction-free `CausalHypothesis` candidates; Drain3 determinism pinned with a blocking gate; audit prune keys on source `ts`; sampled series flagged census-incomplete.
- **Forecasting:** two permitted flows only; footprint stays splice-index trim (no fitted shape); selection ranks on authored attributes only; band calibration is an offline customer-invariant versioned constant; UID bridging is authored succession; covariates are customer-declared and gated.
- **Borrowed normativity:** every numeric cutoff (`τ`, `k`, calibration constant) is a declared, versioned param, never data-fit; bars come only from declared config; undeclared → stated, never invented.

---

## 8. RESIDUAL BLIND SPOTS (honest — no design closes these)

- **Unauthored causal direction.** Vigil will *propose* and *measure co-occurrence*, but a true cause is only ever an AUTHORED human note. Where no human has authored the relation, Vigil shows adjacency labelled as adjacency — and stays silent on cause. This is by charter and is permanent.
- **Data correctness / business semantics.** Vigil reads what exporters emit; it cannot tell that a metric is *wrong*, that a value is semantically corrupt, or that a transaction is business-invalid. No lane closes this.
- **Off-CPU / intra-container attribution.** Profiler-grade "which line/goroutine" attribution is out of scope; the Parca hint is the last, lowest-priority lane and remains a hint, not a measurement.
- **Sampled-input census gaps.** Traces (head/tail sampled) and Drain3-mined templates are census-incomplete by construction; their aggregate series are flagged so and are never asserted byte-identical on replay.
- **Causation truth.** There is **no machine verifier of causation** anywhere in this plan. The 5-gate VERIFY stack is a *quality pre-filter* for the human queue, never a validator of a causal claim. We state this plainly rather than letting "VERIFY" imply otherwise.
- **Per-customer behavioural baselines.** No learned normal exists; if a customer declares no bar, the pair is honestly unbounded/Tier-B-ineligible forever, never auto-baselined.
- **SHADOW-row growth.** **[brittle-flag, dynamic-graph critic]** Append-only SHADOW survivorship grows unbounded; this plan owes a **bounded, deterministic GC/compaction policy** (not yet specified) before P3 ships at scale — flagged as an open operational item, not silently assumed solved.

---

## 9. FIRST CONCRETE STEPS (highest leverage, do these first)

1. **P0 — The firewall, proven by test.** Stand up `internal/candidate` (`candidates.db`, *outside* the graph loader) and `overlays/candidate/`. Write the three enforcing tests **before any consumer exists**: (a) `overlayPaths` returns nothing under `overlays/candidate/` and `LoadReleasePinned` is byte-identical with candidates present/absent; (b) the import-firewall lint — no detection package imports `internal/candidate`; (c) the Gate-2 determinism-diff over a golden replay with injected clock + pinned load. *This is the spine; nothing agentic is safe until these pass. Correct the firewall prose to cite subdir-exclusion, not manifest filtering.*

2. **P0.5 — histogram→quantile interpolation (`scrape.go:277`).** One MEASURED, golden-tested change unlocks p95/p99 latency and counter-rate series for every exporter at once — the single highest-leverage normalization fix, with **zero agentic dependency**. Do it in parallel with P0.

3. **P1 — CEI stray-metric fallback with discrete-evidence ER (no score).** Turn the terminal quarantine drop into a visible PROVISIONAL node + separate `candidate_edge` (agreed-label intersection, lexicographic-by-count, **no Fellegi-Sunter, no scalar**), digest-excluded, surfaced in `api/blindspots.go` and a new honest silence state. Richest near-term coverage win, pure-Go, and it exercises the P0 firewall for real.

4. **P2 prerequisite — `just assoc-gate` first, `assoc` second.** Before writing a single seam consumer, build the byte-identical replay gate (pinned windows, gap policy, fixed-order summation). Only once it is green does `assoc` surface as a candidate input — and even then it stays out of `decompose.go` entirely and out of live selection until the no-fit reduction is proven. This sequencing is what keeps the single most dangerous design move (the `assoc→seam` leak the critics flagged HIGH) from ever reaching the deterministic path.

---

*Bottom line: the DGX skeleton is fundamentally sound — provenance stays three immutable classes, the agent authors nothing, the firewalls are real and mechanically testable. The plan above is charter-clean **only with** the critic fixes folded in: `assoc` is barred from `decompose.go` and from live selection until gated; the footprint seam stays a splice-index trim (never a fitted-shape subtraction); ER drops the learned Fellegi-Sunter score for discrete-evidence intersection; every cutoff becomes a declared param; Drain3 gets a blocking determinism gate; causal hypotheses get a separate direction-free candidate type; the hash-firewall prose is corrected to the real subdir-exclusion mechanism; and proposed edges never share a surface row with authored causes. Execute P0 + P0.5 + P1 first.*
