package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
)

// CoverageView is the Coverage Report payload (doc 10 M1): the honest map of what
// Vigil can and cannot watch on this cluster. Every (entity, variable) pair has a
// visible state; gaps are stated, never blank. The TS type in
// web/src/surfaces/coverage-types.ts mirrors this shape exactly.
type CoverageView struct {
	ClusterID    string           `json:"clusterId"`
	GraphVersion string           `json:"graphVersion"`
	GeneratedAt  time.Time        `json:"generatedAt"`
	Available    bool             `json:"available"` // false => binding not yet compiled (honest empty state)
	Summary      CoverageSummary  `json:"summary"`
	Phenomena    []PhenomenonRow  `json:"phenomena"`
	Rules        []RuleRow        `json:"rules"`
	Selection    SelectionSummary `json:"selection"`
	Unbounded    []string         `json:"unbounded"` // bar-less pairs: Tier-B-ineligible, listed
	Caveats      []string         `json:"caveats"`   // standing honesty notes (pending milestones)
}

// CoverageSummary is the headline rollup.
type CoverageSummary struct {
	Entities         int     `json:"entities"`
	TierA            int     `json:"tierA"`
	Resolvability    float64 `json:"resolvability"`
	ConfigBound      int     `json:"configBound"`
	ConfigEligible   int     `json:"configEligible"`
	DefaultBars      int     `json:"defaultBars"`
	PhenomenaFull    int     `json:"phenomenaFull"`
	PhenomenaPartial int     `json:"phenomenaPartial"`
	PhenomenaNone    int     `json:"phenomenaNone"`
	QAVerified       int     `json:"qaVerified"`
	QASuspect        int     `json:"qaSuspect"`
	QAFailed         int     `json:"qaFailed"`
}

// PhenomenonRow is one phenomenon's observability on this cluster (doc 04 M5 /
// 05 M4). Observability is MEASURED about the system's own coverage.
type PhenomenonRow struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Observability  string   `json:"observability"` // full | partial | none
	RequiredTotal  int      `json:"requiredTotal"`
	RequiredOk     int      `json:"requiredObservable"`
	MissingReasons []string `json:"missingReasons"`
}

// RuleRow is one threshold rule's binding coverage (doc 04 §3.5).
type RuleRow struct {
	RuleID       string `json:"ruleId"`
	Kind         string `json:"kind"`
	EntityScope  string `json:"entityScope"`
	Instantiated int    `json:"instantiated"`
	ConfigBound  int    `json:"configBound"`
	DefaultBound int    `json:"defaultBound"` // flagged
	Unbounded    int    `json:"unbounded"`
	OutOfScope   int    `json:"outOfScope"`
	Unresolved   int    `json:"unresolved"`
}

// SelectionSummary is the attention rollup (doc 06 M1).
type SelectionSummary struct {
	TierA        int            `json:"tierA"`
	NoneByReason map[string]int `json:"noneByReason"`
}

// unavailableCoverage is the honest empty state: binding has not compiled yet (no
// cluster, ontology absent, or first tick pending). The surface says exactly that
// rather than rendering a misleading all-zero map.
func unavailableCoverage(clusterID, graphVersion string, now time.Time) *CoverageView {
	return &CoverageView{
		ClusterID: clusterID, GraphVersion: graphVersion, GeneratedAt: now.UTC(),
		Available: false,
		Selection: SelectionSummary{NoneByReason: map[string]int{}},
		Caveats:   []string{"Binding has not compiled yet — coverage is unavailable. Connect a cluster and load the ontology."},
	}
}

// BuildCoverage composes the view from the runtime's current state. Pure given
// its inputs; cmd/obsd snapshots it each tick.
func BuildCoverage(clusterID, graphVersion string, now time.Time,
	res *binding.Result, obs *binding.ObservabilityReport, sel *selection.Result) *CoverageView {

	if res == nil {
		return unavailableCoverage(clusterID, graphVersion, now)
	}
	cov := res.Coverage
	v := &CoverageView{
		ClusterID: clusterID, GraphVersion: graphVersion, GeneratedAt: now.UTC(),
		Available: true,
		Summary: CoverageSummary{
			Resolvability: cov.Resolvability, ConfigBound: cov.ConfigBound,
			ConfigEligible: cov.ConfigEligible, DefaultBars: cov.DefaultBars,
			QAVerified: cov.Validation.Verified, QASuspect: cov.Validation.Suspect,
			QAFailed: cov.Validation.Failed,
		},
		Selection: SelectionSummary{NoneByReason: map[string]int{}},
		Unbounded: append([]string(nil), cov.UnboundedWorkloads...),
		Caveats:   append([]string(nil), cov.Notes...),
	}

	for _, rc := range cov.PerRule {
		v.Rules = append(v.Rules, RuleRow{
			RuleID: rc.RuleID, Kind: rc.Kind, EntityScope: rc.EntityScope,
			Instantiated: rc.Instantiated, ConfigBound: rc.ConfigBound, DefaultBound: rc.DefaultBound,
			Unbounded: rc.Unbounded, OutOfScope: rc.OutOfScope, Unresolved: rc.Unresolved,
		})
	}
	sort.Slice(v.Rules, func(i, j int) bool { return v.Rules[i].RuleID < v.Rules[j].RuleID })

	if obs != nil {
		v.Summary.PhenomenaFull = obs.Full
		v.Summary.PhenomenaPartial = obs.Partial
		v.Summary.PhenomenaNone = obs.None
		for _, pc := range obs.PerPhenomenon {
			v.Phenomena = append(v.Phenomena, PhenomenonRow{
				ID: pc.PhenomenonID, Label: pc.Label, Observability: pc.Observability,
				RequiredTotal: pc.RequiredTotal, RequiredOk: pc.RequiredObtainable,
				MissingReasons: append([]string(nil), pc.MissingReasons...),
			})
		}
		sort.Slice(v.Phenomena, func(i, j int) bool {
			// full first? No — surface the gaps: none, then partial, then full.
			ri, rj := obsRank(v.Phenomena[i].Observability), obsRank(v.Phenomena[j].Observability)
			if ri != rj {
				return ri < rj
			}
			return v.Phenomena[i].ID < v.Phenomena[j].ID
		})
	}

	if sel != nil {
		v.Summary.Entities = len(sel.Records)
		v.Summary.TierA = sel.TierACount
		v.Selection.TierA = sel.TierACount
		for r, n := range sel.NoneByReason {
			v.Selection.NoneByReason[string(r)] = n
		}
	}
	return v
}

func obsRank(o string) int {
	switch o {
	case "none":
		return 0
	case "partial":
		return 1
	default:
		return 2
	}
}
