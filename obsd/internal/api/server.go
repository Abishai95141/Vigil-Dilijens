package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// Providers supply the handlers with the current runtime state. They are called
// per request and must be cheap + race-free (cmd/obsd publishes immutable
// snapshots via atomics).
type Providers struct {
	// Coverage returns the current Coverage Report view (never nil).
	Coverage func() *CoverageView
	// Findings returns the persisted findings feed (may be nil/empty).
	Findings func(limit int) ([]store.FindingRow, error)
}

// Register mounts the M1 surfacing routes on mux under /api.
func Register(mux *http.ServeMux, p Providers) {
	mux.HandleFunc("/api/coverage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, p.Coverage())
	})

	mux.HandleFunc("/api/findings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if p.Findings == nil {
			writeJSON(w, findingsResponse{Findings: []store.FindingRow{}})
			return
		}
		rows, err := p.Findings(200)
		if err != nil {
			http.Error(w, "findings unavailable", http.StatusServiceUnavailable)
			return
		}
		if rows == nil {
			rows = []store.FindingRow{}
		}
		writeJSON(w, findingsResponse{Findings: rows, GeneratedAt: timeNowUTC()})
	})
}

type findingsResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Findings    []store.FindingRow `json:"findings"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "encoding error", http.StatusInternalServerError)
	}
}

// timeNowUTC is indirected so handlers stay testable without a global clock in
// logic (the no-time.Now-in-logic rule is for deterministic paths; this surfacing
// stamp is display-only).
var timeNowUTC = func() time.Time { return time.Now().UTC() }
