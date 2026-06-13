package governance

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
)

// Migration of bound customer graphs (doc 12 §3.4): a new release does not silently
// mutate live customers. Upgrade means RE-BINDING — the cluster is recompiled (04)
// against the new release, producing a BINDING DIFF (variables gained/lost, bars
// re-resolved, validation statuses changed, per-phenomenon observability shifted),
// which drives re-selection (06 M6) and is shown in operator-readable terms BEFORE
// activation (high-class changes especially). This package computes that diff over
// the real binding.Result structures — never a mock.

// BarChange records one (entity, variable) whose resolved bar moved across the
// upgrade — the operator-readable normative effect ("default for X: A → B").
type BarChange struct {
	Key        string  // "<cei>|<rule>|<metric>" — the binding identity
	Entity     string  // Container | Pod | Node | PVC
	Metric     string  //
	RuleID     string  //
	FromValue  float64 //
	ToValue    float64 //
	FromSource string  // config | override | default
	ToSource   string  //
	FromFlag   bool    // was the bar a flagged (lower-trust) default
	ToFlag     bool    //
}

// ValidationChange records a (entity, variable) whose semantic-QA status moved.
type ValidationChange struct {
	Key  string
	From string
	To   string
}

// ObservabilityShift records a phenomenon whose observability verdict moved.
type ObservabilityShift struct {
	PhenomenonID string
	From         string // full | partial | none
	To           string
}

// SelectionShift records an entity whose Tier-A participation changed (re-selection,
// 06 M6): which phenomena it newly participates in or no longer does.
type SelectionShift struct {
	EntityKey string
	Gained    []string // phenomenon ids gained
	Lost      []string // phenomenon ids lost
}

// Diff is the full migration diff for one customer across two releases (doc 12 §3.7
// "Binding diff").
type Diff struct {
	Customer    string
	FromVersion string
	ToVersion   string

	GainedBindings []string // "<cei>|<rule>|<metric>" present in to, absent in from
	LostBindings   []string // present in from, absent in to
	BarChanges     []BarChange
	Validation     []ValidationChange
	Observability  []ObservabilityShift
	Selection      []SelectionShift
}

// HasEffect reports whether the upgrade changes anything operator-visible.
func (d *Diff) HasEffect() bool {
	return len(d.GainedBindings) > 0 || len(d.LostBindings) > 0 || len(d.BarChanges) > 0 ||
		len(d.Validation) > 0 || len(d.Observability) > 0 || len(d.Selection) > 0
}

func bindingKey(b binding.Binding) string {
	// Container is part of the identity: a Container binding's CEIKey is the POD's
	// instance key (cAdvisor scope), so two containers in one pod share a CEIKey and
	// differ only in Container. Omitting it collapses them — masking a real per-
	// container bar change. (Pod/Node bindings have Container=="" so this is a no-op
	// for them.)
	return b.CEIKey + "|" + b.Container + "|" + b.RuleID + "|" + b.Metric
}

// BindingDiff computes the variable-level + bar-level migration diff between two
// re-binds of the SAME customer (same inventory + config; only the graph release
// differs). Bindings join on (CEI, rule, metric) — exact identity coordinates, never
// fuzzy. ResolvedAt is ignored (it is the injected compile time, not a knowledge
// change).
func BindingDiff(customer string, from, to *binding.Result) *Diff {
	d := &Diff{Customer: customer, FromVersion: from.GraphVersion, ToVersion: to.GraphVersion}

	fromB := map[string]binding.Binding{}
	for _, b := range from.Bindings {
		fromB[bindingKey(b)] = b
	}
	toB := map[string]binding.Binding{}
	for _, b := range to.Bindings {
		toB[bindingKey(b)] = b
	}

	for k, tb := range toB {
		fb, ok := fromB[k]
		if !ok {
			d.GainedBindings = append(d.GainedBindings, k)
			continue
		}
		// Bar re-resolution.
		if barMoved(fb.Bar, tb.Bar) {
			d.BarChanges = append(d.BarChanges, barChange(k, tb, fb.Bar, tb.Bar))
		}
		// Validation status.
		if fb.Validation != tb.Validation {
			d.Validation = append(d.Validation, ValidationChange{k, string(fb.Validation), string(tb.Validation)})
		}
	}
	for k := range fromB {
		if _, ok := toB[k]; !ok {
			d.LostBindings = append(d.LostBindings, k)
		}
	}

	sort.Strings(d.GainedBindings)
	sort.Strings(d.LostBindings)
	sort.Slice(d.BarChanges, func(i, j int) bool { return d.BarChanges[i].Key < d.BarChanges[j].Key })
	sort.Slice(d.Validation, func(i, j int) bool { return d.Validation[i].Key < d.Validation[j].Key })
	return d
}

func barMoved(a, b *binding.ResolvedBar) bool {
	if a == nil || b == nil {
		return a != b // one nil, one not → a bar appeared or vanished
	}
	return a.Value != b.Value || a.Source != b.Source || a.Flagged != b.Flagged || a.Direction != b.Direction
}

func barChange(key string, b binding.Binding, from, to *binding.ResolvedBar) BarChange {
	bc := BarChange{Key: key, Entity: b.Entity, Metric: b.Metric, RuleID: b.RuleID}
	if from != nil {
		bc.FromValue, bc.FromSource, bc.FromFlag = from.Value, string(from.Source), from.Flagged
	} else {
		bc.FromSource = "none"
	}
	if to != nil {
		bc.ToValue, bc.ToSource, bc.ToFlag = to.Value, string(to.Source), to.Flagged
	} else {
		bc.ToSource = "none"
	}
	return bc
}

// AddObservabilityShift folds a per-phenomenon observability comparison into the
// diff (doc 12 §3.4 "per-phenomenon observability shifted"). Caller supplies the two
// reports (computed from each graph's availability on the same platform facts).
func (d *Diff) AddObservabilityShift(from, to *binding.ObservabilityReport) {
	fromO := map[string]string{}
	for _, p := range from.PerPhenomenon {
		fromO[p.PhenomenonID] = p.Observability
	}
	toO := map[string]string{}
	for _, p := range to.PerPhenomenon {
		toO[p.PhenomenonID] = p.Observability
		old, ok := fromO[p.PhenomenonID]
		if ok && old != p.Observability {
			d.Observability = append(d.Observability, ObservabilityShift{p.PhenomenonID, old, p.Observability})
		} else if !ok {
			d.Observability = append(d.Observability, ObservabilityShift{p.PhenomenonID, "absent", p.Observability})
		}
	}
	// A phenomenon present in `from` but absent in `to` was REMOVED by the upgrade —
	// its observability shifts to "absent" (previously dropped silently).
	for _, p := range from.PerPhenomenon {
		if _, ok := toO[p.PhenomenonID]; !ok {
			d.Observability = append(d.Observability, ObservabilityShift{p.PhenomenonID, p.Observability, "absent"})
		}
	}
	sort.Slice(d.Observability, func(i, j int) bool { return d.Observability[i].PhenomenonID < d.Observability[j].PhenomenonID })
}

// AddSelectionShift folds the Tier-A re-selection delta into the diff (06 M6): which
// entities gained or lost phenomenon participation under the new release. Uses the
// same deterministic TierASet detection consumes.
func (d *Diff) AddSelectionShift(fromRes, toRes *binding.Result, fromG, toG *graph.Graph) {
	fromSel := selection.TierASet(fromRes, fromG)
	toSel := selection.TierASet(toRes, toG)
	keys := map[string]bool{}
	for k := range fromSel {
		keys[k] = true
	}
	for k := range toSel {
		keys[k] = true
	}
	kl := make([]string, 0, len(keys))
	for k := range keys {
		kl = append(kl, k)
	}
	sort.Strings(kl)
	for _, k := range kl {
		gained, lost := setDiff(fromSel[k], toSel[k])
		if len(gained) > 0 || len(lost) > 0 {
			d.Selection = append(d.Selection, SelectionShift{EntityKey: k, Gained: gained, Lost: lost})
		}
	}
}

// Human renders the diff in operator-readable terms (doc 12 §3.4: high-class changes
// surface their effect before activation). Returns "" when there is no effect.
func (d *Diff) Human() string {
	if !d.HasEffect() {
		return fmt.Sprintf("migration %s → %s on %s: no operator-visible effect (binding-neutral)",
			short(d.FromVersion), short(d.ToVersion), d.Customer)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Migration diff for %s: %s → %s\n", d.Customer, short(d.FromVersion), short(d.ToVersion))
	if len(d.GainedBindings) > 0 {
		fmt.Fprintf(&b, "  + %d new monitored (entity, variable) pair(s)\n", len(d.GainedBindings))
	}
	if len(d.LostBindings) > 0 {
		fmt.Fprintf(&b, "  - %d (entity, variable) pair(s) no longer monitored\n", len(d.LostBindings))
	}
	// Group bar re-resolutions by rule for operator readability (doc 12 §3.4: "the
	// bar for X changes ... on N workloads"). Per-workload absolute bars differ when
	// the rule is config-relative, so we summarise the count + show a few examples.
	for _, ruleID := range barChangeRules(d.BarChanges) {
		grp := barChangesForRule(d.BarChanges, ruleID)
		fmt.Fprintf(&b, "  ~ %s: %d (entity, variable) bar(s) re-resolved\n", ruleID, len(grp))
		for i, bc := range grp {
			if i >= 4 {
				fmt.Fprintf(&b, "      … and %d more\n", len(grp)-4)
				break
			}
			flagFrom, flagTo := flagNote(bc.FromFlag), flagNote(bc.ToFlag)
			fmt.Fprintf(&b, "      %s %s: %s%s [%s] → %s%s [%s]\n",
				bc.Entity, bc.Metric, humanBytes(bc.FromValue, bc.Metric), flagFrom, bc.FromSource,
				humanBytes(bc.ToValue, bc.Metric), flagTo, bc.ToSource)
		}
	}
	for _, vc := range d.Validation {
		fmt.Fprintf(&b, "  ~ validation %s: %s → %s\n", vc.Key, vc.From, vc.To)
	}
	for _, os := range d.Observability {
		fmt.Fprintf(&b, "  ~ %s observability: %s → %s\n", strings.TrimPrefix(os.PhenomenonID, "PHEN_"), os.From, os.To)
	}
	for _, ss := range d.Selection {
		if len(ss.Gained) > 0 {
			fmt.Fprintf(&b, "  + %s now participates in: %s\n", ss.EntityKey, strings.Join(shortPhen(ss.Gained), ", "))
		}
		if len(ss.Lost) > 0 {
			fmt.Fprintf(&b, "  - %s no longer participates in: %s\n", ss.EntityKey, strings.Join(shortPhen(ss.Lost), ", "))
		}
	}
	return b.String()
}

func flagNote(flagged bool) string {
	if flagged {
		return " (flagged default)"
	}
	return ""
}

// humanBytes renders a bar value as MiB for memory-bytes metrics, else the raw
// number — so a migration diff reads in operator units, not 1.693e+08.
func humanBytes(v float64, metric string) string {
	if strings.Contains(metric, "bytes") {
		return fmt.Sprintf("%.1fMi", v/(1024*1024))
	}
	return fmt.Sprintf("%.4g", v)
}

func barChangeRules(bcs []BarChange) []string {
	seen := map[string]bool{}
	var out []string
	for _, bc := range bcs {
		if !seen[bc.RuleID] {
			seen[bc.RuleID] = true
			out = append(out, bc.RuleID)
		}
	}
	sort.Strings(out)
	return out
}

func barChangesForRule(bcs []BarChange, ruleID string) []BarChange {
	var out []BarChange
	for _, bc := range bcs {
		if bc.RuleID == ruleID {
			out = append(out, bc)
		}
	}
	return out
}

func short(v string) string {
	if strings.HasPrefix(v, "sha256:") && len(v) > 19 {
		return v[:19] + "…"
	}
	return v
}

func shortPhen(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strings.TrimPrefix(id, "PHEN_")
	}
	return out
}
