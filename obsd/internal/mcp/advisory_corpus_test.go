package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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

func advisoryCorpusPath() string {
	return filepath.Join("..", "..", "..", "corpus", "mcp", "advisory-drafts.jsonl")
}

// produceAdvisoryRows folds every honeypot + clean draft through the REAL guard
// (ValidateAdvisory, gate UNPASSED — the shipping posture), DETERMINISTICALLY: the
// registers are sorted so the row order is stable. (The prior map-iteration order made
// regeneration non-deterministic — audit finding D — so the committed corpus could drift
// on a re-run and the drift guard below would have flaked. Sorting fixes the root cause.)
func produceAdvisoryRows() []advisoryDraft {
	registers := make([]string, 0, len(bannedByRegister))
	for r := range bannedByRegister {
		registers = append(registers, r)
	}
	sort.Strings(registers)

	var rows []advisoryDraft
	for _, register := range registers {
		for _, d := range bannedByRegister[register] {
			res := ValidateAdvisory(d, false)
			rows = append(rows, advisoryDraft{Text: d, Label: "banned", Register: register, Refused: res.Refused, Withheld: res.Withheld, TextEmitted: res.Text})
		}
	}
	for _, d := range cleanDrafts {
		res := ValidateAdvisory(d, false)
		rows = append(rows, advisoryDraft{Text: d, Label: "clean", Refused: res.Refused, Withheld: res.Withheld, TextEmitted: res.Text})
	}
	return rows
}

func marshalAdvisoryRows(t *testing.T, rows []advisoryDraft) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// TestRegenMCPAdvisoryCorpus materializes corpus/mcp/advisory-drafts.jsonl. Run with
// REGEN_MCP_CORPUS=1; otherwise skipped.
func TestRegenMCPAdvisoryCorpus(t *testing.T) {
	if os.Getenv("REGEN_MCP_CORPUS") == "" {
		t.Skip("set REGEN_MCP_CORPUS=1 to regenerate corpus/mcp/advisory-drafts.jsonl")
	}
	path := advisoryCorpusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	rows := produceAdvisoryRows()
	if err := os.WriteFile(path, marshalAdvisoryRows(t, rows), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d drafts)", path, len(rows))
}

// TestMCPAdvisoryCorpusFrozenConsistent is the always-on drift guard (audit roadmap #4):
// the committed advisory corpus must equal a fresh, DETERMINISTIC producer run — catching
// both a guard change AND the non-determinism that previously let regen silently reorder
// rows (audit finding D). FAILS (never skips) if the committed corpus is missing.
func TestMCPAdvisoryCorpusFrozenConsistent(t *testing.T) {
	committed, err := os.ReadFile(advisoryCorpusPath())
	if err != nil {
		t.Fatalf("committed advisory corpus missing — regenerate with REGEN_MCP_CORPUS=1: %v", err)
	}
	fresh := marshalAdvisoryRows(t, produceAdvisoryRows())
	if !bytes.Equal(committed, fresh) {
		t.Errorf("DRIFT: corpus/mcp/advisory-drafts.jsonl differs from a fresh producer run (run REGEN_MCP_CORPUS=1 to update)")
	}
}
