package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
)

// holdableSilence are the ONLY silence reasons the debouncer smooths: the
// marginal-projection flicker near a guardrail boundary, where the same
// approaching entity blinks on/off cycle-to-cycle. Every other quiet reason is
// a real state, not flicker — above all `already-crossed`, which is the
// PROJECTED→MEASURED handoff: holding a future-tense projection for an entity
// detection now reports as over the bar would make the two lanes contradict each
// other. Those are dropped immediately so detection is the single source of truth.
var holdableSilence = map[string]bool{
	forecast.SilenceBandTooWide: true,
	forecast.SilenceNoCrossing:  true,
}

// WarningDebouncer stabilises the early-warning surface across forecast cycles.
//
// Project() (doc 09 §3.3/§3.6) is PURE per-cycle — by design, for the backtest
// gate, which scores every tick independently. But near a guardrail boundary a
// projection is genuinely marginal: the median trajectory crosses within the
// horizon one cycle and not the next, or the band-width guardrail trips
// intermittently, so the SAME real, approaching entity FLICKERS on/off on the
// operator's screen. A maintainer cannot act on a blinking warning.
//
// This debouncer holds the LIVE surfacing layer steady WITHOUT touching the gate
// or the determinism guarantee (it lives only in cmd/obsd's warm-path forecast
// loop, off the deterministic digest). It mirrors the unexplained channel's
// aging (doc 08 §3.4): a warning that stops re-projecting is not dropped
// instantly — it is held, LABELLED `aging` (showing its last real projection,
// never a fabricated current one), and cleared only after `clearAfter` of quiet.
// That converts honest per-cycle marginality into a stable, trustworthy signal
// without inventing anything.
type WarningDebouncer struct {
	active     map[string]*heldWarning
	clearAfter time.Duration
}

type heldWarning struct {
	card            WarningCard
	firstSeen       time.Time
	lastProjectedAt time.Time
}

// NewWarningDebouncer holds a warning for clearAfter past its last real
// projection before clearing it (typically ~3× the forecast interval).
func NewWarningDebouncer(clearAfter time.Duration) *WarningDebouncer {
	return &WarningDebouncer{active: map[string]*heldWarning{}, clearAfter: clearAfter}
}

func warnKey(c *WarningCard) string { return c.EntityCEI + "\x1f" + c.Metric }

// Step reconciles this cycle's fresh warnings against the held set, in place on
// the view: fresh warnings refresh (Aging=false, FirstSeenAt preserved); held
// warnings not re-projected this cycle are kept as Aging=true until clearAfter
// elapses, then evicted; and any entity shown as an (aging) warning is removed
// from the Silences list so the same target is never presented as both warned
// and silent. now must be injected (no time.Now in logic — testable).
func (d *WarningDebouncer) Step(v *WarningsView, now time.Time) {
	if v == nil || !v.Enabled {
		return
	}
	fresh := make(map[string]bool, len(v.Warnings))
	for i := range v.Warnings {
		c := &v.Warnings[i]
		k := warnKey(c)
		fresh[k] = true
		first := c.BasisAt
		if h := d.active[k]; h != nil && !h.firstSeen.IsZero() {
			first = h.firstSeen
		}
		c.Aging = false
		c.FirstSeenAt = first
		cp := *c
		d.active[k] = &heldWarning{card: cp, firstSeen: first, lastProjectedAt: now}
	}
	// Why each held target went quiet THIS cycle (read before the silence list is
	// reconciled below). Only a marginal-guardrail silence is flicker to smooth.
	reason := make(map[string]string, len(v.Silences))
	for _, s := range v.Silences {
		reason[s.EntityCEI+"\x1f"+s.Metric] = s.Reason
	}
	for k, h := range d.active {
		if fresh[k] {
			continue
		}
		// Drop unless this cycle silenced it via a marginal guardrail. A crossing
		// (already-crossed), a flat/short/counter stream, or simply leaving the
		// funnel is NOT flicker — release it to the MEASURED lane at once.
		if !holdableSilence[reason[k]] || now.Sub(h.lastProjectedAt) > d.clearAfter {
			delete(d.active, k)
			continue
		}
		held := h.card
		held.Aging = true
		v.Warnings = append(v.Warnings, held)
	}
	// Soonest crossing first; the on-call ordering (stable, deterministic).
	sort.SliceStable(v.Warnings, func(i, j int) bool {
		if !v.Warnings[i].CrossAt.Equal(v.Warnings[j].CrossAt) {
			return v.Warnings[i].CrossAt.Before(v.Warnings[j].CrossAt)
		}
		return v.Warnings[i].EntityCEI < v.Warnings[j].EntityCEI
	})
	// A target shown as an (aging) warning must not ALSO appear in the silence
	// accounting — that would present one entity as both warned and silent.
	shown := make(map[string]bool, len(v.Warnings))
	for i := range v.Warnings {
		shown[warnKey(&v.Warnings[i])] = true
	}
	kept := v.Silences[:0]
	for _, s := range v.Silences {
		if !shown[s.EntityCEI+"\x1f"+s.Metric] {
			kept = append(kept, s)
		}
	}
	v.Silences = kept
}
