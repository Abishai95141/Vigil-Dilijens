package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogTemplatesRouteServesView(t *testing.T) {
	view := NewLogTemplatesView(at, 3, 240, []LogTemplateRow{
		{Pattern: "GET /api/cart <*> <*>", Count: 42},
		{Pattern: "connection to <*> failed", Count: 5},
	})
	mux := http.NewServeMux()
	Register(mux, Providers{LogTemplates: func() *LogTemplatesView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/log-templates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got LogTemplatesView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Templates) != 2 || got.PodsSampled != 3 || got.LinesMined != 240 {
		t.Errorf("served view wrong: %+v", got)
	}
}

func TestLogTemplatesRouteOffStateHonest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{}) // no LogTemplates provider
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/log-templates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got LogTemplatesView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false (the honest OFF state)")
	}
}
