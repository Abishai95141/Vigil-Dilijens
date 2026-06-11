package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

// renderBinding writes the bound-customer-graph coverage report (doc 04 §3.5) to w:
// per-rule binding states, the resolvability metric with its numerator/denominator,
// the unbounded-workload list (never hidden), a sample of resolved per-instance
// bars for the workload namespace, and the standing honesty notes. Everything shown
// is MEASURED-class: facts about the system's own visibility (doc 04 §4).
func renderBinding(w io.Writer, res *binding.Result, focusNamespace string) {
	const rule = "--------------------------------------------------------------------------------"
	fmt.Fprintln(w, rule)
	fmt.Fprintf(w, " bound customer graph (doc 04) — ontology %s · compiled %s\n",
		shortUID(strings.TrimPrefix(res.GraphVersion, "sha256:")), res.At.UTC().Format("15:04:05Z"))

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  RULE\tSCOPE\tINST\tCFG-BOUND\tDEFAULT(flagged)\tUNBOUNDED\tOUT-OF-SCOPE\tUNRESOLVED")
	for _, rc := range res.Coverage.PerRule {
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
			rc.RuleID, rc.EntityScope, rc.Instantiated, rc.ConfigBound, rc.DefaultBound, rc.Unbounded, rc.OutOfScope, rc.Unresolved)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, " resolvability: %d/%d config-eligible pairs have a config-sourced bar (%.2f) · %d flagged default bars\n",
		res.Coverage.ConfigBound, res.Coverage.ConfigEligible, res.Coverage.Resolvability, res.Coverage.DefaultBars)

	if n := len(res.Coverage.UnboundedWorkloads); n > 0 {
		fmt.Fprintf(w, " unbounded (Tier-B ineligible, %d):\n", n)
		for _, u := range res.Coverage.UnboundedWorkloads {
			fmt.Fprintf(w, "   !! %s\n", u)
		}
	}

	// Resolved config-sourced bars for the focus namespace (the workload under
	// test) and the nodes — each value auto-calibrated from that instance's OWN
	// declared config (doc 04 §3.3 axis 2).
	type barRow struct{ role, container, ruleID, bar string }
	var rows []barRow
	for _, b := range res.Bindings {
		if b.State != binding.StateBound || b.Bar == nil || b.Bar.Source != binding.SourceConfig {
			continue
		}
		switch {
		case b.Entity == "Container" && strings.Contains(b.RoleKey, "|"+focusNamespace+"|"):
			rows = append(rows, barRow{roleFromKey(b.RoleKey), b.Container, b.RuleID, humanBar(b.Bar)})
		case b.Entity == "Node":
			rows = append(rows, barRow{nodeFromKey(b.CEIKey), "", b.RuleID, humanBar(b.Bar)})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].role != rows[j].role {
			return rows[i].role < rows[j].role
		}
		return rows[i].ruleID < rows[j].ruleID
	})
	if len(rows) > 0 {
		fmt.Fprintf(w, " resolved bars (config-sourced, %s + nodes):\n", focusNamespace)
		btw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, r := range rows {
			fmt.Fprintf(btw, "   %s\t%s\t%s\t%s\n", r.role, r.container, r.ruleID, r.bar)
		}
		_ = btw.Flush()
	}

	for _, n := range res.Coverage.Notes {
		fmt.Fprintf(w, " note: %s\n", n)
	}
}

// humanBar renders a resolved bar with its provenance: value, direction, factor
// and path for config bars — the bar never hides where it came from.
func humanBar(b *binding.ResolvedBar) string {
	val := fmt.Sprintf("%.0f %s", b.Value, b.Unit)
	if b.Unit == "bytes" {
		val = fmt.Sprintf("%s (%.0f bytes)", humanBytes(b.Value), b.Value)
	}
	return fmt.Sprintf("%s %s = %s x %.2f", b.Direction, val, b.ConfigPath, b.Factor)
}

func humanBytes(v float64) string {
	const mi = 1 << 20
	return fmt.Sprintf("%.1fMi", v/mi)
}

// roleFromKey extracts the human role key from a role CEI key
// ("r|cluster|ns|Kind|Kind/name" -> "Kind/name").
func roleFromKey(key string) string {
	parts := strings.Split(key, "|")
	return parts[len(parts)-1]
}

// nodeFromKey extracts the node name from an instance CEI key
// ("i|cluster||Node|name|uid" -> "node/name").
func nodeFromKey(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 5 {
		return "node/" + parts[4]
	}
	return key
}
