package forecast

import (
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Target is one forecastable (entity, variable) pair — doc 09 §3.9's "forecast
// target": stable identity, the concrete variable, its resolved bar with full
// provenance, and the AUTHORED precursor references that give a crossing its
// early-warning meaning. Everything here is graph/binding-derived; the model
// never sees any of it.
type Target struct {
	CEIKey    string
	Entity    string // Container | Pod | Node | PVC
	Container string // when Entity == Container
	Metric    string // the concrete variable
	StreamUID string // the (UID, metric) join key into the hot store
	RuleID    string
	SignalID  string

	SeriesKind string // "gauge" (v1; counters are deferred-listed, never silently run)

	BarValue   float64
	BarUnit    string
	BarSource  string // config | override | default
	BarFlagged bool   // default-sourced bar — carried onto every surfaced candidate
	Direction  string // above | below

	// Precursor phenomena: the AUTHORED T0- members this signal participates in
	// (doc 09 §3.2 / 00 §5.2 point 3 — the tag is what converts "a number
	// crosses a number" into "early warning for a named phenomenon"). Sorted.
	PrecursorPhenomena []string
}

// Precursor reports whether the target carries early-warning meaning.
func (t Target) Precursor() bool { return len(t.PrecursorPhenomena) > 0 }

// Ineligible is one (entity, variable) pair the funnel REJECTED, with the
// reason — honest partial coverage: every pair has a visible state (doc 04
// §3.4 unbounded policy extended to the whole funnel).
type Ineligible struct {
	CEIKey string
	Metric string
	Reason string
}

// Funnel-rejection reasons (enumerated; surfaced verbatim).
const (
	ReasonNotBound        = "not-bound"              // unresolved/out-of-scope binding
	ReasonUnbounded       = "unbounded"              // no resolvable bar ⇒ nothing to cross (Tier-B ineligible, listed)
	ReasonRateGuardBar    = "rate-guard-bar"         // the bar is a rate guard, not a crossable level
	ReasonCounterDeferred = "counter-deferred"       // counter-typed: rate-derived targets are second-class, deferred (stated)
	ReasonDerivedRatio    = "derived-ratio-deferred" // the evaluated quantity is Metric/Divisor — not a direct level (deferred, stated)
	ReasonNonSeries       = "non-series-signal"      // data_type parses non-series/unknown
	ReasonNoStreamKey     = "no-stream-key"          // entity has no scraped channel (e.g. PVC pseudo-keys)
	ReasonUnknownRule     = "unknown-rule"           // binding references a rule the graph does not carry
)

// EligibleTargets runs the signal-level funnel (doc 09 §3.2) over the bound
// customer graph: bound ∧ gauge-bearing ∧ level bar resolved. The remaining
// gates — real dynamics, budget — are runtime properties (runner.go applies
// the dynamics guard per cycle; selection applies the budget, doc 06 M5).
// Deterministic: results sorted by (CEIKey, Metric); one target per pair.
func EligibleTargets(res *binding.Result, g *graph.Graph) ([]Target, []Ineligible) {
	if res == nil || g == nil {
		return nil, nil
	}
	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	// signal id -> sorted precursor phenomenon ids (T0- membership).
	precursors := precursorIndex(g)

	seen := map[string]bool{} // CEIKey \x1f Metric
	var targets []Target
	var rejected []Ineligible
	for i := range res.Bindings {
		b := &res.Bindings[i]
		key := b.CEIKey + "\x1f" + b.Metric
		if seen[key] {
			continue
		}
		reject := func(reason string) {
			seen[key] = true
			rejected = append(rejected, Ineligible{CEIKey: b.CEIKey, Metric: b.Metric, Reason: reason})
		}
		if b.State != binding.StateBound {
			reject(ReasonNotBound)
			continue
		}
		if b.Bar == nil {
			reject(ReasonUnbounded)
			continue
		}
		if b.Bar.Kind == "rate-of-change" {
			reject(ReasonRateGuardBar)
			continue
		}
		rule := rules[b.RuleID]
		if rule == nil {
			reject(ReasonUnknownRule)
			continue
		}
		if rule.DivisorMetric != "" {
			// The materializer evaluates this as Metric/Divisor (the
			// counter-ratio path, observe.evalThresholdVar) — a DERIVED
			// quantity, not a level you project directly. Mirroring that
			// character decision exactly; a partial mirror here would be a
			// false-equivalence bug. Deferred, listed.
			reject(ReasonDerivedRatio)
			continue
		}
		sig := g.Signals[rule.Signal]
		var shape graph.SeriesShape
		if sig != nil {
			shape = sig.Shape()
		}
		switch {
		case shape.Gauge:
			// the clean fit: a level you project directly (doc 09 §3.2)
		case shape.Counter:
			reject(ReasonCounterDeferred)
			continue
		default:
			reject(ReasonNonSeries)
			continue
		}
		streamUID := b.StreamUID()
		if streamUID == "" {
			reject(ReasonNoStreamKey)
			continue
		}
		seen[key] = true
		targets = append(targets, Target{
			CEIKey: b.CEIKey, Entity: b.Entity, Container: b.Container,
			Metric: b.Metric, StreamUID: streamUID, RuleID: b.RuleID, SignalID: rule.Signal,
			SeriesKind: "gauge",
			BarValue:   b.Bar.Value, BarUnit: b.Bar.Unit,
			BarSource: string(b.Bar.Source), BarFlagged: b.Bar.Flagged,
			Direction:          b.Bar.Direction,
			PrecursorPhenomena: precursors[rule.Signal],
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].CEIKey != targets[j].CEIKey {
			return targets[i].CEIKey < targets[j].CEIKey
		}
		return targets[i].Metric < targets[j].Metric
	})
	sort.Slice(rejected, func(i, j int) bool {
		if rejected[i].CEIKey != rejected[j].CEIKey {
			return rejected[i].CEIKey < rejected[j].CEIKey
		}
		return rejected[i].Metric < rejected[j].Metric
	})
	return targets, rejected
}

// precursorIndex maps signal id -> sorted phenomenon ids in which that signal
// is a T0- member (the authored early-warning meaning, doc 09 §3.2).
func precursorIndex(g *graph.Graph) map[string][]string {
	ids := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := map[string][]string{}
	for _, id := range ids {
		for _, m := range g.Phenomena[id].Members {
			if strings.HasPrefix(m.TemporalOrder, "T0-") || m.TemporalOrder == "T-1" {
				out[m.SignalID] = append(out[m.SignalID], id)
			}
		}
	}
	return out
}
