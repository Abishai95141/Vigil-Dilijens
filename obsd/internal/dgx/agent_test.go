package dgx_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

var t0 = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

func testContext() dgx.Context {
	return dgx.Context{
		GraphVersion: "v0.8.0",
		Observations: []dgx.Observation{
			{Ref: "assoc:node|cpu~node|mem", Kind: "association", Detail: "node|cpu associated-with node|mem (r=0.98)"},
			{Ref: "silence:foo", Kind: "coverage-gap", Detail: "foo: unbounded (no declared bar)"},
		},
		ValidEntities: map[string]bool{"i|c1||Node|worker-1|node-u1": true},
	}
}

const gatedResponse = `{"proposals":[
 {"kind":"edge","subject":"cpu-mem-subsystem","relation":"associated-with","evidence":["assoc:node|cpu~node|mem"],"rationale":"cpu and mem move together"},
 {"kind":"causal_hypothesis","subject":"gap-cooccurs-assoc","relation":"co-occurrence","evidence":["silence:foo","assoc:node|cpu~node|mem"],"rationale":"observed together"},
 {"kind":"edge","subject":"ungrounded","relation":"associated-with","evidence":["assoc:bogus"],"rationale":"x"},
 {"kind":"edge","subject":"causal-edge","relation":"causes","evidence":["assoc:node|cpu~node|mem"],"rationale":"x"},
 {"kind":"edge","subject":"no-evidence","relation":"associated-with","evidence":[],"rationale":"x"},
 {"kind":"edge","subject":"","relation":"associated-with","evidence":["assoc:node|cpu~node|mem"],"rationale":"x"}
]}`

func TestAgentProposeGates(t *testing.T) {
	ag := dgx.New(dgx.NewStaticProvider("fake", gatedResponse), dgx.DefaultParams)
	cands, rep, err := ag.Propose(context.Background(), testContext())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Proposed != 6 {
		t.Errorf("proposed = %d, want 6", rep.Proposed)
	}
	if rep.Accepted != 2 || len(cands) != 2 {
		t.Fatalf("accepted = %d (cands %d), want 2 (the grounded edge + causal_hypothesis)", rep.Accepted, len(cands))
	}
	reasons := map[string]string{}
	for _, r := range rep.Rejected {
		reasons[r.Subject] = r.Reason
	}
	if len(reasons) != 4 {
		t.Fatalf("rejected = %v, want 4", rep.Rejected)
	}
	checks := map[string]string{
		"ungrounded":  "ungrounded",
		"causal-edge": "structural",
		"no-evidence": "evidence below floor",
		"":            "empty subject",
	}
	for subj, want := range checks {
		if !strings.Contains(reasons[subj], want) {
			t.Errorf("reject reason for %q = %q, want it to mention %q", subj, reasons[subj], want)
		}
	}
	// the accepted edge is associated-with (never causal) with grounded evidence + lineage
	for _, c := range cands {
		if c.Lineage.Source != "dgx-agent" || c.Lineage.Method != "llm:fake" {
			t.Errorf("lineage wrong: %+v", c.Lineage)
		}
		if c.Kind == candidate.KindEdge && c.Relation != "associated-with" {
			t.Errorf("accepted edge relation = %q, want associated-with", c.Relation)
		}
		if len(c.Evidence) == 0 || c.Evidence[0].Detail == "" {
			t.Errorf("accepted candidate must carry the grounded MEASURED evidence: %+v", c.Evidence)
		}
	}
}

func TestAgentRunOnceStages(t *testing.T) {
	ag := dgx.New(dgx.NewStaticProvider("fake", gatedResponse), dgx.DefaultParams)
	s, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rep, err := ag.RunOnce(context.Background(), s, t0, testContext())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2", rep.Accepted)
	}
	all, _ := s.List(candidate.Filter{})
	if len(all) != 2 {
		t.Fatalf("store has %d rows, want 2", len(all))
	}
	for _, c := range all {
		if c.Status != candidate.StatusCandidate || c.Lineage.Source != "dgx-agent" {
			t.Errorf("staged candidate wrong: %+v", c)
		}
	}
}

func TestAgentEmptyProposalsOK(t *testing.T) {
	ag := dgx.New(dgx.NewStaticProvider("fake", `{"proposals":[]}`), dgx.DefaultParams)
	cands, rep, err := ag.Propose(context.Background(), testContext())
	if err != nil || len(cands) != 0 || rep.Accepted != 0 {
		t.Errorf("empty proposals should be a clean no-op: cands=%d rep=%+v err=%v", len(cands), rep, err)
	}
}

func TestAgentMalformedJSONIsError(t *testing.T) {
	ag := dgx.New(dgx.NewStaticProvider("fake", "I cannot help with that {oops"), dgx.DefaultParams)
	if _, _, err := ag.Propose(context.Background(), testContext()); err == nil {
		t.Error("malformed model output must be an error, never a silent empty result")
	}
}

func TestAgentProviderError(t *testing.T) {
	ag := dgx.New(dgx.NewStaticProvider("fake", "").WithError(errors.New("boom")), dgx.DefaultParams)
	if _, _, err := ag.Propose(context.Background(), testContext()); err == nil {
		t.Error("a provider error must propagate")
	}
}
