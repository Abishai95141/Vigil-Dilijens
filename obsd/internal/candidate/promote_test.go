package candidate

import (
	"strings"
	"testing"
	"time"
)

func TestDecideRequiresNamedHuman(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	id, err := st.Put(now, Candidate{
		Kind: KindEdge, Relation: "associated-with", Subject: "stray:x ~> entity:y",
		Evidence: []EvidenceRef{{Kind: "stray-metric", Ref: "stray:x"}},
		Lineage:  Lineage{Source: "dgx-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// a decision with no named human is refused (the harness can block, never approve).
	if err := st.Decide(now, id, StatusPromoted, "", "looks right"); err == nil {
		t.Fatal("Decide must refuse an empty decidedBy (a named human is mandatory)")
	}
	// a reset-to-candidate via Decide is refused.
	if err := st.Decide(now, id, StatusCandidate, "alice", "x"); err == nil {
		t.Fatal("Decide must refuse a reset to candidate")
	}
	// a real promotion records the human + note + decided_at.
	if err := st.Decide(now, id, StatusPromoted, "alice@vigil", "this RS metric belongs to currencyservice"); err != nil {
		t.Fatal(err)
	}
	c, found, err := st.Get(id)
	if err != nil || !found {
		t.Fatalf("get promoted: found=%v err=%v", found, err)
	}
	if c.Status != StatusPromoted || c.DecidedBy != "alice@vigil" || c.Note == "" || c.DecidedAt.IsZero() {
		t.Errorf("decision not recorded: %+v", c)
	}
}

func TestPromotedOverlayYAML(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	id, _ := st.Put(now, Candidate{
		Kind: KindEdge, Relation: "associated-with", Subject: "stray:kube_replicaset_owner/x ~> entity:i|c|ns|Deployment|currencyservice|u",
		Evidence: []EvidenceRef{{Kind: "context:stray-metric", Ref: "stray:kube_replicaset_owner/x", Detail: "owner_name=currencyservice"}},
		Lineage:  Lineage{Source: "dgx-agent"},
		Payload:  map[string]any{"rationale": "the model said so"},
	})

	// a non-promoted candidate has no authored overlay.
	c, _, _ := st.Get(id)
	if _, err := PromotedOverlayYAML(*c, "v0.8.0"); err == nil {
		t.Error("a non-promoted candidate must not render an overlay")
	}

	if err := st.Decide(now, id, StatusPromoted, "alice@vigil", "RS owner names the currencyservice workload"); err != nil {
		t.Fatal(err)
	}
	c, _, _ = st.Get(id)
	y, err := PromotedOverlayYAML(*c, "v0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	// the overlay carries the HUMAN's authorship + note + the cited evidence — and NOT the
	// model's rationale (it is discarded at promotion).
	for _, want := range []string{"author: alice@vigil", "RS owner names the currencyservice workload", "kube_replicaset_owner", "associated-with"} {
		if !strings.Contains(y, want) {
			t.Errorf("overlay missing %q:\n%s", want, y)
		}
	}
	if strings.Contains(y, "the model said so") {
		t.Errorf("overlay must NOT carry the model's rationale (it is discarded at promotion):\n%s", y)
	}
}
