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
5. EQUIVALENCE-GROUP MAPPING: a "stray-metric" whose metric is a real OPERATIONAL signal (an exporter series like redis_*, mysqld_*, pg_*, node_*, or an app /metrics gauge) can be mapped into an equivalence group so the resolver stops treating it as a stray. If its meaning clearly matches one of the EQUIVALENCE GROUPS listed (by canonical name), propose kind "equiv_group" with: subject = the metric name, group = the matching group id, pattern = an ANCHORED regex matching the metric exactly (e.g. "^redis_connected_clients$"), evidence = [the stray-metric ref]. If NO listed group fits but the metric is a clearly known quantity, propose a NEW group: set group to a fresh id "EQG_...", and ALSO provide canonical (the canonical OTel name) and label (a short human label). NEVER map a kube_* object-inventory metric. Propose nothing if unsure.
6. OUTPUT: STRICT JSON only, no prose, no markdown fence:
{"proposals":[{"kind":"node|edge|member|bar_source|causal_hypothesis|equiv_group","subject":"short id","relation":"associated-with|topology|topo-adjacent|co-occurrence|","group":"EQG_…","pattern":"^…$","canonical":"…","label":"…","evidence":["ref","ref"],"rationale":"short, non-causal note"}]}
The group/pattern/canonical/label fields are used ONLY for kind "equiv_group"; omit them otherwise. If nothing is well-grounded, return {"proposals":[]}.`

const maxPromptEntities = 40
const maxPromptGroups = 40 // the full base catalog is ~35 groups; bound it like the entity list

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

	if len(c.KnownGroups) > 0 {
		ids := make([]string, 0, len(c.KnownGroups))
		for id := range c.KnownGroups {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		b.WriteString("\nEQUIVALENCE GROUPS (map an operational stray into one of these by id, or propose a new EQG_… id):\n")
		n := len(ids)
		if n > maxPromptGroups {
			n = maxPromptGroups
		}
		for _, id := range ids[:n] {
			fmt.Fprintf(&b, "- %s = %s\n", id, c.KnownGroups[id])
		}
		if len(ids) > n {
			fmt.Fprintf(&b, "(+%d more groups omitted)\n", len(ids)-n)
		}
	}

	b.WriteString("\nPropose candidate graph extensions grounded ONLY in the OBSERVATIONS above. Return strict JSON.")
	return b.String()
}
