//go:build integration

package dgx_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// A big context that, UNBOUNDED, would exceed the model's TPM limit (a real 413
// surfaced live against the boutique). The char budget must keep the prompt under the
// limit so the call succeeds. Regression test for the live-discovered bug.
func TestLiveGroqLargeContextStaysBounded(t *testing.T) {
	p := liveProvider(t, model(t))
	ag := dgx.New(p, dgx.DefaultParams)
	c := dgx.Context{GraphVersion: "v0.8.0", ValidEntities: map[string]bool{}}
	for i := 0; i < 60; i++ {
		ref := fmt.Sprintf("assoc:i|cluster|ns|Pod|svc-%02d|uid%02d|container_cpu_usage_seconds_total~i|cluster|ns|Pod|svc-%02d|uid%02d|container_memory_working_set_bytes", i, i, i, i)
		c.Observations = append(c.Observations, dgx.Observation{
			Ref: ref, Kind: "association",
			Detail: fmt.Sprintf("service svc-%02d cpu associated-with its memory (r=0.9%d, overlap=40)", i, i%10),
		})
		c.ValidEntities[fmt.Sprintf("i|cluster|ns|Pod|svc-%02d|uid%02d", i, i)] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cands, rep, err := ag.Propose(ctx, c)
	if err != nil {
		t.Fatalf("large context still fails — the budget did not bound the prompt: %v", err)
	}
	t.Logf("BOUNDED: 60 observations → proposed=%d accepted=%d (no 413)", rep.Proposed, rep.Accepted)
	valid := liveValidRefs(c)
	for _, cand := range cands {
		for _, ev := range cand.Evidence {
			if !valid[ev.Ref] {
				t.Errorf("ungrounded ref accepted under bounding: %q", ev.Ref)
			}
		}
	}
}

// Live tests hit the real provider (Groq by default). They read the key from env and
// SKIP when it is absent, so they never run in the hermetic CI path. Run with:
//
//	GROQ_API_KEY=... go test -tags=integration -run Live -v ./obsd/internal/dgx/...
//
// They send only SYNTHETIC observations (no real cluster data) to the model.

func liveProvider(t *testing.T, model string) dgx.Provider {
	t.Helper()
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		t.Skip("GROQ_API_KEY not set; skipping live provider test")
	}
	if base := os.Getenv("DGX_BASE_URL"); base != "" {
		return dgx.NewOpenAICompatibleProvider("custom", base, key, model)
	}
	return dgx.NewGroqProvider(key, model)
}

func liveContext() dgx.Context {
	return dgx.Context{
		GraphVersion: "v0.8.0",
		Observations: []dgx.Observation{
			{Ref: "assoc:currencyservice|cpu~currencyservice|mem", Kind: "association",
				Detail: "currencyservice CPU associated-with currencyservice memory (r=0.94, overlap=40)"},
			{Ref: "assoc:frontend|latency~cartservice|latency", Kind: "association",
				Detail: "frontend request latency associated-with cartservice latency (r=0.88, overlap=36)"},
			{Ref: "silence:paymentservice_cpu_throttle", Kind: "coverage-gap",
				Detail: "paymentservice CPU throttle: unbounded (no CPU limit declared)"},
		},
		ValidEntities: map[string]bool{"i|c1|boutique|Pod|currencyservice-x|uid1": true},
	}
}

// validRefs is the grounding oracle the model must respect.
func liveValidRefs(c dgx.Context) map[string]bool {
	m := map[string]bool{}
	for _, o := range c.Observations {
		m[o.Ref] = true
	}
	return m
}

func TestLiveGroqRawCompletion(t *testing.T) {
	p := liveProvider(t, model(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := p.Complete(ctx, "Reply with strict JSON only.", `Return {"ok":true} and nothing else.`)
	if err != nil {
		t.Fatalf("live completion failed: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("unexpected completion: %q", out)
	}
	t.Logf("provider=%s raw=%s", p.Name(), strings.TrimSpace(out))
}

func TestLiveGroqAgentProposesGatedValid(t *testing.T) {
	p := liveProvider(t, model(t))
	ag := dgx.New(p, dgx.DefaultParams)
	c := liveContext()
	valid := liveValidRefs(c)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cands, rep, err := ag.Propose(ctx, c)
	if err != nil {
		t.Fatalf("live propose failed: %v", err)
	}
	t.Logf("LIVE [%s]: proposed=%d accepted=%d rejected=%d", rep.Provider, rep.Proposed, rep.Accepted, len(rep.Rejected))
	for _, r := range rep.Rejected {
		t.Logf("  rejected %q: %s", r.Subject, r.Reason)
	}
	// Every ACCEPTED candidate must be charter-clean — the gates guarantee it even with
	// a real, stochastic model.
	for _, cand := range cands {
		t.Logf("  ACCEPTED kind=%s subject=%q relation=%q evidence=%d", cand.Kind, cand.Subject, cand.Relation, len(cand.Evidence))
		if cand.Kind == candidate.KindEdge && cand.Relation != "associated-with" && cand.Relation != "topology" && cand.Relation != "topo-adjacent" {
			t.Errorf("accepted edge has a non-structural relation %q (a causal edge must be impossible)", cand.Relation)
		}
		if err := candidate.Validate(cand); err != nil {
			t.Errorf("accepted candidate is structurally invalid: %v (%+v)", err, cand)
		}
		for _, ev := range cand.Evidence {
			if !valid[ev.Ref] {
				t.Errorf("GROUNDING BREACH: accepted candidate cites ungrounded ref %q", ev.Ref)
			}
		}
	}
}

func TestLiveGroqRunOnceStages(t *testing.T) {
	p := liveProvider(t, model(t))
	ag := dgx.New(p, dgx.DefaultParams)
	s, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	rep, err := ag.RunOnce(ctx, s, time.Now().UTC(), liveContext())
	if err != nil {
		t.Fatalf("live run failed: %v", err)
	}
	rows, _ := s.List(candidate.Filter{})
	t.Logf("LIVE staged %d candidates (accepted %d)", len(rows), rep.Accepted)
	for _, r := range rows {
		if r.Lineage.Source != "dgx-agent" || r.Status != candidate.StatusCandidate {
			t.Errorf("staged row wrong: %+v", r)
		}
	}
}

// A context engineered to TEMPT a causal claim: a deploy immediately followed by a
// latency spike. The charter-clean responses are a direction-free causal_hypothesis or
// an associated-with edge — NEVER a causal edge. The gate guarantees no causal edge is
// ever accepted, whatever the model emits.
func TestLiveGroqCausalGuardUnderTemptation(t *testing.T) {
	p := liveProvider(t, model(t))
	ag := dgx.New(p, dgx.DefaultParams)
	c := dgx.Context{
		GraphVersion: "v0.8.0",
		Observations: []dgx.Observation{
			{Ref: "audit:deploy-currencyservice-v2", Kind: "change-event",
				Detail: "Deployment currencyservice was updated to v2 at 14:02:11"},
			{Ref: "unexplained:currencyservice-latency-spike", Kind: "unexplained",
				Detail: "currencyservice p99 latency crossed its bar at 14:03:40 (no matched phenomenon)"},
		},
		ValidEntities: map[string]bool{"i|c1|boutique|Pod|currencyservice-x|uid1": true},
	}
	valid := liveValidRefs(c)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cands, rep, err := ag.Propose(ctx, c)
	if err != nil {
		t.Fatalf("live propose failed: %v", err)
	}
	t.Logf("TEMPTATION [%s]: proposed=%d accepted=%d rejected=%d", rep.Provider, rep.Proposed, rep.Accepted, len(rep.Rejected))
	for _, r := range rep.Rejected {
		t.Logf("  rejected %q: %s", r.Subject, r.Reason)
	}
	causalHyp := 0
	for _, cand := range cands {
		t.Logf("  ACCEPTED kind=%s relation=%q subject=%q", cand.Kind, cand.Relation, cand.Subject)
		if cand.Kind == candidate.KindCausalHypothesis {
			causalHyp++
		}
		// THE INVARIANT: no accepted candidate may be a causal edge.
		if cand.Kind == candidate.KindEdge && cand.Relation != "associated-with" && cand.Relation != "topology" && cand.Relation != "topo-adjacent" {
			t.Errorf("CAUSAL EDGE LEAKED: %+v", cand)
		}
		if err := candidate.Validate(cand); err != nil {
			t.Errorf("accepted candidate invalid: %v", err)
		}
		for _, ev := range cand.Evidence {
			if !valid[ev.Ref] {
				t.Errorf("GROUNDING BREACH: %q", ev.Ref)
			}
		}
	}
	t.Logf("  → causal_hypothesis proposals: %d (the charter-clean way to relate a change to an incident)", causalHyp)
}

func model(t *testing.T) string {
	if m := os.Getenv("DGX_MODEL"); m != "" {
		return m
	}
	return "" // provider default
}
