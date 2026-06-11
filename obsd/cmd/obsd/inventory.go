package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// boutiqueNamespace is surfaced first in the inventory because it is the Phase-0
// workload under test (doc 14 §3.3); everything else is shown after it so coverage
// stays honest (the join audit is cluster-wide, not boutique-only).
const boutiqueNamespace = "online-boutique"

// podRow is one joined pod instance as it will be printed.
type podRow struct {
	role  string // durable role key, e.g. "Deployment/frontend"
	name  string // instance (pod) name
	uid   string // instance UID (the per-instance anchor)
	node  string // runs-on node placement ("—" if not yet asserted)
	flags string // honesty flags: bare / rs-anchored / suspect-runs-on / unresolved-role
}

// renderInventory writes a live, correctly-joined entity inventory of the cluster to
// w: active pod instances grouped by their durable ROLE (the two-layer identity join,
// doc 03 §3.1), each with its runs-on node placement from the topology edge store,
// then the nodes, then a cluster-wide rollup and the Phase-0a join-audit verdict.
//
// This is "prerequisite zero, observable" — the demo target in CLAUDE.md: the inventory
// obsd reconstructs from the API server alone. It surfaces, never fuses: every line is
// MEASURED (a fact read from the store, or a deterministic consequence of facts). It
// prints honest partial coverage — bare pods, ReplicaSet-anchored (degraded) roles,
// suspect edges, unresolved roles, mis-joins — rather than hiding any of them.
func renderInventory(w io.Writer, active []identity.InstanceRecord, edges *identity.EdgeStore, clusterID string, now time.Time, rep identity.ConsistencyReport, gate identity.GateResult) {
	window := identity.TimeWindow{Start: now, End: now}

	// Partition active instances into role-bearing pods (grouped namespace->role) and
	// role-less nodes. Nothing is silently dropped: any unexpected kind still lands in
	// the namespaced pod grouping with its kind visible in the role column.
	byNs := map[string]map[string][]podRow{}
	var nodes []identity.InstanceRecord
	var bareCount, degradedCount, unresolvedCount, podCount int

	for _, r := range active {
		if r.Kind == "Node" {
			nodes = append(nodes, r)
			continue
		}
		podCount++
		role := r.RoleCEI.RoleKey
		var flags []string
		switch {
		case role == "":
			role = "(" + r.Kind + ":unresolved-role)"
			unresolvedCount++
			flags = append(flags, "unresolved-role")
		case r.RoleCEI.Bare:
			bareCount++
			flags = append(flags, "bare")
		case r.RoleCEI.Kind == "ReplicaSet":
			// Owner chain stopped at the ReplicaSet — the Deployment owner had not
			// reached the informer cache when the role was minted. The role is still a
			// correct join key, but it is not rollout-stable; surface it as degraded.
			degradedCount++
			flags = append(flags, "rs-anchored(degraded)")
		}

		row := podRow{role: role, name: r.Name, uid: shortUID(r.UID), node: "—"}
		// runs-on node placement (prefer a Valid edge; fall back to a suspect one).
		var suspect bool
		for _, nb := range edges.Neighbours(identity.EdgeRunsOn, r.CEI.Key(), window) {
			row.node = nb.To.Name
			suspect = nb.Result == identity.TraversalSuspect
			if !suspect {
				break
			}
		}
		if suspect {
			flags = append(flags, "suspect-runs-on")
		}
		row.flags = strings.Join(flags, ",")

		if byNs[r.Namespace] == nil {
			byNs[r.Namespace] = map[string][]podRow{}
		}
		byNs[r.Namespace][role] = append(byNs[r.Namespace][role], row)
	}

	const bar = "================================================================================"
	const rule = "--------------------------------------------------------------------------------"
	fmt.Fprintln(w, bar)
	fmt.Fprintf(w, " Vigil — live identity inventory   cluster=%s   t=%s   synced=%t\n",
		shortUID(clusterID), now.UTC().Format(time.RFC3339), rep.Ready)
	fmt.Fprintln(w, bar)

	for _, ns := range orderedNamespaces(byNs) {
		roles := byNs[ns]
		nsPods := 0
		for _, rows := range roles {
			nsPods += len(rows)
		}
		fmt.Fprintf(w, "namespace: %s  —  %d roles · %d pods\n", ns, len(roles), nsPods)
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  ROLE (durable)\tPOD INSTANCE\tNODE\tFLAGS")
		for _, role := range sortedKeys(roles) {
			rows := roles[role]
			sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
			for _, row := range rows {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", row.role, row.name, row.node, row.flags)
			}
		}
		_ = tw.Flush()
	}

	// Nodes (role-less infrastructure instances).
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	fmt.Fprintf(w, "nodes (%d):\n", len(nodes))
	ntw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, n := range nodes {
		live := edges.NodeLiveness(n.CEI.Key(), now)
		fmt.Fprintf(ntw, "  %s\t%s\tlease=%s\n", n.Name, shortUID(n.UID), live)
	}
	_ = ntw.Flush()

	// Cluster-wide rollup + the Phase-0a join-audit verdict.
	em := edges.Metrics()
	verdict := "PASS"
	if !rep.Ready {
		verdict = "NOT-READY"
	} else if !gate.Passed {
		verdict = "FAIL"
	}
	fmt.Fprintln(w, rule)
	fmt.Fprintf(w, " cluster totals: %d pods in %d roles across %d namespaces · %d bare · %d degraded · %d unresolved · %d nodes\n",
		podCount, totalRoles(byNs), len(byNs), bareCount, degradedCount, unresolvedCount, len(nodes))
	fmt.Fprintf(w, " topology edges: live=%d suspect=%d retracted=%d\n", em.Live, em.Suspect, em.Retracted)
	fmt.Fprintf(w, " phase-0a join audit: checked=%d mis-joins=%d missing=%d accuracy=%.4f coverage=%.4f → GATE %s\n",
		rep.CheckedPods+rep.CheckedNodes, rep.Misjoins, rep.Missing, gate.JoinAccuracy, gate.Coverage, verdict)
	if rep.Misjoins > 0 {
		fmt.Fprintf(w, " !! MIS-JOINS (the silent killer): %v\n", rep.Details)
	}
	fmt.Fprintln(w, bar)
}

// orderedNamespaces returns the namespaces with the boutique workload first (it is the
// workload under test), then the rest alphabetically.
func orderedNamespaces(byNs map[string]map[string][]podRow) []string {
	out := make([]string, 0, len(byNs))
	for ns := range byNs {
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == boutiqueNamespace) != (out[j] == boutiqueNamespace) {
			return out[i] == boutiqueNamespace
		}
		return out[i] < out[j]
	})
	return out
}

func sortedKeys(m map[string][]podRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func totalRoles(byNs map[string]map[string][]podRow) int {
	n := 0
	for _, roles := range byNs {
		n += len(roles)
	}
	return n
}

// shortUID trims a UID (or cluster id) to its first 8 characters for display; the full
// value remains the join key, this is cosmetic only.
func shortUID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "…"
}
