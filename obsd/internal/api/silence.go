package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

// The silence ledger — deterministic ABSENCE (the MCP harness's lead feature).
//
// An LLM is structurally bad at one thing Vigil is structurally good at: stating a
// provable NEGATIVE. Ask a model "what are we NOT watching, and why" and it will
// confabulate a plausible answer; ask Vigil and it can enumerate every (entity,
// variable) pair the binding compiler produced and say, for each, whether it is
// WATCHED or SILENT and the exact reason. "Every (entity,variable) pair has a
// visible state" (doc 01) made callable.
//
// PROVENANCE. The ledger is MEASURED about the system's own coverage — a pure,
// deterministic re-projection of the bound graph (binding.Result). It invents no
// bar, learns no value, and asserts no cause. Same Result ⇒ byte-identical ledger.
//
// COMPLETENESS BY CONSTRUCTION. Every binding lands in exactly one bucket (watched
// or one silence class), so TotalPairs == Watched + Silent == len(res.Bindings) and
// no pair can be silently dropped. The silence classes reconcile exactly with the
// per-rule coverage counts the binding compiler computes independently (see
// silence_test.go) — the ledger cannot pass review as a convenient subset.

// Silence reason classes — the exhaustive vocabulary of WHY a pair is not watched.
const (
	// SilenceUnbounded: bound, but no declared limit ⇒ no crossable bar (Tier-B
	// ineligible). Borrowed normativity: where the customer declared nothing, we say
	// so rather than invent a bar.
	SilenceUnbounded = "unbounded"
	// SilenceNoStreamKey: the bar resolves, but no observation stream is ingested for
	// this pair (e.g. the PVC dark-bar — a bound bar with no scraped series yet). The
	// pair is dark for an ingest reason, not a normativity one.
	SilenceNoStreamKey = "no-stream-key"
	// SilenceUnresolved: the entity's config row could not be read at compile time —
	// the bar is unresolvable, stated never guessed.
	SilenceUnresolved = "unresolved"
	// SilenceOutOfScope: excluded with a stated reason (an eligibility gate or an
	// unobtainable signal on this cluster).
	SilenceOutOfScope = "out-of-scope"
)

// SilenceLedgerView is the ABSENCE payload. The TS mirror (if surfaced) follows this
// shape; the MCP server returns it verbatim as a tool result.
type SilenceLedgerView struct {
	Class        string               `json:"class"` // MEASURED — about own coverage
	GeneratedAt  time.Time            `json:"generatedAt"`
	GraphVersion string               `json:"graphVersion"`
	GraphRelease string               `json:"graphRelease"`
	Available    bool                 `json:"available"` // false ⇒ binding not compiled (honest empty state)
	Summary      SilenceLedgerSummary `json:"summary"`
	Silent       []SilenceLedgerRow   `json:"silent"`
	Note         string               `json:"note"`
}

// SilenceLedgerSummary is the headline rollup. ByReason is keyed by the silence
// classes above; encoding/json emits map keys sorted, so the JSON is deterministic.
type SilenceLedgerSummary struct {
	TotalPairs int            `json:"totalPairs"`
	Watched    int            `json:"watched"`
	Silent     int            `json:"silent"`
	ByReason   map[string]int `json:"byReason"`
}

// SilenceLedgerRow is one (entity, variable) pair that is NOT watched, with its
// verbatim reason from the binding compiler (never a generated explanation).
type SilenceLedgerRow struct {
	EntityCEI   string `json:"entityCei"`
	RoleKey     string `json:"roleKey,omitempty"`
	Entity      string `json:"entity"` // Container | Pod | Node | PVC
	Container   string `json:"container,omitempty"`
	RuleID      string `json:"ruleId"`
	Metric      string `json:"metric"`
	State       string `json:"state"`       // bound | unresolved | out-of-scope
	ReasonClass string `json:"reasonClass"` // one of the Silence* classes
	Reason      string `json:"reason"`      // the binding's verbatim honest reason
}

const silenceNote = "Deterministic absence: every (entity,variable) pair the binding produced is either watched or listed here with a reason. MEASURED about the system's own coverage — no bar is invented, no value is learned, no cause is asserted."

// BuildSilenceLedger composes the ledger from the bound customer graph. Pure given
// its inputs; cmd/obsd snapshots it from the SAME binding.Result the Coverage
// surface uses, in the same tick, so the two surfaces can never disagree.
func BuildSilenceLedger(graphVersion, graphRelease string, now time.Time, res *binding.Result) *SilenceLedgerView {
	v := &SilenceLedgerView{
		Class: "MEASURED", GeneratedAt: now.UTC(),
		GraphVersion: graphVersion, GraphRelease: graphRelease,
		Summary: SilenceLedgerSummary{ByReason: map[string]int{}},
		Silent:  []SilenceLedgerRow{},
		Note:    silenceNote,
	}
	if res == nil {
		v.Available = false
		v.Note = "Binding has not compiled yet — the silence ledger is unavailable. Connect a cluster and load the ontology."
		return v
	}
	v.Available = true

	for i := range res.Bindings {
		b := &res.Bindings[i]
		watched, class := classifySilence(b)
		v.Summary.TotalPairs++
		if watched {
			v.Summary.Watched++
			continue
		}
		v.Summary.Silent++
		v.Summary.ByReason[class]++
		v.Silent = append(v.Silent, SilenceLedgerRow{
			EntityCEI: b.CEIKey, RoleKey: b.RoleKey, Entity: b.Entity,
			Container: b.Container, RuleID: b.RuleID, Metric: b.Metric,
			State: string(b.State), ReasonClass: class, Reason: silenceReason(b, class),
		})
	}

	sort.Slice(v.Silent, func(i, j int) bool {
		a, c := &v.Silent[i], &v.Silent[j]
		if a.EntityCEI != c.EntityCEI {
			return a.EntityCEI < c.EntityCEI
		}
		if a.RuleID != c.RuleID {
			return a.RuleID < c.RuleID
		}
		if a.Container != c.Container {
			return a.Container < c.Container
		}
		return a.Metric < c.Metric
	})
	return v
}

// classifySilence partitions one binding: watched, or silent with its class. The
// partition is total — every State is handled, and an unknown State is treated as
// silent (unresolved) rather than silently counted as watched (fail closed).
func classifySilence(b *binding.Binding) (watched bool, class string) {
	switch b.State {
	case binding.StateOutOfScope:
		return false, SilenceOutOfScope
	case binding.StateUnresolved:
		return false, SilenceUnresolved
	case binding.StateBound:
		if b.Bar == nil {
			return false, SilenceUnbounded
		}
		if b.StreamUID() == "" {
			return false, SilenceNoStreamKey
		}
		return true, ""
	default:
		return false, SilenceUnresolved
	}
}

// silenceReason prefers the binding's own verbatim reason; where none was recorded
// (a bound bar with no stream key carries no compiler reason) it states the class
// plainly. Never a generated explanation.
func silenceReason(b *binding.Binding, class string) string {
	if b.Reason != "" {
		return b.Reason
	}
	switch class {
	case SilenceNoStreamKey:
		return "bar resolves but no observation stream is ingested for this pair yet (no stream key)"
	case SilenceUnbounded:
		return "unbounded: no declared limit — Tier-B ineligible, no bar to cross"
	case SilenceOutOfScope:
		return "out-of-scope on this cluster"
	case SilenceUnresolved:
		return "unresolved: config not readable at compile time"
	}
	return class
}
