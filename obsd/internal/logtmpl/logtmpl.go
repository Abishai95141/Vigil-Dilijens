package logtmpl

import (
	"regexp"
	"sort"
	"strings"
)

// wildcard replaces a variable token in a template.
const wildcard = "<*>"

// Params are the DECLARED constants of the miner (doc 20 P4), never fit to the data.
type Params struct {
	Depth        int     // fixed parse-tree depth (prefix tokens that route a line)
	SimThreshold float64 // a line joins a cluster when token similarity ≥ this
	MaxChildren  int     // a tree node beyond this many children collapses to a wildcard child
}

// DefaultParams: depth 4, 0.5 similarity, 100 children — the versioned default.
var DefaultParams = Params{Depth: 4, SimThreshold: 0.5, MaxChildren: 100}

// Template is one mined cluster: the token pattern (with <*> for variable positions)
// and how many lines matched it.
type Template struct {
	Pattern string   `json:"pattern"`
	Count   int      `json:"count"`
	Tokens  []string `json:"-"`
}

// maskRules are DECLARED, ordered LINE-LEVEL substitutions: each replaces a variable
// SUBSTRING with the wildcard (so an embedded value like userId=<uuid> or main.go:<n>
// is masked, not just a whole variable token). UUID is first so its digit groups are
// not partially consumed by the number rule.
var maskRules = []*regexp.Regexp{
	regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`), // uuid
	regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`),                                            // ip[:port]
	regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`),                                                          // hex
	regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`),                                                        // long hex id
	regexp.MustCompile(`\b\d+(\.\d+)?([eE][-+]?\d+)?(ns|us|µs|ms|s|m|h)?\b`),                          // number[+unit]
}

// maskTokens masks variable substrings line-level (deterministic), then tokenizes.
func maskTokens(line string) []string {
	for _, re := range maskRules {
		line = re.ReplaceAllString(line, wildcard)
	}
	return strings.Fields(line)
}

// Mine returns the templates for the given lines. It SORTS the input first, so the
// result is byte-identical across runs and INVARIANT to the order lines arrive in
// (the determinism guarantee). Counts are exact (every line is counted once).
func Mine(lines []string, p Params) []Template {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	d := &drain{p: p, root: map[int]*node{}}
	for _, ln := range sorted {
		toks := maskTokens(ln)
		if len(toks) == 0 {
			continue
		}
		d.add(toks)
	}
	return d.templates()
}

type cluster struct {
	tokens []string
	count  int
}

type node struct {
	children map[string]*node
	clusters []*cluster
}

type drain struct {
	p    Params
	root map[int]*node // token-count → first tree layer
}

func (d *drain) add(tokens []string) {
	n := len(tokens)
	layer := d.root[n]
	if layer == nil {
		layer = &node{children: map[string]*node{}}
		d.root[n] = layer
	}
	cur := layer
	for i := 0; i < d.p.Depth && i < n; i++ {
		key := tokens[i]
		child := cur.children[key]
		if child == nil {
			if key != wildcard && len(cur.children) >= d.p.MaxChildren {
				key = wildcard // collapse an over-wide node to a wildcard catch-all
				child = cur.children[key]
			}
			if child == nil {
				child = &node{children: map[string]*node{}}
				cur.children[key] = child
			}
		}
		cur = child
	}
	// match a cluster in the leaf (all clusters here share the token count n).
	best, bestSim := -1, -1.0
	for idx, cl := range cur.clusters {
		if sim := seqSim(cl.tokens, tokens); sim > bestSim {
			bestSim, best = sim, idx
		}
	}
	if best >= 0 && bestSim >= d.p.SimThreshold {
		cl := cur.clusters[best]
		for i := range cl.tokens {
			if cl.tokens[i] != tokens[i] {
				cl.tokens[i] = wildcard
			}
		}
		cl.count++
		return
	}
	cur.clusters = append(cur.clusters, &cluster{tokens: append([]string(nil), tokens...), count: 1})
}

// seqSim is the fraction of positions that match (an existing wildcard matches
// anything). Clusters in a leaf share the same length, so this is well-defined.
func seqSim(a, b []string) float64 {
	if len(a) != len(b) {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	same := 0
	for i := range a {
		if a[i] == b[i] || a[i] == wildcard {
			same++
		}
	}
	return float64(same) / float64(len(a))
}

// templates collects every cluster and returns them sorted by (pattern, count) — a
// deterministic order independent of the tree walk.
func (d *drain) templates() []Template {
	var out []Template
	for _, layer := range d.root {
		collect(layer, &out)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern != out[j].Pattern {
			return out[i].Pattern < out[j].Pattern
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func collect(n *node, out *[]Template) {
	for _, cl := range n.clusters {
		*out = append(*out, Template{Pattern: strings.Join(cl.tokens, " "), Count: cl.count, Tokens: cl.tokens})
	}
	for _, c := range n.children {
		collect(c, out)
	}
}
