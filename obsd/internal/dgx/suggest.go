package dgx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// Phenomenon-candidate enrichment (doc 21 Phase 4 §C). The agent suggests a human-readable LABEL
// and a one-line, strictly-DESCRIPTIVE summary of a recurring unexplained anomaly, to help the
// human reviewer understand what the recurring pattern is. The result is PROJECTED — a naming
// hint, never AUTHORED: it asserts NO cause, proposes NO detection/bar/member, and is DISCARDED
// at promotion (the named human authors the real label + detection). It is ONE bounded LLM call;
// a provider or parse failure returns an error and NO suggestion, leaving the deterministic
// phenomenon candidate completely unaffected.

const suggestSystemPrompt = `You name recurring infrastructure anomalies for a human operator.
Given a recurring set of loud metric names on a Kubernetes entity kind, output a CONCISE
human-readable LABEL (at most 6 words), a ONE-LINE description of what the recurring pattern most
likely REPRESENTS — strictly descriptive — and a SUGGESTED harm severity from exactly
{critical, high, medium, low} reflecting how much harm it would represent if confirmed. Do NOT
assert a cause, do NOT propose a remediation or a threshold, and do NOT invent metrics that were
not given. The severity is a HINT for the human curator, never authoritative. Output ONLY a JSON
object: {"label": "...", "description": "...", "severity": "..."}.`

func suggestUserPrompt(metrics []string, entityKind string) string {
	return "Recurring loud signal(s) on a " + entityKind + ": " + strings.Join(metrics, ", ") +
		". Suggest a short label, a one-line non-causal description of what this pattern represents, and a harm severity (critical|high|medium|low)."
}

// SuggestPhenomenon performs the single enrichment call and returns a PROJECTED suggestion tagged
// with the producing provider (provenance). metrics MUST be non-empty (the deterministic facts it
// summarises). The label/description are length-bounded so a runaway response cannot bloat the
// surface.
func (a *Agent) SuggestPhenomenon(ctx context.Context, metrics []string, entityKind string) (candidate.Suggestion, error) {
	if len(metrics) == 0 {
		return candidate.Suggestion{}, fmt.Errorf("dgx: suggest phenomenon: no metrics to summarise")
	}
	raw, err := a.provider.Complete(ctx, suggestSystemPrompt, suggestUserPrompt(metrics, entityKind))
	if err != nil {
		return candidate.Suggestion{}, fmt.Errorf("dgx: suggest provider: %w", err)
	}
	label, desc, sev, err := parseSuggestion(raw)
	if err != nil {
		return candidate.Suggestion{}, err
	}
	return candidate.Suggestion{
		Label:       truncate(label, 80),
		Description: truncate(desc, 240),
		Severity:    sev, // already validated to the closed vocabulary (or "" if the model gave a bad/absent value)
		Model:       a.provider.Name(),
	}, nil
}

type suggestionDoc struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

// validSuggestedSeverity keeps the suggested severity to the closed vocabulary (it mirrors
// graph.IsValidSeverity; dgx does not import the graph loader). A bad/absent value drops to ""
// rather than propagating a junk level — the hint is best-effort, the human authors the real one.
func validSuggestedSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(s))
	}
	return ""
}

// parseSuggestion extracts {label, description, severity} from the model's text, tolerating a
// ```json fence / leading prose (scans to the first '{') and trailing prose (json.Decoder reads
// one value and stops). A label is required; a missing label or malformed JSON is an error, never
// a silent empty hint. The severity is normalised to the closed vocabulary (bad/absent -> "").
func parseSuggestion(raw string) (string, string, string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	dec := json.NewDecoder(strings.NewReader(s))
	var d suggestionDoc
	if err := dec.Decode(&d); err != nil {
		return "", "", "", fmt.Errorf("dgx: malformed suggestion JSON: %w", err)
	}
	if strings.TrimSpace(d.Label) == "" {
		return "", "", "", fmt.Errorf("dgx: suggestion has no label")
	}
	return strings.TrimSpace(d.Label), strings.TrimSpace(d.Description), validSuggestedSeverity(d.Severity), nil
}
