package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// advisoryDraft is one frozen gate row: the draft text, its declared label, and the
// REAL guard's verdict (produced by ValidateAdvisory with the gate UNPASSED — the
// shipping posture). The gate certifies recall==1.0 on banned + zero false-blocks on
// clean, and that no content leaks while refused/withheld.
type advisoryDraft struct {
	Text        string `json:"text"`
	Label       string `json:"label"` // "banned" | "clean"
	Register    string `json:"register,omitempty"`
	Refused     bool   `json:"refused"`
	Withheld    bool   `json:"withheld"`
	TextEmitted string `json:"textEmitted"` // must be "" for refused/withheld
}

// bannedByRegister groups honeypots so the corpus can prove per-register coverage.
var bannedByRegister = map[string][]string{
	"causal": {
		"the memory leak is caused by the new deploy",
		"this happened because of the noisy neighbour",
		"the node fell over due to the leak",
		"the root cause is the sidecar",
	},
	"future-certainty": {
		"working set will cross the limit in ten minutes",
		"the pod is going to OOM shortly",
		"a crossing is imminent and inevitably fatal",
	},
	"fusion": {
		"this is a measured forecast of the outage",
		"projected and confirmed: the bar was breached",
	},
}

// TestRegenMCPAdvisoryCorpus materializes corpus/mcp/advisory-drafts.jsonl. Run with
// REGEN_MCP_CORPUS=1; otherwise skipped.
func TestRegenMCPAdvisoryCorpus(t *testing.T) {
	if os.Getenv("REGEN_MCP_CORPUS") == "" {
		t.Skip("set REGEN_MCP_CORPUS=1 to regenerate corpus/mcp/advisory-drafts.jsonl")
	}
	var rows []advisoryDraft
	for register, drafts := range bannedByRegister {
		for _, d := range drafts {
			res := ValidateAdvisory(d, false)
			rows = append(rows, advisoryDraft{Text: d, Label: "banned", Register: register, Refused: res.Refused, Withheld: res.Withheld, TextEmitted: res.Text})
		}
	}
	for _, d := range cleanDrafts {
		res := ValidateAdvisory(d, false)
		rows = append(rows, advisoryDraft{Text: d, Label: "clean", Refused: res.Refused, Withheld: res.Withheld, TextEmitted: res.Text})
	}

	path := filepath.Join("..", "..", "..", "corpus", "mcp", "advisory-drafts.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote %s (%d drafts)", path, len(rows))
}
