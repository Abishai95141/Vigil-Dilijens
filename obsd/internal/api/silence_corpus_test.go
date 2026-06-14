package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// silenceLedgerCorpus is the frozen gate input: two independent ledger builds (the
// determinism floor) plus the compiler's own per-rule aggregates (the reconciliation
// floor). Source is stated honestly — it is a real binding.Compile over the released
// ontology with a SYNTHETIC inventory, not a live-cluster capture.
type silenceLedgerCorpus struct {
	Source  string             `json:"source"`
	LedgerA *SilenceLedgerView `json:"ledgerA"`
	LedgerB *SilenceLedgerView `json:"ledgerB"`
	PerRule perRuleAggregates  `json:"perRule"`
}

type perRuleAggregates struct {
	TotalBindings int `json:"totalBindings"`
	Instantiated  int `json:"instantiated"`
	ConfigBound   int `json:"configBound"`
	DefaultBound  int `json:"defaultBound"`
	Unbounded     int `json:"unbounded"`
	OutOfScope    int `json:"outOfScope"`
	Unresolved    int `json:"unresolved"`
}

// TestRegenMCPSilenceCorpus materializes corpus/mcp/silence-ledger.json. Run with
// REGEN_MCP_CORPUS=1; otherwise skipped (mirrors the REGEN_FIXTURE pattern).
func TestRegenMCPSilenceCorpus(t *testing.T) {
	if os.Getenv("REGEN_MCP_CORPUS") == "" {
		t.Skip("set REGEN_MCP_CORPUS=1 to regenerate corpus/mcp/silence-ledger.json")
	}
	res := silCompile(t)
	c := silenceLedgerCorpus{
		Source:  "synthetic-fixture: real binding.Compile over the released ontology, SYNTHETIC boutique inventory (not a live-cluster capture). The gate's claims (determinism, completeness, reconciliation) are fully deterministic and do not depend on cluster realism.",
		LedgerA: BuildSilenceLedger("gv-corpus", "v-corpus", silAt, res),
		LedgerB: BuildSilenceLedger("gv-corpus", "v-corpus", silAt, res),
	}
	for _, rc := range res.Coverage.PerRule {
		c.PerRule.Instantiated += rc.Instantiated
		c.PerRule.ConfigBound += rc.ConfigBound
		c.PerRule.DefaultBound += rc.DefaultBound
		c.PerRule.Unbounded += rc.Unbounded
		c.PerRule.OutOfScope += rc.OutOfScope
		c.PerRule.Unresolved += rc.Unresolved
	}
	c.PerRule.TotalBindings = len(res.Bindings)

	path := filepath.Join("..", "..", "..", "corpus", "mcp", "silence-ledger.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bindings, %d silent)", path, c.PerRule.TotalBindings, c.LedgerA.Summary.Silent)
}
