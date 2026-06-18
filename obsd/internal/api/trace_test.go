package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceGraphRouteServesView(t *testing.T) {
	view := NewTraceGraphView(at, 7, 1, 1, 3, false, []TraceEdgeRow{
		{Caller: "frontend", Callee: "checkout", Calls: 1, MaxMillis: 85},
		{Caller: "checkout", Callee: "currencyservice", Calls: 2, P50Millis: 17.5, MaxMillis: 20},
	})
	mux := http.NewServeMux()
	Register(mux, Providers{TraceGraph: func() *TraceGraphView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/trace-graph")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got TraceGraphView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Edges) != 2 || got.OrphanSpans != 1 || got.CandidatesStaged != 3 {
		t.Errorf("served view wrong: %+v", got)
	}
	if got.CensusComplete {
		t.Error("trace census is always incomplete (spans are sampled) — must serve censusComplete=false")
	}
}

func TestTraceGraphRouteOffStateHonest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{}) // no TraceGraph provider
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/trace-graph")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got TraceGraphView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false (the honest OFF state)")
	}
}
