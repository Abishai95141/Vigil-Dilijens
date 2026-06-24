# Vigil — Pitch Deck Content & Research Pack

> **Vigil detects what is failing now, forecasts what crosses a limit soon, and refuses to invent *why* — and that refusal is exactly what makes it the only tool you can trust at 3am.**
>
> *Reports what is. Estimates what's soon. Never invents why.*

---

## How to use this document

This is the **source content** for a Vigil pitch deck — one section per investor-deck slide, in the order requested: **Goal · Housekeeping · Problem · Solution · Market · Competition · Go-to-Market · Product · Business Model · Key Milestones · Capital Requirements**, plus three appendices (proof-point cheat-sheet, sourced citations, narrative spine).

Each section contains: a **slide headline**, the **narrative** (what the slide says), **key bullets/stats**, a **suggested visual**, and **speaker notes** where useful. Build the visual deck from this; don't paste it verbatim.

**Honesty flags — read these (they fit the brand):**

- 🟢 **Grounded** = verified in the Vigil codebase / README / docs, or a market figure re-fetched and confirmed on a primary source.
- 🟡 **Founder-owned / illustrative** = business strategy (pricing, GTM, capital, milestones-forward). The repo contains **no** prior business content, so these are reasoned proposals anchored to comparable companies — the founder must own and adjust them.
- 🔴 **Handle with care** = a market/competitor figure that is soft, dated, or disputed; phrasing guidance is in Appendix B. Do not put these on a slide without the caveat.

A deck that over-claims would betray the entire thesis. Where we don't have a number (revenue, customers), the deck says so. That is on-brand, not a weakness.

---

## Deck at a glance (the 13-slide spine)

| # | Slide | One-line job |
|---|---|---|
| 1 | **Title / Goal** | Who we are + the mission in one sentence |
| 2 | **Housekeeping** | Snapshot: stage, team, what's real today, the ask |
| 3 | **Problem** | At 3am, every tool guesses the cause — confidently, and often wrong |
| 4 | **Why now** | Cloud-native complexity + the AIOps trust collapse |
| 5 | **Solution** | An engine that refuses to invent why — provenance you can trust |
| 6 | **Product** | Watch → Detect → Forecast, under one strict rule |
| 7 | **Demo / proof** | 842-node ontology, 21 falsification gates, byte-identical replay |
| 8 | **Market** | $14.2B observability by 2028 inside an $81B ITOM envelope |
| 9 | **Competition** | The 2×2: governed determinism + honest refusal = open white space |
| 10 | **Business model** | Predictable per-cluster pricing — we don't tax your data |
| 11 | **Go-to-market** | Open-source-led, land in regulated + AI-infra teams |
| 12 | **Milestones / roadmap** | What's de-risked, what's next |
| 13 | **The ask** | Capital requirements + use of funds |

---

# 1 · Goal

**Slide headline:** *Make operational truth trustworthy.*

**Narrative.** Vigil's goal is to be the **ground-truth layer for production systems** — the one instrument an operator (or an AI agent) can trust when everything else is on fire. Not another dashboard, not another alert firehose, not another AI that narrates a confident guess. An engine whose every statement carries the *class of knowledge it came from* — measured fact, model projection, or human-authored assertion — and that never silently merges them.

**The mission, stated three ways (pick one for the slide):**

- *"Beyond monitoring — toward an understanding you can actually trust."*
- *"The observability you can put in front of an auditor."*
- *"Reports what is. Estimates what's soon. Never invents why."*

**The arc (why this is a venture-scale goal, not a feature):** 🟡

1. **Today** — the most trustworthy operational-intelligence engine for Kubernetes.
2. **Next** — the trust/provenance layer across any complex distributed system (the engine is cluster-agnostic and modality-agnostic by construction).
3. **The big vision** — Gartner predicts **70% of enterprises will deploy agentic AI to autonomously operate IT infrastructure by 2029** (vs. <5% in 2025). Every one of those agents will need a *non-hallucinating substrate of grounded facts* to reason over — or they will confidently act on garbage. **Vigil is built to be that substrate.** Its MCP relay already exposes classed, governed facts to AI agents today. As ops becomes agentic, the scarcest resource is *trustworthy ground truth*. We make it.

**Speaker note:** The goal slide should land the inversion: everyone else is racing to make the AI *smarter*; we're making the foundation *trustworthy*. In an agentic-ops world, the trustworthy foundation is the harder and more valuable thing to own.

**Suggested visual:** Three stacked layers — *Measured facts → Governed provenance → Agents act on it* — with Vigil as the middle, load-bearing layer everything stands on.

---

# 2 · Housekeeping

**Slide headline:** *Where things stand — honestly.* (A short orientation slide; some investors call it the "company snapshot.")

**Company snapshot:** 🟡 *(founder to confirm/fill)*

| | |
|---|---|
| **Product** | Vigil — Kubernetes-native operational intelligence engine |
| **Stage** | Working system, pre-revenue; seeking design partners + seed capital |
| **Team** | *[Founder name(s), backgrounds — fill in]* |
| **Location / structure** | *[HQ / incorporation — fill in]* |
| **Raising** | Seed (see §11) |
| **Contact** | *[email / site]* |

**What is real *today* (so investors calibrate — all 🟢, from the codebase):**

- A working, **CGO-free Go** engine + **Python TimesFM 2.5** forecasting clock, running live on real Kubernetes clusters (kind, boutique microservices, AWS bring-up).
- A **versioned ontology of 842 nodes / 589 signals / 7 modalities** (`v0.13.0`, content-hashed and release-engineered).
- **21 falsification gates** and **byte-identical deterministic replay** — every finding reproducible from pinned inputs.
- A live operator **console** (React/TypeScript), an **MCP server** exposing classed facts to AI agents, and a working **PROPOSE → VERIFY → PROMOTE** governance loop.

**What is *not* yet real (stated plainly — fits the brand):**

- No paid customers and no revenue yet. (This deck does not pretend otherwise.)
- Application-signal ingestion depth (per-service request-rate / latency / staleness vs. declared SLOs) is the next build, not a shipped claim.

**This-deck roadmap (optional one-liner):** *Problem → why now → what we built → the market → why we win → how we make money → the plan → the ask.*

**Speaker note:** Open by naming the stage honestly. For a product whose entire pitch is "we don't over-claim," the credibility dividend of an honest housekeeping slide is enormous — it makes every later claim more believable.

---

# 3 · Problem

**Slide headline:** *At 3am, every tool tells you the cause — confidently. That confidence is the most dangerous thing in your incident.*

**Cold-open narrative (straight from the pitch script):** 🟢

> It's 3am. One node. Hundreds of pods. Something is dragging the whole cluster down — and every dashboard is green. You ask the only question that matters: *which pod did this, and why?* In the next ten seconds, almost every "AI" tool on the market will answer — confidently. And it's usually **a guess wearing a lab coat.**

**The core problem (three layers):**

1. **Complexity has outrun human comprehension.** The average enterprise spans ~12 cloud platforms; **86% of tech leaders say cloud-native data volume is "beyond humans' ability to manage"** (Dynatrace, 2024). 🟢 Two-thirds of Kubernetes containers live **under 10 minutes** (Datadog, 2025) 🟢 — per-pod monitoring breaks under that churn.

2. **The dominant AI answer is to guess the cause — and the guesses are mostly wrong.** The industry playbook: scrape everything, correlate it, and let an LLM announce the root cause. It demos beautifully. But on **OpenRCA** (Microsoft Research / Tsinghua, ICLR 2025 — 335 real failures), **the best model, Claude 3.5, solved only 11.34% of root-cause cases** even with a purpose-built RCA agent. 🟢 A follow-up study (arXiv 2026, 1,675 agent runs) found hallucinated interpretations "persist across all models… prompt engineering alone cannot resolve the dominant pitfalls." 🟢

3. **It costs a fortune and burns out your team.** Median observability spend is **$1.95M/yr; 67% of orgs spend ≥$1M/yr** (New Relic, 2024); 🟢 **cost is the #1 observability concern** (Grafana, 2025). 🟢 Meanwhile **~70% of SREs say on-call stress drives burnout, and toil *rose* to 30% of their time despite AI investment** (Catchpoint, 2025). 🟢

**The cost of being wrong:** Downtime costs the Global 2000 **~$400B/year — about 9% of their profits** (Splunk/Oxford Economics, 2024); 🟢 a high-impact outage runs a **median $1.9M/hour** (New Relic, 2024). 🟢 When the cause is wrong, you don't just lose the time — you fix the wrong thing while the clock runs.

**The one-sentence problem:** *The moment you most need your monitoring to be trustworthy — during an incident — is the exact moment it fuses measured facts, learned baselines, and confident guesses into an opaque alert, and you have no way to see why.*

**Suggested visual:** Split screen — left, a confident AI banner reading "ROOT CAUSE: pod-api-7 (likely)"; right, the OpenRCA stat "11.34% correct." Caption: *"Likely" is not good enough at 3am.*

---

# 4 · Why Now

**Slide headline:** *Two curves crossed: complexity exploded, and trust in black-box AIOps collapsed.*

**Narrative.** This is the right moment for an *honest* engine for three converging reasons:

1. **Cloud-native went mainstream and got harder.** **82% of Kubernetes users now run it in production** (up from 66% in 2023), **98% of orgs use cloud-native techniques**, and **66% of orgs running GenAI use Kubernetes for inference** (CNCF, 2025). 🟢 ~19.9M cloud-native developers worldwide (CNCF/SlashData, 2026). 🟢 The substrate Vigil is built for is now the default.

2. **The market is openly disillusioned with probabilistic AIOps.** **97% of tech leaders say probabilistic ML has *limited* AIOps' value** (Dynatrace, 2024). 🟢 In 2025, **Gartner retired the entire "AIOps Platforms" category**, renaming it "Event Intelligence Solutions," citing "confusion and disillusionment… whose expectations have not been met." 🟢🔴*(attribute to the Gartner Market Guide as reported; full report paywalled)* The first wave promised autonomy and under-delivered. Buyers are primed for a different posture.

3. **Agentic ops is coming, and it needs a trustworthy floor.** Gartner: **70% of enterprises will deploy agentic AI to operate IT infra by 2029** (vs. <5% in 2025). 🟢🔴 You cannot let an autonomous agent act on a hallucinated cause. The next decade of ops needs a deterministic, governed, honest ground truth. That's the gap Vigil fills.

**The buyer's own words:** *"An AI SRE that produces a confident, plausible, wrong root-cause analysis is worse than no AI SRE… Trust is a separate axis from capability."* — Noah Casarotto-Dinning, 2026. 🟢

**Suggested visual:** Two rising curves crossing — "Cloud-native complexity" up-and-right; "Trust in black-box AIOps" down-and-right — with the crossing point labeled *Vigil*.

---

# 5 · Solution

**Slide headline:** *An engine engineered to make it structurally impossible to convert correlation into causation.*

**The big idea.** Vigil is built on **Judea Pearl's Ladder of Causation**. 🟢 Pearl proved that **no amount of correlation will ever tell you the direction of the arrow** — the arrow is an assumption a human brings *from outside the data*. Every "AI root cause" tool is a rung-one machine (seeing/correlation) wearing a rung-two costume (doing/causation). Pearl showed that costume is empty 40 years ago.

So Vigil made a choice that sounds insane for a startup: **it refuses to invent the arrow. And that one refusal *is* the architecture.**

**How that refusal becomes a product — three provenance classes that *join, never fuse*:** 🟢

| Class | What it is | How it speaks |
|---|---|---|
| 🟩 **MEASURED** | A fact from readings, or its arithmetic (threshold state, rate, co-occurrence) | *"working_set is above its limit and rising; crossed at 10:13"* |
| 🟦 **PROJECTED** | A model forecast against a bar — **always with an uncertainty band that never collapses to a line** | *"projected to cross in ~13 min, band 9–22 min"* |
| 🟪 **AUTHORED** | A human-curated assertion in the ontology — causal direction, default threshold — quoted verbatim, version-pinned | *"per the graph (v0.13.0): MEMORY_LEAK precedes OOM_KILL"* |

These classes meet **only at the surfacing layer**, and they are placed *side by side*, each keeping its label. A finding is *"these MEASURED states co-occurred in this AUTHORED pattern"* — **never** collapsed into a generated sentence like *"X caused Y."* The words *cause / caused / root-cause* appear nowhere in engine output.

**The four prohibitions (enforced by code and tests, not policy):** 🟢

1. **Never invent causation** — direction is exclusively human-authored; correlation stays correlation.
2. **Never invent thresholds** — bars come from *your* declared limits/SLOs (borrowed normativity); undeclared series are listed as *unbounded*, never silently defaulted to a learned baseline.
3. **Never learn per-customer state** — the deterministic core's entire statistical vocabulary is three primitives: threshold, rate-of-change, co-occurrence. No hidden baselines to drift.
4. **Never let the model gate the engine** — detection is byte-identical whether the LLM, forecasting, and every modality lane are present, degraded, or absent. A firewall (enforced by build-failing dependency tests) means the deterministic core literally *cannot* read speculative data.

**The honesty surface (almost nobody else has this):** 🟢 Vigil ships a **Silence Ledger** and **Coverage Report** that tell you, by name and with a reason, **what it is *not* watching** — certificate expiry, DNS failure, etcd latency. *The list of what we can't see is a feature, not an embarrassment. Silence should never be mistaken for safety.*

**Speaker note (the close):** *Everyone else pretends their machine climbed Pearl's ladder on its own. We're the ones who admit a human has to take the last step — and we make that step take five seconds, and last forever.*

**Suggested visual:** Three colored, side-by-side cards (green/blue/purple) under a header "Join, never fuse," with a red ✕ over a single merged "AI says: root cause is X."

---

# 6 · Product

**Slide headline:** *Watch → Detect → Forecast — and tell you exactly where its vision ends.*

**Narrative.** Vigil does three things and refuses to pretend it does more: 🟢

1. **Watch.** Continuously scrapes the cluster and maintains a truthful, time-aware model of entities and the dependencies between them (identity + binding engine; join accuracy 1.0000 live, zero mis-joins).

2. **Detect — *phenomena*, not metrics.** A single threshold crossing is never an alert. A **phenomenon** lights up only when its member signals reach their states in an *authored temporal pattern* (precursors → event → consequence) across topologically related entities — with **cascade recognition** and **blast-radius** derivation. Then **transitive root-cause chains** stitch measured-degraded workloads into an *ordered* chain over observed flow edges — oriented **only** by the authored relation, never by timing. **Cardinal rule:** coincident-but-unrelated faults produce *separate* chains, never one merged story.

3. **Forecast — the narrow clock.** Google's **TimesFM 2.5**, run strictly as a kinematics engine over **bare floats** — no names, labels, or units reach the model (enforced at the proto layer). It projects an imminent threshold crossing *with a widening uncertainty band* and never names a cause. Disciplines built in: footprint subtraction (forecast the clean post-event remainder, not the next restart), regime-shift flagging, and CUSUM trend-rescue for slow creeps.

**The deterministic micro-agents (the operational story):** 🟢

- **Equivalence agent** — hunts "dead data" (telemetry scraped but never mapped), maps an unknown metric back to a canonical variable, proposes a binding for human review.
- **Topological agent** — walks the causal graph during a fault; runs **conditional-independence pruning (PCMCI)**: on a live cluster, **26 raw correlations → 6 real candidates, 7/7 confounder edges dropped.**
- **Forecasting agent** — the clock above; structurally incapable of asserting a cause.

**Governed growth — PROPOSE → VERIFY → PROMOTE:** 🟢 An LLM agent *proposes* typed candidate extensions from **read-only** measured context. Candidates land in a **firewalled `candidates.db` the deterministic path can never read.** Deterministic gates *verify*. A **named human** *promotes* by authoring — the model's prose is discarded; the human writes the note, the name, the version. *The AI is a fast, safe assistant — structurally incapable of inventing your system's causal map behind your back.*

**Proof the engine is real (demo slide):** 🟢

- **842 nodes · 589 signals · 7 modalities · 21 falsification gates · byte-identical replay**
- Onset detection **+6s** (vs. +66s for the textbook alternative); CUSUM **0/400 false onsets**
- Live: one injected memory-leak pod fires one `DEGRADED MEMORY_LEAK`; the healthy cluster stays **silent** (zero false positives); a real OOM ramp produced a PROJECTED warning **≥2 min ahead, recall 1.00, zero false warnings**.

**Suggested visual:** The console screenshot/diagram — topology graph on the left, a finding on the right rendered as three labeled cards (Measured/Projected/Authored) + a "what we can't see" silence-ledger strip.

---

# 7 · Market Size & Dynamics

**Slide headline:** *A $14.2B observability market, inside an $81B IT-operations envelope — and the AI layer on top is the fastest-growing, least-trusted part.*

### Market sizing (the funnel) 🟢 *(cite firm + year on the slide)*

| Layer | Figure | Source |
|---|---|---|
| **Parent envelope — ITOM** | **$81B by 2028** (10.3% CAGR) | Gartner, 2024 |
| **Beachhead category — Observability** | **$14.2B by 2028** (≈20%/yr) | Gartner, 2025 |
| **The AI layer on top — AIOps** | **~$15–34B today → $36–99B by 2030** (15–24% CAGR) | Grand View / Mordor, 2024–25 |

> **Use Gartner's $14.2B-by-2028 as the headline observability number.** It reconciles the wide methodology gap between "narrow" tooling reports (~$2.4B) and bottom-up vendor-revenue tallies (~$12B+). Do **not** cite the $2.4B figure standalone — it's smaller than Datadog's annual revenue alone. 🔴

### A simple, defensible TAM → SAM → SOM 🟡 *(illustrative — founder to refine)*

- **TAM:** the observability + AIOps spend Vigil could ultimately address — **~$14–20B (2028).**
- **SAM:** organizations running **Kubernetes in production** with material observability spend — given **82% of K8s users are in production** and observability is **~17% of compute spend**, this is a multi-billion serviceable slice.
- **SOM (3-yr beachhead):** regulated + AI-infrastructure K8s teams who most need trust/auditability — a focused, winnable wedge (size it from design-partner pipeline once it exists).

### Why the dollars are moving now (market dynamics)

- **Cloud-native is the default:** 82% K8s in production, 98% cloud-native, **66% of GenAI orgs run K8s for inference** (CNCF, 2025). 🟢 ~19.9M cloud-native developers (2026). 🟢
- **Cost is out of control:** observability is **~17% of compute spend** (Grafana, 2025); 🟢 median bill **$1.95M/yr** (New Relic, 2024); 🟢 Kubernetes raises data volume ~100× vs. legacy, and *"observability can cost more than the application environment"* (Chronosphere). 🟢 Marquee: **Coinbase reportedly spent ~$65M/yr on Datadog** (Pragmatic Engineer / Datadog earnings call, 2023). 🟢🔴*(say "reportedly ~$65M" — earnings-call estimate, not an SEC filing)*
- **Repatriation is real:** teams are moving off premium SaaS to cut bills — ClickHouse reported its own stack is *"at least 200× less expensive than Datadog for our workload."* 🟢🔴*(Datadog figure is a list-price extrapolation)*
- **The category is consolidating (validation):** Cisco bought Splunk for **~$28B** (2024); New Relic taken private at **~$6.5B** (2023); **Palo Alto Networks is acquiring Chronosphere for $3.35B** (Nov 2025). 🟢 Big money believes this market matters — *and* the incumbent path bundles observability into autonomous-remediation megavendors, which is the opposite of Vigil's human-in-the-loop posture.

**Suggested visual:** A nested-funnel: ITOM $81B → Observability $14.2B → AIOps layer, with the Vigil wedge highlighted at the K8s + regulated/AI-infra intersection.

---

# 8 · Competition

**Slide headline:** *Everyone ships an AI that asserts the cause. Nobody makes governed determinism + honest refusal their thesis. That corner is empty.*

### The landscape (three bands)

**A. Incumbent platforms** 🟢 — *the cost-and-trust wedge*

| | Scale | Their AI/RCA | Weakness Vigil exploits |
|---|---|---|---|
| **Datadog** | FY25 rev **$3.43B**, +28%; ~32,700 customers | Watchdog RCA, Bits AI SRE | Bill shock (usage-based); AI asserts "likely" causes; no blind-spot surface |
| **Dynatrace** | FY25 rev **$1.7B**; ARR $1.73B | Davis "hypermodal" causal AI | *Closest philosophical rival* — but **no human-governance gate, no blind-spot honesty** |
| **Splunk (Cisco)** | acquired **~$28B** (2024) | ITSI, AI Assistant | Ingestion-priced; buried in a networking giant |
| **New Relic** | private **~$6.5B** (2023) | Intelligent RCA — *explicitly probabilistic* | Probabilistic cause ranking; PE cost-extraction |
| **Grafana** | ~$250M ARR; **>$6B** valuation | Sift/Assistant (weakest RCA) | DIY assembly — *you* build the RCA |

**B. The AI-SRE / agentic-RCA wave (2024–26)** 🟢 — *the "who authors the graph" wedge*

- **Resolve.ai** — LLM agents + knowledge graph; **$125M Series A at $1B** (Lightspeed, Dec 2025). Output = a "theory."
- **Traversal** — real causal ML "world model"; **$48M** (Sequoia + Kleiner Perkins, 2025). Generates the graph *for* you.
- **Causely** ⭐ — deterministic causal models, **$8.8M seed** (2023). **Closest "causal/deterministic" rival.**
- **Cleric** — read-only LLM agent, "we say I don't know"; **$9.8M.** **Closest "honesty" rival.**
- **Komodor / Robusta-HolmesGPT / Edwin AI / incident.io** — K8s-native or agentic RCA; most are LLM/RAG agents.

**C. Event-correlation legacy** — Moogsoft (Dell), BigPanda ($1.2B, 2022), PagerDuty — *cluster symptoms, don't prove cause.*

### How we win the two rivals that matter (don't fumble these) 🟢

- **vs. Causely** (deterministic too — don't say "they hallucinate"): the wedge is **who authorizes the causal graph.** Causely auto-instantiates a *vendor-authored* causal model with "minimal human intervention," and its edges carry probabilities. Vigil's inverse: **a named human authors and governs each causal relation per environment, the system refuses to assert direction it wasn't given, and it is honest about its blind spots.**
- **vs. Cleric** (read-only, "I don't know" too): Cleric's honesty is *confidence-score suppression* over an *inferred* knowledge graph (itself a hallucination surface). Vigil's honesty is **architectural refusal + a deterministic, human-authored graph + a provable silence ledger.** Their own changelog admits an era where the agent "would confidently point to the most recent deployment for every incident" — the exact failure an authored design eliminates structurally.

**The tell to quote on stage:** Komodor *advertises* "extremely low hallucination rates." The market already worries the machine is guessing — we're the only ones who removed the guess by construction.

### The 2×2 (the money slide) 🟢

- **X-axis:** *Confidently asserts cause* ⟷ *Admits uncertainty / refuses to invent*
- **Y-axis:** *Black-box / inferred causality* ⟷ *Deterministic & human-governed provenance*

```
        Deterministic & human-governed provenance
                        ▲
            Dynatrace ● │      ★ VIGIL
           (causal, no  │   (refuses to invent ·
            gate)       │    surfaces blind spots ·
                        │    PROPOSE→VERIFY→PROMOTE)
 asserts cause ─────────┼─────────────► admits uncertainty
                        │
   Datadog ● Resolve ●  │   ● Cleric
   Causely ● Traversal ●│  (read-only, inferred graph)
   Komodor ● HolmesGPT ●│
                        ▼
            Black-box / inferred causality
```

**Vigil sits alone in the top-right.** Determinism is becoming table stakes; **governed, honest causality is not.**

**Suggested visual:** The 2×2 above, clean, with logos placed and Vigil starred alone in the upper-right quadrant.

---

# 9 · Go-to-Market Strategy

🟡 *Net-new strategy — no prior GTM content in the repo. Reasoned from the product's shape (in-cluster, open-source-friendly, CNCF-adjacent) and comparable companies. Founder to own.*

**Slide headline:** *Open-source-led adoption, landing where trust is non-negotiable.*

### The motion: bottom-up, then enterprise expand

1. **Land (free, in-cluster, developer-led).** Ship the deterministic core as an **open / free in-cluster agent** (Helm chart; pursue the **CNCF Sandbox** path the way Robusta did). The Pearl/causality narrative and the "honesty surface" are exceptional *inbound content* — platform engineers adopt it because it's rigorous and it runs next to their cluster, data never leaving. This mirrors the proven Grafana/Robusta/Prometheus community-led path.
2. **Expand (paid governance + fleet).** Monetize the moment a team has **more than one cluster, more than one operator, or an auditor** — multi-cluster fleet view, RBAC/SSO, the governance UI, audit export, premium curated ontology packs, SLA support (see §10 Business Model).
3. **Enterprise (top-down, trust-led).** A focused sales motion into regulated buyers where *determinism + provenance + a named-human governance trail* is a compliance asset, not just a nicety.

### Beachhead: who feels the pain *most* (and trusts black boxes *least*)

- **Regulated / high-stakes K8s teams** — fintech, healthcare, infra/platform teams under SOC 2 / audit pressure. Byte-identical replay and named-human-authored causal provenance are *exactly* what an auditor wants. **"The observability you can put in front of an auditor."**
- **AI-infrastructure teams** — **66% of GenAI orgs run K8s for inference** (CNCF, 2025). 🟢 These teams have huge, expensive, fast-churning clusters and the lowest tolerance for a hallucinating ops tool. Natural early adopters.

### Channels

- **Open-source distribution + CNCF ecosystem** (Sandbox → community → conferences).
- **Thought-leadership content** — the causality/honesty thesis is genuinely differentiated and very shareable (Pearl's ladder, the "11.34% RCA accuracy" stat, the silence ledger). Inbound engine.
- **Design-partner program** — 5–10 hand-picked regulated/AI-infra teams; co-author ontology packs for their stacks (turns onboarding labor into a reusable asset).
- **Platform-engineering / SRE communities** (Slack/Discord, KubeCon, r/kubernetes).

### Wedge message by audience

- *To the CFO/buyer:* "Predictable per-cluster pricing — we don't tax your data volume. We cost less and tell you the truth, including what we can't see."
- *To the SRE/operator:* "At 3am, it shows you what's measured, what's projected, and what a human authored — each labeled — plus what it isn't watching. No guess to second-guess."
- *To the compliance/security lead:* "Every finding is reproducible byte-for-byte, and every causal relation names the human who authored it."

**Suggested visual:** A funnel — *OSS in-cluster adoption → multi-cluster/governance upgrade → enterprise/regulated expansion* — with the two beachhead segments (regulated, AI-infra) called out at the top.

---

# 10 · Business Model

🟡 *Net-new — no pricing/revenue content in the repo. The structure below is grounded in Vigil's real architecture (in-cluster, deterministic, no central data hoard) and in the market's #1 pain (volume-based bill shock). Specific dollar figures are illustrative placeholders for the founder.*

**Slide headline:** *We charge for clusters, not for data — predictable pricing in a market drowning in surprise bills.*

### The model: open-core + per-cluster subscription

**The strategic insight (put this on the slide):** the entire incumbent pain is **volume-based pricing** — per-host, per-GB-ingested — which explodes under Kubernetes autoscaling (Coinbase's reportedly ~$65M Datadog bill; "costs too much" is the #1 concern). Because Vigil is **deterministic and runs in-cluster — it does *not* need to ship all your telemetry to a vendor backend** — it can price on something **predictable and aligned: the cluster/fleet, not the data firehose.** That is both a business-model and a trust differentiator.

| Tier | Who | What | Price axis 🟡 |
|---|---|---|---|
| **Community (free / open core)** | Individual teams, single cluster | Deterministic detection + forecasting core, console, MCP relay | $0 — drives adoption |
| **Team** | Growing teams, a few clusters | + multi-cluster view, governance UI, alerting lane, email/SSO | **per cluster / month** (flat, predictable) |
| **Enterprise** | Regulated / large fleets | + RBAC, audit export, premium curated ontology packs, fleet governance, SLA support, on-prem/air-gapped | **annual per-fleet contract** |

### Secondary revenue 🟡

- **Curated ontology packs** — phenomenon/signal libraries for specific stacks (Postgres, Kafka, Redis, GPU/inference). A recurring, defensible content moat that compounds as the ontology grows (842 nodes today).
- **Professional services / onboarding** — author the customer's first causal relations and bindings (high-touch for enterprise; productize over time).
- **(Future) the trust substrate for agentic ops** — as AI agents need governed ground truth, the MCP relay becomes a platform surface.

### Why these unit economics work 🟡

- **Low marginal cost to serve:** the engine runs in the *customer's* cluster (CGO-free Go); we are not paying to ingest/store petabytes, unlike SaaS observability vendors whose COGS scales with customer data.
- **Predictable, expansion-friendly:** revenue grows with the customer's *cluster count* (which only goes up) — clean net-revenue-retention story without bill-shock churn.
- **Open-core flywheel:** the free core lowers CAC (community-led), the ontology packs + governance create lock-in and expansion.

**Suggested visual:** Two pricing curves vs. cluster growth — incumbent (steep, jagged "bill shock") vs. Vigil (flat, predictable per-cluster) — under the header *"We don't tax your data."*

---

# 11 · Key Milestones

**Slide headline:** *What we've already de-risked — and what the round unlocks.*

### Already achieved (de-risked) 🟢 *(from the codebase — this is the credibility column)*

- ✅ **Deterministic core** (identity, binding, observation, entity-local detection) — byte-identical replay proven across process restarts on live captures; join accuracy 1.0000.
- ✅ **Topological detection** — first/second-order phenomena, cascades, blast-radius; live-proven on boutique microservices + chaos-injected clusters; independent-faults-stay-separate gate passing.
- ✅ **Forecasting layer** (TimesFM 2.5) — calibration gate passed: band coverage 0.806, **event recall 1.00, zero false warnings** on certified config; live OOM warnings ≥2 min ahead.
- ✅ **Governance spine** (PROPOSE→VERIFY→PROMOTE) — live-exercised: a real behavior-change release landed through governance; a misclassified variant was *blocked*.
- ✅ **Honesty surface** — coverage report + silence ledger + unexplained-anomaly channel, live.
- ✅ **MCP relay + operator console** — classed facts to AI agents; React/TS console live on real clusters.
- ✅ **Engineering rigor** — 842-node versioned ontology, **21 falsification gates**, CGO-free, `-race` clean, AWS bring-up.

### Next 12–18 months (what the round funds) 🟡

| Horizon | Milestone |
|---|---|
| **0–3 mo** | Open-source launch (Helm chart) + CNCF Sandbox application; 3–5 signed design partners (regulated / AI-infra). |
| **3–6 mo** | **Application-signal ingestion** (per-service request-rate / latency / staleness vs. declared SLOs) — the keystone for deep multi-service chain visibility; histogram→quantile (p99). |
| **6–9 mo** | Multi-cluster fleet view + governance UI + RBAC/SSO → first **paid** Team-tier conversions. |
| **9–12 mo** | First 2–3 curated ontology packs; first Enterprise contract; published case studies (MTTR / bill reduction). |
| **12–18 mo** | $X ARR / N paying clusters *(founder to target)*; raise Series A from traction. |

**Suggested visual:** A timeline with a clear "you are here" marker between the green (done/de-risked) block and the forward roadmap — emphasizing how much is *already built*.

---

# 12 · Capital Requirements (The Ask)

🟡 *Net-new — no funding content in the repo. The range and allocation below are reasoned from comparable AI-SRE seed rounds; the founder sets the final number.*

**Slide headline:** *Raising a seed round to turn a proven engine into a product teams pay for.*

### The ask 🟡

**Raising ~$3.5–5M seed** for ~18–24 months of runway to: ship application-signal depth, launch open-source, sign design partners, and convert the first paying customers.

**Comparable seed rounds (calibration, all 🟢):** Causely **$8.8M** seed (2023); Cleric **$9.8M** total (2024–25); Resolve.ai **$35M** seed (2024); Parity **~$500K** (YC). A focused **$3.5–5M** is credible for a pre-revenue team with a *working, validated* engine.

### Use of funds 🟡 *(illustrative split)*

| Allocation | ~% | What it buys |
|---|---|---|
| **Engineering** | ~55% | Application-signal ingestion + histogram→quantile, multi-cluster/fleet, governance UI, ontology-pack tooling, hardening. |
| **GTM / DevRel** | ~25% | Open-source community + CNCF, design-partner program, content engine, founding go-to-market hire. |
| **Ontology / domain** | ~10% | Curated phenomenon packs for top stacks (the content moat). |
| **Ops / runway buffer** | ~10% | Cloud, security/compliance (SOC 2 to sell into regulated buyers), G&A. |

### What the round proves (the Series-A setup) 🟡

- Open-source traction (stars / clusters running the free core).
- 3–5 design partners → first paid conversions → **$X ARR**.
- A repeatable wedge in regulated + AI-infra segments, with case studies on MTTR and bill reduction.

**The honest close (on-brand):** *We won't show you revenue we don't have. We'll show you a working engine that does what every competitor only claims to do — and the exact, de-risked plan to put it in front of the teams who need it most.*

**Suggested visual:** A use-of-funds donut + a one-line "$3.5–5M → 18–24 mo → OSS launch + design partners + first ARR → Series A."

---

# Appendix A · Proof-point cheat-sheet (drop-in stat bar)

**Product (🟢 from codebase):**
- 842 nodes · 589 signals · 7 modalities · ontology `v0.13.0`
- 21 falsification gates · byte-identical deterministic replay
- CGO-free Go + TimesFM 2.5 (Python) · `-race` clean
- Onset detection +6s (vs +66s) · CUSUM 0/400 false onsets
- PCMCI pruning: 26 correlations → 6 candidates · 7/7 confounders dropped
- Forecast: recall 1.00, band coverage 0.806, zero false warnings (certified config)
- 3 provenance classes (MEASURED/PROJECTED/AUTHORED) · join-never-fuse · 4 prohibitions · PROPOSE→VERIFY→PROMOTE

**Market (🟢 verified hero stats):**
- Observability **$14.2B by 2028** (Gartner 2025); ITOM **$81B by 2028** (Gartner)
- AIOps **~$15–34B → $36–99B by 2030** (Grand View / Mordor)
- **82%** K8s in production · **98%** cloud-native · **66%** of GenAI orgs use K8s for inference (CNCF 2025)
- Downtime **$400B/yr = 9% of Global-2000 profits** (Splunk/Oxford 2024); **$1.9M/hr** median (New Relic 2024)
- **86%** say data beyond human management; **97%** say probabilistic ML limited AIOps' value (Dynatrace 2024)
- Observability **17% of compute spend**, median **$1.95M/yr**, cost = #1 concern (Grafana 2025 / New Relic 2024)
- **~70%** SRE burnout from on-call; toil rose to **30%** despite AI (Catchpoint 2025)
- Best LLM solved only **11.34%** of RCA cases (OpenRCA, ICLR 2025)
- Gartner retired the "AIOps Platforms" category in 2025

---

# Appendix B · Sources & "handle with care"

**Verified hero stats — safe to print (🟢):** Gartner observability $14.2B/2028 (Network World, Aug 2025); Gartner ITOM $81B/2028 (Oct 2024); CNCF 2025 Annual Survey (82% / 98% / 66%, released Jan 2026); Splunk + Oxford Economics "Hidden Costs of Downtime" (Jun 2024); New Relic 2024 Observability Forecast ($1.9M/hr, $1.95M median); Dynatrace Global CIO Report 2024 (86%, 97%, 81%); Grafana 2025 Observability Survey (17% of spend, cost #1); Catchpoint SRE Report 2025 (~70% burnout, 30% toil); OpenRCA (Microsoft Research/Tsinghua, ICLR 2025, 11.34%). Competitor financials: Datadog FY2025 ($3.43B), Dynatrace FY2025 ($1.7B), Splunk/Cisco (~$28B, 2024), New Relic take-private (~$6.5B, 2023), Grafana (>$6B), PANW/Chronosphere ($3.35B, Nov 2025); AI-SRE rounds: Resolve $125M Series A @ $1B (Dec 2025), Traversal $48M (2025), Causely $8.8M (2023), Cleric $9.8M.

**🔴 Handle with care — phrasing guidance:**
- **Coinbase Datadog bill** → say *"reportedly ~$65M/yr"* (Datadog earnings-call estimate + Pragmatic Engineer confirmation; **not** an SEC filing).
- **Gartner $5,600/min downtime** → 2014, soft, deprecated URL. Use only as historical framing ("for a decade the benchmark was…"); lead with the 2024 first-party figures instead.
- **Gartner retiring "AIOps Platforms"** → attribute to the *Gartner Market Guide for Event Intelligence Solutions (2025)* "as reported" — the full report is paywalled.
- **Gartner 70%-agentic-by-2029 / >90%-containers** → secondary citations; verify the paywalled original before printing a hard number.
- **AIOps & observability TAM** → always cite as a *range with the firm named*; sources disagree >10× by scope.
- **ClickHouse "200× cheaper than Datadog"** → the Datadog side is a list-price extrapolation, not a paid bill.
- **"% of MTTR spent on diagnosis"** and **"60–80% false-positive alerts"** → no clean SRE-specific primary source; keep qualitative.
- **Ontology version** → repo badge shows `v0.13.0` and 842/589/7; internal release work is ahead of that. Use the public `v0.13.0` numbers for external decks unless the founder updates them.

**🟡 Founder-owned (must be set/confirmed by you):** team & company snapshot (§2), all pricing/tiers (§10), GTM specifics (§9), forward milestones & targets (§11), the raise amount, use-of-funds split, and any ARR/customer targets (§12).

---

# Appendix C · Narrative spine & taglines

**Primary tagline:** *Reports what is. Estimates what's soon. Never invents why.*

**Trust hook:** *It refuses to invent the cause — and that's exactly why you can trust it.*

**Operational tagline:** *Beyond monitoring — toward an understanding you can actually trust.*

**Buyer one-liners:**
- *CFO:* "We charge for clusters, not for data. Predictable, in a market full of surprise bills."
- *SRE:* "What's measured, what's projected, what a human authored — each labeled — plus what we can't see."
- *Compliance:* "The observability you can put in front of an auditor."

**The walkaway (close every pitch with this):** *Pearl gave us the ladder of causation. Everyone else pretends their machine climbed it on its own. We're the ones who admit a human takes the last step — and we make that step take five seconds, and last forever.*

---

*Built from the Vigil codebase (README, `docs/01-epistemic-separation-charter.md`, `docs/15`, `docs/22`, `docs/33`), the three existing pitch scripts in `pitch/`, and ~40 verified external sources. Business-strategy sections (Business Model, GTM, Capital, forward Milestones) are reasoned proposals, not repo facts — flagged 🟡 throughout for the founder to own.*
