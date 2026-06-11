// Package detect is the "now" half of the runtime — see doc.go for the full
// contract. This file implements M1: the entity-local (zeroth-order) matcher.
package detect

import (
	"fmt"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// MatchQuality is full or degraded (doc 07 §3.3). A lone crossing is not an alert
// (§3.5) — only a phenomenon's required-member conjunction surfaces.
type MatchQuality string

const (
	QualityFull     MatchQuality = "full"     // every required member observable and matching
	QualityDegraded MatchQuality = "degraded" // required members matched, but span/coverage incomplete
)

// MemberEvidence is one member's contribution to a finding — the temporal evidence
// row of doc 07 §3.8, with its authored note attached verbatim.
type MemberEvidence struct {
	SignalID   string
	Metric     string
	Role       string // required | corroborating | supporting
	Temporal   string // T0- | T0 | T0+...
	Observable bool   // a check exists AND its fingerprint variable was fresh
	Met        bool
	State      string // the fingerprint state that satisfied/failed the check
	SampleAt   time.Time
	BarFlagged bool   // the bar this state was computed against is a flagged ontology default
	Note       string // AUTHORED member note (the only "why", attributed)
}

// Finding is a detection finding (doc 07 §3.8): MEASURED, with AUTHORED references
// attached — never fused.
type Finding struct {
	Phenomenon   string
	Label        string
	GraphVersion string
	EntityCEI    string
	Namespace    string
	Name         string
	Kind         string
	EvaluatedAt  time.Time

	Quality      MatchQuality
	Completeness float64 // observable-required-met / total-required (doc 07 §8)

	RequiredTotal       int
	RequiredMet         int
	RequiredUnobserved  int
	SupportingMet       int
	SupportingObservble int

	Members      []MemberEvidence // the evidence trail (required + observed supporting)
	Unobservable []string         // required members that could not be checked here
}

// Matcher evaluates entity-local phenomena against fingerprints. Stateless and
// deterministic: same fingerprint + same graph version => same findings.
type Matcher struct {
	g          *graph.Graph
	checkBySig map[string]map[string]*graph.MemberCheck // phen -> signal -> check
	entityLoc  []*graph.Phenomenon                      // entity-local phenomena, sorted by id
}

// NewMatcher indexes the graph's entity-local phenomena and their authored checks.
func NewMatcher(g *graph.Graph) *Matcher {
	m := &Matcher{g: g, checkBySig: map[string]map[string]*graph.MemberCheck{}}
	for id, p := range g.Phenomena {
		if p.Span != graph.SpanEntityLocal {
			continue // M1 is zeroth-order; first/second-order are M2/M3
		}
		m.entityLoc = append(m.entityLoc, p)
		bySig := map[string]*graph.MemberCheck{}
		for _, c := range g.ChecksFor(id) {
			bySig[c.Signal] = c
		}
		m.checkBySig[id] = bySig
	}
	sort.Slice(m.entityLoc, func(i, j int) bool { return m.entityLoc[i].ID < m.entityLoc[j].ID })
	return m
}

// EntityLocalCount is how many entity-local phenomena the matcher evaluates.
func (m *Matcher) EntityLocalCount() int { return len(m.entityLoc) }

// MatchFingerprint evaluates every entity-local phenomenon against one entity's
// fingerprint, returning the findings that lit up (full or degraded). A phenomenon
// with no observable matching required member produces no finding (a single
// fingerprint state is not a phenomenon, doc 07 §3.5).
func (m *Matcher) MatchFingerprint(fp observe.Fingerprint) []Finding {
	var out []Finding
	for _, p := range m.entityLoc {
		if f, ok := m.evalPhenomenon(p, fp); ok {
			out = append(out, f)
		}
	}
	// Strongest (most complete) first, then by phenomenon id for determinism.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Completeness != out[j].Completeness {
			return out[i].Completeness > out[j].Completeness
		}
		return out[i].Phenomenon < out[j].Phenomenon
	})
	return out
}

func (m *Matcher) evalPhenomenon(p *graph.Phenomenon, fp observe.Fingerprint) (Finding, bool) {
	bySig := m.checkBySig[p.ID]
	f := Finding{
		Phenomenon: p.ID, Label: p.Label, GraphVersion: m.g.Version,
		EntityCEI: fp.CEIKey, Namespace: fp.Namespace, Name: fp.Name, Kind: fp.Kind,
		EvaluatedAt: fp.EvaluatedAt,
	}

	// Members come from the resolved participates_in edges (signal -> phenomenon),
	// already on the graph. Stable-sort by a TOTAL key (signal, temporal, role) so
	// ties never reorder non-deterministically (doc 07 §3.7).
	members := append([]graph.Member(nil), p.Members...)
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if a.SignalID != b.SignalID {
			return a.SignalID < b.SignalID
		}
		if a.TemporalOrder != b.TemporalOrder {
			return a.TemporalOrder < b.TemporalOrder
		}
		return a.Role < b.Role
	})

	for _, mem := range members {
		required := mem.Role == "required"
		check := bySig[mem.SignalID]
		ev := MemberEvidence{
			SignalID: mem.SignalID, Role: mem.Role, Temporal: mem.TemporalOrder, Note: mem.Why,
		}
		if check != nil {
			ev.Metric = check.Metric
			met, state, fresh, at := satisfies(check, fp)
			ev.Observable = fresh
			ev.Met = met && fresh
			ev.State = state
			ev.SampleAt = at
			ev.BarFlagged = barFlagged(check, fp)
			if check.Note != "" {
				ev.Note = mem.Why + " [" + check.Note + "]"
			}
		}

		if required {
			f.RequiredTotal++
			switch {
			case ev.Observable && ev.Met:
				f.RequiredMet++
				f.Members = append(f.Members, ev)
			case ev.Observable && !ev.Met:
				// A required member is observable and NOT satisfied: the conjunction
				// fails — this phenomenon is not happening here.
				return Finding{}, false
			default:
				// No check, or its variable absent/stale: unobservable here. Carry the
				// AUTHORED note (the only "why") so the curator's explanation of the
				// MISSING member is surfaced, not dropped (doc 07 §4).
				f.RequiredUnobserved++
				label := mem.SignalID
				if ev.Metric != "" {
					label += " (" + ev.Metric + ", stale/absent)"
				}
				if ev.Note != "" {
					label += " — " + ev.Note
				}
				f.Unobservable = append(f.Unobservable, label)
			}
		} else {
			// Supporting member: strengthens, never required.
			if check != nil {
				f.SupportingObservble++
				if ev.Met {
					f.SupportingMet++
					f.Members = append(f.Members, ev)
				}
			}
		}
	}

	// No positive required evidence ⇒ not a match (a phenomenon needs at least one
	// of its required members observed and matching).
	if f.RequiredMet == 0 {
		return Finding{}, false
	}
	if f.RequiredTotal > 0 {
		f.Completeness = float64(f.RequiredMet) / float64(f.RequiredTotal)
	}
	if f.RequiredUnobserved == 0 {
		f.Quality = QualityFull
	} else {
		f.Quality = QualityDegraded
	}
	return f, true
}

// satisfies evaluates one member check against the fingerprint, returning
// (conditionMet, observedState, fresh, sampleTime). fresh is false when the
// fingerprint variable is absent or stale (treated as missing, doc 05 §6).
func satisfies(c *graph.MemberCheck, fp observe.Fingerprint) (met bool, state string, fresh bool, at time.Time) {
	switch c.Facet {
	case "slope":
		vt, ok := findThreshold(fp, c.Metric)
		if !ok || vt.Stale || vt.SlopeInconclusive || vt.SlopeSamples < 2 {
			return false, "no-slope", false, time.Time{}
		}
		state = fmt.Sprintf("%s, slope %+.3g/s", vt.State, vt.Slope)
		// Materiality guard (doc 07 §3.6): a rise only counts if the level is also at
		// least the authored min_state — a trivial slope on an idle entity does not fire.
		if !meetsMinState(vt.State, c.MinState) {
			return false, state, true, vt.Deriv.SampleAt
		}
		switch c.Expect {
		case "rising":
			return vt.Slope > 0, state, true, vt.Deriv.SampleAt
		case "falling":
			return vt.Slope < 0, state, true, vt.Deriv.SampleAt
		}
		return false, state, true, vt.Deriv.SampleAt
	case "level", "ratio":
		vt, ok := findThreshold(fp, c.Metric)
		if !ok || vt.Stale {
			return false, "no-sample", false, time.Time{}
		}
		state = vt.State.String()
		switch c.Expect {
		case "crossed", "at-or-above":
			// Both require the bar actually CROSSED. StateAtThreshold is the healthy
			// approach band (strictly below the bar) and must not satisfy at-or-above.
			return vt.State.Crossed(), state, true, vt.Deriv.SampleAt
		}
		return false, state, true, vt.Deriv.SampleAt
	case "rate-guard":
		vr, ok := findRate(fp, c.Metric)
		if !ok || vr.Stale || vr.Inconclusive {
			return false, "inconclusive", false, time.Time{}
		}
		state = fmt.Sprintf("Δ%.0f/window", vr.WindowDelta)
		if c.Expect == "breached" {
			return vr.Breached, state, true, vr.Deriv.SampleAt
		}
		return false, state, true, vr.Deriv.SampleAt
	}
	return false, "unknown-facet", false, time.Time{}
}

// meetsMinState reports whether a ladder state is at least the required minimum.
func meetsMinState(s observe.ThresholdState, min string) bool {
	rank := map[string]observe.ThresholdState{
		"": observe.StateUnknown, "at-threshold": observe.StateAtThreshold,
		"above": observe.StateAbove, "well-above": observe.StateWellAbove,
	}
	return s >= rank[min]
}

// findThreshold returns the entity's threshold variable for a metric only if it is
// UNAMBIGUOUS. Two variables on one entity sharing a metric is an ambiguity (mirrors
// binding QA): rather than silently take the first (fp.Thresholds is sorted by
// RuleID, not metric), treat it as no usable evidence so the member is unobserved,
// never matched against an arbitrary bar.
func findThreshold(fp observe.Fingerprint, metric string) (observe.VariableThreshold, bool) {
	var found observe.VariableThreshold
	n := 0
	for _, t := range fp.Thresholds {
		if t.Metric == metric {
			found, n = t, n+1
		}
	}
	return found, n == 1
}

func findRate(fp observe.Fingerprint, metric string) (observe.VariableRate, bool) {
	var found observe.VariableRate
	n := 0
	for _, r := range fp.Rates {
		if r.Metric == metric {
			found, n = r, n+1
		}
	}
	return found, n == 1
}

// barFlagged reports whether the fingerprint variable a check consults was computed
// against a flagged ontology-default bar (lower trust, doc 04 §3.4) — surfaced on the
// finding so a default bar never masquerades as config-sourced.
func barFlagged(c *graph.MemberCheck, fp observe.Fingerprint) bool {
	if c.Facet == "rate-guard" {
		if vr, ok := findRate(fp, c.Metric); ok {
			return vr.Flagged
		}
		return false
	}
	if vt, ok := findThreshold(fp, c.Metric); ok {
		return vt.Flagged
	}
	return false
}
