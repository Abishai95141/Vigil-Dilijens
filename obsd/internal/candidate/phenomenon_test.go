package candidate

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func samplefPhenProposal() PhenomenonCandidateProposal {
	return PhenomenonCandidateProposal{
		Signature:  "Node\x1fnode_pressure_cpu_waiting_seconds_total",
		EntityKind: "Node",
		Metrics:    []string{"node_pressure_cpu_waiting_seconds_total"},
		Windows:    7,
		Entities:   []string{"i|c|ns|Node|worker-1|u1"},
		Label:      "",
	}
}

func TestPhenomenonPayloadRoundTrip(t *testing.T) {
	p := samplefPhenProposal()
	p.Metrics = []string{"b_total", "a_total"} // payload must sort
	got, err := ParsePhenomenonCandidatePayload(PhenomenonCandidatePayload(p))
	if err != nil {
		t.Fatal(err)
	}
	if got.Signature != p.Signature || got.EntityKind != p.EntityKind || got.Windows != p.Windows {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, p)
	}
	if !reflect.DeepEqual(got.Metrics, []string{"a_total", "b_total"}) {
		t.Fatalf("metrics not sorted/preserved: %v", got.Metrics)
	}
}

func TestPhenomenonValidate(t *testing.T) {
	ok := samplefPhenProposal()
	if err := ValidatePhenomenonCandidateProposal(ok); err != nil {
		t.Fatalf("valid proposal rejected: %v", err)
	}
	bad := []PhenomenonCandidateProposal{
		{Signature: "", Metrics: []string{"m"}, Windows: 1},  // empty signature
		{Signature: "s", Metrics: nil, Windows: 1},           // no metrics
		{Signature: "s", Metrics: []string{"m"}, Windows: 0}, // no recurrence
	}
	for i, p := range bad {
		if err := ValidatePhenomenonCandidateProposal(p); err == nil {
			t.Errorf("case %d: expected error for %+v", i, p)
		}
	}
}

func TestPhenomenonIDDeterministic(t *testing.T) {
	p := samplefPhenProposal()
	a, b := PhenomenonCandidateID(p), PhenomenonCandidateID(p)
	if a != b {
		t.Fatalf("id not deterministic: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "PHEN_CANDIDATE_NODE_PRESSURE_CPU_WAITING") {
		t.Fatalf("id not derived from the metric: %s", a)
	}
}

func TestPromotedPhenomenonOverlay(t *testing.T) {
	p := samplefPhenProposal()
	c := Candidate{
		ID:        "cand:abc123",
		Kind:      KindPhenomenonCandidate,
		Subject:   PhenomenonCandidateSubject(p.Signature),
		Payload:   PhenomenonCandidatePayload(p),
		Status:    StatusPromoted,
		DecidedBy: "alice",
		Note:      "this recurring CPU-wait pressure is worth a phenomenon",
		DecidedAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	}
	y, err := PromotedPhenomenonOverlayYAML(c, "v0.9.0")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The rendered YAML must parse and carry a valid phenomena block with 4-element signal tuples.
	var doc struct {
		Author    string `yaml:"author"`
		Status    string `yaml:"status"`
		Phenomena []struct {
			ID      string     `yaml:"id"`
			Label   string     `yaml:"label"`
			Signals [][]string `yaml:"signals"`
			Notes   string     `yaml:"notes"`
		} `yaml:"phenomena"`
	}
	body := y[strings.Index(y, "overlay:"):] // strip the leading comment header before unmarshal
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("rendered overlay is not valid YAML: %v\n%s", err, y)
	}
	if doc.Author != "alice" || doc.Status != "promoted" {
		t.Fatalf("author/status wrong: %+v", doc)
	}
	if len(doc.Phenomena) != 1 {
		t.Fatalf("want 1 phenomenon, got %d", len(doc.Phenomena))
	}
	ph := doc.Phenomena[0]
	if !strings.HasPrefix(ph.ID, "PHEN_CANDIDATE_") {
		t.Errorf("phenomenon id = %q", ph.ID)
	}
	if len(ph.Signals) != 1 || len(ph.Signals[0]) != 4 {
		t.Fatalf("signal tuple must be [pattern,role,temporal,note]: %+v", ph.Signals)
	}
	if ph.Signals[0][0] != "node_pressure_cpu_waiting_seconds_total" || ph.Signals[0][1] != "corroborating" {
		t.Errorf("member tuple wrong: %v", ph.Signals[0])
	}
	if !strings.Contains(ph.Notes, "7 evaluation windows") || !strings.Contains(ph.Notes, c.Note) {
		t.Errorf("notes must carry the recurrence evidence + the human note: %q", ph.Notes)
	}
}

func TestPromotedPhenomenonRefusals(t *testing.T) {
	p := samplefPhenProposal()
	base := Candidate{Kind: KindPhenomenonCandidate, Subject: "phenomenon:x", Payload: PhenomenonCandidatePayload(p)}
	// not promoted
	if _, err := PromotedPhenomenonOverlayYAML(base, "v"); err == nil {
		t.Error("expected refusal for non-promoted candidate")
	}
	// promoted but no author
	c := base
	c.Status = StatusPromoted
	if _, err := PromotedPhenomenonOverlayYAML(c, "v"); err == nil {
		t.Error("expected refusal for missing author")
	}
	// promoted + author but no note
	c.DecidedBy = "bob"
	if _, err := PromotedPhenomenonOverlayYAML(c, "v"); err == nil {
		t.Error("expected refusal for empty note")
	}
	// wrong kind
	wrong := Candidate{Kind: KindEdge, Status: StatusPromoted, DecidedBy: "bob", Note: "n"}
	if _, err := PromotedPhenomenonOverlayYAML(wrong, "v"); err == nil {
		t.Error("expected refusal for wrong kind")
	}
}

func TestStoreAcceptsPhenomenonCandidate(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := samplefPhenProposal()
	c := Candidate{
		Kind:    KindPhenomenonCandidate,
		Subject: PhenomenonCandidateSubject(p.Signature),
		Payload: PhenomenonCandidatePayload(p),
		Evidence: []EvidenceRef{
			{Kind: "context:unexplained", Ref: "unexplained:" + p.Signature, Detail: "recurring"},
		},
		Lineage: Lineage{Source: "unexplained-curation", Method: "recurrence", GraphVersion: "v0.9.0"},
	}
	id, err := st.Put(time.Now().UTC(), c)
	if err != nil {
		t.Fatalf("store rejected phenomenon_candidate: %v", err)
	}
	got, found, err := st.Get(id)
	if err != nil || !found {
		t.Fatalf("get: %v found=%v", err, found)
	}
	if got.Kind != KindPhenomenonCandidate {
		t.Fatalf("kind = %q", got.Kind)
	}
}
