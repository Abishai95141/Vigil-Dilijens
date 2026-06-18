package dgx

import (
	"fmt"
	"sort"
	"strings"
)

const systemPrompt = `You are the Vigil Dynamic Graph eXtension PROPOSER. You PROPOSE candidate graph extensions; you AUTHOR nothing and decide nothing. A human promotes (or rejects) every proposal later. Follow these rules exactly:

1. GROUNDING: You may ONLY cite evidence by the exact "ref" strings listed under OBSERVATIONS. Never invent a ref, a metric, an entity, or a fact. A proposal that cites anything not in OBSERVATIONS will be discarded.
2. NO CAUSATION: A correlation or co-occurrence is association, NOT cause. For "X is related to Y" use kind "edge" with relation "associated-with" (or "topology"/"topo-adjacent"). For a temporal "X co-occurs with / precedes Y" hunch use kind "causal_hypothesis" with relation "co-occurrence" — it is direction-free and is NEVER a cause. An edge using a causal relation ("causes", "leads to", ...) is discarded.
3. EVIDENCE: cite at least the stated minimum number of refs per proposal. Propose only what the listed evidence supports; propose nothing if the evidence is thin.
4. OUTPUT: STRICT JSON only, no prose, no markdown fence:
{"proposals":[{"kind":"node|edge|member|bar_source|causal_hypothesis","subject":"short id","relation":"associated-with|topology|topo-adjacent|co-occurrence|","evidence":["ref","ref"],"rationale":"short, non-causal note"}]}
If nothing is well-grounded, return {"proposals":[]}.`

// userPrompt renders the read-only context the model may reason over.
func userPrompt(c Context, p Params) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Graph version: %s\n", c.GraphVersion)
	fmt.Fprintf(&b, "Minimum evidence refs per proposal: %d\n\n", p.MinEvidence)

	b.WriteString("OBSERVATIONS (cite ONLY by these refs):\n")
	for _, o := range c.Observations {
		fmt.Fprintf(&b, "- [%s] (%s) %s\n", o.Ref, o.Kind, o.Detail)
	}
	if len(c.Observations) == 0 {
		b.WriteString("(none)\n")
	}

	if len(c.ValidEntities) > 0 {
		keys := make([]string, 0, len(c.ValidEntities))
		for k := range c.ValidEntities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nVALID ENTITY KEYS (an edge may reference these):\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s\n", k)
		}
	}

	b.WriteString("\nPropose candidate graph extensions grounded ONLY in the OBSERVATIONS above. Return strict JSON.")
	return b.String()
}
