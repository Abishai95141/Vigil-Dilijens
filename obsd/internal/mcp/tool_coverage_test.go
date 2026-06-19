package mcp

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tool_coverage_test.go — the META-GATE that future-proofs the MCP tool surface. The
// existing tests check that NAMED tools are advertised and round-trip their class; none
// stopped a v5 dev from adding a `tool…` const that is never advertised or never handled,
// or from silently dropping a tool. This file closes that:
//
//   1. AST-discovers every `tool… = "…"` const declared in server.go (the source of truth).
//   2. Pins that set against wantMCPTools — add/remove a tool const and this fails until
//      the pin is consciously updated.
//   3. Asserts tools/list advertises EXACTLY the declared set (no missing, no extra).
//   4. Asserts every declared tool is actually DISPATCHED (tools/call never returns
//      "unknown tool") — a declared-but-unhandled tool cannot ship.

// wantMCPTools is the pinned set of MCP tool names. It is the human-acknowledged registry;
// the discovery + behavioural checks below tie it to reality in three directions.
var wantMCPTools = map[string]bool{
	"get_coverage":         true,
	"get_silence_ledger":   true,
	"get_warnings":         true,
	"get_incidents":        true,
	"get_events":           true,
	"get_insights":         true,
	"get_root_cause_chain": true,
	"get_cross_service":    true,
	"get_topology":         true,
	"get_unexplained":      true,
	"get_departures":       true,
	"get_blindspots":       true, // v5: coverage blind-spot registry (MEASURED), advertised + dispatched

	"get_authored_relations": true,
	"validate_claim":         true,
	"emit_advisory":          true,
}

// discoverDeclaredTools AST-parses server.go and returns the value of every const named
// `tool…` (e.g. toolCoverage = "get_coverage"). Reads the real declarations, so the guard
// tracks the source, not a copy.
func discoverDeclaredTools(t *testing.T) map[string]bool {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src := filepath.Join(filepath.Dir(thisFile), "server.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	tools := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
			return true
		}
		name := vs.Names[0].Name
		if !strings.HasPrefix(name, "tool") || len(name) < 5 || name[4] < 'A' || name[4] > 'Z' {
			return true
		}
		lit, ok := vs.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		tools[lit.Value[1:len(lit.Value)-1]] = true
		return true
	})
	if len(tools) == 0 {
		t.Fatal("discovered zero tool consts — the AST walk is broken (server.go moved?)")
	}
	return tools
}

// TestMCPToolRegistryComplete: the declared `tool…` consts must equal the pinned set —
// no tool added without acknowledgement, none removed without dropping the pin.
func TestMCPToolRegistryComplete(t *testing.T) {
	declared := discoverDeclaredTools(t)
	for name := range declared {
		if !wantMCPTools[name] {
			t.Errorf("server.go declares tool %q but it is not pinned in wantMCPTools — "+
				"a new MCP tool shipped unacknowledged. Add it to wantMCPTools AND ensure it is "+
				"advertised + dispatched + has a server_test round-trip.", name)
		}
	}
	for name := range wantMCPTools {
		if !declared[name] {
			t.Errorf("wantMCPTools pins %q but server.go no longer declares it — remove the stale pin.", name)
		}
	}
}

// TestMCPToolsAdvertisedExactly: tools/list advertises exactly the declared tool set.
func TestMCPToolsAdvertisedExactly(t *testing.T) {
	declared := discoverDeclaredTools(t)
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/list", "")
	var r struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	advertised := map[string]bool{}
	for _, tl := range r.Tools {
		advertised[tl.Name] = true
	}
	for name := range declared {
		if !advertised[name] {
			t.Errorf("tool %q is declared but tools/list does NOT advertise it (add it to toolDefs).", name)
		}
	}
	for name := range advertised {
		if !declared[name] {
			t.Errorf("tools/list advertises %q with no matching tool const — phantom advertisement.", name)
		}
	}
}

// TestMCPEveryToolIsDispatched: every declared tool is handled by callTool (it must not
// return the "unknown tool" error). Tools needing arguments may return a different
// invalid-params error (e.g. "requires a non-empty claim") — that still proves the tool
// is RECOGNISED, which is what this guards.
func TestMCPEveryToolIsDispatched(t *testing.T) {
	declared := discoverDeclaredTools(t)
	s := New(testSources(), true, "vigil-test", "v3") // advisory gate passed so emit_advisory dispatches
	for name := range declared {
		resp := call(t, s, "tools/call", `{"name":"`+name+`"}`)
		if resp.Error != nil && strings.Contains(resp.Error.Message, "unknown tool") {
			t.Errorf("tool %q is declared but callTool reports it as unknown — declared-but-unhandled.", name)
		}
	}
}
