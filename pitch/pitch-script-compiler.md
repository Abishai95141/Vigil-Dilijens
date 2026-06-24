# Vigil Pitch Deck & Storytelling Script — "The Telemetry Compiler" (Variation 2, enriched)
### Theme 2: Beyond monitoring — AI agents for real-time pod resource discovery and dependency mapping

This is **Variation 2** — the *Compiler / Dead-Data* framing — kept intact and enriched with Vigil's own grounded content: the deeper *Book of Why* ladder, the honesty surface (coverage report + silence ledger + blindspots), the real numbers, and implicit criticism that is grounded in how the dominant approach **actually** works (verified against real engine code). We never name a competitor. We say: *"this is said to be done this way — but there's a better way."*

**Read key:** `SCRIPT:` = spoken words · `VISUAL:` = the slide · `ENERGY:` = delivery · **⚔️** = an implicit-criticism line (collected and justified in Appendix C).

---

## Part 1: Storytelling Strategy Map (Variation 2, enriched)

| Storytelling Principle | How it is applied |
|---|---|
| **Curiosity & Contrast** | **Expectation:** scaling microservices needs bigger LLMs or thousands of lines of custom config to map heterogeneous metrics across namespaces. **Contrarian Reality:** you don't need a smarter guesser — you need a **Telemetry Compiler**. We compile a static ontology directly onto live Kubernetes topology. |
| **Academic Anchoring** | Judea Pearl's **Ladder of Causation** + **Causal DAG** as the compiler's *target*. The ontology is the source code; the bound graph is the executable; the human authors the one thing data can't supply — the arrow. |
| **Speed to Value** | Within 15 seconds we name the enemy: the **"dead data" problem** — telemetry collected but never mapped — and show the compiler turning it into structured, predictable causal pathways. |
| **Metaphors** | Forecasting = a **"semantics-blind kinematics clock"** (measures motion, blind to what's moving). Detection = **borrowed normativity** (your declared limits are the bar). Honesty = **"the compiler's warnings"** — what it could not bind, declared by name. |
| **Implicit criticism** | The **"Compiler vs. Guesser"** dichotomy — every jab targets a real, widespread *pattern* (learned baselines, auto-direction, LLM-narrated cause, point-estimate forecasts), never a person. |
| **Rhythm & Cadence** | High-energy, analytical, clean staccato. Slow on *Compiler* and *DAG*; accelerate on the *Micro-Agents*. |

---

## Part 2: Slide-by-Slide Pitch Script

### Slide 1 — The Telemetry Tower of Babel (The Hook)
* **VISUAL:** A chaotic web of raw metrics across namespaces, distros (K3s, MicroK8s), and exporters. Stark title: **"The Dead Data Problem."**
* **ENERGY:** Tense, frustrated — pointing at a systemic industry failure.

> **SCRIPT:**
> You scale your cluster to hundreds of pods on one node. Now try to map how they depend on each other.
>
> The database team exports latency one way. The app team exports request rate another. Different namespaces. Different naming conventions. Different exporters.
>
> *[short pause]*
>
> Traditional monitoring drowns in this. It leaves you with **dead data** — telemetry that's collected but never mapped. So engineers write thousands of lines of custom PromQL by hand, or they throw an expensive LLM at the raw streams and hope the model *guesses* the relationships.
>
> ⚔️ That's the whole industry's move: **guess harder.** We didn't build a better guesser. We built a **compiler**. This is **Vigil**.

---

### Slide 2 — The Telemetry Compiler (Speed to Value)
* **VISUAL:** Split-screen — a curated ontology (left) compiles against the live Kubernetes API topology (middle) → a clean **Bound Customer Graph** *and a Coverage Report* (right).
* **ENERGY:** Relieved, clear, structural.

> **SCRIPT:**
> Vigil doesn't monitor by matching raw strings. Vigil compiles.
>
> We author one versioned **Ontology Graph** at the type level — 842 nodes, 589 signals across CPU, memory, storage, PVC, network, logs, traces, and audit. Everything we know about system physics, signals, and failure patterns, written down once.
>
> At onboarding, our **Binding Engine** compiles that ontology against your live topology. The output is a **Bound Customer Graph** — a tailored, instance-level model of *your* cluster where every metric is resolved, normalized, and mapped to its physical dependencies.
>
> And here's the part a compiler does that a monitor never does: it emits a **coverage report**. Like compiler warnings, it tells you exactly what it *couldn't* bind — which exporter is offline, which workload declared no limit. We turn chaos into an executable map — and we're honest about the gaps in the map.

> **⚔️ Optional add-on line (if you have 10s):** "A monitor shows you what it found. A compiler also shows you what it couldn't resolve. Silence should never be mistaken for safety."

---

### Slide 3 — Judea Pearl's Ladder & the Causal DAG (The Causal Model)
* **VISUAL:** A 3-rung ladder builds (Seeing → Doing → Imagining), then resolves into a clean DAG with nodes `Ingestion → DB → Storage` and directional arrows. Title: **"You can't compute the arrow. You compile it."**
* **ENERGY:** Authoritative, academic — slow down here.

> **SCRIPT:**
> Why a compiler and not a smarter model? Judea Pearl — Turing Award winner — answered this in *The Book of Why*. He draws a **Ladder of Causation**, three rungs.
>
> Rung one — **Seeing.** Correlation. "These move together." This is all of statistics and all of machine learning.
> Rung two — **Doing.** Intervention. "If I change this, what happens to that?"
> Rung three — **Imagining.** Counterfactuals. "What would've happened if I hadn't?"
>
> *[full pause]*
>
> Pearl's theorem — the foundation of our whole design: **you cannot climb from rung one to rung two with data alone.** No amount of correlation tells you the *direction* of the arrow. The arrow is an assumption a human brings from *outside* the data. In his words: *"data is profoundly dumb about causation."*
>
> ⚔️ So watch what the standard tool does. It computes correlation at different time lags. It picks a direction from whichever signal moved first. Then it has a language model write a confident sentence about the cause. But at five-second resolution, "which moved first" is a coin-flip — and a shared third cause makes two unrelated pods correlate across *every* lag window. "After" is not "because."
>
> Vigil refuses to flip that coin. Our compiled **Bound Customer Graph is the Causal DAG** — Pearl's structural model, authored and version-pinned. We don't let a statistical model *invent* the arrow. The AI doesn't guess the "why." The graph contains it.

> **Slide footnote:** Pearl & Mackenzie, *The Book of Why* (2018) — the Ladder of Causation; "data is profoundly dumb about causation."

---

### Slide 4 — The Deterministic Micro-Agents (Value Density)
* **VISUAL:** Three clean columns: **Equivalence Agent** (strays → proposed overlays) · **Topological Agent** (walks the DAG, traces cascades) · **Forecasting Agent** (zero-shot TimesFM clock).
* **ENERGY:** Technical, fast, high density.

> **SCRIPT:**
> To run the compiled map, we deploy three specialized, deterministic micro-agents.
>
> **One — the Equivalence Agent.** It hunts dead data. When an unmapped metric is scraped, it semantically maps it back to a canonical variable and *proposes* a regex rule for a human to review. It's the compiler's parser.
>
> **Two — the Topological Agent.** It walks the causal graph. When a failure starts, it traces the cascade up to two hops over *physical* dependencies — and only over edges that were actually live in the window. Not noisy metric timing. A stale dependency can't fabricate a fake link.
>
> ⚔️ And before it ranks anything, it does what correlation engines skip: it *prunes the fakes*. We run conditional-independence testing — Pearl's back-door reasoning, the algorithm is PCMCI. On our live cluster it cut twenty-six raw correlations to six real candidates and dropped all seven shared-cause edges. The usual fix is "demand a higher score across more lag windows" — but a shared confounder scores high across every window. Stacking windows doesn't remove it; it *launders* it. Conditioning removes it.
>
> **Three — the Forecasting Agent.** Google's TimesFM, zero-shot, run strictly as a **clock** — a semantics-blind kinematics clock. It's blind to pod names, labels, and meaning. It only projects a trajectory against your resolved config limit. It is *structurally impossible* for this agent to hallucinate a cause — it was never given the words for one.

---

### Slide 5 — Provenance + The Honesty Surface (Clarity & Comprehension)
* **VISUAL:** The operator dashboard — three colored streams: `MEASURED` (green), `PROJECTED` (blue), `AUTHORED` (purple) — joined side-by-side, never merged. Beneath them: the **Silence Ledger / Blindspot panel**.
* **ENERGY:** Informational, structured, then quietly proud on the honesty beat.

> **SCRIPT:**
> The compilation lands on the operator's dashboard, under one strict rule: **measured facts, projections, and authored relationships never merge.**
>
> **Measured** — the raw thermometer readings of what's happening now.
> **Projected** — TimesFM's warning of when a metric crosses a bar, shown as a *widening band* of uncertainty, never a flat line. ⚔️ A single number — "OOM in 47 seconds" — is false precision. Uncertainty is the honest unit.
> **Authored** — the human blueprint explaining the relationship, quoted, with a name on it.
>
> We **join** these side by side. We never **fuse** them. ⚔️ The standard demo ends with an AI writing a confident paragraph about the cause — and those same tools list "the model might hallucinate" as a known risk, then "fix" it by feeding cleaner data. But clean data stops a crash, not a confident wrong answer. We never let a model write the word "cause" at all.
>
> ⚔️ And we respect *your* limits. When a tool advertises "threshold-free anomaly detection," it's either flooding you with alarms on normal bursty traffic, or it quietly learned a baseline that calls whatever a pod usually does "fine" — even when "usually" is unhealthy. Vigil borrows the limit you already declared. Right on the first tick. No warm-up, no drift.
>
> *[honesty beat — slow]* And below the findings: the **Silence Ledger.** Most tools show you everything they found. Vigil also shows you everything it *cannot* see — certificate expiry, DNS failure, etcd latency — by name, with the reason. The list of our blind spots is a feature, not an embarrassment. The final counterfactual call stays with you.

---

### Slide 6 — The Agent Harness & Human Governance (Safe Scaling)
* **VISUAL:** The Governance Panel. A "Metric Equivalence Proposal" card with a deterministic **Support Score** (95% metric coverage · 3 distinct pods affected · regex compile check passed). Buttons: **"Promote to Graph"** · **"Reject (provide reason)."**
* **ENERGY:** Controlled, reassuring.

> **SCRIPT:**
> So how does Vigil scale to new microservices and unmapped metrics? The **Intelligent Agent Harness.**
>
> ⚔️ Giving an AI write-access to your production cluster is a disaster waiting to happen. So Vigil enforces a firewalled lifecycle: **Propose → Verify → Promote.**
>
> Our agents use *read-only* tools to gather evidence about unmapped strays. They **never** write to the ontology or your graph. They *propose* — a new metric mapping, a new failure rule.
>
> Vigil then runs a deterministic suite to **verify** it — a support score computed from actual cluster observations. Finally a named human reviews the diff and **promotes** it. The arrow they author is remembered forever, version-pinned, so the next incident is faster.
>
> The human is the absolute governor. The AI is a fast, safe assistant — and it is structurally incapable of inventing your system's causal map behind your back.

---

### Slide 7 — The Replay Guarantee & Edge Stability (Call to Action)
* **VISUAL:** Stark text: **"Pure Go + Python TimesFM clock · Zero CGO · 21 falsification gates · 100% deterministic replay."**
* **ENERGY:** Solid, impactful.

> **SCRIPT:**
> Because Vigil is a compiler, it's deterministic.
>
> We run a validation harness that *replays* cluster incidents. Same metrics, same graph, same topology — **byte-identical** results, every time, guarded by twenty-one falsification gates. So when you're triaging a live outage and someone asks "are you *sure*?" — you don't argue. You replay.
>
> It's built in pure, CGO-free Go with a gRPC Python forecasting service — designed to run on lightweight edge boxes where CPU and memory are scarce. The exact single-node, industrial environments this theme is about.
>
> We don't ask you to trust a black box. We compile your cluster's physics, predict its bottlenecks, and *prove* every finding.
>
> Vigil. It detects what's failing, forecasts what's about to, and refuses to invent *why*. **Beyond monitoring — toward understanding you can actually trust.**
>
> Thank you.

---

## Part 3: Presenter Delivery Guide

* **Key metaphor:** **"Compiler vs. Guesser."** Hammer it. Other systems *guess* names and relationships; Vigil *compiles* them like source code.
* **Cadence:** slower and deliberate on *Compiler* (Slide 2) and *DAG / Ladder* (Slide 3); accelerate through the *Micro-Agents* (Slide 4) to show execution velocity; settle into a calm, proud tone for the *Honesty Surface* (Slide 5).
* **The three pauses** — land each by stopping and dropping pitch: *"data is profoundly dumb about causation"* (S3) · *"'after' is not 'because'"* (S3) · *"silence should never be mistaken for safety"* (S5).
* **Repeaters** — say every load-bearing idea twice, once technical, once as metaphor (already written in: "conditional-independence pruning" → "asking if they're still linked once you account for the third thing").
* **Walkaway line** a judge can repeat in the hallway: *"It refuses to invent the cause — and that's exactly why you can trust it."*

---

# Appendix A — Theme requirement → where we satisfy it
*For Q&A — prove we hit every line of the brief.*

| Brief asks for | Vigil delivers | Slide |
|---|---|---|
| Real-time discovery (CPU, RAM, disk, **PVC**, network) | 589 signals, 7 modalities, per-instance binding | 2, 4 |
| Multi-agent AI across CPU/Mem/Storage/Log-IO | Three deterministic micro-agents + read-only MCP layer | 4 |
| Interdependency mapping | Compiled DAG + observed topology (flow/eBPF/conntrack) + pruned candidates | 3, 4 |
| Recommendations: optimization, **alerts**, **forecasting** | Right-sizing advisory · early-warning bands · phenomena | 4, 5 |
| Rich dashboard + NLP insights | React console: provenance streams, causal graph, coverage | 5 |
| Prototype on K3s/MicroK8s/Minikube | Cluster-agnostic identity; runs on kind / k3s / single-node | 7 |
| Single-node / edge | Borrowed normativity + capability detection; CGO-free Go | 5, 7 |

**The judges' four foundational questions, the Vigil way:**
1. *Which pod causes the CPU spike?* → a ranked candidate **with evidence and an authored arrow** — never an auto-guess.
2. *How is PVC I/O linked to restarts?* → a phenomenon + cascade across the storage→pod topology; the link stays correlation until a human confirms direction.
3. *Are services starving each other?* → direction-free co-onset hypotheses, **confounder-pruned**, operator-authored.
4. *Which workloads need optimizing?* → off-digest right-sizing advisory against **declared** limits.

---

# Appendix B — The Book of Why → Vigil (the compiler mapping)

| Pearl (*The Book of Why*) | Vigil as a Telemetry Compiler |
|---|---|
| **Rung 1 — Association.** Correlation. | Detection's co-occurrence + association lane. Surfaced **as** correlation (MEASURED), never relabeled cause. |
| **You can't reach Rung 2 from data alone — supply a causal model.** | The **ontology = source code**; the **Bound Customer Graph = the executable Causal DAG**. The model is supplied, not inferred. |
| **Confounding / back-door / d-separation.** | **PCMCI conditional-independence pruning** drops shared-cause & transitive edges (26→6 live; 7/7 confounders). |
| **The arrow is an assumption, not a measurement.** | **Direction is operator-authored**, then compiled into the version-pinned graph. |
| **Counterfactual honesty.** | Forecasts are **banded**, horizon-stated, silent by default — never a point "will." |
| **"Data is profoundly dumb about causation."** | Charter: no model may author a cause; MEASURED / PROJECTED / AUTHORED **join, never fuse**. |

**The one-liner:** *"Everyone's trying to compute causation from correlation. Pearl proved that's impossible. So we compile it instead — and let a human supply the one line of source data can't."*

---

# Appendix C — The implicit-criticism lines, collected (and why they're fair)
*Every line targets a real, widespread engineering pattern — verified against actual production-style engine code — framed as "said to be done this way → better way," never naming anyone.*

| # | Slide | The line | The real pattern it targets |
|---|---|---|---|
| 1 | 1 | "We didn't build a better guesser. We built a compiler." | Throwing an LLM at raw streams to *guess* metric relationships. |
| 2 | 3 | "At 5-second resolution, 'which moved first' is a coin-flip. 'After' is not 'because.'" | Auto-picking causal direction from time-lag / onset ordering (always returns a direction, even at zero real lag). |
| 3 | 4 | "A shared confounder scores high across every window. Stacking windows doesn't remove it; it launders it." | "Require high correlation across more lag windows" offered as the false-positive fix — passes confounders straight through; no conditional-independence step. |
| 4 | 5 | "Clean data stops a crash, not a confident wrong answer. We never let a model write the word 'cause.'" | An LLM narrator emitting "X is the probable root cause," with "hallucination" listed as a known risk mitigated by "clean input." |
| 5 | 5 | "Threshold-free anomaly detection either floods you, or quietly learns a baseline that calls 'usually' fine." | Deviation-from-learned-baseline detection (warm-up required; "normal" is statistical, not your declared limit). |
| 6 | 5 | "A single number is false precision. Uncertainty is the honest unit." | Point-estimate forecasts ("OOM in N seconds") with no uncertainty band. |
| 7 | 2/5 | "Silence should never be mistaken for safety." | Tools that surface only what they found, with no coverage report or declared blind spots. |

**Why this is fair, not cheap:** we contrast *patterns*, not people. Each pattern is genuinely common and genuinely weaker than the alternative. If a judge has seen a tool that does these things, the lines land; if not, they still teach. Tune the heat per room; drop any line freely.

---

# Appendix D — Sources
- Judea Pearl & Dana Mackenzie, *The Book of Why* (2018) — Ladder of Causation; the *do*-operator; "data is profoundly dumb about causation."
- *The Book of Why* — Wikipedia: https://en.wikipedia.org/wiki/The_Book_of_Why
- Pearl, "The Seven Tools of Causal Inference" / "Measuring Causality": https://arxiv.org/pdf/1910.08750
- Vigil internal: `docs/01-epistemic-separation-charter.md` · `docs/04-binding-and-generalization-engine.md` (the compiler) · `docs/07-topological-detection-engine.md` · `docs/09-forecasting-layer.md` (banded forecast + backtest gate) · `docs/19-mcp-blindspot-surface.md` (silence ledger / blindspots) · `docs/22` & `docs/29` (PCMCI pruning, direction-unreliability probes).
