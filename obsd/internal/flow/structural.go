package flow

import (
	"sort"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// StructuralResult is the DIGEST-BEARING core of the cascade: the named root and
// the impacted-caller↔degraded-callee pairs, derived PURELY from the flow EdgeStore
// under the validity contract. It carries NO surfacing metadata (labels, ports,
// conn-depth) — those are decorated on top by Walk and stay OFF the digest. This
// separation is what makes the production integration (Phase B) replay-safe: the
// structural cascade is a pure function of the captured topology snapshot, exactly
// like every other cascade the matcher produces.
type StructuralResult struct {
	Root  string           `json:"root"` // callee role key, or "" if none
	Links []StructuralLink `json:"links"`
}

// StructuralLink is one impacted-caller ← degraded-callee pairing, with the
// traversal result of the flow edge that connects them (the validity contract).
type StructuralLink struct {
	Impacted  string `json:"impacted"`  // caller role key
	Degraded  string `json:"degraded"`  // degraded callee role key
	Traversal string `json:"traversal"` // "valid" | "suspect"
}

// StructuralCascade walks the flow EdgeStore BACKWARD from each degraded callee to
// its callers under the validity contract, and names the most-upstream degraded
// node (the inbound-clean degraded callee reaching the most impacted callers). It
// depends ONLY on (store state, degraded set, window) — so it is identical live and
// in replay whenever the captured EdgeSnap topology is identical. This is the
// function the production matcher would call; the spike's Walk decorates its output.
func StructuralCascade(store *identity.EdgeStore, degradedKeys []string, w identity.TimeWindow) StructuralResult {
	degraded := make(map[string]bool, len(degradedKeys))
	for _, k := range degradedKeys {
		degraded[k] = true
	}
	sorted := append([]string(nil), degradedKeys...)
	sort.Strings(sorted)

	impacted := make(map[string]map[string]bool) // callee -> set of callers
	var links []StructuralLink
	for _, callee := range sorted {
		for _, n := range store.NeighboursInto(EdgeTypeFlow, callee, w) { // n.To = the caller
			caller := n.To.Key()
			if impacted[callee] == nil {
				impacted[callee] = make(map[string]bool)
			}
			impacted[callee][caller] = true
			links = append(links, StructuralLink{Impacted: caller, Degraded: callee, Traversal: n.Result.String()})
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Degraded != links[j].Degraded {
			return links[i].Degraded < links[j].Degraded
		}
		return links[i].Impacted < links[j].Impacted
	})

	// inbound-clean: a degraded callee that does not itself call another degraded callee.
	callsAnotherDegraded := make(map[string]bool)
	for _, d := range sorted {
		for _, n := range store.Neighbours(EdgeTypeFlow, d, w) { // n.To = the callee d calls
			if degraded[n.To.Key()] {
				callsAnotherDegraded[d] = true
			}
		}
	}
	root, best := "", -1
	for _, callee := range sorted {
		if callsAnotherDegraded[callee] {
			continue
		}
		if n := len(impacted[callee]); n > best {
			best, root = n, callee
		}
	}
	if root == "" { // cyclic fallback: max impacted, deterministic by sorted order
		for _, callee := range sorted {
			if n := len(impacted[callee]); n > best {
				best, root = n, callee
			}
		}
	}
	return StructuralResult{Root: root, Links: links}
}
