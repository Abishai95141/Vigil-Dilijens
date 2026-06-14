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
