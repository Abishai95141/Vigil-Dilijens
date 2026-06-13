package governance

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// ChangeClass is the blast-radius taxonomy (doc 12 §3.2). Review rigor scales with
// how far a change reaches. The ordering is meaningful: a release's class is the
// MAX over its individual changes (the widest-reaching change sets the rigor).
type ChangeClass int

const (
	// ClassNone — no semantic change to the graph (e.g. identical releases, or a
	// pure provenance-manifest edit). Never ships as a knowledge change.
	ClassNone ChangeClass = iota
	// ClassAdditiveLow — new capability only; existing behaviour untouched.
	// New equivalence variant; new signal; new entity-type metadata; reason prose.
	ClassAdditiveLow
	// ClassBehaviouralMedium — detection behaviour changes wherever the element
	// binds. New phenomenon; member/tag/span edit; relation-edge change; new
	// detection check; sensitivity-relevant metadata.
	ClassBehaviouralMedium
	// ClassNormativeHigh — the widest reach in the system: every customer on a
	// fallback default, or every binding through an equivalence group. Default-
	// threshold change; threshold-rule kind/factor change; equivalence-group
	// semantic redefinition.
	ClassNormativeHigh
)

// String renders the canonical declared-class token used in proposals and the CLI.
func (c ChangeClass) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassAdditiveLow:
		return "additive-low"
	case ClassBehaviouralMedium:
		return "behavioural-medium"
	case ClassNormativeHigh:
		return "normative-high"
	default:
		return fmt.Sprintf("ChangeClass(%d)", int(c))
	}
}

// ParseChangeClass parses a declared-class token (proposal field / CLI flag).
func ParseChangeClass(s string) (ChangeClass, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "none":
		return ClassNone, nil
	case "additive-low", "additive", "low":
		return ClassAdditiveLow, nil
	case "behavioural-medium", "behavioral-medium", "behavioural", "behavioral", "medium":
		return ClassBehaviouralMedium, nil
	case "normative-high", "normative", "high":
		return ClassNormativeHigh, nil
	default:
		return ClassNone, fmt.Errorf("unknown change class %q (additive-low|behavioural-medium|normative-high)", s)
	}
}

// ChangeItem is one derived element-level change between two releases. The Class
// is the class THIS change alone reaches; the release's class is the max over items.
type ChangeItem struct {
	Element string      // element id, e.g. "PHEN_OOM_KILL_CGROUP" or "THR_CONTAINER_MEM"
	Kind    string      // node/element kind: Signal | Phenomenon | ThresholdRule | DetectionCheck | EquivalenceGroup | Node
	Op      string      // added | removed | changed
	Class   ChangeClass // the class this change reaches
	Detail  string      // human-readable description of what changed (operator-readable)
}

// Classify diffs two loaded graph releases and derives the highest-reaching change
// class plus the per-element change list (doc 12 §3.2). `from` may be nil — the
// first release of a line — in which case every element is an addition classified
// on its own merits (a first release that introduces default bars is still
// normative). The result is deterministic: items are returned in a stable order.
//
// The class derivation is conservative-up: where a change could be read two ways,
// it takes the WIDER-reaching class, because under-classification is the release-
// blocking defect (doc 12 §3.2 — "misclassification is itself a release-blocking
// defect", and the dangerous direction is calling a normative change additive).
func Classify(from, to *graph.Graph) (ChangeClass, []ChangeItem) {
	var items []ChangeItem
	add := func(it ChangeItem) { items = append(items, it) }

	// Treat a nil `from` as an empty baseline so first-release additions classify.
	var fromSignals map[string]*graph.Signal
	var fromPhen map[string]*graph.Phenomenon
	var fromEG map[string]*graph.EquivalenceGroup
	fromRules := map[string]*graph.ThresholdRule{}
	fromChecks := map[string]map[string]*graph.MemberCheck{}
	fromNodes := map[string]nodeRef{}
	if from != nil {
		fromSignals = from.Signals
		fromPhen = from.Phenomena
		fromEG = from.EquivalenceGroups
		for _, r := range from.Rules {
			fromRules[r.ID] = r
		}
		fromChecks = indexChecks(from)
		fromNodes = indexSimpleNodes(from)
	}

	// --- Signals (doc 02 §3.3) ---------------------------------------------
	for _, id := range sortedSignalIDs(to.Signals) {
		s := to.Signals[id]
		old, ok := fromSignals[id]
		if !ok {
			add(ChangeItem{id, "Signal", "added", ClassAdditiveLow,
				"new signal " + id + " (new capability; existing behaviour untouched)"})
			continue
		}
		// A change to a signal's character (modality/data_type) shifts what binds
		// and the forecast funnel — behavioural.
		if old.Modality != s.Modality {
			add(ChangeItem{id, "Signal", "changed", ClassBehaviouralMedium,
				fmt.Sprintf("signal %s modality %q → %q", id, old.Modality, s.Modality)})
		}
		if old.DataType != s.DataType {
			add(ChangeItem{id, "Signal", "changed", ClassBehaviouralMedium,
				fmt.Sprintf("signal %s data_type %q → %q (forecast funnel character)", id, old.DataType, s.DataType)})
		}
	}
	for _, id := range sortedSignalIDs(fromSignals) {
		if _, ok := to.Signals[id]; !ok {
			add(ChangeItem{id, "Signal", "removed", ClassBehaviouralMedium,
				"removed signal " + id + " (bindings/detection referencing it change)"})
		}
	}

	// --- Phenomena (doc 02 §3.5) -------------------------------------------
	for _, id := range sortedPhenIDs(to.Phenomena) {
		p := to.Phenomena[id]
		old, ok := fromPhen[id]
		if !ok {
			add(ChangeItem{id, "Phenomenon", "added", ClassBehaviouralMedium,
				"new phenomenon " + id + " (detection behaviour changes wherever it binds)"})
			continue
		}
		classifyPhenomenonEdit(id, old, p, add)
	}
	for _, id := range sortedPhenIDs(fromPhen) {
		if _, ok := to.Phenomena[id]; !ok {
			add(ChangeItem{id, "Phenomenon", "removed", ClassBehaviouralMedium,
				"removed phenomenon " + id})
		}
	}

	// --- Threshold rules (doc 02 §3.4 / doc 04 §3.4 — bars) ----------------
	toRules := map[string]*graph.ThresholdRule{}
	for _, r := range to.Rules {
		toRules[r.ID] = r
	}
	for _, id := range sortedRuleIDs(toRules) {
		r := toRules[id]
		old, ok := fromRules[id]
		if !ok {
			// A brand-NEW rule is new detection behaviour (behavioural-medium), even
			// when it carries a flagged default: the normative-high blast radius
			// "every customer on the fallback" (doc 12 §3.2) is about CHANGING a
			// default customers already silently rely on. A new, disclosed default is
			// not yet relied upon — adding it cannot regress existing behaviour.
			detail := "new threshold rule " + id + " (adds a bar wherever it binds — new detection behaviour)"
			if r.Default != nil {
				detail = fmt.Sprintf("new threshold rule %s with a flagged ontology default %.4g (new, disclosed fallback — not a change to existing normativity)", id, *r.Default)
			}
			add(ChangeItem{id, "ThresholdRule", "added", ClassBehaviouralMedium, detail})
			continue
		}
		classifyRuleEdit(id, old, r, add)
	}
	for _, id := range sortedRuleIDs(fromRules) {
		if _, ok := toRules[id]; !ok {
			cls := ClassBehaviouralMedium
			detail := "removed threshold rule " + id
			if fromRules[id].Default != nil {
				cls = ClassNormativeHigh
				detail = "removed threshold rule " + id + " (withdraws a fleet-wide fallback bar)"
			}
			add(ChangeItem{id, "ThresholdRule", "removed", cls, detail})
		}
	}

	// --- Detection member-checks (doc 07 §3.1) -----------------------------
	toChecks := indexChecks(to)
	classifyChecks(fromChecks, toChecks, add)

	// --- Equivalence groups (doc 02 §3.1) ----------------------------------
	for _, id := range sortedEGIDs(to.EquivalenceGroups) {
		eg := to.EquivalenceGroups[id]
		old, ok := fromEG[id]
		if !ok {
			add(ChangeItem{id, "EquivalenceGroup", "added", ClassAdditiveLow,
				"new equivalence group " + id})
			continue
		}
		classifyEquivalenceEdit(id, old, eg, add)
	}
	for _, id := range sortedEGIDs(fromEG) {
		if _, ok := to.EquivalenceGroups[id]; !ok {
			add(ChangeItem{id, "EquivalenceGroup", "removed", ClassNormativeHigh,
				"removed equivalence group " + id + " (every binding through it changes)"})
		}
	}

	// --- Simple metadata nodes (Entity/Tool/Gotcha/Capability/Distro/Agent) -
	toNodes := indexSimpleNodes(to)
	for _, id := range sortedNodeKeys(toNodes) {
		if _, ok := fromNodes[id]; !ok {
			n := toNodes[id]
			add(ChangeItem{n.id, n.typ, "added", ClassAdditiveLow,
				"new " + n.typ + " metadata " + n.id})
		}
	}
	for _, id := range sortedNodeKeys(fromNodes) {
		if _, ok := toNodes[id]; !ok {
			n := fromNodes[id]
			// Removing a referenced metadata node can break references — behavioural.
			add(ChangeItem{n.id, n.typ, "removed", ClassBehaviouralMedium,
				"removed " + n.typ + " metadata " + n.id})
		}
	}

	// Max class over all items.
	max := ClassNone
	for _, it := range items {
		if it.Class > max {
			max = it.Class
		}
	}
	// Stable, operator-readable ordering: by reach (widest first), then id.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Class != items[j].Class {
			return items[i].Class > items[j].Class
		}
		if items[i].Element != items[j].Element {
			return items[i].Element < items[j].Element
		}
		return items[i].Detail < items[j].Detail
	})
	return max, items
}

func classifyPhenomenonEdit(id string, old, p *graph.Phenomenon, add func(ChangeItem)) {
	if old.Span != p.Span {
		add(ChangeItem{id, "Phenomenon", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("phenomenon %s span %q → %q (the topological walk changes)", id, old.Span, p.Span)})
	}
	if old.Anchor != p.Anchor {
		add(ChangeItem{id, "Phenomenon", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("phenomenon %s anchor %q → %q", id, old.Anchor, p.Anchor)})
	}
	if !equalStrings(old.TraversalEdgeTypes, p.TraversalEdgeTypes) {
		add(ChangeItem{id, "Phenomenon", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("phenomenon %s traversal edges %v → %v", id, old.TraversalEdgeTypes, p.TraversalEdgeTypes)})
	}
	if !equalMembers(old.Members, p.Members) {
		add(ChangeItem{id, "Phenomenon", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("phenomenon %s membership changed (%s → %s)", id, memberSig(old.Members), memberSig(p.Members))})
	}
	if !equalRelations(old.Relations, p.Relations) {
		add(ChangeItem{id, "Phenomenon", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("phenomenon %s relations changed (cascade/blast-radius edges)", id)})
	}
	// Reason prose / label only — surfaced verbatim but no detection effect.
	if old.Notes != p.Notes || old.Label != p.Label {
		add(ChangeItem{id, "Phenomenon", "changed", ClassAdditiveLow,
			"phenomenon " + id + " reason/label prose edited (surfaced verbatim; no detection change)"})
	}
}

func classifyRuleEdit(id string, old, r *graph.ThresholdRule, add func(ChangeItem)) {
	// Any change to a rule's *evaluative* fields reaches the widest: a rule feeds a
	// bar, and a default bar is the fleet-wide fallback (doc 12 §3.2 normative-high).
	if !equalFloatPtr(old.Default, r.Default) {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s DEFAULT %s → %s (fleet-wide fallback bar; every customer on the fallback)", id, floatPtr(old.Default), floatPtr(r.Default))})
	}
	if old.Factor != r.Factor {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s factor %.4g → %.4g (every binding through the rule re-resolves)", id, old.Factor, r.Factor)})
	}
	if old.Kind != r.Kind {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s kind %q → %q", id, old.Kind, r.Kind)})
	}
	if old.ConfigPath != r.ConfigPath {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s config_path %q → %q (the bar reads a different config field)", id, old.ConfigPath, r.ConfigPath)})
	}
	if old.Signal != r.Signal {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s signal %q → %q (the bar is re-pointed to a different signal)", id, old.Signal, r.Signal)})
	}
	if old.Eligibility != r.Eligibility {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s eligibility_config_path %q → %q (which instances get the bar changes)", id, old.Eligibility, r.Eligibility)})
	}
	if old.Direction != r.Direction {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s direction %q → %q (which side is the violation)", id, old.Direction, r.Direction)})
	}
	if old.DivisorMetric != r.DivisorMetric {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s divisor %q → %q (the evaluated ratio changes)", id, old.DivisorMetric, r.DivisorMetric)})
	}
	if old.EntityScope != r.EntityScope {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("rule %s entity_scope %q → %q (instantiation fan-out)", id, old.EntityScope, r.EntityScope)})
	}
	if old.Metric != r.Metric {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassBehaviouralMedium,
			fmt.Sprintf("rule %s metric %q → %q", id, old.Metric, r.Metric)})
	}
	if old.Window != r.Window {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassNormativeHigh,
			fmt.Sprintf("rule %s window %q → %q (evaluation window of the bar)", id, old.Window, r.Window)})
	}
	if old.Rationale != r.Rationale {
		add(ChangeItem{id, "ThresholdRule", "changed", ClassAdditiveLow,
			"rule " + id + " rationale prose edited (no evaluative change)"})
	}
}

func classifyEquivalenceEdit(id string, old, eg *graph.EquivalenceGroup, add func(ChangeItem)) {
	addedPatterns, removedPatterns := setDiff(old.Patterns, eg.Patterns)
	if len(removedPatterns) > 0 {
		add(ChangeItem{id, "EquivalenceGroup", "changed", ClassNormativeHigh,
			fmt.Sprintf("equivalence group %s removed patterns %v (semantic redefinition — every binding through it)", id, removedPatterns)})
	}
	if len(addedPatterns) > 0 {
		// A new variant is additive (doc 12 §3.2 — "new equivalence variant").
		add(ChangeItem{id, "EquivalenceGroup", "changed", ClassAdditiveLow,
			fmt.Sprintf("equivalence group %s new variant patterns %v", id, addedPatterns)})
	}
	if old.CanonicalOTel != eg.CanonicalOTel {
		add(ChangeItem{id, "EquivalenceGroup", "changed", ClassNormativeHigh,
			fmt.Sprintf("equivalence group %s canonical %q → %q (semantic redefinition)", id, old.CanonicalOTel, eg.CanonicalOTel)})
	}
}

// --- check indexing + diff -------------------------------------------------

func indexChecks(g *graph.Graph) map[string]map[string]*graph.MemberCheck {
	out := map[string]map[string]*graph.MemberCheck{}
	for phen, checks := range g.Checks {
		m := map[string]*graph.MemberCheck{}
		for _, c := range checks {
			m[c.Signal] = c
		}
		out[phen] = m
	}
	return out
}

func classifyChecks(from, to map[string]map[string]*graph.MemberCheck, add func(ChangeItem)) {
	phens := map[string]bool{}
	for p := range from {
		phens[p] = true
	}
	for p := range to {
		phens[p] = true
	}
	pl := make([]string, 0, len(phens))
	for p := range phens {
		pl = append(pl, p)
	}
	sort.Strings(pl)
	for _, phen := range pl {
		fc, tc := from[phen], to[phen]
		sigs := map[string]bool{}
		for s := range fc {
			sigs[s] = true
		}
		for s := range tc {
			sigs[s] = true
		}
		sl := make([]string, 0, len(sigs))
		for s := range sigs {
			sl = append(sl, s)
		}
		sort.Strings(sl)
		for _, sig := range sl {
			o, oo := fc[sig], tc[sig]
			switch {
			case oo == nil:
				add(ChangeItem{phen, "DetectionCheck", "removed", ClassBehaviouralMedium,
					fmt.Sprintf("detection check on %s for %s removed", phen, sig)})
			case o == nil:
				add(ChangeItem{phen, "DetectionCheck", "added", ClassBehaviouralMedium,
					fmt.Sprintf("detection check on %s for %s added (%s/%s)", phen, sig, oo.Facet, oo.Expect)})
			case !equalCheck(o, oo):
				add(ChangeItem{phen, "DetectionCheck", "changed", ClassBehaviouralMedium,
					fmt.Sprintf("detection check on %s for %s changed", phen, sig)})
			}
		}
	}
}

func equalCheck(a, b *graph.MemberCheck) bool {
	return a.Metric == b.Metric && a.Facet == b.Facet && a.Expect == b.Expect &&
		a.MinState == b.MinState && a.On == b.On && a.Note == b.Note
}

// --- simple-node indexing --------------------------------------------------

type nodeRef struct {
	id  string
	typ string
}

func indexSimpleNodes(g *graph.Graph) map[string]nodeRef {
	out := map[string]nodeRef{}
	for id := range g.Entities {
		out["Entity/"+id] = nodeRef{id, "Entity"}
	}
	for id := range g.Tools {
		out["Tool/"+id] = nodeRef{id, "Tool"}
	}
	for id := range g.Gotchas {
		out["Gotcha/"+id] = nodeRef{id, "Gotcha"}
	}
	for id := range g.Capabilities {
		out["Capability/"+id] = nodeRef{id, "CapabilityPrereq"}
	}
	for id := range g.DistroGates {
		out["Distro/"+id] = nodeRef{id, "DistroVersionGate"}
	}
	for id := range g.Agents {
		out["Agent/"+id] = nodeRef{id, "Agent"}
	}
	for id := range g.Modalities {
		out["Modality/"+id] = nodeRef{id, "Modality"}
	}
	return out
}

// --- comparison + sorting helpers ------------------------------------------

func sortedSignalIDs(m map[string]*graph.Signal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func sortedPhenIDs(m map[string]*graph.Phenomenon) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func sortedEGIDs(m map[string]*graph.EquivalenceGroup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func sortedRuleIDs(m map[string]*graph.ThresholdRule) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func sortedNodeKeys(m map[string]nodeRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func setDiff(old, neu []string) (added, removed []string) {
	o := map[string]bool{}
	for _, x := range old {
		o[x] = true
	}
	n := map[string]bool{}
	for _, x := range neu {
		n[x] = true
	}
	for x := range n {
		if !o[x] {
			added = append(added, x)
		}
	}
	for x := range o {
		if !n[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func memberSig(ms []graph.Member) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.SignalID+":"+m.Role+":"+m.TemporalOrder)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func equalMembers(a, b []graph.Member) bool { return memberSig(a) == memberSig(b) }

func relationSig(rs []graph.Relation) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, r.TargetID+":"+r.Role+":"+r.TemporalOrder)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func equalRelations(a, b []graph.Relation) bool { return relationSig(a) == relationSig(b) }

func equalFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func floatPtr(p *float64) string {
	if p == nil {
		return "none"
	}
	return fmt.Sprintf("%.4g", *p)
}
