# VIGIL — Judge Pitch Script
### Theme 2: Beyond monitoring — AI agents for real-time pod resource discovery and dependency mapping

> **One sentence:** Vigil detects what is failing now, forecasts what crosses a limit soon, and *refuses to invent why* — and that refusal is exactly what makes it the only tool you can trust at 3am.

This script is built on the **8 short-form storytelling principles** (Curiosity & Contrast · Speed to Value · Value Density · Clarity & Comprehension · Absorption Rate · Anticipation · Emotional Resonance · Rhythm & Tone). It is paced for **~8 minutes** but every section is modular — cut Section 4's demo depth or Section 3's lane count to hit 5 minutes.

**How to read this script**
- **`SAY:`** = the words to speak (6th-grade reading level, active voice, mostly staccato — say it almost exactly).
- **`SCREEN:`** = what's on the slide / demo behind you.
- **`BEAT:`** = director's note (tone, pause, the contrast moment).
- **`⚔️ CONTRAST`** = an *implicit-criticism* line. We never name a competitor. We say "the standard approach is said to work this way — but there's a better way." These lines are collected in **Appendix C** so you can tune or drop them.

---

## SECTION 0 — COLD OPEN / THE HOOK  ⏱ 0:00–0:35
*Principles: Curiosity & Contrast · Speed to Value · Emotional Resonance · Anticipation*

**SCREEN:** A single-node cluster. ~200 pods, multiple namespaces. Everything green. One number deep in the grid is climbing.

**SAY:**
> It's 3am. One node. Hundreds of pods.
> Something is dragging the whole cluster down — and every dashboard is green.
> You ask the one question that matters: *which pod did this?*

**BEAT:** *Pause. Drop your voice for the contrast.*

> Here's the part nobody admits.
> In the next ten seconds, almost every "AI" tool on the market will tell you the cause — confidently.
> And that confidence is the most dangerous thing in your incident.

**BEAT:** *Lift energy. This is the value signal — say it punchy.*

> We built Vigil to do the opposite. To be the one tool that refuses to lie to you.
> And here's the strange part: *that* is what makes it the only one you can trust when it's 3am and the actuator hasn't fired.

**⚔️ CONTRAST** (seeded, not yet aimed): *"confidently … the most dangerous thing"* — frames the whole category's confident-root-cause demo as the risk, before we ever describe ours.

---

## SECTION 1 — THE PROBLEM, FELT  ⏱ 0:35–1:35
*Principles: map the common belief · find the pain they care about · Value Density*

**SCREEN:** The four questions from the theme, appearing one line at a time.

**SAY:**
> The brief asks four questions. They sound simple. They're brutal.
> *Which pod is causing the CPU spike? How is PVC I/O linked to pod restarts? Are services starving each other? What needs optimizing?*
> Every one of those is a question about **cause**. Not "what happened" — *why*.

**BEAT:** *This is the crack. Slow down.*

> And here's the trap the whole industry walks into.
> The metrics are full of things that move together. CPU and disk. Memory and restarts.
> Moving together is **correlation**. The brief is asking for **cause**.
> Those are not the same thing — and a tool that pretends they are will point you at the wrong pod, at 3am, with a straight face.

**SAY** (the common belief, named so we can break it):
> So the standard playbook is: scrape every metric, throw it at an AI, let it correlate everything, and have a language model announce the root cause.
> It demos beautifully. It's also, quietly, a guess wearing a lab coat.

**⚔️ CONTRAST:** *"a guess wearing a lab coat"* — names the dominant pattern (correlate-everything → LLM names the cause) as the thing to distrust. The competitor's pipeline is literally `lagged-Pearson → auto-pick-a-direction → LLM narrative`; this line targets that shape without naming them.

---

## SECTION 2 — THE BOOK OF WHY  ⏱ 1:35–3:35  ★ THE INTELLECTUAL CORE
*Principles: Absorption (reframe an old idea in a new way) · Anticipation · the biggest Contrast in the talk*

**SCREEN:** Book cover — *The Book of Why*, Judea Pearl. Then a 3-rung ladder builds upward.

**SAY:**
> To explain why Vigil is built the way it is, I have to hand you one idea. It comes from Judea Pearl — Turing Award winner, the man who gave machines a grammar for cause — in his book *The Book of Why*.
> He draws a ladder. **The Ladder of Causation.** Three rungs.

**SCREEN:** Rung 1 lights.
> **Rung one — Seeing.** Association. "These two things move together." This is correlation. This is *all* of classical statistics and *all* of machine learning. It answers: *what if I see X?*

**SCREEN:** Rung 2 lights.
> **Rung two — Doing.** Intervention. "If I *change* this, what happens to that?" This needs a model of the world. Pearl built the math for it — the *do*-operator.

**SCREEN:** Rung 3 lights.
> **Rung three — Imagining.** Counterfactuals. "What *would* have happened if I hadn't done that?"

**BEAT:** *This is the bombshell. Full stop before it. Make eye contact.*

> Now here is Pearl's bombshell — and it is the foundation of our entire project.
> **You cannot climb from rung one to rung two with data alone.**
> No amount of correlation — not lagged, not filtered, not stacked across windows — will ever tell you the *direction* of the arrow. The arrow is not in the data. The arrow is an *assumption* a human brings from outside the data.
> In Pearl's words: *data is profoundly dumb about causation.*

**BEAT:** *Now aim it. This is the talk's sharpest contrast.*

> So look again at the standard "AI root cause" tool.
> It computes correlation — rung one.
> Then it *guesses* an arrow from which signal twitched first.
> Then it has a language model write a confident sentence about the cause.
> That is a rung-one machine wearing a rung-two costume. Pearl proved that costume is empty forty years ago.

**SAY** (the non-obvious claim — the hook pays off):
> So we made a choice that sounds insane in a hackathon: **Vigil refuses to invent the arrow.**
> And that one refusal *is* the architecture. Let me show you what it buys you.

**⚔️ CONTRAST:** *"a rung-one machine wearing a rung-two costume"* — the central, principled jab. It's not an insult; it's Pearl's theorem applied to the whole correlate-then-name-the-cause genre. Devastating *because* it's true and cited.

> **Sources for the slide footnote:** Pearl & Mackenzie, *The Book of Why* (2018) — Ladder of Causation (Association / Intervention / Counterfactual); "data is profoundly dumb about causation." See Appendix D.

---

## SECTION 3 — HOW VIGIL CLIMBS THE LADDER HONESTLY  ⏱ 3:35–6:05
*Principles: Clarity & Comprehension (reframe + simplify) · Value Density · Repeaters (technical, then metaphor)*

**BEAT:** *Transition line — this is your spine. Each beat below = one rung-honest move, each carries one quiet contrast.*

**SAY:**
> Pearl's recipe is simple: use data for what data *can* do — find associations, kill the fakes — and let a human supply the one thing data can't: the arrow. Vigil is that recipe, turned into a running system. Six moves.

### 3.1 — Borrowed normativity (not a learned baseline)
**SCREEN:** A pod with a 512Mi limit; bar drawn at ~486Mi, sourced from *its own* config.
> **One.** What counts as "too much"? We don't learn it. We *read* it — from your own Kubernetes limits and your own SLOs. Your declared 512-meg limit *is* the bar. No training. No warm-up. The same rule fits a tiny pod and a huge one, because the number comes from *you*.

**⚔️ CONTRAST:**
> The common approach learns a "normal" baseline for each pod over a warm-up window. Sounds smart. But a learned baseline calls whatever a pod *usually* does "fine" — even when "usually" is quietly unhealthy — and it drifts, and it needs fifteen minutes of soak before it'll even talk to you. We borrow the limit you already declared. It's right on the first tick, and it never drifts.

*(Grounded: their detection is deviation-from-learned-baseline with a 15–20 min warm-up; "normal" is statistical, not the declared limit.)*

### 3.2 — Phenomena, not alerts
**SCREEN:** A multi-signal pattern lighting across three related pods; a 2-hop cascade A→node→B.
> **Two.** A single metric crossing a line is not an alert here. It's noise. Vigil matches **phenomena** — multi-signal failure patterns — across the *topology*: a pod saturates, the node bottlenecks, a neighbor throttles. We walk that chain up to two hops, and we only walk an edge that was actually *live* during the window. A stale dependency can't fabricate a fake link.

### 3.3 — The ontology graph = Pearl's causal model
**SCREEN:** The graph: 842 nodes, 589 signals, 7 modalities, versioned `v0.13.0`.
> **Three.** Remember Pearl's "assumptions from outside the data"? We wrote them down. It's a human-authored, version-pinned **ontology** — 842 nodes, 589 signals across CPU, memory, storage, network, logs, traces, audit. This is the model. The data lights it up; the data never *invents* it.

### 3.4 — Rung one, done rigorously: conditional-independence pruning
**SCREEN:** 26 raw correlations collapsing to 6 candidates; 7 confounder edges struck through.
> **Four.** Now we *do* use correlation — but honestly, and we kill the fakes. The classic false alarm is two pods that look coupled only because a *third* thing drives both — a shared node, a cron tick. We run conditional-independence pruning — Pearl's back-door reasoning, the algorithm is called PCMCI — and on our live cluster it cut twenty-six raw correlations down to six real candidates and dropped all seven confounder edges.

**⚔️ CONTRAST:**
> The usual fix for false correlations is "require a high score across more lag windows." But think about it — a shared confounder scores high across *every* window. Stacking windows doesn't remove it; it *launders* it. The only real fix is conditioning — asking "are these still linked once I account for the third thing?" That's what we run.

*(Grounded: their stated #1 risk is correlation false-positives; their mitigation is "high r across multiple lag windows + eBPF confirmation" — which a common driver passes straight through. No conditional-independence step exists in their engine.)*

### 3.5 — The arrow is authored, never guessed
**SCREEN:** The direction-free *CausalHypothesis* tab: "A ~ B coupled, both stepped within window W." Operator clicks `A → B`. It commits into the graph.
> **Five.** This is the heart. When two things genuinely move together, we do **not** pick a direction for you. We hand you a ranked shortlist of direction-*free* hypotheses, with the evidence — who led, by how much, over which shared resource. Then *you* — the operator who actually knows the system — draw the arrow. Once. And it's remembered forever, version-pinned into the graph, so the next 3am is faster.

**⚔️ CONTRAST:**
> The standard pipeline picks the direction automatically — from which signal moved first. At five-second resolution that ordering is a coin-flip, and we tested exactly this: with no real lead at all, that kind of logic still draws an arrow about eighty-five percent of the time, and with a pure confounder it invents an edge that was never there. "After" is not "because." We refuse to flip that coin. You draw the arrow; the math just hands you the short list.

*(Grounded: their `best_directed()` always returns a direction — `forward=True` by default even on a zero-lag tie — and orients edges by temporal precedence; our E3b/E4b probes against that logic showed direction drawn ~170/200 at zero lag and a fabricated `svc-a→svc-b` from a pure confounder.)*

### 3.6 — Forecast with a band, never a point; and never let a model name a cause
**SCREEN:** "Working set projected to cross its limit in ~13 min — band 9–22 min. Known OOM precursor *per the graph*. At risk: node X + 4 siblings." Three labels, three colors.
> **Six.** We forecast — *when* does this number cross that limit — with a model that's good at exactly that and nothing else. But we never give you a single number. We give you a *band*: "crosses in about thirteen minutes, somewhere between nine and twenty-two." Uncertainty is the honest unit.
> And every finding keeps its label to the screen. **Measured** is what we saw. **Projected** is what might happen — always a band, never a line. **Authored** is a human's note, quoted, with their name on it. We *join* them side by side. We never *fuse* them into one confident sentence — and we never let a language model write the word "cause."

**⚔️ CONTRAST:**
> The standard demo ends with an AI writing a confident paragraph: "X is the probable root cause, causing Y." Notice the risk they all list in their own slides — *the model might hallucinate*. Their fix is "feed it clean data." But clean data doesn't stop a confident wrong answer — it just stops a crash. We don't ask a model to be honest about cause. We don't let it speak about cause at all.

*(Grounded: their L4 is an Ollama/Gemma narrator that emits "cooling-monitor I/O spike is the probable root cause…"; their own Risk #2 is "LLM hallucination," mitigated by "clean JSON input." Their OOM forecaster returns a single point ETA — no uncertainty band.)*

### 3.7 — The honesty surface (the move nobody else makes)
**SCREEN:** Silence ledger + coverage report + blindspot registry. "We cannot see: cert expiry, DNS failure, etcd latency — here's why."
> And then the move I'm proudest of. Most tools show you everything they *found*. Vigil also shows you everything it *cannot see*. Certificate expiry. DNS failures. etcd slowness — if the metric isn't scraped, we don't guess, we *declare the blind spot*, by name, with the reason. So you never mistake our silence for safety. The list of what we can't see is a feature, not an embarrassment.

---

## SECTION 4 — PROOF: THE LIVE DEMO  ⏱ 6:05–7:25
*Principles: Value Density · Emotional payoff · "walkaway value"*

**SCREEN:** Live cluster. Fire the PVC I/O contention cascade (the disk-storm scenario).

**SAY** (narrate as it happens — short sentences):
> Watch. I trigger a disk storm on a shared volume. Every pod still reports healthy.
> Vigil lights a **phenomenon** — not one alert, the whole pattern.
> It traces the cascade across the *observed* dependency graph — the real network and storage links, discovered, not configured.
> Here's the candidate list. Two services, genuinely coupled. Vigil does **not** tell me who's to blame. It ranks them and shows me the evidence.
> I author the arrow. *Now* it's in the graph — and it'll be there next time, instantly.
> Over here, the forecast: a working set climbing toward its limit, with a band and a horizon. Early warning, not a postmortem.
> And down here, the silence ledger — what I'm *not* watching on this cluster, in writing.

**BEAT:** *The trust mic-drop.*
> One more thing. Every tick Vigil produces is **byte-for-byte replayable**. Twenty-one falsification gates guard it. So when you're triaging a live outage and someone asks "are you *sure*?" — you don't argue. You replay. Same inputs, same answer, every time.

**SCREEN (numbers to leave on screen):**
> 842 graph nodes · 589 signals · 7 modalities · 21 falsification gates · onset detection +6s vs +66s for the textbook alternative · 26→6 candidate pruning, 7/7 confounders dropped · per-cluster coverage report.

---

## SECTION 5 — THE CLOSE  ⏱ 7:25–8:15
*Principles: callback to the hook · Emotional Resonance · the line they remember*

**SCREEN:** Back to the 3am cluster. Now: one phenomenon lit, one authored arrow, one forecast band, one honest blind-spot list.

**SAY:**
> Back to 3am.
> One tool hands you a confident guess and a paragraph that *sounds* like an answer.
> The other hands you what's measured, what's projected, what a human authored — each labeled — plus an honest list of what it can't see.
> Under pressure, you don't want the one that sounds smartest. You want the one you can trust.

**BEAT:** *Slow. Land it.*
> Pearl gave us the ladder. Everyone else pretends their machine climbed it on its own.
> We're the ones who admit a human has to take the last step — and we make that step take five seconds, and last forever.

**SAY** (the tagline):
> Vigil. It detects what's failing, forecasts what's about to, and refuses to invent why.
> Beyond monitoring — toward understanding you can actually trust.

**SCREEN:** Logo + tagline. Hold.

---

# APPENDIX A — Theme requirement → where we satisfy it
*Use in Q&A to prove we met every line of the brief.*

| Brief asks for | Vigil delivers | In the talk |
|---|---|---|
| Real-time discovery (CPU, RAM, disk, **PVC**, network) | 589 signals, 7 modalities; per-instance binding | 3.1, 3.3, demo |
| Multi-agent AI across CPU/Mem/Storage/Log-IO | Off-digest lanes + read-only MCP agents reasoning over classed facts | 3.4–3.6 |
| Interdependency mapping | Observed topology (flow/eBPF/conntrack) + authored relations + causal candidates | 3.2, 3.5, demo |
| Recommendations: optimization, **alerts**, **forecasting** | Right-sizing advisory, early-warning bands, phenomena | 3.6, demo |
| Rich real-time dashboard + NLP insights | React console: causal graph, timeline, coverage, chat | demo |
| Prototype on K3s/MicroK8s/Minikube | Runs on kind / k3s / single-node; cluster-agnostic identity | demo |
| Single-node / edge | Borrowed normativity + capability detection, no heavy assumptions | 3.1, 3.7 |

**The judges' four foundational questions, answered Vigil's way:**
1. *Which pod is causing the CPU spike?* → A ranked candidate with evidence and an **authored** arrow — never an auto-guess.
2. *How is PVC I/O linked to restarts?* → A phenomenon + cascade across the storage→pod topology, with the link surfaced as correlation until a human confirms direction.
3. *Are services starving each other?* → Direction-free co-onset hypotheses, confounder-pruned, operator-authored.
4. *Which workloads need optimizing?* → Off-digest right-sizing advisory against declared limits.

---

# APPENDIX B — The Book of Why → Vigil, the exact mapping
*If a judge is technical, this is your deep cut.*

| Pearl (*The Book of Why*) | Vigil's practical implementation |
|---|---|
| **Rung 1 — Association / Seeing.** P(Y\|X). Correlation. | Detection's co-occurrence + the association lane. Surfaced **as correlation** (MEASURED), never relabeled as cause. |
| **"You can't get to Rung 2 from data alone — you must supply a causal model."** | The **ontology graph** = the supplied model: human-authored, versioned admissible entity types, phenomena, and trigger→downstream edges. |
| **Confounding / back-door / d-separation.** | **PCMCI conditional-independence pruning** drops common-driver & transitive edges (26→6 live; 7/7 confounders). |
| **The causal arrow is an assumption, not a measurement.** | **Direction is operator-authored.** Vigil ranks direction-free candidates; a named human draws the arrow; it commits into the graph. |
| **Rung 2 — Intervention / Doing.** P(Y\|do(X)). | The authored arrow + blast-radius become the actionable "if this, then downstream" — owned by a human, not a model. |
| **Counterfactual honesty / uncertainty.** | Forecasts are **banded** ("might cross, 9–22 min"), horizon-stated, silent by default — never a point "will." |
| **"Data is profoundly dumb about causation."** | The charter: no LLM may author a cause; MEASURED / PROJECTED / AUTHORED **join, never fuse**. |

**The one-liner for the room:** *"Everyone's trying to squeeze causation out of correlation. Pearl proved that's impossible. So we stopped trying — and that's exactly why our answers hold up."*

---

# APPENDIX C — The implicit-criticism lines, collected
*Every jab below is (a) never names a competitor, (b) framed as "the standard approach is said to work this way — here's a better way," and (c) grounded in real, common engineering. Tune the heat per audience; drop any line freely.*

1. **(Hook)** "Almost every AI tool will tell you the cause, confidently — and that confidence is the most dangerous thing in your incident."
2. **(Problem)** "Throw it at an AI, let it correlate, have a model announce the root cause … a guess wearing a lab coat."
3. **(Book of Why)** "A rung-one machine wearing a rung-two costume. Pearl proved that costume is empty forty years ago." ← *the centerpiece.*
4. **(Baselines)** "A learned baseline calls whatever a pod usually does 'fine' — even when usually is quietly unhealthy — and it needs fifteen minutes before it'll even talk to you."
5. **(Confounders)** "Require a high score across more lag windows? A shared confounder scores high across every window. Stacking windows doesn't remove it; it launders it."
6. **(Direction)** "Pick the direction from which signal moved first? At five-second resolution that's a coin-flip. 'After' is not 'because.'"
7. **(LLM cause)** "Their own slides list the risk: the model might hallucinate. Their fix is 'feed it clean data.' Clean data stops a crash, not a confident wrong answer."
8. **(Honesty)** "Most tools show you everything they found. We also show you everything we can't see."

**Why these are fair, not cheap:** each targets a *real, widespread* engineering pattern that the dominant approach genuinely uses — learned baselines, lag-window stacking, first-mover direction, LLM narration of cause, point-estimate forecasts. We contrast *patterns*, not people. If a judge has seen a tool that does these things, the lines land; if not, they still teach.

---

# APPENDIX D — Delivery notes (Rhythm, Tone & Energy)
*From the storytelling system — the recording/delivery layer.*

- **Staccato by default.** Short sentences. The brain absorbs one idea, shifts it to memory, takes the next. Run-ons delete the oldest idea to make room.
- **Vary the rhythm at the contrast beats.** Most of the talk is fast and clipped. At the three big pauses — *"the most dangerous thing,"* *"you cannot climb from rung one with data alone,"* *"a human has to take the last step"* — **stop**, drop your pitch, slow down. Silence is the frame around the non-obvious claim.
- **Transfer energy through the screen.** Force the pace up 10% above your natural level; a hackathon room reads calm as low-conviction.
- **Repeaters.** Say every load-bearing point twice — once technical, once as a metaphor. ("Conditional-independence pruning" → "asking whether they're still linked once you account for the third thing.") It's in the script; keep both halves.
- **Emotional spine = fear → relief.** The 3am outage and the actuator that never fired carry the fear; "you replay, same answer every time" carries the relief. Land both.
- **Anticipation.** In Section 0 you promise "the one thing everyone else does that we refuse to do." Don't reveal it until Section 2. Make them wait for the arrow.
- **The walkaway value.** A judge should be able to repeat one sentence in the hallway: *"It refuses to invent the cause — and that's why you can trust it."* Say it enough that it sticks.

---

# APPENDIX E — Sources
- Judea Pearl & Dana Mackenzie, *The Book of Why: The New Science of Cause and Effect* (2018) — the Ladder of Causation (Association / Intervention / Counterfactual); the do-operator; "data is profoundly dumb about causation."
- *The Book of Why* — Wikipedia: https://en.wikipedia.org/wiki/The_Book_of_Why
- Pearl, "The Seven Tools of Causal Inference" / "Measuring Causality: The Science of Cause and Effect": https://arxiv.org/pdf/1910.08750
- Vigil internal: `docs/01-epistemic-separation-charter.md` (provenance, join-never-fuse), `docs/29-anomaly-detection-and-causal-inference.md` (CUSUM bake-off, PCMCI pruning), `docs/22-competitor-component-adoption.md` (direction-unreliability probes E3b/E4b), `docs/09-forecasting-layer.md` (banded forecast, backtest gate), `docs/07-topological-detection-engine.md`, `docs/19-mcp-blindspot-surface.md`.
