# Vigil Pitch Deck & Storytelling Script (Variation 2)

This variation of the presentation flow shifts the focus from the "Friday Night Outage" scenario to the **"Onboarding and Telemetry Complexity"** problem (Schema Heterogeneity). It frames Vigil's solution as a **Telemetry Compiler** rather than a traditional monitoring system, highlighting our dynamic binding engine and specialized micro-agents.

---

## Part 1: Storytelling Strategy Map (Variation 2)

| Storytelling Principle | How it is applied in Variation 2 |
|---|---|
| **Curiosity & Contrast** | **Expectation:** Scaling microservices requires massive LLMs or complex custom configurations to map heterogeneous metrics across namespaces.<br>**Contrarian Reality:** You don't need a smarter LLM to guess your metrics. You need a **Telemetry Compiler**. We compile static ontology rules directly onto dynamic Kubernetes topology. |
| **Academic Anchoring** | Rather than explaining the Causal Ladder first, it introduces **Judea Pearl's Causal DAG** as a compiler target. The ontology is the source code; the bound graph is the executable. |
| **Speed to Value** | Signals within 15 seconds that Vigil resolves the "dead data" problem—turning raw, unmapped metric streams into structured, predictable causal pathways. |
| **Metaphors** | Reframes the forecasting layer as a **"semantics-blind kinematics clock"** (a clock that measures movement without knowing what is moving). |
| **Rhythm & Cadence** | High-energy, analytical, structured in clean staccato segments. |

---

## Part 2: Slide-by-Slide Pitch Script (Variation 2)

### Slide 1: The Telemetry Tower of Babel (The Hook)
* **Visual:** A chaotic web of raw metrics from different namespaces, distros (K3s, MicroK8s), and exporters. Stark title: **"The Dead Data Problem."**
* **Presenter Energy:** Tense, frustrated, pointing out a systemic industry failure.

> **Script:**
> You scale your Kubernetes cluster to hundreds of pods. Now, try to map their dependencies. 
> 
> The database team exports latency one way. The application team exports request rate another. Different namespaces, different naming conventions, and different exporters. 
> 
> *[Short pause]*
> 
> Traditional monitoring systems drown in this noise. They leave you with "dead data"—telemetry that is collected but never mapped. To fix it, engineers try to write thousands of lines of custom PromQL, or they throw expensive LLMs at raw streams, hoping the model will guess the relationships.
> 
> They are guessing. We built a compiler. Introducing **Vigil**.

---

### Slide 2: The Telemetry Compiler (Speed to Value)
* **Visual:** An elegant split-screen showing a curated YAML Ontology (left) compiling against a live Kubernetes API Topology (middle) to output a clean **Bound Customer Graph** (right).
* **Presenter Energy:** Relieved, clear, structural.

> **Script:**
> Vigil does not monitor clusters by matching raw strings. Vigil is a compiler. 
> 
> We author a single, versioned **Ontology Graph** at the type level. It contains everything we know about system physics, signals, and failure patterns. 
> 
> At onboarding, our **Binding Engine** compiles this ontology directly against the customer's live Kubernetes topology. 
> 
> The result is a **Bound Customer Graph**—a live, tailored, instance-level model of your cluster where every metric is automatically resolved, normalized, and mapped to its physical dependencies. We turn chaos into an executable map.

---

### Slide 3: Judea Pearl's Causal DAG (The Causal Model)
* **Visual:** A clean DAG diagram with nodes labeled `Ingestion`, `DB`, and `Storage`. Highlight the directional arrows. Title: **"Judea Pearl's Causal Model."**
* **Presenter Energy:** Authoritative, academic.

> **Script:**
> In *The Book of Why*, Turing-award winner Judea Pearl proved that you cannot deduce cause and effect from observational data alone. You need a structural model of the world—a Causal Directed Acyclic Graph, or **Causal DAG**.
> 
> Some tools attempt to dynamically calculate causal arrows at runtime by analyzing time-lag correlations or metric onsets. But in a live cluster, random coincidences happen constantly. Two unrelated pods spike together, and a correlation engine invents a causal link. 
> 
> Vigil rejects this. Our compiled Bound Customer Graph acts as the system's Causal DAG. We do not let statistical models invent relationships. Every causal path is authored and compiled. The AI doesn't guess the "Why"—the graph contains it.

---

### Slide 4: The Micro-Agents (Value Density)
* **Visual:** Three clean columns representing the three agents:
  1. **Equivalence Agent** (Resolves strays → overlays)
  2. **Topological Agent** (Walks the DAG to trace cascades)
  3. **Forecasting Agent** (Zero-shot time-series transformer clock)
* **Presenter Energy:** Technical, high density.

> **Script:**
> To run this compiled map, we deploy three specialized micro-agents. 
> 
> First, the **Equivalence Agent** hunts for "dead data." When an unmapped metric is scraped, this agent semantically maps it back to a canonical variable and proposes a regex rule for a human to review. It is the compiler's parser.
> 
> Second, the **Topological Agent** walks the causal graph. When a failure starts, it traces the cascade up to two hops based on physical dependencies, not noisy metric timing.
> 
> Third, the **Forecasting Agent** runs a zero-shot time-series transformer strictly as a clock. It is completely blind to pod names, labels, or semantics. It only projects trajectories against resolved configuration limits. It is structurally impossible for this agent to hallucinate a causal explanation.

---

### Slide 5: The Four Provenance Classes (Clarity & Comprehension)
* **Visual:** The Vigil Operator Dashboard showing four colored streams: `MEASURED` (green), `PROJECTED` (blue), `AUTHORED` (purple), and `ADVISORY` (amber).
* **Presenter Energy:** Informational, structured.

> **Script:**
> This compilation culminates in the operator’s dashboard, where we enforce a strict rule: **Measured facts, projections, authored relationships, and AI advisories never merge.**
> 
> We show you the **Measured** facts: the raw thermometer readings of what is happening now.
> 
> We show you the **Projected** warnings: a time-series transformer forecasting when a metric will cross a bar, shown as a widening band of uncertainty, never a flat line.
> 
> We show you the **Authored** explanation: the human blueprint explaining the system relationship.
> 
> And we show you the **Advisory**: the AI's synthesized recommendation—what to investigate, what to right-size—derived from the other three classes and stamped, plainly, as advice. Never as fact.
> 
> We never fuse these. If a tool claims "threshold-free anomaly detection," it is flooding you with alarms on normal bursty traffic. Vigil respects your own declared limits. We join the evidence side-by-side, leaving the final counterfactual reasoning to the human operator.

---

### Slide 6: The Advisory Layer — Right-Sizing & the Honest Edge (Walkaway Value)
* **Visual:** A split panel. **Left — "Right-Sizing Advisory":** a few table rows — `payments-api · cpu · request 2000m · p95 410m → RECLAIM to 530m` · `timescaledb · memory · limit 1.0Gi · p95 920Mi → RESIZE-UP to 1.2Gi` · a greyed `vision-worker · cpu → UNSTABLE (mid-rollout) — no number`. **Right — "Coverage & Silence Ledger":** green = bound/observable, grey = unbound (exporter offline · no declared limit), red = structural blind spots `CERT_EXPIRY` · `DNS_FAILURE` · `ETCD_SLOW_PATH`, each with a one-line reason.
* **Presenter Energy:** Practical and concrete on the left; quietly confident on the honesty beat.

> **Script:**
> Detection tells you what's breaking. Forecasting tells you what's about to. But the operator has two more questions—"what should I *fix*?" and the one nobody says out loud, "what are you *not* telling me?" Vigil answers both. As advisories. Never as fact.
>
> On the left: **right-sizing.** Vigil watches each workload's sustained usage—a high percentile over a window—against the requests and limits *you* declared. A pod that reserves two cores but sustains four-tenths is stranded capacity; on a single edge node, that's the difference between fitting your workloads and evicting them. So Vigil proposes a concrete number: reclaim this over-provisioned request, grow that under-provisioned limit, with headroom. That is the brief's fourth question—*which workloads need optimizing?*—answered directly.
>
> But here's the discipline. It is easy to spit out a number: take one p95 and divide. Run that on a workload mid-rollout, or one that is naturally bursty, and you get confident, *wrong* advice. So Vigil gates on stability—if the usage is churning or ramping, it stays **silent**, no number—and it is QoS-aware: it will never quietly reclaim a Guaranteed pod into Burstable. Honest silence beats a wrong recommendation. And every row is stamped **Advisory**: grounded in measured usage and your declared limits, labeled as advice. Vigil never re-sizes your cluster. You do.
>
> On the right: the part most tools hide—the **Coverage Report and the Silence Ledger.** A compiler tells you what it could bind, and what it could *not*: which exporter is offline, which workload declared no limit. And the failure classes it *structurally cannot see* on this cluster—certificate expiry, DNS resolution, etcd latency—by name, with the reason.
>
> Most tools show you only what they found. Vigil also shows you the edge of its own vision. Because silence should never be mistaken for safety.

---

### Slide 7: The MCP Synthesis Relay — Plugging In AI, Safely (AI Interoperability)
* **Visual:** An LLM client (Claude, or any agent) on the left, connected by a single labeled pipe—**MCP · READ-ONLY**—to a rack of classed-fact tools: `get_coverage` · `get_silence_ledger` · `get_root_cause_chain` · `get_early_warnings` · `get_authored_relations` · `validate_claim` · `emit_advisory`. Every tool result wears a colored provenance badge (green/blue/purple). A red **"REFUSED"** stamp sits on an arrow where the model tried to assert a cause.
* **Presenter Energy:** Forward-looking, confident, technical.

> **Script:**
> The theme asks for AI agents. Here is how Vigil lets *any* AI plug in—without letting it lie.
>
> Vigil exposes a **Model Context Protocol** server: a read-only interface an LLM client connects to. But the model never sees raw metrics. It sees Vigil's already-*classed* facts—the coverage report, the silence ledger, the early-warning bands, the authored relations, the root-cause chain Vigil already computed. Each arrives stamped with its provenance—measured, projected, authored—so the AI can *cite* its evidence instead of inventing it.
>
> This is the opposite of the standard pattern. Most tools point a language model at raw telemetry and let it narrate a cause. We hand it grounded facts, and we move the discipline to the boundary. The AI may *synthesize* a summary, but the one generative tool—`emit_advisory`—is doubly fenced. It **refuses** outright any text that tries to assert a cause or a future certainty, and it is **withheld** until that advisory class passes its own backtest gate. An AI hypothesis can never wear Vigil's measured badge.
>
> So you get both: a conversational, AI-native interface on top of a system that is structurally incapable of hallucinating your root cause. The model talks. The graph is still the only thing that decides *why*.

---

### Slide 8: The Agent Harness & Human Governance (Safe Scaling)
* **Visual:** A UI mock of the Vigil Governance Panel. Show a "Metric Equivalence Proposal" card with a deterministic "Support Score" (e.g., 95% metric coverage, 3 distinct pods affected, regex compile check passed). Large buttons: **"Promote to Graph"** and **"Reject (Provide Reason)"**.
* **Presenter Energy:** Controlled, reassuring, demonstrating governance.

> **Script:**
> But how does Vigil scale to handle new microservices or unmapped metrics? This is where the **Intelligent Agent Harness** comes in.
> 
> Giving AI agents write-access to your production cluster is a disaster waiting to happen. That is why Vigil enforces a strict, firewalled lifecycle: **Propose, Verify, and Promote.**
> 
> Our agents use read-only tools to gather evidence about unmapped strays. They *never* write directly to the ontology or customer graph. Instead, they propose a change—like a new metric mapping or a failure rule.
> 
> Vigil then runs a deterministic validation suite to **verify** the proposal—calculating a support score based on actual cluster observations. Finally, a named human operator reviews the diff and **promotes** it. 
> 
> The human operator remains the absolute governor; the AI remains a safe, highly efficient assistant.

---

### Slide 9: The Replay Guarantee & Edge Stability (Call to Action)
* **Visual:** stark text: **"Pure Go + Python Time-Series-Transformer Clock + Zero CGO. 100% Deterministic Replay."**
* **Presenter Energy:** Solid, impactful.

> **Script:**
> Because Vigil is a compiler, it is completely deterministic. 
> 
> We run a validation harness that replays cluster incidents. Same metrics, same graph, same topology—equals byte-identical results. Every time.
> 
> Built in pure, CGO-free Go with a gRPC Python forecasting service, Vigil is designed to run reliably on lightweight edge boxes where CPU and memory are at a premium. 
> 
> We don't ask you to trust a black box. We compile your cluster's physics, predict its bottlenecks, and prove our findings.
> 
> Thank you.

---

## Part 3: Presenter Delivery Guide (Variation 2)

* **Key Metaphor:** The **"Compiler vs. Guesser"** dichotomy. Keep hammering home that other systems are guessing names and relationships, while Vigil compiles them like source code.
* **Cadence:** Use a slower, more deliberate cadence when explaining the *Compiler* and *DAG* concepts, then speed up when describing the *Micro-Agents* in Slide 4 to show high execution velocity.
