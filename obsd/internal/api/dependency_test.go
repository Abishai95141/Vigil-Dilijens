package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDependencyRouteServesView(t *testing.T) {
	view := NewDependencyView(at, at.Add(-time.Hour), at,
		[]DependencyEdge{{A: "a", B: "b", Relation: "associated-with", Coefficient: 0.9, Overlap: 12}}, 2, 5)
	mux := http.NewServeMux()
	Register(mux, Providers{Dependency: func() *DependencyView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dependency")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got DependencyView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Edges) != 1 || got.Edges[0].Relation != "associated-with" {
		t.Errorf("served view wrong: %+v", got)
	}
	if got.StreamsConsidered != 2 || got.StreamsTotal != 5 {
		t.Errorf("honest coverage fields lost: %+v", got)
	}
}

func TestDependencyRouteOffStateHonest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{}) // no Dependency provider
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dependency")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got DependencyView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false (the honest OFF state)")
	}
}
