# Vigil — Success Metrics (judge scorecard)

Numbers that prove Vigil works. Every figure is grounded in a re-runnable gate, a test
assertion, a content-pinned release, or a measurement taken live this session — **source given
for each**, so a judge can verify, not just trust. Honest caveats are included on purpose: that
discipline *is* the product.

---

## 1. The one-slide scorecard (lead with these)

| Dimension | Number | What it proves |
|---|---|---|
| **Root-cause accuracy** | **1.000** root + **1.000** caller recall + **1.000** caller precision, **0** false cascades | It names the real upstream cause, every caller, and never a phantom |
| **Anti-hallucination** | **16/16** fabrications caught, **0/17** true claims wrongly blocked | The AI referee catches invented causes without censoring real ones |
| **Never merges unrelated faults** | **0** false chains (CARDINAL, zero-tolerance) | Two coincident problems stay two incidents |
| **Forecast** | recall **1.00** on real OOM crossings, band coverage **0.806** (target 0.8), lead **~10 min** | Calibrated early warning, not an overconfident guess |
| **Cost of detection** | **$0** inference / 24-7 (0 LLM calls in the hot path) vs ~**$4,200/mo** LLM-per-metric | Deterministic engine — monitoring is free; the LLM is optional & governed |
| **Determinism** | **34/42** Go pkgs pass, **0** fail; graph pinned `sha256:f13e5101…`; **byte-identical** replay | Same inputs → same outputs, always; tamper-rejecting |
| **Honesty surface** | **14** named blind spots + a silence ledger where every pair is *watched-or-silent-with-reason* | It tells you what it canNOT see |
| **Scale** | **842**-node / **3,759**-edge KG, **589** signals, **28** MCP tools, **17** pages | Real engine, not a toy |

> All accuracy/forecast/determinism numbers come from **offline gates anyone can re-run in seconds**
> (`just xsvc-gate`, `just validate-claim-gate`, `just transitive-chain-gate`, `just forecast-gate`, …).

---

## 2. Detection & root-cause accuracy (exact correctness floors, not fuzzy scores)

| Metric | Value | Source / reproduce |
|---|---|---|
| Cross-service **root** accuracy | **1.000** (10/10 + 10/10 over 2 topologies, 20 fired ticks) | `just xsvc-gate` · crossservice_gate.py:46 |
| Cross-service **caller recall** | **1.000** | `just xsvc-gate` · :47 |
| Cross-service **caller precision** | **1.000** (zero phantom callers) | `just xsvc-gate` · :48 |
| **False cascades** | **0** (zero-tolerance; 16 quiet ticks, 0 fired) | `just xsvc-gate` · :49 |
| Transitive root-cause **false-chain** | **0** (CARDINAL — coincident faults never merge) | `just transitive-chain-gate` |
| Transitive root + path fidelity | **EXACT** (root==structural root, path==authored-reach) | `just transitive-chain-gate` |
| Claim-referee **false-blocks** | **0 / 17** legit claims (ABSOLUTE floor) | `just validate-claim-gate` |
| Claim-referee **recall on fabrications** | **1.000 (16/16)**; structural-only backstop **9/9** | `just validate-claim-gate` |
| Incident grouping | **EXACT** (N occurrences → 1 incident, recurrence=N; restart-invariant) | `just incident-gate` |
| App-SLO no-fabrication / no-cross-talk | **0 / 0** over 10 scenarios | `just app-slo-gate` |
| Event-detection fidelity / no-false-upgrade | produced==oracle / **0** | `just event-detection-gate` |
| Dependency discovery P/R/F1 (live boutique) | **1.000** (16/16 edges, decoy excluded) | docs/15-cross-service-flow-lane.md:38 |

**Pitch:** "These aren't ML confidence scores — they're *exact correctness floors* enforced by seven
re-runnable gates. A single labeled miss flips the gate red. The rule is **'a class ships on
evidence, not its absence'** — so a thin corpus is rejected, never passed."

---

## 3. Forecasting / early-warning accuracy

| Metric | Value | Source |
|---|---|---|
| Event recall on genuine OOM crossings | **1.00** (3/3, each ≥2 min ahead) | decomposition-09M5.md:147 |
| Band coverage (calibration) | **0.806** vs nominal **0.8** (deliberately not pinned to 0.98) | decomposition-09M5.md:153 |
| Decomposition error reduction | hidden crossings **95 → 16 (−83%)** | decomposition-09M5.md:156 |
| Plateau gate (scale) | band **0.755** over **213,978 points**, in-band 15/16, recall 1.00, **0/16** false | forecast gate 09M3 |
| Robustness re-gate | band 0.804, recall 1.00, in-band **93/93**, 0 false, band-too-wide **13→0** | robustness audit |
| **Demo-measured live lead** | **~10.5 min**; ttc 675s vs actual 631s = **7% error**; crossAt within **23s** | live kind+TimesFM |
| This session's Grand Tour | card at **t+13 min**, **~34-min lead counting down to 10 min** | measured this session |
| Anticipatory cross-service lead | **9.5–14.5 min** (floor ≥120s) | projected-xsvc gate |
| Horizon | **1 h** (240 × 15s) | forecast config |

**Pitch:** "Calibrated, not overconfident: 100% of real crossings warned, with bands sized to a 0.8
target — and on this very cluster it called the OOM **~13 minutes early** and you watch the lead
count down live."

---

## 4. Cost & efficiency (the differentiator)

| Metric | Value | Source |
|---|---|---|
| LLM calls in detection/scrape/match hot path | **0** (CI import-firewall proves it) | firewall_test.go |
| Detection cost, 24/7 | **$0** inference | deterministic engine |
| LLM-per-metric alternative (100 series) | ~**$141/day ≈ $4,234/mo** | cost model |
| Cost per operator question (DeepSeek) | **~$0.004–$0.015** (≤$0.026 heavy) | agent loop estimate |
| Engine footprint | **1 process, 76 MB** static Go binary | single binary |
| Detection cadence | 15 s scrape + 15 s evaluate, forever, no LLM | obsd |

**Pitch:** "Monitoring a cluster 24/7 costs **$0** in inference — a CI firewall proves the detection
packages *cannot even import* the model layer. The LLM is invoked only when a human asks a question,
at sub-cent cost, and it can never write to the graph."

---

## 5. Determinism & test health

| Metric | Value | Source |
|---|---|---|
| Go packages pass / fail | **34 ok / 0 FAIL** (8 no-test; 42 total) | `go test ./...` |
| Race detector | green | `go test -race ./...` |
| Graph content-pin | `sha256:f13e51010e65…c94390` (v0.18.0) | ontology/releases/v0.18.0.yaml |
| Drift gate | **PASS** "graph matches sha256:f13e5101…" (graphlint exit 0) | `just lint` |
| Replay | **byte-identical**, 0 ticks diverged; tamper + CRC-corruption **rejected** | replay tests |

---

## 6. Charter discipline (mechanically enforced, not promised)

| Guarantee | Value | Source |
|---|---|---|
| Provenance classes | **3 core** (MEASURED/PROJECTED/AUTHORED) + ADVISORY + ASSOCIATION badge = **5** | graph |
| Agent write-tools to the graph | **0** | MCP surface (26 read + 2 non-mutating) |
| Read-firewall | **0 of 9** deterministic pkgs may import the candidate store (test PASS) | firewall import test |
| Invented causal directions | **0** — only a named operator authors an arrow | root-cause code |
| Forbidden cause-words in system scaffolding | **0** ("cause/root cause appear NOWHERE", test-asserted) | root-cause-chain test |
| Governance decision without a named human | **refused** (authorizedBy mandatory) | governance |

---

## 7. Coverage, honesty surface & scale

| Metric | Value |
|---|---|
| Authored phenomena / rules / KG signals | **47 / 29 / 589** |
| Honestly detection-wired (kind cluster) | **7 full, 12 partial, 28 none** (it says so out loud) |
| Named static blind spots | **14** (5 modality-absent, 4 unwired-reachable, 5 epistemic) |
| Silence-ledger invariant | every (entity,variable) pair == watched **or** silent-with-reason |
| Knowledge graph | **842 nodes, 3,759 edges, 7 modalities** |
| Surfaces | **28** MCP tools · **17** console pages · **35** /api endpoints |
| Stream capacity | assoc 256 · co-onset 512 |

---

## 8. This session's live-measured demo numbers

| Metric | Value |
|---|---|
| Detection latency: `WORKLOAD_UNAVAILABLE` | **t+19 s** after the click |
| Cross-service cascade | **t+2 min** |
| Memory-leak onset (anomaly) | **t+7 min** |
| Forecast card | **t+13 min** (≈34-min lead) |
| HEAL ALL recovery (historian) | **~13 s** |
| Ask agent | **16–23** live MCP tool calls/query, structured WHAT/WHEN/WHY/HOW advisory |
| Prompt validation | **6/7 prompts** scored **5/5 actionable, 100% charter-clean** |
| Live surface health | **26/26** /api endpoints 200+valid · **28/28** MCP tools · **17/17** pages |

---

## 9. Honest caveats (say these before a judge does — it builds trust)

- The accuracy gates are **exact correctness floors over curated, hand-labeled corpora** (e.g.
  cross-service = 2 topologies/36 ticks; validate-claim = 33 verdicts). They prove the join/walk
  **logic is correct**, not a statistical production recall against unlabeled real-world faults.
- Forecast "**0 false**" came from a **monotonic single-leaker corpus** where the false-warning
  branch wasn't exercised — it's "not-yet-contradicted," and a near-miss corpus is on the backlog.
- The **cost per query is a token estimate**, not metered (we can instrument it — see §10).
- Detection-wired counts (**7 full**) are for a **vanilla kind cluster**; richer clusters graduate
  more phenomena to full. On this AWS cluster, more are live.

---

## 10. Plan — what Vigil could ADD to prove even more

1. **`just metrics` / a one-command proof bundle** — run all gates + `go test` + replay + print a
   scoreboard. (One click in front of judges → all green with numbers.)
2. **A `/metrics` console page** — surface the gate results, determinism hash, coverage vector, and
   live detection-latency per finding, so the scorecard is *in the product*.
3. **Metered LLM cost** — have the agent report actual input/output tokens + $ per answer (turns the
   §4 estimate into a measured number live).
4. **Detection-latency timestamps on findings** — stamp "detected N s after onset" on each finding
   (we measured t+19s live; make it a first-class field).
5. **A near-miss forecast corpus** — exercises the false-warning branch so "0 false" becomes *earned*.
6. **A live end-to-end recall harness** — inject a known fault catalog, measure detected/total, to
   complement the structural gates with a production-style recall number.

Items 1–4 are small and high-impact for a demo; 5–6 are the honest next steps to upgrade the
caveats in §9 into hard numbers.
