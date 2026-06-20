package dgx

import (
	"encoding/json"
	"fmt"
	"strings"
)

// rawProposal is one proposal as the model emits it (untrusted until gated).
type rawProposal struct {
	Kind      string   `json:"kind"`
	Subject   string   `json:"subject"`
	Relation  string   `json:"relation"`
	Evidence  []string `json:"evidence"`
	Rationale string   `json:"rationale"`
	// equiv_group fields (doc 21 §5) — read ONLY when kind=="equiv_group", ignored otherwise.
	Group     string `json:"group,omitempty"`     // target equivalence-group id (existing) or a new EQG_... id
	Pattern   string `json:"pattern,omitempty"`   // anchored regex that captures the stray metric
	Canonical string `json:"canonical,omitempty"` // NEW group only: canonical OTel variable name
	Label     string `json:"label,omitempty"`     // NEW group only: short human label
}

type proposalDoc struct {
	Proposals []rawProposal `json:"proposals"`
}

// parseProposals extracts the proposals object from the model's text. It tolerates a model
// that wraps the JSON in a ```json fence / leading prose (it scans to the first '{') AND a model
// that appends trailing prose after the JSON — a reasoning model routinely writes "{...} With
// these proposals…". A json.Decoder reads exactly ONE value and stops, so trailing text (even
// text containing braces) never corrupts the parse; only genuinely malformed JSON is an error,
// never a silent empty result.
func parseProposals(raw string) (proposalDoc, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	dec := json.NewDecoder(strings.NewReader(s))
	var doc proposalDoc
	if err := dec.Decode(&doc); err != nil {
		return proposalDoc{}, fmt.Errorf("malformed proposal JSON: %w", err)
	}
	return doc, nil
}
