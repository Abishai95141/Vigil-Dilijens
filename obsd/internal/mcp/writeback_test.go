package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
)

// TestNoWriteBack proves a full MCP session — including emit_advisory — mutates
// nothing the server can reach. The Sources funcs return a shared snapshot pointer;
// after the session the snapshot's JSON must be byte-identical to before. (The
// structural guarantee is stronger — the package imports no writer — but this
// catches a behavioural regression too.)
func TestNoWriteBack(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	cov := &api.CoverageView{GraphVersion: "gv", Available: true, GeneratedAt: now, Summary: api.CoverageSummary{Entities: 7}}
	ledger := &api.SilenceLedgerView{Class: "MEASURED", Available: true, GeneratedAt: now,
		Summary: api.SilenceLedgerSummary{TotalPairs: 4, Watched: 2, Silent: 2, ByReason: map[string]int{api.SilenceNoStreamKey: 1, api.SilenceUnbounded: 1}}}
	calls := 0
	s := New(Sources{
		Coverage:      func() *api.CoverageView { calls++; return cov },
		SilenceLedger: func() *api.SilenceLedgerView { calls++; return ledger },
	}, true, "vigil-test", "v3")

	before, _ := json.Marshal(struct {
		C *api.CoverageView
		L *api.SilenceLedgerView
	}{cov, ledger})

	// A representative session that exercises every code path.
	for _, frame := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_coverage"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_silence_ledger"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"emit_advisory","arguments":{"text":"two pairs are silent per the ledger"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"emit_advisory","arguments":{"text":"the leak is caused by the deploy"}}}`,
	} {
		s.Handle([]byte(frame))
	}

	after, _ := json.Marshal(struct {
		C *api.CoverageView
		L *api.SilenceLedgerView
	}{cov, ledger})

	if calls == 0 {
		t.Fatal("expected the source funcs to have been read during the session")
	}
	if string(before) != string(after) {
		t.Fatal("MCP session mutated a source snapshot — no-write-back violated")
	}
}
