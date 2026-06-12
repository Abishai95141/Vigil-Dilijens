package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// Providers supply the handlers with the current runtime state. They are called
// per request and must be cheap + race-free (cmd/obsd publishes immutable
// snapshots via atomics).
type Providers struct {
	// Coverage returns the current Coverage Report view (never nil).
	Coverage func() *CoverageView
	// Findings returns the persisted findings feed (may be nil/empty).
	Findings func(limit int) ([]store.FindingRow, error)
	// Unexplained returns the current unexplained-channel snapshot (doc 08);
	// may be nil when the channel is not running.
	Unexplained func() *UnexplainedView
	// Insights returns the current "now" surface snapshot (doc 10 M2); may be nil.
	Insights func() *InsightsView
	// Topology returns the current topology surface snapshot (doc 10 M3); may be nil.
	Topology func() *TopologyView
	// Timeline returns the composed anomaly timeline (doc 10 M4); may be nil.
	Timeline func() (*TimelineView, error)
	// Warnings returns the early-warning surface snapshot (doc 10 M5 / 09 M4);
	// may be nil — the handler then serves the honest OFF state (gate rule).
	Warnings func() *WarningsView
}

// UnexplainedView is the unexplained-channel surface (doc 08 §3.7, doc 10): the
// open loud-but-unmatched cards, the candidate-phenomenon reports for curation,
// and the channel's OWN residual blind-spot notice (§3.2) — reflexive honesty,
// surfaced, never implied away.
type UnexplainedView struct {
	GeneratedAt  time.Time                     `json:"generatedAt"`
	GraphVersion string                        `json:"graphVersion"`
	OpenCards    []unexplained.Finding         `json:"openCards"`
	Candidates   []unexplained.CandidateReport `json:"candidates"`
	BlindSpot    string                        `json:"blindSpot"`
}

// Register mounts the surfacing routes on mux under /api.
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

	mux.HandleFunc("/api/unexplained", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *UnexplainedView
		if p.Unexplained != nil {
			v = p.Unexplained()
		}
		if v == nil {
			// The channel is not running — say so honestly, with the blind-spot
			// notice still surfaced (it is a static property, not a runtime one).
			v = &UnexplainedView{
				GeneratedAt: timeNowUTC(),
				OpenCards:   []unexplained.Finding{},
				Candidates:  []unexplained.CandidateReport{},
				BlindSpot:   unexplained.BlindSpotNotice,
			}
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/insights", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *InsightsView
		if p.Insights != nil {
			v = p.Insights()
		}
		if v == nil {
			v = &InsightsView{GeneratedAt: timeNowUTC(), Findings: []InsightCard{}, Cascades: []CascadeCard{}}
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/topology", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *TopologyView
		if p.Topology != nil {
			v = p.Topology()
		}
		if v == nil {
			v = &TopologyView{GeneratedAt: timeNowUTC(), Nodes: []TopoNode{}, Edges: []TopoEdge{}}
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/warnings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *WarningsView
		if p.Warnings != nil {
			v = p.Warnings()
		}
		if v == nil {
			// Forecasting is not running: the lane states WHY it is dark
			// (the gate rule) rather than implying a quiet cluster.
			v = &WarningsView{
				Class: "PROJECTED", GeneratedAt: timeNowUTC(),
				Enabled: false, GateNote: gateNote,
				Warnings: []WarningCard{}, Silences: []SilenceRow{},
			}
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/timeline", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if p.Timeline == nil {
			writeJSON(w, &TimelineView{GeneratedAt: timeNowUTC(), Matches: []TimelineSpan{}, Unexplained: []TimelineSpan{}, Projected: []TimelineSpan{}, ProjectedNote: projectedLaneNote})
			return
		}
		v, err := p.Timeline()
		if err != nil {
			http.Error(w, "timeline unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, v)
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
