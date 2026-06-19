package dgx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sampleLedger() Ledger {
	return Ledger{
		Promoted: []LedgerEntry{{Kind: "equiv_group", Subject: "stray:redis_clients", Note: "redis clients gauge"}},
		Rejected: []LedgerEntry{{Kind: "edge", Subject: "a~>b", Reason: "ungrounded evidence ref: bar"}},
		Pending:  []LedgerEntry{{Kind: "node", Subject: "stray:foo"}},
	}
}

func TestLedgerRender(t *testing.T) {
	var b strings.Builder
	sampleLedger().render(&b, 6000)
	s := b.String()
	for _, want := range []string{
		"PRIOR PROPOSAL HISTORY", "PROMOTED", "stray:redis_clients", "redis clients gauge",
		"REJECTED", "ungrounded evidence ref: bar", "PENDING", "stray:foo",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ledger render missing %q in:\n%s", want, s)
		}
	}

	// An empty ledger renders nothing.
	var empty strings.Builder
	Ledger{}.render(&empty, 6000)
	if empty.Len() != 0 {
		t.Errorf("empty ledger should render nothing, got %q", empty.String())
	}

	// Byte-stable across two identical renders (determinism).
	var x, y strings.Builder
	sampleLedger().render(&x, 6000)
	sampleLedger().render(&y, 6000)
	if x.String() != y.String() {
		t.Error("ledger render is not deterministic")
	}
}

// userPrompt embeds the ledger (memory) so the agent sees its own prior proposals.
func TestUserPromptIncludesLedger(t *testing.T) {
	c := Context{GraphVersion: "v", Observations: []Observation{{Ref: "silence:foo", Kind: "coverage-gap", Detail: "foo"}}}
	s := userPrompt(c, sampleLedger(), DefaultParams)
	if !strings.Contains(s, "PRIOR PROPOSAL HISTORY") || !strings.Contains(s, "a~>b") {
		t.Errorf("user prompt missing the ledger section:\n%s", s)
	}
}

// toolSystemSuffix lists the tools when present and is empty otherwise.
func TestToolSystemSuffix(t *testing.T) {
	if toolSystemSuffix(nil) != "" {
		t.Error("no tools ⇒ empty suffix")
	}
	reg := NewToolRegistry(toolSystemFake{})
	suf := toolSystemSuffix(reg)
	if !strings.Contains(suf, "TOOL USAGE") || !strings.Contains(suf, "zfake") {
		t.Errorf("suffix should name the tools: %q", suf)
	}
}

type toolSystemFake struct{}

func (toolSystemFake) Name() string                { return "zfake" }
func (toolSystemFake) Description() string         { return "d" }
func (toolSystemFake) Parameters() json.RawMessage { return nil }
func (toolSystemFake) Call(context.Context, json.RawMessage) ([]Observation, error) {
	return nil, nil
}
