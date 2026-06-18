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
}

type proposalDoc struct {
	Proposals []rawProposal `json:"proposals"`
}

// parseProposals extracts the proposals array from the model's text. It tolerates a
// model that wraps the JSON in prose or a ```json fence by locating the outermost JSON
// object, but it never tolerates malformed JSON — a parse failure is an error, not a
// silent empty result.
func parseProposals(raw string) (proposalDoc, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 && j < len(s)-1 {
		s = s[:j+1]
	}
	var doc proposalDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return proposalDoc{}, fmt.Errorf("malformed proposal JSON: %w", err)
	}
	return doc, nil
}
