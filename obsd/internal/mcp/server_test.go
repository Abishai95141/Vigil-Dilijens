package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
)

// testSources builds a Sources whose payloads carry distinctive classed fields so
// the round-trip tests can prove class integrity is preserved across the MCP wire.
func testSources() Sources {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	return Sources{
		Coverage: func() *api.CoverageView {
			return &api.CoverageView{GraphVersion: "gv", Available: true, GeneratedAt: now}
		},
		SilenceLedger: func() *api.SilenceLedgerView {
			return &api.SilenceLedgerView{
				Class: "MEASURED", Available: true, GeneratedAt: now,
				Summary: api.SilenceLedgerSummary{TotalPairs: 3, Watched: 1, Silent: 2, ByReason: map[string]int{api.SilenceNoStreamKey: 1, api.SilenceUnbounded: 1}},
				Silent: []api.SilenceLedgerRow{
					{EntityCEI: "pvc|shop|data-0", Entity: "PVC", RuleID: "THR_PVC", Metric: "kubelet_volume_stats_used_bytes", State: "bound", ReasonClass: api.SilenceNoStreamKey, Reason: "no stream key"},
				},
				Note: "deterministic absence",
			}
		},
		Warnings: func() *api.WarningsView {
			return &api.WarningsView{
				Class: "PROJECTED", Enabled: true, GeneratedAt: now,
				Warnings: []api.WarningCard{{
					Class: "PROJECTED", IsProjection: true, EntityCEI: "i|c||Pod|p|u", Metric: "working_set",
					EarliestAt: now.Add(5 * time.Minute), LatestAt: now.Add(11 * time.Minute), LatestBeyondHorizon: true,
				}},
				Silences: []api.SilenceRow{},
			}
		},
		Incidents: func() *api.IncidentsView {
			return &api.IncidentsView{
				Class: "MEASURED", Available: true, GeneratedAt: now,
				Summary:   api.IncidentsSummary{Total: 1, Recurring: 1},
				Incidents: []api.IncidentCard{{Phenomenon: "PHEN_MEMORY_LEAK", RecurrenceCount: 3, Summary: "seen 3 times"}},
			}
		},
		Events: func() *api.EventsView {
			return &api.EventsView{
				Class: "MEASURED", Available: true, GeneratedAt: now,
				Summary: api.EventsSummary{Total: 1, Corroborated: 1},
				Events: []api.EventCard{{
					Reason: "OOMKilled", Class: "MEASURED", Corroborates: "PHEN_OOM_KILL_CGROUP",
					Corroborated: true, CorroborationWhy: "co-occurs with the cgroup-OOM phenomenon on the same role",
				}},
			}
		},
		// --- the v3.1 synthesis relay: each carries a distinctive classed marker so the
		// round-trip tests prove the class + payload survive the MCP wire unchanged.
		Insights: func() *api.InsightsView {
			return &api.InsightsView{GraphVersion: "insights-gv-marker", GeneratedAt: now, Findings: []api.InsightCard{}, Cascades: []api.CascadeCard{}}
		},
		RootCauseChain: func() *api.RootCauseChainView {
			return &api.RootCauseChainView{Class: "MEASURED ⋈ AUTHORED (joined, never fused)", Enabled: true, Active: true, GeneratedAt: now, Note: "a transitive dependency chain"}
		},
		CrossService: func() *api.CrossServiceView {
			return &api.CrossServiceView{Class: "MEASURED ⋈ AUTHORED (joined, never fused)", Enabled: true, Active: true, GeneratedAt: now, Note: "a cross-service cascade"}
		},
		Topology: func() *api.TopologyView {
			return &api.TopologyView{GraphVersion: "topo-gv-marker", GeneratedAt: now, Nodes: []api.TopoNode{}, Edges: []api.TopoEdge{}}
		},
		Unexplained: func() *api.UnexplainedView {
			return &api.UnexplainedView{GraphVersion: "gv", GeneratedAt: now, BlindSpot: "blind-spot-marker"}
		},
		Departures: func() *api.DepartureView {
			return &api.DepartureView{Class: "PROJECTED band ⋈ MEASURED sample (joined, never fused)", Enabled: true, GeneratedAt: now, Note: "band-departure"}
		},
		AuthoredRelations: func() *api.AuthoredRelationsView {
			return api.BuildAuthoredRelations(
				map[string][]string{"PHEN_MEMORY_LEAK": {"memory leak"}, "PHEN_OOM_KILL_CGROUP": {"oom kill"}},
				[]api.AuthoredLink{{Src: "PHEN_MEMORY_LEAK", Dst: "PHEN_OOM_KILL_CGROUP", Why: "Eventual outcome"}},
				now)
		},
		Referee: func(claim string) api.ClaimVerdict {
			return api.ValidateClaim(claim, api.ClaimContext{
				Phenomena:     map[string][]string{"PHEN_MEMORY_LEAK": {"memory leak"}, "PHEN_OOM_KILL_CGROUP": {"oom kill"}},
				AuthoredLinks: []api.AuthoredLink{{Src: "PHEN_MEMORY_LEAK", Dst: "PHEN_OOM_KILL_CGROUP", Why: "Eventual outcome"}},
			}, api.ClaimOpts{})
		},
		// --- operator-parity eyes (doc 23): each carries a distinctive marker so the
		// round-trip tests prove the payload + class survive the MCP wire unchanged.
		TraceGraph: func() *api.TraceGraphView {
			return &api.TraceGraphView{Class: "MEASURED", Available: true, GeneratedAt: now, Note: "trace-graph-marker"}
		},
		Timeline: func() *api.TimelineView {
			return &api.TimelineView{GeneratedAt: now, ProjectedNote: "timeline-marker"}
		},
		Onsets: func() *api.OnsetView {
			return &api.OnsetView{Class: "MEASURED", Enabled: true, GeneratedAt: now, Note: "onset-marker"}
		},
		CausalHypotheses: func() *api.CausalHypothesesView {
			return &api.CausalHypothesesView{Class: "DIRECTION-FREE", Enabled: true, GeneratedAt: now, Note: "cohyp-marker"}
		},
		Config: func() *api.ConfigView {
			return &api.ConfigView{GeneratedAt: now, ClusterID: "cl", Note: "config-marker"}
		},
		Candidates: func() *api.CandidatesView {
			return &api.CandidatesView{Class: "CANDIDATE", Available: true, GeneratedAt: now, Note: "candidate-marker"}
		},
		ProvisionalCoverage: func() *api.ProvisionalCoverageView {
			return &api.ProvisionalCoverageView{Class: "CANDIDATE", Available: true, GeneratedAt: now, Note: "provcov-marker"}
		},
		Governance: func() *api.GovernanceView {
			return &api.GovernanceView{Class: "MEASURED", Available: true, GeneratedAt: now, Note: "governance-marker"}
		},
		Findings: func() *api.FindingsView {
			return &api.FindingsView{Class: "MEASURED", Available: true, GeneratedAt: now}
		},
	}
}

func call(t *testing.T, s *Server, method string, params string) rpcResp {
	t.Helper()
	frame := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		frame += `,"params":` + params
	}
	frame += `}`
	out, isNote := s.Handle([]byte(frame))
	if isNote {
		t.Fatalf("method %s unexpectedly treated as a notification", method)
	}
	var resp rpcResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response for %s: %v (raw=%s)", method, err, out)
	}
	return resp
}

// toolText extracts the single text content of a tools/call result.
func toolText(t *testing.T, resp rpcResp) (string, bool) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	var tr toolResult
	if err := json.Unmarshal(resp.Result, &tr); err != nil {
		t.Fatalf("unmarshal toolResult: %v", err)
	}
	if len(tr.Content) != 1 || tr.Content[0].Type != "text" {
		t.Fatalf("want one text content, got %+v", tr.Content)
	}
	return tr.Content[0].Text, tr.IsError
}

func TestInitializeHandshake(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "initialize", "")
	var r struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	if r.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", r.ProtocolVersion, protocolVersion)
	}
	if _, ok := r.Capabilities["tools"]; !ok {
		t.Error("capabilities must advertise tools")
	}
}

func TestToolsListAdvertisesClassedTools(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/list", "")
	var r struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{toolCoverage: false, toolSilenceLedger: false, toolWarnings: false, toolIncidents: false, toolEmitAdvisory: false}
	for _, tl := range r.Tools {
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
		if tl.Name == toolSilenceLedger && !strings.Contains(tl.Description, "ABSENCE") {
			t.Error("silence ledger tool must describe deterministic ABSENCE")
		}
		if tl.Name == toolIncidents && !strings.Contains(tl.Description, "MEASURED") {
			t.Error("incidents tool must label itself MEASURED")
		}
		if tl.Name == toolWarnings && !strings.Contains(tl.Description, "PROJECTED") {
			t.Error("warnings tool must label itself PROJECTED")
		}
		if len(tl.InputSchema) == 0 {
			t.Errorf("tool %s missing inputSchema", tl.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tools/list missing %s", name)
		}
	}
}

// TestSynthesisRelayRoundTripsClassed proves each new relay tool returns its
// already-classed payload verbatim across the MCP wire (the class/marker survives).
func TestSynthesisRelayRoundTripsClassed(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	cases := []struct{ tool, want string }{
		{toolRootCauseChain, "MEASURED ⋈ AUTHORED"},
		{toolCrossService, "MEASURED ⋈ AUTHORED"},
		{toolDepartures, "PROJECTED band"},
		{toolInsights, "insights-gv-marker"},
		{toolTopology, "topo-gv-marker"},
		{toolUnexplained, "blind-spot-marker"},
		{toolAuthoredRels, "AUTHORED"},
		{toolAuthoredRels, "PHEN_MEMORY_LEAK"},
	}
	for _, c := range cases {
		resp := call(t, s, "tools/call", `{"name":"`+c.tool+`"}`)
		text, isErr := toolText(t, resp)
		if isErr {
			t.Errorf("%s returned isError on a live source", c.tool)
		}
		if !strings.Contains(text, c.want) {
			t.Errorf("%s did not preserve %q across the wire; got %s", c.tool, c.want, text)
		}
	}
}

// TestToolsListAdvertisesSynthesisTools asserts the synthesis tools are advertised
// with class-correct descriptions so the agent picks the right grounding.
func TestToolsListAdvertisesSynthesisTools(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/list", "")
	var r struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	byName := map[string]toolDef{}
	for _, tl := range r.Tools {
		byName[tl.Name] = tl
	}
	for _, name := range []string{toolInsights, toolRootCauseChain, toolCrossService, toolTopology, toolUnexplained, toolDepartures, toolAuthoredRels} {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("tools/list missing synthesis tool %s", name)
			continue
		}
		if len(tl.InputSchema) == 0 {
			t.Errorf("%s missing inputSchema", name)
		}
	}
	// class labels must be correct: the chain/cross-service carry the join, topology
	// is MEASURED, departures are PROJECTED.
	if !strings.Contains(byName[toolRootCauseChain].Description, "AUTHORED") {
		t.Error("root-cause-chain tool must name its AUTHORED orientation")
	}
	if !strings.Contains(byName[toolTopology].Description, "MEASURED") {
		t.Error("topology tool must label itself MEASURED")
	}
	if !strings.Contains(byName[toolDepartures].Description, "PROJECTED") {
		t.Error("departures tool must label itself PROJECTED")
	}
	// the referee must be framed as a never-blocking labeler, not a gate.
	if !strings.Contains(byName[toolValidateClaim].Description, "NEVER") {
		t.Error("validate_claim must state it NEVER blocks")
	}
}

// TestSynthesisLaneOffIsHonest proves a nil synthesis source is surfaced as an honest
// "not available" text result (isError=false), never an error or an implied-empty fact.
func TestSynthesisLaneOffIsHonest(t *testing.T) {
	s := New(Sources{}, false, "vigil-test", "v3") // every source nil
	for _, name := range []string{toolRootCauseChain, toolInsights, toolCrossService, toolTopology, toolUnexplained, toolDepartures} {
		resp := call(t, s, "tools/call", `{"name":"`+name+`"}`)
		text, isErr := toolText(t, resp)
		if isErr {
			t.Errorf("%s off-lane must not be isError (an off lane is a true state)", name)
		}
		if !strings.Contains(text, "available") {
			t.Errorf("%s off-lane must state availability honestly; got %s", name, text)
		}
	}
}

// operatorParityTools is the full set of doc-23 eyes (every console surface the agent
// must also see). Kept here so the list/round-trip/off tests stay in lockstep.
var operatorParityTools = []string{
	toolTraceGraph, toolTimeline, toolOnsets, toolCausalHypotheses,
	toolFindings, toolConfig, toolCandidates, toolProvisionalCoverage, toolGovernance,
}

// TestOperatorParityToolsAdvertised proves all nine doc-23 tools are listed with an
// inputSchema and a class-honest description (the discipline the agent reads).
func TestOperatorParityToolsAdvertised(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/list", "")
	var r struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatal(err)
	}
	byName := map[string]toolDef{}
	for _, tl := range r.Tools {
		byName[tl.Name] = tl
	}
	for _, name := range operatorParityTools {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("tools/list missing operator-parity tool %s", name)
			continue
		}
		if len(tl.InputSchema) == 0 {
			t.Errorf("%s missing inputSchema", name)
		}
	}
	// The direction-free tool MUST frame itself as non-causal (the charter line C3 rides on).
	if d := byName[toolCausalHypotheses].Description; !strings.Contains(d, "DIRECTION-FREE") || !strings.Contains(d, "NEVER assign") {
		t.Errorf("causal-hypotheses tool must be framed direction-free + never-assign-direction; got %q", d)
	}
	// Candidates/provisional-coverage must NOT claim to be MEASURED/AUTHORED facts.
	if d := byName[toolCandidates].Description; !strings.Contains(d, "CANDIDATE") {
		t.Errorf("candidates tool must label status CANDIDATE; got %q", d)
	}
	if !strings.Contains(byName[toolTraceGraph].Description, "MEASURED") {
		t.Error("trace-graph tool must label itself MEASURED")
	}
}

// TestOperatorParityRoundTrips proves each new view survives the MCP wire with its
// distinctive marker intact (no class/payload loss at the boundary).
func TestOperatorParityRoundTrips(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	cases := []struct{ tool, want string }{
		{toolTraceGraph, "trace-graph-marker"},
		{toolTimeline, "timeline-marker"},
		{toolOnsets, "onset-marker"},
		{toolCausalHypotheses, "cohyp-marker"},
		{toolCausalHypotheses, "DIRECTION-FREE"},
		{toolConfig, "config-marker"},
		{toolCandidates, "candidate-marker"},
		{toolCandidates, "CANDIDATE"},
		{toolProvisionalCoverage, "provcov-marker"},
		{toolGovernance, "governance-marker"},
		{toolFindings, `"available":true`},
	}
	for _, c := range cases {
		resp := call(t, s, "tools/call", `{"name":"`+c.tool+`"}`)
		text, isErr := toolText(t, resp)
		if isErr {
			t.Errorf("%s returned isError on a live source", c.tool)
		}
		if !strings.Contains(text, c.want) {
			t.Errorf("%s did not preserve %q across the wire; got %s", c.tool, c.want, text)
		}
	}
}

// TestOperatorParityLaneOffIsHonest proves a nil source for each new tool is surfaced as
// an honest "not available" text result (isError=false) — an off lane is a true state.
func TestOperatorParityLaneOffIsHonest(t *testing.T) {
	s := New(Sources{}, false, "vigil-test", "v3") // every source nil
	for _, name := range operatorParityTools {
		resp := call(t, s, "tools/call", `{"name":"`+name+`"}`)
		text, isErr := toolText(t, resp)
		if isErr {
			t.Errorf("%s off-lane must not be isError (an off lane is a true state)", name)
		}
		if !strings.Contains(text, `"available":false`) {
			t.Errorf("%s off-lane must state unavailable, got %s", name, text)
		}
	}
}

func TestSilenceLedgerRoundTripsClassed(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/call", `{"name":"get_silence_ledger"}`)
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatal("silence ledger tool returned isError")
	}
	var v api.SilenceLedgerView
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ledger did not round-trip: %v", err)
	}
	if v.Class != "MEASURED" {
		t.Errorf("class lost across the wire: %q", v.Class)
	}
	if v.Summary.TotalPairs != 3 || v.Summary.Silent != 2 {
		t.Errorf("summary corrupted: %+v", v.Summary)
	}
}

func TestIncidentsRoundTripsClassed(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/call", `{"name":"get_incidents"}`)
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatal("incidents tool returned isError")
	}
	var v api.IncidentsView
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("incidents did not round-trip: %v", err)
	}
	if v.Class != "MEASURED" || v.Summary.Recurring != 1 {
		t.Errorf("incidents corrupted across the wire: class %q recurring %d", v.Class, v.Summary.Recurring)
	}
}

func TestValidateClaimToolRoundTrips(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	// A relation-absent fabrication (oom kill → memory leak is not authored) must flag.
	resp := call(t, s, "tools/call", `{"name":"validate_claim","arguments":{"claim":"the oom kill caused the memory leak"}}`)
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatal("validate_claim returned isError on a normal claim")
	}
	var v api.ClaimVerdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("verdict did not round-trip: %v", err)
	}
	if !v.Flagged || !v.LabelledBestEffort {
		t.Errorf("a relation-absent causal claim must be flagged + labelled best-effort: %+v", v)
	}
}

func TestEventsRoundTripsClassed(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/call", `{"name":"get_events"}`)
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatal("events tool returned isError")
	}
	var v api.EventsView
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("events did not round-trip: %v", err)
	}
	if v.Class != "MEASURED" || v.Summary.Corroborated != 1 || !v.Events[0].Corroborated {
		t.Errorf("events corrupted across the wire: %+v", v)
	}
}

// TestWarningsPreservesProjectionBand is the class-integrity floor: a PROJECTED
// payload must keep its isProjection mark and its non-collapsing band across MCP.
func TestWarningsPreservesProjectionBand(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	resp := call(t, s, "tools/call", `{"name":"get_warnings"}`)
	text, _ := toolText(t, resp)
	var v api.WarningsView
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	if v.Class != "PROJECTED" {
		t.Errorf("warnings class = %q, want PROJECTED", v.Class)
	}
	if len(v.Warnings) != 1 {
		t.Fatalf("want 1 warning, got %d", len(v.Warnings))
	}
	w := v.Warnings[0]
	if !w.IsProjection {
		t.Error("isProjection mark stripped across the wire")
	}
	if w.EarliestAt.Equal(w.LatestAt) || !w.LatestBeyondHorizon {
		t.Error("projection band collapsed/lost across the wire")
	}
}

func TestLaneOffIsHonestNotError(t *testing.T) {
	// A nil source func ⇒ the tool reports unavailable, NOT an rpc error.
	s := New(Sources{}, false, "vigil-test", "v3")
	resp := call(t, s, "tools/call", `{"name":"get_coverage"}`)
	text, isErr := toolText(t, resp)
	if isErr {
		t.Error("an off lane must not be an error (it is a true state)")
	}
	if !strings.Contains(text, `"available":false`) {
		t.Errorf("off lane must state unavailable, got %s", text)
	}
}

func TestNotificationGetsNoReply(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	out, isNote := s.Handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if !isNote {
		t.Fatal("a frame with no id must be a notification")
	}
	if out != nil {
		t.Fatalf("a notification must produce no reply, got %s", out)
	}
}

func TestMalformedAndUnknownMethod(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	out, _ := s.Handle([]byte(`{not json`))
	var r rpcResp
	_ = json.Unmarshal(out, &r)
	if r.Error == nil || r.Error.Code != codeParse {
		t.Errorf("malformed frame must return parse error, got %+v", r.Error)
	}
	resp := call(t, s, "does/not/exist", "")
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method must return method-not-found, got %+v", resp.Error)
	}
}

// TestDispatchDeterministic: the same request over the same snapshot yields a
// byte-identical response (the dispatcher is a pure function of frame + snapshot).
func TestDispatchDeterministic(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	frame := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_silence_ledger"}}`)
	a, _ := s.Handle(frame)
	b, _ := s.Handle(frame)
	if !bytes.Equal(a, b) {
		t.Fatal("MCP response is not deterministic across two identical calls")
	}
}

func TestHTTPTransport(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	h := s.HTTPHandler()

	// POST a request frame ⇒ 200 + result.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), toolSilenceLedger) {
		t.Error("tools/list over HTTP did not list the silence ledger")
	}

	// POST a notification ⇒ 204.
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("notification status = %d, want 204", rec.Code)
	}

	// GET ⇒ 405.
	req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}
