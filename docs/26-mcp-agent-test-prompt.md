# 26 — MCP Agent Max-Potential Test Prompt

> A single, copy-pasteable prompt for an MCP-connected AI (e.g. Claude with the Vigil
> `obsd` MCP server wired at `/mcp`). It drives the agent through the entire 27-tool
> surface (`docs/23`) in the incident-investigation playbook order, and enforces the
> charter discipline so the answer is grounded, provenance-labelled, and honest about
> blind spots. Use it after injecting an incident (`docs/25`) for the strongest test.

## How to run it

1. Bring the system up (`docs/24`) and connect your MCP client to Vigil's `/mcp`
   (the 27 tools should list).
2. Inject a scenario (e.g. `kubectl apply -f corpus/chaos/abb-broker-backpressure.yaml`).
3. Paste the prompt below into the MCP-connected agent.
4. Grade the answer against the rubric at the end.

---

## THE PROMPT

```
You are the on-call SRE for our Kubernetes platform. The Vigil MCP server is your ONLY
source of ground truth about the cluster — you have no other telemetry. Vigil is a
deterministic observability substrate: every tool returns ALREADY-CLASSED facts —
MEASURED (read or arithmetic), PROJECTED (a forecast band, never a single line), or
AUTHORED (a curated graph note). Your job is to SYNTHESIZE what is happening and what to
do — Vigil never authors prose or causes.

Investigate the current state of the cluster end to end and give me a complete incident
report. Work the tools, do not guess. Specifically:

1. SITUATION. Call get_insights and get_findings to establish what is degraded right now
   (and what just fired or cleared). Call get_coverage / get_config so you know what is
   even watched and the posture that set the bars.

2. EARLY WARNING. Call get_warnings. Is anything forecast to cross a bar soon? Report the
   entity, the metric, the uncertainty band (earliest/latest), and the lead time. Treat it
   as a projection — never say "will", say "is projected to, within this band".

3. TIMELINE & ONSET. Call get_timeline to order what happened, and get_onsets for the
   precise MEASURED time and direction each affected series stepped ("since when, which
   way"), including sub-threshold shifts.

4. THE CHAIN & THE CULPRIT. Call get_root_cause_chain (the spine) and get_cross_service.
   Identify the ROOT (the deepest degraded node on an authored edge) and the downstream
   impacts. Use get_topology and get_trace_graph to confirm the dependency/blast radius
   (the observed call graph). Relay Vigil's computed chain — do NOT invent a different
   root. If intermediates are silent, say so; do not bridge a gap.

5. THE PHENOMENON & ITS AUTHORED WHY. From get_insights, name each matched phenomenon and
   QUOTE its authored "why" verbatim. Call get_authored_relations to confirm any
   cause→effect you assert is actually an authored relation — if it is not, it is YOUR
   hypothesis, label it so.

6. CORROBORATE ACROSS MODALITIES. Call get_events (OOMKilled/CrashLoop), get_log_templates
   (what the degraded pod is logging), get_audit_changes (what changed shortly before
   onset — an antecedent in time, NEVER a proven cause), and get_dependency (which signals
   co-move — associated-with, never causal). Note get_causal_hypotheses ONLY as
   direction-free leads — never assign their direction (that is a human's to author).

7. ANOMALIES & THE UNMAPPED TAIL. Call get_unexplained and get_departures. Is there loud
   behaviour that matches no known phenomenon?

8. HONEST BLIND SPOTS. Call get_silence_ledger and get_blindspots. If a workload is
   degraded but no finding explains it, the cause is likely something Vigil structurally
   cannot see — say so and recommend an out-of-band check; never blame a visible-but-
   unflagged object as a substitute.

9. PROPOSALS PENDING A HUMAN. Call get_provisional_coverage, get_candidates, get_governance
   to note what Vigil has only PROPOSED (status=CANDIDATE) and is awaiting a human to
   promote — never present these as adopted facts.

10. SYNTHESIZE. Draft (a) the current status, (b) the most likely root cause WITH its
    provenance (authored chain vs your hypothesis), (c) the blast radius, (d) the lead time
    if a forecast applies, and (e) a concrete recommended action. Run every causal/forecast
    sentence through validate_claim and adjust per its labels (matchedAuthored → present as
    authored and quote it; flagged generated-causation → keep but label it your hypothesis;
    flagged future-certainty → soften to a band). Then emit the final narrative via
    emit_advisory.

Deliverable: a structured incident report — Current status / Early warning / Event timeline
/ Root-cause chain (with provenance per claim) / Affected services / Corroborating evidence
/ What is NOT explained (blind spots) / Recommended action. For EVERY claim, cite the tool
it came from and its class (MEASURED / PROJECTED / AUTHORED / your-hypothesis). Where Vigil
is blind, say so plainly. Do not fabricate a cause Vigil did not give you.
```

---

## Grading rubric (what a maximum-potential answer looks like)

| Dimension | Pass | Fail |
|---|---|---|
| Grounding | every claim cites a tool + a class | unsourced assertions |
| Forecast honesty | projection stated as a band + lead time, never "will" | a single-number / certain future |
| Root cause | relays Vigil's `get_root_cause_chain` root; quotes the authored why | invents a different root or an unauthored cause |
| Provenance discipline | authored vs hypothesis cleanly separated; `validate_claim` used | launders a hypothesis as a Vigil fact |
| Direction restraint | `get_causal_hypotheses` surfaced as direction-free leads | assigns a causal direction the operator never authored |
| Blind-spot honesty | names silent intermediates / unobservable modalities; recommends out-of-band check | blames a visible-but-unflagged object to fill the gap |
| Cardinal rule | independent faults NOT merged into one chain | coincident faults fused |
| Actionability | a concrete, scoped recommendation | vague "investigate further" |

A strong run reads like a senior SRE who (1) knows exactly what is degraded and since when,
(2) has a forecast with a lead time, (3) names the root with cited provenance, (4) maps the
blast radius over the real call graph, (5) corroborates across events/logs/audit, and
(6) is explicit about what it cannot see — all without inventing a single cause.
