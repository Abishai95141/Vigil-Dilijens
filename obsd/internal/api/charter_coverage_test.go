package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/departure"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
)

// charter_coverage_test.go — the META-GATE that future-proofs the charter sweep
// (doc 01 §5 / doc 11 M6). The piecemeal battery (charter_battery_test.go) hardcodes a
// handful of surfaces; nothing stopped a NEW /api endpoint from shipping operator-facing
// text that was never register-audited. This file closes that blind spot structurally:
//
//   1. It AST-parses server.go and discovers EVERY route Register() actually mounts.
//   2. It requires every discovered route to carry an explicit charter DISPOSITION here
//      — so a new endpoint cannot be added without a conscious classification.
//   3. For routes classed `dispSwept`, it RUNS the charter linter over a real payload
//      (teeth: you cannot claim "swept" without a payload the sweep actually scans).
//
// A v5 dev who adds `/api/new-thing` gets a failing test until they classify it — and if
// it emits authored/projected text, the only clean classification is `dispSwept`, which
// forces a payload through the register audit.

type charterDisposition int

const (
	// dispSwept: the surface carries AUTHORED notes and/or the PROJECTED band and is run
	// through CharterViolations in this file (chartableSurfaces). The strong path.
	dispSwept charterDisposition = iota
	// dispCheckedElsewhere: register-audited by a dedicated test (named in the reason),
	// because building its payload needs that test's fixtures (operator-input echoes).
	dispCheckedElsewhere
	// dispAuthoredVerbatim: surfaces AUTHORED graph text VERBATIM (the one place authored
	// causal language is correct and expected — it IS the curated note). Must NOT go
	// through the generic causal denylist, by design.
	dispAuthoredVerbatim
	// dispMeasuredStructured: MEASURED structured rows / enums only, no free authored
	// prose to breach a register (findings, coverage, events, incidents, config, …).
	dispMeasuredStructured
)

// apiRouteDisposition is the COMPLETE charter classification of every /api route. The
// completeness test below asserts this map's keys equal the routes Register() mounts —
// exactly, in both directions. Adding a route to server.go without a row here FAILS.
var apiRouteDisposition = map[string]charterDisposition{
	// authored / projected text → swept through the register audit here.
	"/api/insights":         dispSwept,
	"/api/topology":         dispSwept,
	"/api/warnings":         dispSwept,
	"/api/timeline":         dispSwept,
	"/api/cross-service":    dispSwept,
	"/api/root-cause-chain": dispSwept,
	"/api/departures":       dispSwept,
	// operator-input echoes / referee — register-guarded in dedicated tests.
	"/api/chat":           dispCheckedElsewhere, // chat_test.go (responder refuses banned registers)
	"/api/validate-claim": dispCheckedElsewhere, // validate_test.go (the referee never blocks, flags)
	"/api/unexplained":    dispCheckedElsewhere, // surfaces_test.go / unexplained causal-vocab audit
	// the AUTHORED graph, verbatim — causal language here is the curated note itself.
	"/api/authored-relations": dispAuthoredVerbatim,
	// MEASURED structured surfaces — no free authored prose.
	"/api/coverage":        dispMeasuredStructured,
	"/api/findings":        dispMeasuredStructured,
	"/api/events":          dispMeasuredStructured,
	"/api/incidents":       dispMeasuredStructured,
	"/api/silence-ledger":  dispMeasuredStructured,
	"/api/context-windows": dispMeasuredStructured,
	"/api/config":          dispMeasuredStructured,
	// v5 doc-20/21 dynamic-graph-extension + governance surfaces. MEASURED structured
	// rows/enums + system status notes only — no authored causal "why", no projected band
	// (the same bar as /api/findings, /api/coverage above; verified field-by-field).
	"/api/blindspots":           dispMeasuredStructured,
	"/api/provisional-coverage": dispMeasuredStructured,
	"/api/governance":           dispMeasuredStructured,
	"/api/dependency":           dispMeasuredStructured, // assoc "associated-with" edges — never causal
	"/api/log-templates":        dispMeasuredStructured, // MEASURED mined templates; the denylist would false-positive on quoted log text
	"/api/audit-changes":        dispMeasuredStructured, // MEASURED change records; hypotheses are counted here, staged to /api/candidates
	"/api/trace-graph":          dispMeasuredStructured, // observed call graph — MEASURED topology
	// the dgx lane's LLM-PROPOSED grounding text (CandidateRow.Reason) is generated prose,
	// so it is SWEPT through the register audit here (payload in chartableSurfaces).
	"/api/candidates": dispSwept,
	// a governance decision returns the human-promoted overlay YAML verbatim — authored
	// graph text, correct by design (like /api/authored-relations).
	"/api/governance/decide": dispAuthoredVerbatim,
}

// discoverRegisteredRoutes AST-parses server.go and returns every string literal passed
// as the first arg to a `mux.HandleFunc("/api/…", …)` call inside Register(). This reads
// the ACTUAL registration code, so the guard tracks reality, not a hand-copied list.
func discoverRegisteredRoutes(t *testing.T) map[string]bool {
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
	routes := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		// strip the surrounding quotes.
		path := lit.Value[1 : len(lit.Value)-1]
		if len(path) >= 5 && path[:5] == "/api/" {
			routes[path] = true
		}
		return true
	})
	if len(routes) == 0 {
		t.Fatal("discovered zero /api routes — the AST walk is broken (server.go moved?)")
	}
	return routes
}

// TestAPIRouteRegistryComplete is the COMPLETENESS teeth: the set of routes Register()
// mounts must equal the set classified in apiRouteDisposition — no unclassified route
// (a new endpoint shipped without a charter decision) and no stale entry (a row for a
// route that no longer exists).
func TestAPIRouteRegistryComplete(t *testing.T) {
	registered := discoverRegisteredRoutes(t)
	for route := range registered {
		if _, ok := apiRouteDisposition[route]; !ok {
			t.Errorf("route %q is registered in server.go but has NO charter disposition in "+
				"apiRouteDisposition (charter_coverage_test.go). Classify it: dispSwept if it "+
				"emits authored/projected text (and add it to chartableSurfaces), else "+
				"dispCheckedElsewhere / dispAuthoredVerbatim / dispMeasuredStructured with a reason.", route)
		}
	}
	for route := range apiRouteDisposition {
		if !registered[route] {
			t.Errorf("apiRouteDisposition lists %q but server.go no longer registers it — remove the stale row.", route)
		}
	}
}

// chartableSurfaces builds a real, populated payload for every `dispSwept` route and runs
// it through the register audit. It extends the 4-surface battery (builtSurfaces) with the
// three authored-text surfaces it never covered: cross-service, root-cause-chain, departures.
func chartableSurfaces(t *testing.T) map[string][]byte {
	t.Helper()
	out := builtSurfaces(t) // insights, topology, timeline, warnings

	// A MEASURED transitive chain carrying an AUTHORED "why" verbatim (the highest charter
	// risk on the flow lane — the join must never fuse it into a causal sentence).
	chain := flow.Chain{
		MostUpstreamDegradedNode: "shop/back",
		NodeClass:                "MEASURED (structural fan-in over observed-flow edges)",
		NodeBasis:                "no inbound flow-symptom edge",
		GeneratedAt:              at,
		Path: []flow.PathStep{{
			Hop: 1, Upstream: "shop/back", UpstreamPhenomenon: "PHEN_APP_QUEUE_SATURATION",
			Downstream: "shop/mid", DownstreamPhenomenon: "PHEN_APP_QUEUE_SATURATION",
			EdgeClass: "MEASURED observed flow", EdgeTraversal: "valid",
			Why: "Downstream dependency saturates the caller", WhyClass: "AUTHORED",
			Temporal: "T0+", Author: "vigil", Version: "v0.4.0",
		}},
	}
	deps := []departure.Departure{{
		EntityCEI: podKey, Metric: "container_memory_working_set_bytes", At: at,
		Class: "PROJECTED band ⋈ MEASURED sample (joined, never fused)", Side: "above",
		Realized: 304, Lower: 120, Upper: 290, BandWidth: 170, Exceedance: 14,
		Confidence: "wide-band", Detail: "realized sample left its projected band (above edge)",
	}}

	// A representative dgx-PROPOSED candidate carrying the lane's free-text grounding
	// (CandidateRow.Reason). The register audit must find no causal/fusion register in the
	// proposed prose — the lane GROUNDS (evidence + co-occurrence), it never authors a cause.
	candidates := NewCandidatesView(at, []CandidateRow{{
		ID: "cand-eqg-001", Kind: "equivalence_group_candidate", Status: "proposed",
		Subject: "container_memory_working_set_bytes ⋈ container_memory_rss",
		Source:  "dgx-agent", Method: "co-occurrence + name-affinity", GraphVersion: "v0.8.0",
		EvidenceCount: 7,
		Reason:        "co-occurs across 7 observed windows; staged for human review (PROPOSED, never authored)",
		CreatedAt:     at, UpdatedAt: at,
	}})

	extra := map[string]any{
		"cross-service":    BuildCrossService(&chain, nil, true, phaseECrossServiceGatePassedForTest, at),
		"root-cause-chain": BuildRootCauseChain([]flow.Chain{chain}, nil, true, false, at),
		"departures":       BuildDepartures(deps, true, true, at),
		"candidates":       candidates,
	}
	for name, v := range extra {
		raw := mustJSON(t, v)
		out[name] = raw
	}
	return out
}

// phaseECrossServiceGatePassedForTest mirrors the cmd/obsd gate const so the surface is
// populated (the measured chain is always shown; only the projected lane is gated).
const phaseECrossServiceGatePassedForTest = true

// TestChartableSurfacesAreClean runs the register audit over EVERY swept surface — now
// including cross-service, root-cause-chain, departures (previously never swept).
func TestChartableSurfacesAreClean(t *testing.T) {
	for name, payload := range chartableSurfaces(t) {
		if vs := CharterViolations(name, payload); len(vs) > 0 {
			for _, v := range vs {
				t.Errorf("CHARTER VIOLATION on %s: %s", name, v)
			}
		}
	}
}

// TestSweptDispositionMatchesSurfaces is the cross-check that keeps the disposition map
// HONEST: the set of routes marked dispSwept must be exactly the set chartableSurfaces
// builds a payload for. You cannot mark a route "swept" without a payload (a lie the
// register audit would never catch), nor build a payload for a route not marked swept.
func TestSweptDispositionMatchesSurfaces(t *testing.T) {
	built := chartableSurfaces(t)
	for route, disp := range apiRouteDisposition {
		surface := route[len("/api/"):]
		if disp == dispSwept {
			if _, ok := built[surface]; !ok {
				t.Errorf("route %q is dispSwept but chartableSurfaces builds no %q payload — "+
					"add it to chartableSurfaces (the sweep has no teeth on a surface it never scans).", route, surface)
			}
		}
	}
	for surface := range built {
		route := "/api/" + surface
		if apiRouteDisposition[route] != dispSwept {
			t.Errorf("chartableSurfaces builds %q but route %q is not marked dispSwept — "+
				"classify it dispSwept or drop the payload.", surface, route)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
