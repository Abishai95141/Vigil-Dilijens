package cohypothesis

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

func TestDiscoveredCandidatesDirectionFree(t *testing.T) {
	links := []DiscoveredLink{
		{From: "stream.queue", To: "assetapi.staleness", LagSeconds: 20, LagBins: 4, Coefficient: 0.81, LeadlagHintBins: 4},
		{From: "z.metric", To: "a.metric", LagSeconds: 0, Coefficient: -0.7, Contemporaneous: true},
	}
	cands := DiscoveredCandidates(links, "v0.0.0", 0)
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}
	for _, c := range cands {
		if c.Kind != candidate.KindCausalHypothesis {
			t.Errorf("kind = %s, want causal_hypothesis", c.Kind)
		}
		if c.Relation != "co-occurrence" {
			t.Errorf("relation = %s, want co-occurrence (direction-free)", c.Relation)
		}
		a, _ := c.Payload["a"].(string)
		b, _ := c.Payload["b"].(string)
		if a >= b {
			t.Errorf("payload pair not sorted: a=%q b=%q", a, b)
		}
		if c.Subject != a+" ~ "+b {
			t.Errorf("subject %q != sorted pair %q ~ %q", c.Subject, a, b)
		}
		// charter: NO asserted-direction/cause key — only the PROJECTED hint
		for _, k := range []string{"cause", "direction", "from", "to"} {
			if _, bad := c.Payload[k]; bad {
				t.Errorf("payload carries a direction-asserting key %q (must be direction-free)", k)
			}
		}
		if _, ok := c.Payload["directionHint"]; !ok {
			t.Errorf("missing the PROJECTED directionHint")
		}
		if c.Lineage.Source != "offline-causal-discovery" || c.Lineage.Method != "pcmci-parcorr" {
			t.Errorf("lineage = %s/%s", c.Lineage.Source, c.Lineage.Method)
		}
	}
}

func TestDiscoveredDirectionHintMapping(t *testing.T) {
	// from > to so the sorted pair flips the a/b slots; the PCMCI hint must become b-to-a
	c := DiscoveredCandidates([]DiscoveredLink{{From: "zeta", To: "alpha", Coefficient: 0.9}}, "v", 0)[0]
	if c.Payload["a"] != "alpha" || c.Payload["b"] != "zeta" {
		t.Fatalf("sort: a=%v b=%v", c.Payload["a"], c.Payload["b"])
	}
	if c.Payload["directionHint"] != "b-to-a" {
		t.Errorf("hint = %v, want b-to-a (PCMCI said zeta→alpha == b→a)", c.Payload["directionHint"])
	}
}

func TestStageDiscoveredPairStable(t *testing.T) {
	s, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1700000000, 0).UTC()
	data := []byte(`{"shortlist":[{"from":"x.a","to":"y.b","lag_seconds":15,"coefficient":0.77}]}`)
	if n, err := StageDiscovered(s, now, data, "v", 0); err != nil || n != 1 {
		t.Fatalf("stage1 n=%d err=%v", n, err)
	}
	// re-stage the SAME pair with DIFFERENT per-run values (lag/coef) — must update in place, not flood
	data2 := []byte(`{"shortlist":[{"from":"x.a","to":"y.b","lag_seconds":40,"coefficient":0.55}]}`)
	if n, err := StageDiscovered(s, now.Add(time.Minute), data2, "v", 0); err != nil || n != 1 {
		t.Fatalf("stage2 n=%d err=%v", n, err)
	}
	rows, err := s.List(candidate.Filter{Kind: candidate.KindCausalHypothesis})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("pair not stable: want 1 row after re-staging the same pair, got %d", len(rows))
	}
}

func TestParseShortlist(t *testing.T) {
	if _, err := ParseShortlist([]byte("not json")); err == nil {
		t.Error("want error on malformed json")
	}
	s, err := ParseShortlist([]byte("   "))
	if err != nil || len(s.Shortlist) != 0 {
		t.Errorf("blank should yield zero links, no error; got err=%v n=%d", err, len(s.Shortlist))
	}
}
