package dgx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// AI-assisted governance triage (docs/33 build 4). A NAMED OPERATOR authorizes the agent to
// review ONE staged candidate and recommend a governance verdict — promote, reject, or hold —
// grounded ONLY in the candidate's MEASURED evidence + lineage. This is the agent's JUDGEMENT,
// elevated to a governance ACTION solely by the operator's deliberate click, recorded in an AI
// audit log, and REVERSIBLE. It is fallible by construction (a disclaimer says so): the model
// can be wrong, so the conservative default is HOLD, and every promotion can be reverted.
//
// CHARTER: this is the ONE place an agent's verdict drives a write, and only under the four
// guardrails the rest of the system holds for AUTHORED content — a named authorizer (the
// operator), full provenance (the AI audit log records model + rationale + confidence + who
// authorized), reversibility (Revert), and honesty (the verdict is labelled agent-made, never a
// human's). The agent NEVER invents a cause: for a causal hypothesis it may recommend a
// DIRECTION, but only one the existing dual-witness already supports, and promotion routes
// through the same authorCausalDirection path a human uses.

// TriageVerdict is the agent's recommendation on a candidate.
type TriageVerdict string

const (
	VerdictPromote TriageVerdict = "promote" // the evidence supports authoring this into the graph
	VerdictReject  TriageVerdict = "reject"  // the candidate is unnecessary / unsupported / noise
	VerdictHold    TriageVerdict = "hold"    // genuinely uncertain — leave for a human (the safe default)
)

// IsValidVerdict reports whether v is one of the closed verdicts.
func IsValidVerdict(v TriageVerdict) bool {
	return v == VerdictPromote || v == VerdictReject || v == VerdictHold
}

// TriageDecision is the agent's structured recommendation. Confidence ∈ {high,medium,low}.
// Direction is set ONLY for a causal hypothesis the agent recommends promoting (a-to-b|b-to-a),
// "" otherwise; it is a hint the human-equivalent authoring path re-validates against the witness.
type TriageDecision struct {
	Verdict    TriageVerdict
	Confidence string
	Rationale  string
	Direction  string
	Model      string
}

const triageSystemPrompt = `You are a conservative governance reviewer for a Kubernetes observability
system. An operator has asked you to triage ONE staged "candidate" — a machine-proposed addition to
the system's knowledge graph (a stray-metric mapping, an equivalence group, a topology edge, a
recurring-anomaly phenomenon, or a cross-workload causal hypothesis). Decide whether it should be:
  - "promote": the MEASURED evidence clearly supports authoring it into the graph (it is specific,
    well-grounded, and useful),
  - "reject": it is unnecessary, redundant, noise, or unsupported by its evidence,
  - "hold": you are genuinely uncertain — DEFAULT to this whenever the evidence is thin or ambiguous.

Rules you must obey:
  - Decide ONLY from the evidence and fields given. Never invent facts, metrics, or a cause.
  - You are FALLIBLE and the operator can revert you. Be conservative: when unsure, "hold".
  - For a causal hypothesis you may recommend a direction ONLY if the evidence's lead-lag / onset
    witness supports it; output "a-to-b" or "b-to-a". If the witness is absent or contradictory,
    do NOT promote a direction — choose "reject" (not-causal) or "hold". Never guess an arrow.

Output ONLY a JSON object:
{"verdict":"promote|reject|hold","confidence":"high|medium|low","direction":"a-to-b|b-to-a|","rationale":"one or two sentences citing the evidence"}.`

// triageUserPrompt renders the candidate's MEASURED facts for the model — kind, subject, relation,
// the agent's prior projected hint (if any), the evidence refs, and the salient payload fields.
func triageUserPrompt(c candidate.Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Candidate kind: %s\nSubject: %s\n", c.Kind, c.Subject)
	if c.Relation != "" {
		fmt.Fprintf(&b, "Relation: %s\n", c.Relation)
	}
	if c.Lineage.Source != "" {
		fmt.Fprintf(&b, "Proposed by: %s (%s)\n", c.Lineage.Source, c.Lineage.Method)
	}
	if c.Suggestion != nil {
		if c.Suggestion.Direction != "" {
			fmt.Fprintf(&b, "Prior projected direction hint: %s — %s\n", c.Suggestion.Direction, c.Suggestion.DirectionRationale)
		}
		if c.Suggestion.Label != "" {
			fmt.Fprintf(&b, "Prior projected label: %s — %s\n", c.Suggestion.Label, c.Suggestion.Description)
		}
	}
	if len(c.Evidence) > 0 {
		b.WriteString("Evidence:\n")
		for _, e := range c.Evidence {
			fmt.Fprintf(&b, "  - [%s] %s %s\n", e.Kind, e.Ref, e.Detail)
		}
	}
	if len(c.Payload) > 0 {
		// Deterministic, bounded rendering of the salient payload fields.
		keys := make([]string, 0, len(c.Payload))
		for k := range c.Payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("Detail:\n")
		n := 0
		for _, k := range keys {
			if n >= 16 {
				break
			}
			fmt.Fprintf(&b, "  %s: %v\n", k, c.Payload[k])
			n++
		}
	}
	b.WriteString("\nDecide: promote, reject, or hold (default hold if unsure).")
	return b.String()
}

// TriageCandidate performs ONE bounded triage call and returns the agent's recommendation,
// tagged with the producing provider. A provider or parse failure returns an error and NO
// decision, leaving the candidate completely untouched (the deterministic store is never
// mutated by a failed call). The caller logs the decision and — only on the operator's
// authorization — applies it.
func (a *Agent) TriageCandidate(ctx context.Context, c candidate.Candidate) (TriageDecision, error) {
	if a == nil || a.provider == nil {
		return TriageDecision{}, fmt.Errorf("dgx: triage: no provider")
	}
	raw, err := a.provider.Complete(ctx, triageSystemPrompt, triageUserPrompt(c))
	if err != nil {
		return TriageDecision{}, fmt.Errorf("dgx: triage provider: %w", err)
	}
	return parseTriage(raw, a.provider.Name())
}

type triageDoc struct {
	Verdict    string `json:"verdict"`
	Confidence string `json:"confidence"`
	Direction  string `json:"direction"`
	Rationale  string `json:"rationale"`
}

// parseTriage extracts the verdict from the model's text (tolerating a ```json fence / leading
// prose like parseSuggestion). An unrecognised verdict is COERCED to "hold" — a malformed model
// answer must never silently promote; the safe default wins. A direction is normalised and kept
// only on a promote verdict.
func parseTriage(raw, model string) (TriageDecision, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	dec := json.NewDecoder(strings.NewReader(s))
	var d triageDoc
	if err := dec.Decode(&d); err != nil {
		return TriageDecision{}, fmt.Errorf("dgx: malformed triage JSON: %w", err)
	}
	v := TriageVerdict(strings.ToLower(strings.TrimSpace(d.Verdict)))
	if !IsValidVerdict(v) {
		v = VerdictHold // a bad/absent verdict never promotes — fail safe
	}
	conf := strings.ToLower(strings.TrimSpace(d.Confidence))
	switch conf {
	case "high", "medium", "low":
	default:
		conf = "low"
	}
	dir := ""
	if v == VerdictPromote {
		if nd, ok := normalizeDirection(d.Direction); ok && (nd == "a-to-b" || nd == "b-to-a") {
			dir = nd
		}
	}
	return TriageDecision{
		Verdict:    v,
		Confidence: conf,
		Rationale:  truncate(strings.TrimSpace(d.Rationale), 400),
		Direction:  dir,
		Model:      model,
	}, nil
}
