package observe

import (
	"math"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Fingerprint materialization (doc 05 §3.4, M3): per selected entity, the live
// primitive results compacted into a fingerprint that detection (07) reads — a
// threshold ladder per thresholded variable, rate summaries per rate-guarded
// variable, each stamped with evaluation time and the bar SOURCE it used (config /
// default-flagged) plus the stream + readings it derived from. Detection reads
// fingerprints, not raw history: the hot path stays cheap and the evidence trail
// stays exact. Everything here is MEASURED-class arithmetic; nothing learns.

// FPParams are the evaluation constants (from the params file, doc 14 §5).
type FPParams struct {
	ScrapeInterval     time.Duration
	RateWindow         time.Duration
	Watermark          time.Duration
	Band               float64 // at-threshold approach band
	WellAboveFactor    float64
	CooccurrenceWindow time.Duration
}

// StreamReader is the read-side of the observation store the materializer needs
// (the Ingestor implements it; tests use a fake).
type StreamReader interface {
	StreamsFor(uid, metric string) []string
	Latest(streamID string) (qss.Sample, bool)
	LastN(streamID string, n int) []qss.Sample
	StreamType(streamID string) (expoType string, ok bool)
}

// DerivationRef is a fingerprint component's evidence trail (doc 05 §3.4): the
// stream(s) and the bar it derived from, so every component can cite its readings.
type DerivationRef struct {
	StreamID  string
	DivisorID string // for ratio variables
	SampleAt  time.Time
	Samples   int    // window samples consulted (rate/ratio)
	How       string // "gauge-level" | "counter-rate" | "counter-ratio" | "rate-guard"
}

// VariableThreshold is one thresholded variable's fingerprint component.
type VariableThreshold struct {
	RuleID    string
	Metric    string
	State     ThresholdState
	Value     float64 // the evaluated quantity, in the bar's unit
	Bar       float64
	WellAbove float64
	Unit      string
	Direction string
	BarSource string // config | default (flagged) — provenance rides along
	Flagged   bool   // default-sourced bar
	Stale     bool   // newest sample older than the watermark (treat as missing, 07)
	Deriv     DerivationRef
}

// VariableRate is one rate-guarded variable's fingerprint component.
type VariableRate struct {
	RuleID      string
	Metric      string
	WindowDelta float64
	PerSecond   float64
	Bar         float64 // the guard (e.g. 3 restarts / window)
	Breached    bool    // WindowDelta >= Bar
	BarSource   string
	Flagged     bool
	Resets      int
	GapBroken   bool
	// Inconclusive: a scrape gap left fewer than 2 usable samples after it, so the
	// window delta is NOT a confident measurement — the guard cannot say breached or
	// not. Must never be read as a clean "not breached" (the silent-failure-during-
	// the-incident case). Surfaced as degraded, kept out of Crossed()'s healthy bucket.
	Inconclusive bool
	Stale        bool
	Deriv        DerivationRef
}

// Fingerprint is one entity's compacted live state (doc 05 §3.6).
type Fingerprint struct {
	CEIKey      string
	RoleKey     string
	Namespace   string
	Name        string
	Kind        string // Container | Pod | Node
	EvaluatedAt time.Time
	Thresholds  []VariableThreshold
	Rates       []VariableRate
}

// Crossed reports whether any thresholded variable is at/past its bar — the cheap
// "is anything happening on this entity" check detection screens on.
func (f Fingerprint) Crossed() bool {
	for _, t := range f.Thresholds {
		if !t.Stale && t.State.Crossed() {
			return true
		}
	}
	for _, r := range f.Rates {
		if !r.Stale && !r.Inconclusive && r.Breached {
			return true
		}
	}
	return false
}

// Materialize evaluates every usable bound (entity, variable) into per-entity
// fingerprints. Usable = StateBound, has a resolved bar, validation != failed
// (a failed binding's evidence contradicts its semantics — never evaluated).
// Deterministic: entities and components are sorted; the clock is injected.
func Materialize(res *binding.Result, rules map[string]*graph.ThresholdRule, reader StreamReader, p FPParams, evalNow time.Time) []Fingerprint {
	byEntity := map[string]*Fingerprint{}
	order := []string{}

	get := func(b *binding.Binding) *Fingerprint {
		fp, ok := byEntity[b.CEIKey]
		if !ok {
			fp = &Fingerprint{
				CEIKey: b.CEIKey, RoleKey: b.RoleKey, Kind: b.Entity, EvaluatedAt: evalNow,
			}
			ns, name := entityCoords(b.CEIKey)
			fp.Namespace, fp.Name = ns, name
			byEntity[b.CEIKey] = fp
			order = append(order, b.CEIKey)
		}
		return fp
	}

	for i := range res.Bindings {
		b := &res.Bindings[i]
		if b.State != binding.StateBound || b.Bar == nil || b.Validation == binding.ValidationFailed {
			continue
		}
		uid := b.StreamUID()
		if uid == "" {
			continue
		}
		streams := reader.StreamsFor(uid, b.Metric)
		if len(streams) != 1 {
			continue // no evidence, or ambiguous (QA already flags >1)
		}
		rule := rules[b.RuleID]

		if b.Bar.Kind == graph.RuleRateOfChange {
			if vr, ok := evalRateGuard(b, streams[0], reader, p, evalNow); ok {
				fp := get(b)
				fp.Rates = append(fp.Rates, vr)
			}
			continue
		}
		if vt, ok := evalThresholdVar(b, rule, streams[0], uid, reader, p, evalNow); ok {
			fp := get(b)
			fp.Thresholds = append(fp.Thresholds, vt)
		}
	}

	out := make([]Fingerprint, 0, len(order))
	sort.Strings(order)
	for _, k := range order {
		fp := byEntity[k]
		sort.Slice(fp.Thresholds, func(i, j int) bool { return fp.Thresholds[i].RuleID < fp.Thresholds[j].RuleID })
		sort.Slice(fp.Rates, func(i, j int) bool { return fp.Rates[i].RuleID < fp.Rates[j].RuleID })
		out = append(out, *fp)
	}
	return out
}

// evalThresholdVar produces a thresholded-variable component: derives the evaluated
// quantity in the bar's unit (gauge level, counter→rate, or counter-ratio), then
// places it on the ladder.
func evalThresholdVar(b *binding.Binding, rule *graph.ThresholdRule, streamID, uid string, reader StreamReader, p FPParams, evalNow time.Time) (VariableThreshold, bool) {
	bar := b.Bar
	vt := VariableThreshold{
		RuleID: b.RuleID, Metric: b.Metric, Bar: bar.Value, Unit: bar.Unit,
		Direction: bar.Direction, BarSource: string(bar.Source), Flagged: bar.Flagged,
	}
	vt.WellAbove = WellAboveLine(bar.Value, bar.Factor, p.WellAboveFactor, bar.Direction)

	expoType, _ := reader.StreamType(streamID)

	switch {
	case rule != nil && rule.DivisorMetric != "":
		// Derived ratio: numerator/divisor. For counters, the rate-ratio over the
		// window (Δnum/Δden); for gauges, latest/latest.
		divs := reader.StreamsFor(uid, rule.DivisorMetric)
		if len(divs) != 1 {
			return VariableThreshold{}, false
		}
		num, numAt, nok := windowValue(streamID, expoType, reader, p, evalNow)
		den, denAt, dok := windowValue(divs[0], expoType, reader, p, evalNow)
		// A non-finite or zero denominator (or non-finite numerator) must not produce
		// a fabricated ratio; drop the component rather than feed Inf/NaN to the ladder.
		if !nok || !dok || den == 0 || math.IsNaN(num) || math.IsInf(num, 0) || math.IsNaN(den) || math.IsInf(den, 0) {
			return VariableThreshold{}, false
		}
		vt.Value = num / den
		vt.Deriv = DerivationRef{StreamID: streamID, DivisorID: divs[0], SampleAt: numAt, How: "counter-ratio"}
		// The ratio is only as fresh as its LEAST-fresh input — staleness was being
		// silently dropped on this path (a stale ratio over its bar would surface as a
		// live crossing). Anchor staleness on the older of the two streams.
		anchor := numAt
		if denAt.Before(anchor) {
			anchor = denAt
		}
		vt.Stale = stale(anchor, evalNow, p.Watermark)
	case expoType == "counter":
		// A cumulative counter compared to a level bar (e.g. cpu_seconds vs a
		// millicore limit): convert to a rate. cores = Δsec/Δt; ×1000 → millicores.
		rr := EvalRate(reader.LastN(streamID, ringWindowN(p)), evalNow, p.RateWindow, p.ScrapeInterval)
		if rr.Elapsed <= 0 {
			return VariableThreshold{}, false
		}
		v := rr.PerSecond
		if bar.Unit == "millicores" {
			v *= 1000
		}
		vt.Value = v
		latest, _ := reader.Latest(streamID)
		vt.Deriv = DerivationRef{StreamID: streamID, SampleAt: latest.At, Samples: rr.Samples, How: "counter-rate"}
		vt.Stale = stale(latest.At, evalNow, p.Watermark)
	default:
		// Gauge: the latest value, directly.
		latest, ok := reader.Latest(streamID)
		if !ok {
			return VariableThreshold{}, false
		}
		vt.Value = latest.Value
		vt.Deriv = DerivationRef{StreamID: streamID, SampleAt: latest.At, How: "gauge-level"}
		vt.Stale = stale(latest.At, evalNow, p.Watermark)
	}

	vt.State = EvalThreshold(vt.Value, bar.Value, vt.WellAbove, p.Band, bar.Direction)
	return vt, true
}

// evalRateGuard produces a rate-guarded-variable component (rate-of-change rules):
// the reset-aware window delta vs the guard.
func evalRateGuard(b *binding.Binding, streamID string, reader StreamReader, p FPParams, evalNow time.Time) (VariableRate, bool) {
	rr := EvalRate(reader.LastN(streamID, ringWindowN(p)), evalNow, p.RateWindow, p.ScrapeInterval)
	latest, ok := reader.Latest(streamID)
	if !ok {
		return VariableRate{}, false
	}
	// A gap that leaves <2 usable samples after it (Elapsed<=0 with GapBroken) means
	// the window delta was computed over nothing — inconclusive, not "0 restarts".
	// This is exactly the incident case (a node briefly unreachable as it thrashes),
	// so it must never read as a confident not-breached.
	inconclusive := rr.GapBroken && rr.Elapsed <= 0
	vr := VariableRate{
		RuleID: b.RuleID, Metric: b.Metric,
		WindowDelta: rr.WindowDelta, PerSecond: rr.PerSecond,
		Bar: b.Bar.Value, Breached: rr.WindowDelta >= b.Bar.Value && !inconclusive,
		BarSource: string(b.Bar.Source), Flagged: b.Bar.Flagged,
		Resets: rr.Resets, GapBroken: rr.GapBroken, Inconclusive: inconclusive,
		Stale: stale(latest.At, evalNow, p.Watermark),
		Deriv: DerivationRef{StreamID: streamID, SampleAt: latest.At, Samples: rr.Samples, How: "rate-guard"},
	}
	return vr, true
}

// windowValue returns the comparable quantity for a ratio component: a counter's
// reset-aware window delta, or a gauge's latest value.
func windowValue(streamID, expoType string, reader StreamReader, p FPParams, evalNow time.Time) (float64, time.Time, bool) {
	if expoType == "counter" {
		rr := EvalRate(reader.LastN(streamID, ringWindowN(p)), evalNow, p.RateWindow, p.ScrapeInterval)
		latest, _ := reader.Latest(streamID)
		if rr.Samples < 2 {
			return 0, latest.At, false
		}
		return rr.WindowDelta, latest.At, true
	}
	latest, ok := reader.Latest(streamID)
	return latest.Value, latest.At, ok
}

// ringWindowN is how many recent samples to pull for a window evaluation: the rate
// window over the scrape interval, padded.
func ringWindowN(p FPParams) int {
	if p.ScrapeInterval <= 0 {
		return 64
	}
	return int(p.RateWindow/p.ScrapeInterval) + 4
}

func stale(sampleAt, now time.Time, watermark time.Duration) bool {
	return now.Sub(sampleAt) > watermark
}

// entityCoords pulls (namespace, name) from an instance CEI key for display.
func entityCoords(ceiKey string) (ns, name string) {
	parts := splitKey(ceiKey)
	if len(parts) >= 5 && parts[0] == "i" {
		return parts[2], parts[4]
	}
	return "", ceiKey
}

func splitKey(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '|' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
