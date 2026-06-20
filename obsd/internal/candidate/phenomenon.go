package candidate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Phenomenon-candidate proposal (doc 21 Phase 4): the anomaly→phenomenon loop. A RECURRING
// unexplained anomaly (the unexplained channel's deterministic recurrence aggregation, doc 08
// §3.6 — same loud-signal set, same entity kind, across enough windows/entities) becomes a
// CANDIDATE phenomenon for the governance queue. It is the highest-value extension: the agent
// authors ZERO phenomena and CANNOT author detection logic (charter); this loop lets a NAMED
// HUMAN curate a recurring anomaly into a real phenomenon. The candidate's EXISTENCE is
// deterministic (a MEASURED recurrence count, never a model invention); on promotion the human
// authors the real members/roles/bars — the rendered overlay is a STARTING phenomenon skeleton,
// never a finished detection. status=candidate, firewalled, the same as every other kind.

// PhenomenonCandidateProposal is the typed payload of a KindPhenomenonCandidate candidate.
type PhenomenonCandidateProposal struct {
	Signature  string   // the recurrence signature (entity kind + sorted metric set) — the dedup key
	EntityKind string   // the entity kind the pattern recurs on
	Metrics    []string // the recurring loud-signal set — the candidate member signals
	Windows    int      // recurrence count across evaluation windows (the deterministic support)
	Entities   []string // distinct entities exhibiting it (cross-entity recurrence)
	Label      string   // OPTIONAL human-readable label (agent/human enrichment; "" ⇒ derived)
}

// PhenomenonCandidateSubject is the canonical subject for a phenomenon candidate.
func PhenomenonCandidateSubject(signature string) string {
	return "phenomenon:" + strings.TrimSpace(signature)
}

// PhenomenonCandidatePayload marshals a proposal into the opaque candidate payload map.
func PhenomenonCandidatePayload(p PhenomenonCandidateProposal) map[string]any {
	metrics := append([]string(nil), p.Metrics...)
	sort.Strings(metrics)
	ms := make([]any, len(metrics))
	for i, v := range metrics {
		ms[i] = v
	}
	ents := append([]string(nil), p.Entities...)
	sort.Strings(ents)
	es := make([]any, len(ents))
	for i, v := range ents {
		es[i] = v
	}
	m := map[string]any{
		"signature":   p.Signature,
		"entity_kind": p.EntityKind,
		"metrics":     ms,
		"windows":     p.Windows,
		"entities":    es,
	}
	if strings.TrimSpace(p.Label) != "" {
		m["label"] = p.Label
	}
	return m
}

// ParsePhenomenonCandidatePayload reads a proposal back out of the candidate payload map.
func ParsePhenomenonCandidatePayload(payload map[string]any) (PhenomenonCandidateProposal, error) {
	var p PhenomenonCandidateProposal
	p.Signature, _ = payload["signature"].(string)
	p.EntityKind, _ = payload["entity_kind"].(string)
	p.Label, _ = payload["label"].(string)
	p.Windows = anyInt(payload["windows"])
	p.Metrics = anyStrings(payload["metrics"])
	p.Entities = anyStrings(payload["entities"])
	return p, nil
}

// ValidatePhenomenonCandidateProposal enforces the structural floor: a signature, at least one
// member metric, and a positive recurrence count (a candidate exists ONLY because the anomaly
// recurred — the declared count is the support, never a learned threshold).
func ValidatePhenomenonCandidateProposal(p PhenomenonCandidateProposal) error {
	if strings.TrimSpace(p.Signature) == "" {
		return fmt.Errorf("phenomenon_candidate: empty signature")
	}
	if len(p.Metrics) == 0 {
		return fmt.Errorf("phenomenon_candidate: at least one recurring member metric is required")
	}
	if p.Windows <= 0 {
		return fmt.Errorf("phenomenon_candidate: windows must be > 0 (a candidate exists only because the anomaly recurred)")
	}
	return nil
}

// PhenomenonCandidateID derives a deterministic, readable phenomenon id from the signature, so
// the same recurrence always promotes to the same PHEN_CANDIDATE_* id.
func PhenomenonCandidateID(p PhenomenonCandidateProposal) string {
	base := "PHEN_CANDIDATE"
	if len(p.Metrics) > 0 {
		base = "PHEN_CANDIDATE_" + sanitizePhenToken(p.Metrics[0])
	}
	sum := sha256.Sum256([]byte(p.Signature))
	return base + "_" + hex.EncodeToString(sum[:])[:8]
}

var phenTokenStrip = regexp.MustCompile(`[^A-Z0-9_]`)

func sanitizePhenToken(metric string) string {
	t := strings.ToUpper(strings.TrimSpace(metric))
	t = phenTokenStrip.ReplaceAllString(t, "_")
	t = strings.Trim(t, "_")
	if len(t) > 30 {
		t = t[:30]
	}
	if t == "" {
		t = "RECUR"
	}
	return t
}

// phenomenonOverlayDoc is the rendered overlay for a promoted phenomenon candidate.
type phenomenonOverlayDoc struct {
	Overlay   string                   `yaml:"overlay"`
	Version   int                      `yaml:"version"`
	Author    string                   `yaml:"author"`
	Status    string                   `yaml:"status"`
	Phenomena []phenomenonOverlayEntry `yaml:"phenomena"`
}

// phenomenonOverlayEntry mirrors graph.overlayPhenomenon: id, label, member-signal tuples
// [pattern, role, temporal, note], and notes. The signal tuples are the recurring metrics as
// CANDIDATE members — the human refines their role/temporal + authors a detection check + a bar.
type phenomenonOverlayEntry struct {
	ID      string     `yaml:"id"`
	Label   string     `yaml:"label"`
	Signals [][]string `yaml:"signals"`
	Notes   string     `yaml:"notes"`
}

const phenomenonOverlayHeader = "# Promoted phenomenon CANDIDATE — AUTHORED starting skeleton (doc 21 Phase 4 / doc 08 §3.6).\n" +
	"# This recurring unexplained anomaly is proposed as a phenomenon for human curation. The block\n" +
	"# below is a STARTING POINT, not a finished detection: adding a phenomenon via overlay creates a\n" +
	"# CorrelationGroup node with NO detection condition implied. You (the named human) author the real\n" +
	"# member roles/temporal order, a detection check, and a DECLARED bar before it can ever fire. The\n" +
	"# system proposes; it never authors detection. Commit through graphlint -strict + a new release.\n"

// PromotedPhenomenonOverlayYAML renders the committable phenomenon-skeleton overlay for a
// PROMOTED KindPhenomenonCandidate. It refuses a non-promoted candidate, a missing author, a
// wrong kind, or an empty note (the rationale a falsifiable authored delta requires, doc 02
// §3.6). Pure given the candidate + graph version. The members are role="corroborating" by
// construction — a candidate phenomenon implies NO required-member detection until the human
// authors one (honest: it is not yet observable, never silently "full").
func PromotedPhenomenonOverlayYAML(c Candidate, graphVersion string) (string, error) {
	if c.Kind != KindPhenomenonCandidate {
		return "", fmt.Errorf("candidate %s is kind %q, not %q", c.ID, c.Kind, KindPhenomenonCandidate)
	}
	if c.Status != StatusPromoted {
		return "", fmt.Errorf("candidate %s is not promoted (status %q)", c.ID, c.Status)
	}
	if c.DecidedBy == "" {
		return "", fmt.Errorf("candidate %s has no named human — an authored overlay needs an author", c.ID)
	}
	if strings.TrimSpace(c.Note) == "" {
		return "", fmt.Errorf("candidate %s: a phenomenon promotion needs an authored note (the rationale a falsifiable delta requires)", c.ID)
	}
	p, err := ParsePhenomenonCandidatePayload(c.Payload)
	if err != nil {
		return "", err
	}
	if err := ValidatePhenomenonCandidateProposal(p); err != nil {
		return "", err
	}
	label := p.Label
	if strings.TrimSpace(label) == "" {
		label = "Recurring unexplained: " + strings.Join(p.Metrics, ", ")
	}
	signals := make([][]string, 0, len(p.Metrics))
	for _, m := range p.Metrics {
		signals = append(signals, []string{
			m, "corroborating", "T0",
			"recurring unexplained loud signal — author the real role + a declared bar before this can fire",
		})
	}
	entry := phenomenonOverlayEntry{
		ID:      PhenomenonCandidateID(p),
		Label:   label,
		Signals: signals,
		Notes: fmt.Sprintf("CANDIDATE from recurring unexplained loudness on %s (%d evaluation windows, %d distinct %s entity/entities). %s",
			strings.Join(p.Metrics, ", "), p.Windows, len(p.Entities), p.EntityKind, c.Note),
	}
	doc := phenomenonOverlayDoc{
		Overlay:   "phenomenon-candidate-" + shortID(c.ID),
		Version:   1,
		Author:    c.DecidedBy,
		Status:    "promoted",
		Phenomena: []phenomenonOverlayEntry{entry},
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("candidate: render phenomenon overlay: %w", err)
	}
	return phenomenonOverlayHeader + string(raw), nil
}

// anyInt / anyStrings tolerate the JSON round-trip (numbers become float64; arrays become []any).
func anyInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func anyStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
