package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// WarningsView is the early-warning surface payload (doc 10 §3.1 M5 / doc 09
// M4): PROJECTED-class candidates with AUTHORED references attached, adjacent,
// never fused — plus the surface's own honesty: the silence accounting, the
// unbudgeted-eligible count (doc 06 M5), and the clock-degradation state
// (doc 14 A13). The register is fixed at this boundary: "projected to cross",
// never "will cross"; the band is mandatory and never collapses to a line.
type WarningsView struct {
	Class        string    `json:"class"` // PROJECTED — the lane's provenance class
	GeneratedAt  time.Time `json:"generatedAt"`
	GraphVersion string    `json:"graphVersion"`
	GraphRelease string    `json:"graphRelease"`

	// Enabled=false ⇒ the forecasting layer is OFF (the gate rule, doc 11
	// §3.5: no class becomes operator-visible before its backtest gate) — the
	// surface states WHY it is empty rather than implying quiet.
	Enabled  bool   `json:"enabled"`
	GateNote string `json:"gateNote"`

	Warnings   []WarningCard  `json:"warnings"`
	Silences   []SilenceRow   `json:"silences"`   // every quiet target, with its guardrail reason (§3.6)
	Unbudgeted int            `json:"unbudgeted"` // eligible-but-unbudgeted count (visible, never silent)
	Clock      ClockHealthRow `json:"clock"`
}

// WarningCard is one early-warning candidate, rendered. The projection is the
// clock's; the bar, the precursor meaning, and the blast radius are the
// graph's; they meet here LABELLED (doc 00 §5.2 — the join, never the fusion).
type WarningCard struct {
	Class        string `json:"class"`        // PROJECTED
	IsProjection bool   `json:"isProjection"` // mandatory mark (doc 09 §3.6)

	EntityCEI  string  `json:"entityCei"`
	Namespace  string  `json:"namespace"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Metric     string  `json:"metric"`
	SeriesKind string  `json:"seriesKind"`
	BarValue   float64 `json:"barValue"`
	BarUnit    string  `json:"barUnit"`
	BarSource  string  `json:"barSource"`
	BarFlagged bool    `json:"barFlagged"`
	Direction  string  `json:"direction"`

	// The projection — always with its band. LatestBeyondHorizon means the
	// band's far edge is OPEN (the crossing may not happen): stated, never
	// clamped into false precision.
	BasisAt             time.Time `json:"basisAt"`
	CrossAt             time.Time `json:"crossAt"`
	EarliestAt          time.Time `json:"earliestAt"`
	LatestAt            time.Time `json:"latestAt"`
	LatestBeyondHorizon bool      `json:"latestBeyondHorizon"`
	TimeToCrossSeconds  float64   `json:"timeToCrossSeconds"`
	Confidence          string    `json:"confidence"`

	// AUTHORED references, cited — never paraphrased into a causal sentence.
	PrecursorPhenomena []string    `json:"precursorPhenomena"` // "known <phen> precursor per the graph"
	AtRisk             []AtRiskRow `json:"atRisk"`             // blast radius per the graph (07's walk)

	// Decomposition record (doc 09 §3.9, v1 fields).
	ContextPoints  int     `json:"contextPoints"`
	HorizonSteps   int     `json:"horizonSteps"`
	CadenceSeconds float64 `json:"cadenceSeconds"`
	GraphVersion   string  `json:"graphVersion"`
}

// SilenceRow is one quiet target with its guardrail reason — the audit trail
// that makes "no warning" a statement instead of an absence.
type SilenceRow struct {
	EntityCEI string `json:"entityCei"`
	Metric    string `json:"metric"`
	Reason    string `json:"reason"`
}

// ClockHealthRow is the Tier-B degradation panel's source (doc 14 A13).
type ClockHealthRow struct {
	Ready         bool      `json:"ready"`
	StatusCode    uint32    `json:"statusCode"`
	DegradedSince time.Time `json:"degradedSince"` // zero when healthy
}

// gateNote states why the lane is dark when forecasting is disabled.
const gateNote = "Early warnings are OFF: no forecast class is operator-visible " +
	"before its backtest calibration gate passes (doc 11 §3.5 / 09 M3). The lane " +
	"is reserved — never a fabricated future."

// BuildWarnings composes the early-warning surface from one forecast cycle's
// output plus the blast-radius resolver. Pure given its inputs; cmd/obsd
// snapshots it per forecast cycle (warm path, off the deterministic tick).
func BuildWarnings(graphVersion, graphRelease string, now time.Time, enabled bool,
	res forecast.CycleResult, unbudgeted int, clock ClockHealthRow,
	inventory []identity.InstanceRecord, atRisk func(phenomenon, anchor string) []detect.AtRisk) *WarningsView {

	byKey := make(map[string]*identity.InstanceRecord, len(inventory))
	for i := range inventory {
		byKey[inventory[i].CEI.Key()] = &inventory[i]
	}

	v := &WarningsView{
		Class: "PROJECTED", GeneratedAt: now.UTC(),
		GraphVersion: graphVersion, GraphRelease: graphRelease,
		Enabled: enabled, Warnings: []WarningCard{}, Silences: []SilenceRow{},
		Unbudgeted: unbudgeted, Clock: clock,
	}
	if !enabled {
		v.GateNote = gateNote
		return v
	}
	for i := range res.Candidates {
		c := &res.Candidates[i]
		card := WarningCard{
			Class: c.Class, IsProjection: c.IsProjection,
			EntityCEI: c.EntityCEI, Metric: c.Metric, SeriesKind: c.SeriesKind,
			BarValue: c.BarValue, BarUnit: c.BarUnit, BarSource: c.BarSource,
			BarFlagged: c.BarFlagged, Direction: c.Direction,
			BasisAt: c.BasisAt, CrossAt: c.CrossAt,
			EarliestAt: c.EarliestAt, LatestAt: c.LatestAt,
			LatestBeyondHorizon: c.LatestBeyondHorizon,
			TimeToCrossSeconds:  c.TimeToCross.Seconds(),
			Confidence:          c.Confidence,
			PrecursorPhenomena:  append([]string{}, c.PrecursorPhenomena...),
			AtRisk:              []AtRiskRow{},
			ContextPoints:       c.ContextPoints, HorizonSteps: c.HorizonSteps,
			CadenceSeconds: c.Cadence.Seconds(),
			GraphVersion:   c.GraphVersion,
		}
		if e, ok := byKey[c.EntityCEI]; ok {
			card.Namespace, card.Name, card.Kind = e.Namespace, e.Name, e.Kind
		}
		if atRisk != nil {
			seen := map[string]bool{}
			for _, phen := range c.PrecursorPhenomena {
				for _, r := range atRisk(phen, c.EntityCEI) {
					k := r.CEIKey + "\x1f" + r.Phenomenon
					if seen[k] {
						continue
					}
					seen[k] = true
					card.AtRisk = append(card.AtRisk, AtRiskRow{
						CEIKey: r.CEIKey, Phenomenon: r.Phenomenon,
						Related: r.Related, Temporal: r.Temporal, Why: r.Why,
					})
				}
			}
			sort.Slice(card.AtRisk, func(i, j int) bool {
				if card.AtRisk[i].CEIKey != card.AtRisk[j].CEIKey {
					return card.AtRisk[i].CEIKey < card.AtRisk[j].CEIKey
				}
				return card.AtRisk[i].Phenomenon < card.AtRisk[j].Phenomenon
			})
		}
		v.Warnings = append(v.Warnings, card)
	}
	// Soonest crossing first — the on-call ordering.
	sort.SliceStable(v.Warnings, func(i, j int) bool {
		if !v.Warnings[i].CrossAt.Equal(v.Warnings[j].CrossAt) {
			return v.Warnings[i].CrossAt.Before(v.Warnings[j].CrossAt)
		}
		return v.Warnings[i].EntityCEI < v.Warnings[j].EntityCEI
	})
	for _, s := range res.Silences {
		v.Silences = append(v.Silences, SilenceRow{EntityCEI: s.EntityCEI, Metric: s.Metric, Reason: s.Reason})
	}
	return v
}
