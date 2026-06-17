package api

import (
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// TopologyView is the topology surface payload (doc 10 §3.2, M3): the bound
// customer graph rendered live — entities as nodes, valid edges as links
// (suspect edges visibly distinct), with the CURRENT-condition marks overlaid
// (matched phenomena, loud entities). Predictive marks are a SEPARATE visual
// language added in M5/Phase 2 — there are none here, by construction, so "is"
// and "might" cannot be confused. The TS type mirrors this shape.
type TopologyView struct {
	ClusterID    string          `json:"clusterId"`
	GraphVersion string          `json:"graphVersion"`
	GeneratedAt  time.Time       `json:"generatedAt"`
	Nodes        []TopoNode      `json:"nodes"`
	Edges        []TopoEdge      `json:"edges"`
	Summary      TopologySummary `json:"summary"`
	Truncated    int             `json:"truncated"` // entities omitted past the cap (stated, never silent)
}

// TopologySummary is the headline rollup.
type TopologySummary struct {
	Nodes        int `json:"nodes"`
	Edges        int `json:"edges"`
	ValidEdges   int `json:"validEdges"`
	SuspectEdges int `json:"suspectEdges"`
	Matched      int `json:"matched"`  // nodes carrying a current phenomenon match
	Loud         int `json:"loud"`     // nodes carrying an unexplained loud card
	Selected     int `json:"selected"` // Tier-A nodes (selection earned them attention)
	Warned       int `json:"warned"`   // nodes carrying a PROJECTED early warning (separate glyph family)
}

// TopoNode is one WORKLOAD (a Deployment/DaemonSet/StatefulSet role, or a Node).
// The graph is workload-centric: an operator reasons about services, not the many
// pods behind them. Pods roll up to their role (identity.RoleCEI); marks are the
// OR across the workload's pods. Marks are MEASURED facts about current state; each
// is a labelled boolean so the surface renders distinct glyphs.
type TopoNode struct {
	CEIKey    string   `json:"ceiKey"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Layer     string   `json:"layer,omitempty"`    // workload | node | service | storage — for view-mode filtering
	Replicas  int      `json:"replicas,omitempty"` // pods rolled into this workload (0 for a Node)
	Selected  bool     `json:"selected"`           // Tier-A (06)
	Matched   bool     `json:"matched"`            // a current phenomenon match (07) — "is"
	Degraded  bool     `json:"degraded"`           // the match(es) here are degraded
	Loud      bool     `json:"loud"`               // an unexplained loud card (08)
	Warned    bool     `json:"warned"`             // an early-warning target (09) — "might", a SEPARATE visual language (10 M5)
	Phenomena []string `json:"phenomena"`          // matched phenomenon ids on this workload
}

// TopoEdge is one topology edge with its validity verdict at `now`. A suspect
// edge is rendered visibly distinct (doc 03/10): detection degrades across it,
// it never silently supports.
type TopoEdge struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"` // valid | suspect | retracted
}

// topoCap bounds the rendered node set so a large cluster cannot produce an
// unusable surface; the omitted count is stated (Truncated), never hidden.
const topoCap = 400

// BuildTopology composes the topology surface from the inventory, the edge
// snapshot, the current findings + unexplained cards, and the Tier-A set. Pure
// given its inputs; cmd/obsd snapshots it each tick. budgets are the per-edge-
// type staleness budgets (doc 14 §1.2) — a live edge confirmed longer ago than
// its budget is SUSPECT, mirroring EdgeStore.Status exactly.
func BuildTopology(clusterID, graphVersion string, now time.Time,
	inventory []identity.InstanceRecord, edgeSnap []identity.EdgeSnap, budgets map[string]time.Duration,
	findings []detect.Finding, unexp []unexplained.Finding, selected map[string][]string,
	warned map[string]bool) *TopologyView {

	v := &TopologyView{
		ClusterID: clusterID, GraphVersion: graphVersion, GeneratedAt: now.UTC(),
		Nodes: []TopoNode{}, Edges: []TopoEdge{},
	}

	// ── 1. fold the inventory: pods roll up into their workload role; a Node (no
	//        role layer) stands as its own node. podToNode maps every instance key
	//        to the node key that represents it (workload role key, or Node self).
	type wmeta struct {
		ns, kind, name string
		replicas       int
	}
	nodeMeta := map[string]*wmeta{}
	podToNode := map[string]string{}
	for i := range inventory {
		rec := &inventory[i]
		ik := rec.CEI.Key()
		if rec.RoleCEI.RoleKey != "" {
			nk := rec.RoleCEI.Key()
			podToNode[ik] = nk
			m := nodeMeta[nk]
			if m == nil {
				wk, wn := splitRoleKey(rec.RoleCEI.RoleKey)
				m = &wmeta{ns: rec.RoleCEI.Namespace, kind: wk, name: wn}
				nodeMeta[nk] = m
			}
			m.replicas++
		} else {
			podToNode[ik] = ik
			if nodeMeta[ik] == nil {
				nodeMeta[ik] = &wmeta{ns: rec.Namespace, kind: rec.Kind, name: rec.Name}
			}
		}
	}
	// roll an instance/role key up to its node key (passthrough when unmapped —
	// a role key already names its node; an unknown key stays itself).
	roll := func(key string) string {
		if nk, ok := podToNode[key]; ok {
			return nk
		}
		return key
	}

	// ── 2. roll current marks up to the workload (OR across its pods) ──
	matched := map[string]map[string]bool{} // node -> set of matched phenomenon ids
	degraded := map[string]bool{}
	addPhen := func(nk, p string) {
		if matched[nk] == nil {
			matched[nk] = map[string]bool{}
		}
		matched[nk][p] = true
	}
	for i := range findings {
		f := &findings[i]
		nk := roll(f.EntityCEI)
		addPhen(nk, f.Phenomenon)
		if f.Quality == detect.QualityDegraded {
			degraded[nk] = true
		}
	}
	loud := map[string]bool{}
	for _, c := range unexp {
		if c.Status == unexplained.StatusNew || c.Status == unexplained.StatusAging {
			loud[roll(c.Scope)] = true
		}
	}
	sel := map[string]bool{}
	for k := range selected {
		sel[roll(k)] = true
	}
	warn := map[string]bool{}
	for k, w := range warned {
		if w {
			warn[roll(k)] = true
		}
	}

	// ── 3. nodes (workloads + Nodes), capped. Sort so truncation is deterministic
	//        and keeps matched/loud/selected workloads preferentially. ──
	keys := make([]string, 0, len(nodeMeta))
	for k := range nodeMeta {
		keys = append(keys, k)
	}
	priority := func(k string) int {
		switch {
		case len(matched[k]) > 0:
			return 0
		case loud[k]:
			return 1
		case sel[k]:
			return 2
		default:
			return 3
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, pj := priority(keys[i]), priority(keys[j])
		if pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})
	included := map[string]bool{}
	for _, k := range keys {
		if len(included) >= topoCap {
			v.Truncated++
			continue
		}
		included[k] = true
		m := nodeMeta[k]
		phens := make([]string, 0, len(matched[k]))
		for p := range matched[k] {
			phens = append(phens, p)
		}
		sort.Strings(phens)
		layer := "workload"
		if m.kind == "Node" {
			layer = "node"
		}
		node := TopoNode{
			CEIKey: k, Kind: m.kind, Namespace: m.ns, Name: m.name, Layer: layer, Replicas: m.replicas,
			Selected: sel[k], Matched: len(phens) > 0, Degraded: degraded[k],
			Loud: loud[k], Warned: warn[k], Phenomena: phens,
		}
		v.Nodes = append(v.Nodes, node)
		if node.Matched {
			v.Summary.Matched++
		}
		if node.Loud {
			v.Summary.Loud++
		}
		if node.Selected {
			v.Summary.Selected++
		}
		if node.Warned {
			v.Summary.Warned++
		}
	}
	sort.Slice(v.Nodes, func(i, j int) bool { return v.Nodes[i].CEIKey < v.Nodes[j].CEIKey })

	// ── 4. edges: observed-flow DEPENDENCIES (workload→workload, the call graph)
	//        + aggregated placement (workload→Node). node-lease is placement noise
	//        for an operator and is dropped. A workload's many pods produce ONE
	//        workload-level edge (deduped). ──
	seen := map[string]bool{}
	for _, e := range edgeSnap {
		if e.Type == "node-lease" {
			continue
		}
		from, to := roll(e.From), roll(e.To)
		if from == to || !included[from] || !included[to] {
			continue
		}
		id := from + "\x00" + to + "\x00" + e.Type
		if seen[id] {
			continue
		}
		seen[id] = true
		status := edgeStatus(e, budgets, now)
		v.Edges = append(v.Edges, TopoEdge{Type: e.Type, From: from, To: to, Status: status})
		switch status {
		case "valid":
			v.Summary.ValidEdges++
		case "suspect":
			v.Summary.SuspectEdges++
		}
	}

	// ── 5. service-routing + storage layers: the k8s relationships the identity
	//        layer already reconciles (selects = Service→Pod, mounts = Pod→PVC) but
	//        the workload-centric default dropped. Fold each to the workload role
	//        (Service→workload, workload→PVC) and mint the Service/PVC node, tagged
	//        with a Layer so the surface shows routing/storage on demand without
	//        cluttering the dependency view. Generic: every cluster has Services,
	//        EndpointSlices and PVCs — nothing here is app-specific. These nodes are
	//        secondary (an operator toggles them per view) and are appended without
	//        re-running the workload cap. ──
	extra := map[string]TopoNode{}
	for _, e := range edgeSnap {
		var svc, pvc, wl string
		switch e.Type {
		case "selects": // Service → Pod  ⇒  Service → workload
			svc, wl = e.From, roll(e.To)
		case "mounts": // Pod → PVC  ⇒  workload → PVC
			pvc, wl = e.To, roll(e.From)
		default:
			continue
		}
		// Honesty guard: Service/PVC endpoints are not tracked instances, so the
		// only proof the relationship currently exists is a LIVE (non-retracted)
		// edge. A retracted selects/mounts edge is a dangling endpoint (the Service
		// or PVC was deleted) — never mint a ghost node for it. (flow/runs-on edges
		// are surfaced even when retracted, ghosted, because their endpoints ARE
		// tracked workloads/nodes; these are not.)
		if !e.RetractedAt.IsZero() {
			continue
		}
		if !included[wl] {
			continue // the fronted/mounting workload was capped or absent — stay honest
		}
		status := edgeStatus(e, budgets, now)
		if svc != "" {
			if _, ok := extra[svc]; !ok {
				ns, _, name := parseInstanceCEI(svc)
				extra[svc] = TopoNode{CEIKey: svc, Kind: "Service", Namespace: ns, Name: name, Layer: "service", Phenomena: []string{}}
			}
			if id := svc + "\x00" + wl + "\x00selects"; !seen[id] {
				seen[id] = true
				v.Edges = append(v.Edges, TopoEdge{Type: "selects", From: svc, To: wl, Status: status})
			}
		} else {
			if _, ok := extra[pvc]; !ok {
				ns, _, name := parseInstanceCEI(pvc)
				extra[pvc] = TopoNode{CEIKey: pvc, Kind: "PVC", Namespace: ns, Name: name, Layer: "storage", Phenomena: []string{}}
			}
			if id := wl + "\x00" + pvc + "\x00mounts"; !seen[id] {
				seen[id] = true
				v.Edges = append(v.Edges, TopoEdge{Type: "mounts", From: wl, To: pvc, Status: status})
			}
		}
	}
	for _, n := range extra {
		v.Nodes = append(v.Nodes, n)
	}
	sort.Slice(v.Nodes, func(i, j int) bool { return v.Nodes[i].CEIKey < v.Nodes[j].CEIKey })

	sort.Slice(v.Edges, func(i, j int) bool {
		a, b := v.Edges[i], v.Edges[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})

	v.Summary.Nodes = len(v.Nodes)
	v.Summary.Edges = len(v.Edges)
	return v
}

// parseInstanceCEI extracts namespace/kind/name from an instance CEI key
// ("i|cluster|ns|kind|name|uid"). Returns empties on a malformed or non-instance
// key — callers mint a node from it only for Service/PVC endpoints, which are
// always minted as instances (identity.MintInstance), so the shape is stable.
func parseInstanceCEI(key string) (ns, kind, name string) {
	p := strings.Split(key, "|")
	if len(p) >= 5 && p[0] == "i" {
		return p[2], p[3], p[4]
	}
	return "", "", ""
}

// splitRoleKey turns a role key ("Deployment/currencyservice") into its workload
// kind and name. A bare/ownerless role with no slash is labelled a Workload.
func splitRoleKey(rk string) (kind, name string) {
	if i := strings.IndexByte(rk, '/'); i >= 0 {
		return rk[:i], rk[i+1:]
	}
	return "Workload", rk
}

// edgeStatus classifies a snapshot edge at `now` — mirrors EdgeStore.statusLocked
// (doc 03 §3.6): retracted edges report retracted; a live edge confirmed longer
// ago than its budget is suspect; otherwise valid. The default budget (5m)
// matches the edge store's fallback for unconfigured types.
func edgeStatus(e identity.EdgeSnap, budgets map[string]time.Duration, now time.Time) string {
	if !e.RetractedAt.IsZero() {
		return "retracted"
	}
	budget := 5 * time.Minute
	if b, ok := budgets[strings.TrimSpace(e.Type)]; ok {
		budget = b
	}
	if now.Sub(e.LastConfirmed) > budget {
		return "suspect"
	}
	return "valid"
}
