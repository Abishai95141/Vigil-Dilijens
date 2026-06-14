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
	// SilenceLedger returns the deterministic absence ledger (the MCP harness's
	// lead feature): every (entity,variable) pair watched-or-silent-with-reason.
	// Built from the SAME binding.Result as Coverage. nil ⇒ honest unavailable state.
	SilenceLedger func() *SilenceLedgerView
	// Incidents returns the durable cross-run incident memory (v3 T-B): phenomena
	// joined across time with recurrence counts. nil ⇒ the honest "not enabled" state.
	Incidents func() (*IncidentsView, error)
	// Findings returns the persisted findings feed (may be nil/empty).
	Findings func(limit int) ([]store.FindingRow, error)
	// FindingsStaleAfter is the freshness horizon for the findings feed (doc 14
	// A7 store is durable): a row whose last match is older than this is served
	// `stale` ("last seen Xago"), never as firing NOW. Zero ⇒ never stale.
	FindingsStaleAfter time.Duration
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
	// ContextWindows is the operator-defined context-window store (doc 10 M6,
	// begun); nil = the routes are not mounted.
	ContextWindows *ContextWindowStore
	// Chat returns the read-only snapshot the register-guarded chat answers from
	// (doc 10 M7, begun); nil = the route is not mounted.
	Chat func() *ChatSnapshot
	// Config returns the runtime configuration view (doc 10 M6) — cadences,
	// graph release, and the forecast lane's gate posture; nil = not mounted.
	Config func() *ConfigView
	// CrossService returns the v2 cross-service cascade surface (doc 15 phase F);
	// nil = the route serves the honest OFF state (flow discovery not running).
	CrossService func() *CrossServiceView
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

	mux.HandleFunc("/api/silence-ledger", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *SilenceLedgerView
		if p.SilenceLedger != nil {
			v = p.SilenceLedger()
		}
		if v == nil {
			// Binding has not compiled — say so honestly rather than implying a
			// fully-watched cluster.
			v = BuildSilenceLedger("", "", timeNowUTC(), nil)
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/incidents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if p.Incidents == nil {
			writeJSON(w, unavailableIncidents("", timeNowUTC()))
			return
		}
		v, err := p.Incidents()
		if err != nil {
			http.Error(w, "incidents unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, v)
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
		now := timeNowUTC()
		for i := range rows {
			rows[i].MarkFreshness(now, p.FindingsStaleAfter) // serve-time: stale vs firing-now
		}
		writeJSON(w, findingsResponse{Findings: rows, GeneratedAt: now})
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

	mux.HandleFunc("/api/cross-service", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var v *CrossServiceView
		if p.CrossService != nil {
			v = p.CrossService()
		}
		if v == nil {
			// Flow discovery is not running: state WHY the lane is dark (the gate
			// rule's honesty), never imply there is no cross-service dependency.
			v = BuildCrossService(nil, nil, false, false, timeNowUTC())
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("/api/timeline", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if p.Timeline == nil {
			writeJSON(w, &TimelineView{GeneratedAt: timeNowUTC(), Matches: []TimelineSpan{}, Unexplained: []TimelineSpan{}, Projected: []TimelineSpan{}, ProjectedNote: projectedLaneOffNote})
			return
		}
		v, err := p.Timeline()
		if err != nil {
			http.Error(w, "timeline unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, v)
	})

	// Context windows (doc 10 M6, begun): GET lists; POST defines an operator
	// window (which doubles as a Phase-3 splice point). Off the deterministic path.
	if p.ContextWindows != nil {
		mux.HandleFunc("/api/context-windows", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				list := p.ContextWindows.List()
				splice := 0
				for _, cw := range list {
					if cw.SpliceEligible {
						splice++
					}
				}
				writeJSON(w, &ContextWindowsView{
					GeneratedAt: timeNowUTC(), Windows: list, SpliceCount: splice,
					Note: "operator annotations (no provenance class); splice-eligible windows are Phase-3 forecasting context boundaries (doc 09 §3.4)",
				})
			case http.MethodPost:
				var cw ContextWindow
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&cw); err != nil {
					http.Error(w, "bad context window: "+err.Error(), http.StatusBadRequest)
					return
				}
				stored, err := p.ContextWindows.Add(cw, timeNowUTC())
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, stored)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}

	// Chat (doc 10 M7, begun): POST a question; the register-guarded responder
	// answers ONLY from the structured snapshot, refusing any draft that would
	// cross the charter.
	if p.Chat != nil {
		mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				Question string `json:"question"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
				http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, AnswerChat(req.Question, p.Chat()))
		})
	}

	// Config (doc 10 M6): the runtime configuration view — cadences, graph
	// release, and the forecast lane's gate posture. Read-only; not a
	// provenance-classed statement (system configuration, not a finding).
	if p.Config != nil {
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, p.Config())
		})
	}
}

// maxRequestBody caps POST bodies on the operator surface (context windows + chat) —
// these are small operator inputs; a large body is rejected rather than buffered.
const maxRequestBody = 64 * 1024

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
