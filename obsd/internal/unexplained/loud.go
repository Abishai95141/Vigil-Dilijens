// Package unexplained — see doc.go for the full contract. This file implements
// M1: the loudness evaluator (doc 08 §3.1).
package unexplained

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// LoudKind names which clause of the two-clause loudness definition fired.
type LoudKind string

const (
	// LoudBarCrossing: a thresholded signal is at `above` or `well-above` on its
	// resolved bar (doc 08 §3.1, clause 1).
	LoudBarCrossing LoudKind = "bar-crossing"
	// LoudRateExcursion: a rate-guarded signal exceeds its authored rate guard
	// (doc 08 §3.1, clause 2).
	LoudRateExcursion LoudKind = "rate-excursion"
)

// LoudState is one bar crossing or rate excursion — MEASURED, with its bar/guard
// provenance and timestamp (doc 08 §3.7). It is the ONLY thing loudness can be:
// no distribution distance, no novelty score, no "looks unusual" (§3.1).
type LoudState struct {
	Metric    string    `json:"metric"`
	Kind      LoudKind  `json:"kind"`
	State     string    `json:"state"`     // ladder state ("above"/"well-above") or "breached"
	BarSource string    `json:"barSource"` // config | default
	Flagged   bool      `json:"flagged"`   // default-sourced bar (lower trust)
	SampleAt  time.Time `json:"sampleAt"`
}

// Loud returns an entity's loud states this window from its fingerprint — the
// exact two-clause definition of doc 08 §3.1, in the SAME bar/guard vocabulary
// detection uses (mirrors observe.Fingerprint.Crossed). Stale and inconclusive
// components never qualify (they are not a confident crossing). Deterministic:
// sorted by (metric, kind).
func Loud(fp observe.Fingerprint) []LoudState {
	var out []LoudState
	for _, t := range fp.Thresholds {
		if t.Stale || !t.State.Crossed() {
			continue
		}
		out = append(out, LoudState{
			Metric: t.Metric, Kind: LoudBarCrossing, State: t.State.String(),
			BarSource: t.BarSource, Flagged: t.Flagged, SampleAt: t.Deriv.SampleAt,
		})
	}
	for _, r := range fp.Rates {
		if r.Stale || r.Inconclusive || !r.Breached {
			continue
		}
		out = append(out, LoudState{
			Metric: r.Metric, Kind: LoudRateExcursion, State: "breached",
			BarSource: r.BarSource, Flagged: r.Flagged, SampleAt: r.Deriv.SampleAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// metricsOf returns the sorted distinct metric names of a loud-state set — the
// dedup-key component (doc 08 §3.4) and the candidate-report signature (§3.6).
func metricsOf(states []LoudState) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(states))
	for _, s := range states {
		if !seen[s.Metric] {
			seen[s.Metric] = true
			out = append(out, s.Metric)
		}
	}
	sort.Strings(out)
	return out
}
