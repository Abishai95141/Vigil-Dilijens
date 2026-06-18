package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The /api/candidates route serves the provider's view (doc 20 P0.b).
func TestCandidatesRouteServesView(t *testing.T) {
	view := &CandidatesView{
		Class: CandidateClass, Available: true, GeneratedAt: at,
		Counts:     map[string]int{"candidate": 1},
		Candidates: []CandidateRow{{ID: "cand:x", Kind: "node", Status: "candidate", Subject: "stray:foo"}},
		Note:       "n",
	}
	mux := http.NewServeMux()
	Register(mux, Providers{Candidates: func() *CandidatesView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/candidates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got CandidatesView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Candidates) != 1 || got.Candidates[0].ID != "cand:x" {
		t.Errorf("served view wrong: %+v", got)
	}
}

// With no provider, the route serves the honest OFF state, never a misleading empty
// "active" store.
func TestCandidatesRouteOffStateHonest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{}) // no Candidates provider
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/candidates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got CandidatesView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false (the honest OFF state)")
	}
	if got.Class != CandidateClass {
		t.Errorf("off-state still labels the class: %q", got.Class)
	}
}
