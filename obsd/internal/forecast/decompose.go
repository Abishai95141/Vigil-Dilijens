package forecast

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Decomposition — pull the footprint, not the reason (doc 09 §3.4, M5). A spike or
// reset with a KNOWN cause is a one-off the zero-shot clock cannot predict, and
// feeding it the past event only pollutes the forecast (the 09 M3 sawtooth failure:
// on a ramp→OOM-kill→reset history the clock projects the next RESET, not the bar
// crossing). So before inference the pipeline splices the context at the most recent
// known event boundary and forecasts only the clean post-event remainder. The
// FOOTPRINT leaves the model's input; the REASON never enters the model and stays in
// the graph (charter §4). This is a NO-OP on a clean series (no boundary found ⇒ the
// full window passes through) and only changes a polluted one — exactly the property
// the Phase-3 exit gate demands (improve backtest error WITHOUT degrading band
// coverage).

// FootprintKind names a per-event-class footprint shape (doc 09 §3.4).
type FootprintKind string

const (
	// FootprintReset — a transient/restart: the gauge dropped sharply (a container
	// restart resets the cgroup working_set). Spliced (the pre-reset history is the
	// previous instance's, irrelevant to the current ramp).
	FootprintReset FootprintKind = "reset"
	// FootprintLevelShift — an operator deploy/config window: a baseline change.
	// v1 splices at the boundary (the pre-event regime is a different baseline).
	FootprintLevelShift FootprintKind = "level-shift"
)

// Splice is one applied context cut: the boundary AFTER which the clean remainder
// begins (doc 09 §3.4).
type Splice struct {
	Kind    FootprintKind `json:"kind"`
	AtIndex int           `json:"atIndex"` // index in the ORIGINAL window where the remainder starts
	At      time.Time     `json:"at"`
	Source  string        `json:"source"` // "gauge-reset" | "context-window"
}

// DecompositionRecord is the candidate's decomposition provenance (doc 09 §3.9):
// what footprints were removed and how much of the window they explained. Surfaced
// on the candidate so the operator can see the forecast ran on a spliced remainder.
type DecompositionRecord struct {
	Splices        []Splice `json:"splices"`
	OriginalPoints int      `json:"originalPoints"`
	UsedPoints     int      `json:"usedPoints"`
	ExplainedFrac  float64  `json:"explainedFrac"` // fraction of the window cut away by splicing
	Aborted        bool     `json:"aborted"`
	AbortReason    string   `json:"abortReason,omitempty"`
}

// Spliced reports whether decomposition actually cut the context (a candidate ran on
// less than the full window).
func (d DecompositionRecord) Spliced() bool { return len(d.Splices) > 0 && !d.Aborted }

// Decompose applies splice-point decomposition to a context window (doc 09 §3.4). It
// finds the most recent known event boundary — an operator splicePoint (deploy/config
// context window, 10 M6) or an auto-detected gauge RESET (a restart) — and returns
// the post-event remainder as the series the clock should forecast. When the clean
// remainder is too short, or splicing removes more than MaxExplainedFraction of the
// window, it ABORTS (rec.Aborted): forecasting on residue is untrustworthy. When no
// boundary is found it is a no-op and returns the full window's values.
//
// Pure + deterministic: same samples + same splice points + same params ⇒ same output
// (the backtest replays it byte-identically).
func Decompose(samples []qss.Sample, splicePoints []time.Time, p params.ForecastParams) ([]float64, DecompositionRecord) {
	rec := DecompositionRecord{OriginalPoints: len(samples), UsedPoints: len(samples)}
	if !p.Decompose || len(samples) == 0 {
		return values(samples), rec
	}

	resetIdx, resetAt := lastReset(samples, p.ResetDropFraction)
	ctxIdx, _ := latestSplicePoint(samples, splicePoints)
	var ctxAt time.Time
	if ctxIdx >= 0 {
		ctxAt = samples[ctxIdx].At // the boundary SAMPLE's time (consistent with reset At)
	}

	// The effective boundary is the LATEST of the two (a later event supersedes an
	// earlier one — the clean remainder is everything after the most recent event).
	spliceIdx := -1
	if resetIdx > spliceIdx {
		spliceIdx = resetIdx
	}
	if ctxIdx > spliceIdx {
		spliceIdx = ctxIdx
	}
	if spliceIdx <= 0 {
		return values(samples), rec // no boundary with pre-history to cut: no-op
	}

	// One boundary = ONE splice record. When a reset and an operator window resolve
	// to the SAME index (a deploy that caused the restart it coincides with), record
	// the RESET — the observed numeric footprint — not two entries for one cut.
	switch {
	case resetIdx == spliceIdx:
		rec.Splices = append(rec.Splices, Splice{Kind: FootprintReset, AtIndex: resetIdx, At: resetAt, Source: "gauge-reset"})
	case ctxIdx == spliceIdx:
		rec.Splices = append(rec.Splices, Splice{Kind: FootprintLevelShift, AtIndex: ctxIdx, At: ctxAt, Source: "context-window"})
	}
	rec.ExplainedFrac = float64(spliceIdx) / float64(len(samples))

	remainder := samples[spliceIdx:]
	rec.UsedPoints = len(remainder)

	// Abort criterion (doc 09 §3.4): emit nothing on residue.
	if p.MaxExplainedFraction > 0 && rec.ExplainedFrac > p.MaxExplainedFraction {
		rec.Aborted = true
		rec.AbortReason = "too much of the window explained by event footprints"
		return nil, rec
	}
	if len(remainder) < p.MinContext {
		rec.Aborted = true
		rec.AbortReason = "clean remainder too short after splicing the last event boundary"
		return nil, rec
	}
	return values(remainder), rec
}

// resetPersistWindow is how many points after a drop must stay low to confirm a
// RESET rather than a transient dip. A container restart drops the cgroup gauge and
// then ramps SLOWLY from the new baseline; a GC free / cache eviction / single noisy
// scrape drops and RECOVERS within a scrape or two. Requiring the level to stay down
// for a few points distinguishes them — without it, a transient dip on a CLEAN series
// would falsely splice and degrade band coverage (the exit gate forbids this).
const resetPersistWindow = 4

// lastReset returns the index (and time) of the LAST confirmed gauge RESET (a
// container restart). A reset must satisfy ALL of:
//
//  1. a sharp relative drop: series[i] < (1−frac)×series[i-1];
//  2. a magnitude floor: the drop is a significant fraction (frac) of the series'
//     observed RANGE — so a small absolute wobble on a near-zero baseline does NOT
//     trigger (pure relative tests fire on proportional noise);
//  3. PERSISTENCE: the gauge does not recover toward the pre-drop level within
//     resetPersistWindow points — a transient dip bounces back, a restart stays low.
//
// Returns (-1, zero) if none. The LAST reset is chosen: only the most recent
// instance's ramp is relevant to a "soon" projection. This is the no-false-positive
// guarantee the Phase-3 exit gate depends on (a clean series has no reset → no splice).
func lastReset(samples []qss.Sample, frac float64) (int, time.Time) {
	if frac <= 0 || len(samples) < 2 {
		return -1, time.Time{}
	}
	lo, hi := samples[0].Value, samples[0].Value
	for _, s := range samples {
		if s.Value < lo {
			lo = s.Value
		}
		if s.Value > hi {
			hi = s.Value
		}
	}
	span := hi - lo
	idx := -1
	var at time.Time
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1].Value, samples[i].Value
		if prev <= 0 || cur >= prev*(1-frac) {
			continue // not a sharp relative drop
		}
		if span > 0 && (prev-cur) < frac*span {
			continue // magnitude floor: a small wobble on a small baseline, not a reset
		}
		if i+1 >= len(samples) {
			continue // a drop at the very last point can't be confirmed (restart vs dip) — don't splice
		}
		// Persistence: scan the next window; if the gauge climbs back toward the
		// pre-drop level it was a transient dip on a clean series, not a restart.
		recovered := false
		end := i + 1 + resetPersistWindow
		if end > len(samples) {
			end = len(samples)
		}
		for j := i + 1; j < end; j++ {
			if samples[j].Value >= prev*(1-frac) {
				recovered = true
				break
			}
		}
		if recovered {
			continue
		}
		idx, at = i, samples[i].At
	}
	return idx, at
}

// latestSplicePoint returns the index of the first sample at-or-after the LATEST
// operator splice point that falls strictly inside the window (there must be
// pre-event history to cut). Returns (-1, zero) if none applies.
func latestSplicePoint(samples []qss.Sample, splicePoints []time.Time) (int, time.Time) {
	idx := -1
	var at time.Time
	for _, sp := range splicePoints {
		first := -1
		for i := range samples {
			if !samples[i].At.Before(sp) {
				first = i
				break
			}
		}
		if first > 0 && first > idx { // first>0: pre-event history exists to splice away
			idx, at = first, sp
		}
	}
	return idx, at
}

func values(samples []qss.Sample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Value
	}
	return out
}
