package dgx_test

import (
	"context"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// A reasoning model routinely appends prose AFTER the JSON (and that prose may contain braces).
// The parser reads the FIRST JSON object and ignores the rest, so the proposal still lands —
// the exact failure mode observed live ("invalid character 'W' after top-level value").
func TestProposeToleratesTrailingProse(t *testing.T) {
	resp := "{\"proposals\":[{\"kind\":\"edge\",\"subject\":\"x\",\"relation\":\"associated-with\"," +
		"\"evidence\":[\"assoc:node|cpu~node|mem\"],\"rationale\":\"r\"}]}\n\n" +
		"With these proposals we map {cpu} onto {mem}. Hope this helps!"
	ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	cands, _, err := ag.Propose(context.Background(), testContext())
	if err != nil {
		t.Fatalf("trailing prose must not fail the parse: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("accepted %d, want 1 (the JSON before the prose)", len(cands))
	}
}

// Leading prose / a ```json fence before the object is also tolerated.
func TestProposeToleratesLeadingFence(t *testing.T) {
	resp := "```json\n{\"proposals\":[{\"kind\":\"edge\",\"subject\":\"x\",\"relation\":\"associated-with\"," +
		"\"evidence\":[\"assoc:node|cpu~node|mem\"],\"rationale\":\"r\"}]}\n```"
	ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	cands, _, err := ag.Propose(context.Background(), testContext())
	if err != nil {
		t.Fatalf("fenced JSON must parse: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("accepted %d, want 1", len(cands))
	}
}
