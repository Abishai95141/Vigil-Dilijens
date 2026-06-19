package dgx_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// loopContext seeds one association fact (so the single-shot fallback has something to
// ground on) and NO strays/entities — the tools must supply the rest.
func loopContext() dgx.Context {
	return dgx.Context{
		GraphVersion: "v",
		Observations: []dgx.Observation{{Ref: "seed:1", Kind: "association", Detail: "seed fact"}},
	}
}

// THE core Phase-2 guarantee: a ref returned by a tool THIS cycle becomes citable (enters the
// accumulating grounding index); a ref no tool returned is rejected exactly like an invented one.
func TestLoopGroundingAccumulation(t *testing.T) {
	reg := dgx.NewToolRegistry(fakeTool{name: "get_silence_ledger",
		obs: []dgx.Observation{{Ref: "silence:foo_total", Kind: "coverage-gap", Detail: "foo unbounded"}}})

	// turn 1 calls the tool; turn 2 proposes citing the tool-returned ref → ACCEPTED.
	prov := dgx.NewScriptedProvider("s").
		AddToolCall("c1", "get_silence_ledger", "{}").
		AddText(`{"proposals":[{"kind":"causal_hypothesis","subject":"foo-gap","relation":"co-occurrence","evidence":["silence:foo_total"],"rationale":"observed together"}]}`)
	ag := dgx.New(prov, dgx.DefaultParams)
	ag.SetTools(reg)
	cands, rep, err := ag.Propose(context.Background(), loopContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("accepted %d, want 1 (a tool-returned ref must be citable): rejected=%+v", len(cands), rep.Rejected)
	}
	if rep.ToolCalls != 1 {
		t.Errorf("toolCalls = %d, want 1", rep.ToolCalls)
	}

	// A ref NO tool returned is rejected as ungrounded — the anti-fabrication guarantee.
	prov2 := dgx.NewScriptedProvider("s").
		AddToolCall("c1", "get_silence_ledger", "{}").
		AddText(`{"proposals":[{"kind":"causal_hypothesis","subject":"x","relation":"co-occurrence","evidence":["silence:never_returned"],"rationale":"x"}]}`)
	ag2 := dgx.New(prov2, dgx.DefaultParams)
	ag2.SetTools(reg)
	cands2, _, _ := ag2.Propose(context.Background(), loopContext())
	if len(cands2) != 0 {
		t.Errorf("a ref no tool returned must be rejected, got %d accepted", len(cands2))
	}
}

// A tools-unsupported provider degrades gracefully to the single-shot path over the SEED
// context (no crash, a stated note).
func TestLoopFallbackOnUnsupported(t *testing.T) {
	prov := dgx.NewStaticProvider("s",
		`{"proposals":[{"kind":"causal_hypothesis","subject":"seed-hyp","relation":"co-occurrence","evidence":["seed:1"],"rationale":"x"}]}`).
		WithToolsUnsupported()
	ag := dgx.New(prov, dgx.DefaultParams)
	ag.SetTools(dgx.NewToolRegistry(fakeTool{name: "t"}))
	cands, rep, err := ag.Propose(context.Background(), loopContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("fallback should propose from the seed context, got %d", len(cands))
	}
	if !strings.Contains(rep.Note, "single-shot") {
		t.Errorf("note = %q, want the single-shot fallback note", rep.Note)
	}
}

// A tool error becomes a TOOL ERROR turn — never a crash — and adds NO ref, so a model that
// then cites an invented ref is still rejected.
func TestLoopToolErrorNoFabrication(t *testing.T) {
	reg := dgx.NewToolRegistry(fakeTool{name: "get_strays", err: errors.New("store down")})
	prov := dgx.NewScriptedProvider("s").
		AddToolCall("c1", "get_strays", "{}").
		AddText(`{"proposals":[{"kind":"causal_hypothesis","subject":"x","relation":"co-occurrence","evidence":["stray:phantom"],"rationale":"x"}]}`)
	ag := dgx.New(prov, dgx.DefaultParams)
	ag.SetTools(reg)
	cands, _, err := ag.Propose(context.Background(), loopContext())
	if err != nil {
		t.Fatalf("a tool error must not crash the loop: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("a failed tool must not let an invented ref through, got %d", len(cands))
	}
}

// The loop terminates even if the model never stops calling tools (hard-bounded by
// MaxToolIterations) — no infinite loop, no error.
func TestLoopBounded(t *testing.T) {
	reg := dgx.NewToolRegistry(fakeTool{name: "get_strays", obs: []dgx.Observation{{Ref: "stray:a/1", Kind: "stray-metric", Detail: "a"}}})
	prov := dgx.NewScriptedProvider("s")
	for i := 0; i < 20; i++ {
		prov.AddToolCall("c", "get_strays", "{}") // never a text turn
	}
	ag := dgx.New(prov, dgx.Params{MinEvidence: 1, MaxProposals: 20, MaxContextChars: 6000, MaxToolIterations: 3, MaxToolResultChars: 500})
	ag.SetTools(reg)
	cands, rep, err := ag.Propose(context.Background(), loopContext())
	if err != nil {
		t.Fatalf("bounded loop must not error: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("no proposals turn ⇒ accept 0, got %d", len(cands))
	}
	if rep.Iterations > 3 {
		t.Errorf("iterations = %d, exceeded the MaxToolIterations bound", rep.Iterations)
	}
}
