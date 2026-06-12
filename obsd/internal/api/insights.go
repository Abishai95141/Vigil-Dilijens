package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

// InsightsView is the "now" surface payload (doc 10 §3.1, M2): the phenomena
// currently matched — entity-local, first- or second-order, full or degraded —
// each with the full MEASURED evidence trail and its AUTHORED member/relation
// notes attached ADJACENTLY (never fused), plus the recognized cascade stories.
// This is a live snapshot, published per tick off the deterministic path; the
// TS type in web/src/surfaces/types.ts mirrors it.
type InsightsView struct {
	ClusterID    string         `json:"clusterId"`
	GraphVersion string         `json:"graphVersion"`
	GraphRelease string         `json:"graphRelease"`
	GeneratedAt  time.Time      `json:"generatedAt"`
	Summary      InsightSummary `json:"summary"`
	Findings     []InsightCard  `json:"findings"`
	Cascades     []CascadeCard  `json:"cascades"`
}

// InsightSummary is the headline rollup of the current findings.
type InsightSummary struct {
	Total       int `json:"total"`
	Full        int `json:"full"`
	Degraded    int `json:"degraded"`
	EntityLocal int `json:"entityLocal"`
	FirstOrder  int `json:"firstOrder"`
	SecondOrder int `json:"secondOrder"`
	Cascades    int `json:"cascades"`
	WithAtRisk  int `json:"withAtRisk"` // findings carrying a blast radius
}

// InsightCard is one matched phenomenon, rendered. MEASURED match (the states
// co-occurred in the authored pattern) with AUTHORED notes adjacent — the
// surface composes them side by side and labels each; it never paraphrases the
// authored note into a causal sentence (doc 10 §4).
type InsightCard struct {
	Phenomenon    string        `json:"phenomenon"`
	Label         string        `json:"label"`
	EntityCEI     string        `json:"entityCei"`
	Namespace     string        `json:"namespace"`
	Name          string        `json:"name"`
	Kind          string        `json:"kind"`
	Span          string        `json:"span"` // entity-local | first-order | second-order
	Quality       string        `json:"quality"`
	Completeness  float64       `json:"completeness"`
	RequiredMet   int           `json:"requiredMet"`
	RequiredTotal int           `json:"requiredTotal"`
	Members       []MemberRow   `json:"members"`      // the MEASURED evidence trail, each with its AUTHORED note
	Unobservable  []string      `json:"unobservable"` // required members that could not be checked here
	SpanPath      []EdgeStepRow `json:"spanPath"`     // the edges crossed, with validity verdicts
	SuspectEdges  []string      `json:"suspectEdges"` // edges whose staleness degrades the match
	BlastRadius   []AtRiskRow   `json:"blastRadius"`  // authored downstream relations made concrete (AUTHORED)
	GraphVersion  string        `json:"graphVersion"`
}

// MemberRow is one member's contribution — MEASURED state, with its bar
// provenance and the AUTHORED note kept as a labelled, separate field.
type MemberRow struct {
	Metric     string    `json:"metric"`
	Role       string    `json:"role"`     // required | supporting | corroborating
	Temporal   string    `json:"temporal"` // T0- | T0 | T0+...
	State      string    `json:"state"`
	SampleAt   time.Time `json:"sampleAt"`
	BarFlagged bool      `json:"barFlagged"` // a default-sourced bar (lower trust), surfaced
	Note       string    `json:"note"`       // AUTHORED member note (the only "why", attributed)
	Neighbour  string    `json:"neighbour"`  // cross-entity evidence (first/second-order): the CEI it crossed to
	Via        string    `json:"via"`        // edge type(s) crossed
	Hop        int       `json:"hop"`        // 1 = neighbour, 2 = two-hop (0 = on the anchor)
	EdgeResult string    `json:"edgeResult"` // valid | suspect
}

// EdgeStepRow is one edge in a span's derivation, with its validity verdict.
type EdgeStepRow struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	To     string `json:"to"`
	Result string `json:"result"` // valid | suspect
}

// AtRiskRow is one blast-radius entry — an AUTHORED downstream relation made
// concrete on the current topology, never a prediction.
type AtRiskRow struct {
	CEIKey     string `json:"ceiKey"`
	Phenomenon string `json:"phenomenon"`
	Related    string `json:"related"` // same-entity | <edge-type>
	Temporal   string `json:"temporal"`
	Why        string `json:"why"` // AUTHORED relation note, verbatim
}

// CascadeCard is one recognized cascade story (doc 07 §3.4): a trigger and its
// graph-declared downstream both manifest on related entities — two MEASURED
// co-occurrences joined by an AUTHORED relation, adjacent, never a causal proof.
type CascadeCard struct {
	Trigger    FindingRef `json:"trigger"`
	Downstream FindingRef `json:"downstream"`
	Why        string     `json:"why"`      // AUTHORED relation note
	Temporal   string     `json:"temporal"` // authored temporal tag
	Related    string     `json:"related"`  // same-entity | <edge-type>
}

// FindingRef names one finding occurrence in a cascade story.
type FindingRef struct {
	Phenomenon  string    `json:"phenomenon"`
	EntityCEI   string    `json:"entityCei"`
	EvaluatedAt time.Time `json:"evaluatedAt"`
	Quality     string    `json:"quality"`
}

// BuildInsights composes the "now" surface from the current findings + cascades.
// Pure given its inputs; cmd/obsd snapshots it each tick. Findings are presented
// strongest-first (full before degraded, then by entity) — the same ordering the
// matcher emits, kept stable here.
func BuildInsights(clusterID, graphVersion, graphRelease string, now time.Time,
	findings []detect.Finding, cascades []detect.Cascade) *InsightsView {

	v := &InsightsView{
		ClusterID: clusterID, GraphVersion: graphVersion, GraphRelease: graphRelease,
		GeneratedAt: now.UTC(),
		Findings:    make([]InsightCard, 0, len(findings)),
		Cascades:    make([]CascadeCard, 0, len(cascades)),
	}

	for i := range findings {
		f := &findings[i]
		card := InsightCard{
			Phenomenon: f.Phenomenon, Label: f.Label, EntityCEI: f.EntityCEI,
			Namespace: f.Namespace, Name: f.Name, Kind: f.Kind, Span: spanLabel(f.Span),
			Quality: string(f.Quality), Completeness: f.Completeness,
			RequiredMet: f.RequiredMet, RequiredTotal: f.RequiredTotal,
			Members:      make([]MemberRow, 0, len(f.Members)),
			Unobservable: f.Unobservable,
			SuspectEdges: f.SuspectEdges,
			GraphVersion: f.GraphVersion,
		}
		if card.Unobservable == nil {
			card.Unobservable = []string{}
		}
		if card.SuspectEdges == nil {
			card.SuspectEdges = []string{}
		}
		for _, m := range f.Members {
			card.Members = append(card.Members, MemberRow{
				Metric: m.Metric, Role: m.Role, Temporal: m.Temporal, State: m.State,
				SampleAt: m.SampleAt.UTC(), BarFlagged: m.BarFlagged, Note: m.Note,
				Neighbour: m.Neighbour, Via: m.Via, Hop: m.Hop, EdgeResult: m.EdgeResult,
			})
		}
		for _, s := range f.SpanPath {
			card.SpanPath = append(card.SpanPath, EdgeStepRow{Type: s.Type, From: s.From, To: s.To, Result: s.Result})
		}
		if card.SpanPath == nil {
			card.SpanPath = []EdgeStepRow{}
		}
		for _, r := range f.BlastRadius {
			card.BlastRadius = append(card.BlastRadius, AtRiskRow{
				CEIKey: r.CEIKey, Phenomenon: r.Phenomenon, Related: r.Related, Temporal: r.Temporal, Why: r.Why,
			})
		}
		if card.BlastRadius == nil {
			card.BlastRadius = []AtRiskRow{}
		}

		v.Findings = append(v.Findings, card)
		v.Summary.Total++
		if f.Quality == detect.QualityFull {
			v.Summary.Full++
		} else {
			v.Summary.Degraded++
		}
		switch f.Span {
		case "first-order":
			v.Summary.FirstOrder++
		case "second-order":
			v.Summary.SecondOrder++
		default:
			v.Summary.EntityLocal++
		}
		if len(f.BlastRadius) > 0 {
			v.Summary.WithAtRisk++
		}
	}

	for _, c := range cascades {
		v.Cascades = append(v.Cascades, CascadeCard{
			Trigger: FindingRef{Phenomenon: c.Trigger.Phenomenon, EntityCEI: c.Trigger.EntityCEI,
				EvaluatedAt: c.Trigger.EvaluatedAt.UTC(), Quality: string(c.Trigger.Quality)},
			Downstream: FindingRef{Phenomenon: c.Downstream.Phenomenon, EntityCEI: c.Downstream.EntityCEI,
				EvaluatedAt: c.Downstream.EvaluatedAt.UTC(), Quality: string(c.Downstream.Quality)},
			Why: c.Why, Temporal: c.Temporal, Related: c.Related,
		})
	}
	v.Summary.Cascades = len(v.Cascades)

	// Stable presentation order, independent of the matcher's slice order.
	sort.SliceStable(v.Findings, func(i, j int) bool {
		a, b := v.Findings[i], v.Findings[j]
		if (a.Quality == "full") != (b.Quality == "full") {
			return a.Quality == "full" // full before degraded
		}
		if a.EntityCEI != b.EntityCEI {
			return a.EntityCEI < b.EntityCEI
		}
		return a.Phenomenon < b.Phenomenon
	})
	return v
}

func spanLabel(s string) string {
	if s == "" {
		return "entity-local"
	}
	return s
}
