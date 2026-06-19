package candidate

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// PromotionEvidence is one MEASURED fact the promoted candidate rests on, carried onto
// the authored overlay so the promotion stays falsifiable (doc 02 §3.6).
type PromotionEvidence struct {
	Kind   string `yaml:"kind"`
	Ref    string `yaml:"ref"`
	Detail string `yaml:"detail,omitempty"`
}

// PromotionOverlay is the AUTHORED artifact a promotion produces: a committable record of
// the human's decision, attributed to THEM, citing the candidate's evidence. It is a
// STARTING POINT the operator refines into the released graph through the governance gate
// (doc 12) — promotion NEVER auto-mutates the released graph (the candidate-store firewall
// holds), so the overlay does not auto-load; the human commits it deliberately. The
// model's rationale is deliberately ABSENT here: the human authors the note.
type PromotionOverlay struct {
	CandidateID  string              `yaml:"candidate_id"`
	Kind         string              `yaml:"kind"`
	Relation     string              `yaml:"relation,omitempty"`
	Subject      string              `yaml:"subject"`
	Author       string              `yaml:"author"` // the named human (AUTHORED provenance, doc 02 §3.6)
	Note         string              `yaml:"note"`   // the human's authored prose
	PromotedAt   string              `yaml:"promoted_at,omitempty"`
	GraphVersion string              `yaml:"graph_version,omitempty"`
	Evidence     []PromotionEvidence `yaml:"evidence,omitempty"`
}

const promotionOverlayHeader = "# Promoted candidate — AUTHORED overlay (doc 20 governance promotion).\n" +
	"# A committable STARTING ARTIFACT: review + refine into the released graph through the\n" +
	"# governance gate (doc 12; the diff is authoritative). Promotion does NOT auto-load this —\n" +
	"# the named human commits it deliberately, so the candidate-store firewall stays intact.\n"

// PromotedOverlayYAML renders the AUTHORED overlay artifact for a PROMOTED candidate.
// It refuses a non-promoted candidate or one with no named human — a promotion's overlay
// must carry an author (doc 02 §3.6). Pure given the candidate + version.
func PromotedOverlayYAML(c Candidate, graphVersion string) (string, error) {
	if c.Status != StatusPromoted {
		return "", fmt.Errorf("candidate %s is not promoted (status %q) — only a promoted candidate has an authored overlay", c.ID, c.Status)
	}
	if c.DecidedBy == "" {
		return "", fmt.Errorf("candidate %s has no named human — an authored overlay needs an author", c.ID)
	}
	ov := PromotionOverlay{
		CandidateID: c.ID, Kind: string(c.Kind), Relation: c.Relation, Subject: c.Subject,
		Author: c.DecidedBy, Note: c.Note, GraphVersion: graphVersion,
	}
	if !c.DecidedAt.IsZero() {
		ov.PromotedAt = c.DecidedAt.UTC().Format(time.RFC3339)
	}
	for _, e := range c.Evidence {
		ov.Evidence = append(ov.Evidence, PromotionEvidence{Kind: e.Kind, Ref: e.Ref, Detail: e.Detail})
	}
	raw, err := yaml.Marshal(struct {
		Promotion PromotionOverlay `yaml:"promotion"`
	}{ov})
	if err != nil {
		return "", fmt.Errorf("candidate: render promotion overlay: %w", err)
	}
	return promotionOverlayHeader + string(raw), nil
}
