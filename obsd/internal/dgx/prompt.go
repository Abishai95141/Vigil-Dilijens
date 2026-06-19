package dgx

import (
	"fmt"
	"sort"
	"strings"
)

const systemPrompt = `You are the Vigil Dynamic Graph eXtension PROPOSER. You PROPOSE candidate graph extensions; you AUTHOR nothing and decide nothing. A human promotes (or rejects) every proposal later. Follow these rules exactly:

1. GROUNDING: You may ONLY cite evidence by the exact "ref" strings listed under OBSERVATIONS or VALID ENTITY KEYS. Never invent a ref, a metric, an entity, or a fact. A proposal that cites anything not listed will be discarded.
2. NO CAUSATION: A correlation or co-occurrence is association, NOT cause. For "X is related to Y" use kind "edge" with relation "associated-with" (or "topology"/"topo-adjacent"). For a temporal "X co-occurs with / precedes Y" hunch use kind "causal_hypothesis" with relation "co-occurrence" — it is direction-free and is NEVER a cause. An edge using a causal relation ("causes", "leads to", ...) is discarded.
3. EVIDENCE: cite at least the stated minimum number of refs per proposal. Propose only what the listed evidence supports; propose nothing if the evidence is thin.
4. STRAY MAPPING: an observation of kind "stray-metric" is a metric that could not be auto-joined to an entity. If (and ONLY if) its labels clearly identify the workload/entity it belongs to, propose an "edge" with relation "associated-with" whose subject is "<stray-ref> ~> <entity-key>" and which cites BOTH the stray's ref AND the matching "entity:<key>" ref. If no listed entity clearly matches, propose nothing for that stray — never guess.
5. OUTPUT: STRICT JSON only, no prose, no markdown fence:
{"proposals":[{"kind":"node|edge|member|bar_source|causal_hypothesis","subject":"short id","relation":"associated-with|topology|topo-adjacent|co-occurrence|","evidence":["ref","ref"],"rationale":"short, non-causal note"}]}
If nothing is well-grounded, return {"proposals":[]}.`

const maxPromptEntities = 40

// userPrompt renders the read-only context the model may reason over, BOUNDED by a
// char budget (p.MaxContextChars) so the prompt stays under the model's token/rate
// limit even when a real cluster has hundreds of observations (a 413 TPM rejection was
// surfaced live against the boutique). Truncation is stated, never silent.
func userPrompt(c Context, p Params) string {
	budget := p.MaxContextChars
	if budget <= 0 {
		budget = DefaultParams.MaxContextChars
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Graph version: %s\n", c.GraphVersion)
	fmt.Fprintf(&b, "Minimum evidence refs per proposal: %d\n\n", p.MinEvidence)

	b.WriteString("OBSERVATIONS (cite ONLY by these refs):\n")
	shown := 0
	for _, o := range c.Observations {
		line := fmt.Sprintf("- [%s] (%s) %s\n", o.Ref, o.Kind, truncate(o.Detail, 160))
		if b.Len()+len(line) > budget && shown > 0 {
			break
		}
		b.WriteString(line)
		shown++
	}
	if shown == 0 {
		b.WriteString("(none)\n")
	}
	if omitted := len(c.Observations) - shown; omitted > 0 {
		fmt.Fprintf(&b, "(+%d more observations omitted to fit the context budget)\n", omitted)
	}

	if len(c.ValidEntities) > 0 {
		keys := make([]string, 0, len(c.ValidEntities))
		for k := range c.ValidEntities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nVALID ENTITY KEYS (cite as \"entity:<key>\"; an edge may reference these):\n")
		n := len(keys)
		if n > maxPromptEntities {
			n = maxPromptEntities
		}
		for _, k := range keys[:n] {
			fmt.Fprintf(&b, "- entity:%s\n", k)
		}
		if len(keys) > n {
			fmt.Fprintf(&b, "(+%d more entity keys omitted)\n", len(keys)-n)
		}
	}

	b.WriteString("\nPropose candidate graph extensions grounded ONLY in the OBSERVATIONS above. Return strict JSON.")
	return b.String()
}
