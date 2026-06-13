package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The config route serves the runtime configuration, and — critically — states
// the forecast lane's gate posture honestly when the lane is off (the gate rule,
// doc 11 §3.5). It must be GET-only and carry no provenance class.
func TestConfigRoute(t *testing.T) {
	want := &ConfigView{
		GeneratedAt:  time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC),
		ClusterID:    "test-cluster",
		Profile:      "dev",
		GraphRelease: "v0.3.0",
		Forecast: ForecastConfigView{
			Enabled:  false,
			GateNote: ForecastGateNote(),
		},
	}
	mux := http.NewServeMux()
	Register(mux, Providers{Config: func() *ConfigView { return want }})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config = %d, want 200", rec.Code)
	}
	var got ConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ClusterID != "test-cluster" || got.GraphRelease != "v0.3.0" {
		t.Fatalf("config fields wrong: %+v", got)
	}
	if got.Forecast.Enabled {
		t.Fatal("forecast should be off in this fixture")
	}
	if got.Forecast.GateNote == "" {
		t.Fatal("a dark forecast lane MUST state WHY (the gate rule), got empty GateNote")
	}

	// POST is rejected — config is read-only.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/config = %d, want 405", rec.Code)
	}
}

// When no Config provider is wired, the route is simply absent (404) — not a
// crash, not an empty 200 implying "no config."
func TestConfigRouteAbsentWhenNotMounted(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmounted /api/config = %d, want 404", rec.Code)
	}
}
