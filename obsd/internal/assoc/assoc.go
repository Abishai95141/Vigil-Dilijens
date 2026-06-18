package assoc

import (
	"math"
	"sort"
	"time"
)

// Point is one (time, value) reading of a series — the assoc input. A plain type so
// the package imports nothing (the caller maps qss.Sample -> assoc.Point).
type Point struct {
	At    time.Time
	Value float64
}

// Edge is one MEASURED association between two series over a window. A and B are
// sorted (A < B): the edge is UNDIRECTED (Pearson is symmetric) — never a causal
// arrow. Relation is always "associated-with".
type Edge struct {
	A           string    `json:"a"`
	B           string    `json:"b"`
	Relation    string    `json:"relation"`
	Coefficient float64   `json:"coefficient"` // Pearson r over the overlapping bins, in [-1, 1]
	Overlap     int       `json:"overlap"`     // number of co-observed bins
	WindowStart time.Time `json:"windowStart"`
	WindowEnd   time.Time `json:"windowEnd"`
}

// Params are the DECLARED constants of the association computation (doc 20 P2): a
// window, a bin width, and two structural floors. They are declared (borrowed-
// normativity), never fit to the data they are applied to — a data-fit cutoff is a
// learned threshold. In production these live in the parameters file; DefaultParams is
// the versioned default.
type Params struct {
	Window            time.Duration // how far back to associate over
	Bin               time.Duration // bin width for aligning two series (≈ scrape cadence)
	MinOverlap        int           // structural validity floor: need ≥ this many co-observed bins
	MinAbsCoefficient float64       // surfacing floor: only |r| ≥ this is a notable association
}

// DefaultParams is the declared default (doc 20 P2): a 1h window, 15s bins, an
// 8-bin overlap floor, and a 0.6 |r| surfacing floor.
var DefaultParams = Params{
	Window:            time.Hour,
	Bin:               15 * time.Second,
	MinOverlap:        8,
	MinAbsCoefficient: 0.6,
}

// Associate computes the MEASURED association edges over the series within
// [now-Window, now]. Pure + deterministic: same (now, series, params) ⇒ byte-identical
// edges. The result is sorted by |coefficient| desc, then A, then B.
func Associate(now time.Time, series map[string][]Point, p Params) []Edge {
	windowStart := now.Add(-p.Window)
	windowEnd := now

	binned := make(map[string]map[int]float64, len(series))
	keys := make([]string, 0, len(series))
	for id, pts := range series {
		b := binSeries(pts, windowStart, windowEnd, p.Bin)
		if len(b) == 0 {
			continue
		}
		binned[id] = b
		keys = append(keys, id)
	}
	sort.Strings(keys) // deterministic pair order

	var edges []Edge
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			a, b := keys[i], keys[j]
			r, n, ok := pearsonOverlap(binned[a], binned[b])
			if !ok || n < p.MinOverlap || math.Abs(r) < p.MinAbsCoefficient {
				continue
			}
			edges = append(edges, Edge{
				A: a, B: b, Relation: "associated-with", Coefficient: r, Overlap: n,
				WindowStart: windowStart, WindowEnd: windowEnd,
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		ai, aj := math.Abs(edges[i].Coefficient), math.Abs(edges[j].Coefficient)
		if ai != aj {
			return ai > aj
		}
		if edges[i].A != edges[j].A {
			return edges[i].A < edges[j].A
		}
		return edges[i].B < edges[j].B
	})
	return edges
}

// binSeries maps a series to bin index -> last (latest-timestamp) value in that bin,
// within [start, end]. Sorting by time first makes "last in bin" deterministic
// regardless of input order; non-finite values are excluded.
func binSeries(pts []Point, start, end time.Time, bin time.Duration) map[int]float64 {
	cp := append([]Point(nil), pts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].At.Before(cp[j].At) })
	out := make(map[int]float64)
	for _, pt := range cp {
		if pt.At.Before(start) || pt.At.After(end) {
			continue
		}
		if math.IsNaN(pt.Value) || math.IsInf(pt.Value, 0) {
			continue
		}
		idx := int(pt.At.Sub(start) / bin)
		out[idx] = pt.Value // ascending time ⇒ latest in the bin wins
	}
	return out
}

// pearsonOverlap is the Pearson correlation over the bins both series observe, summed
// in ascending bin-index order (fixed order ⇒ reproducible float result). Returns
// (r, overlap, ok); ok=false for <2 overlapping bins or a constant series (r undefined).
func pearsonOverlap(a, b map[int]float64) (float64, int, bool) {
	idxs := make([]int, 0, len(a))
	for k := range a {
		if _, ok := b[k]; ok {
			idxs = append(idxs, k)
		}
	}
	sort.Ints(idxs)
	n := len(idxs)
	if n < 2 {
		return 0, n, false
	}
	var sx, sy float64
	for _, k := range idxs {
		sx += a[k]
		sy += b[k]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var sxy, sxx, syy float64
	for _, k := range idxs {
		dx, dy := a[k]-mx, b[k]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	denom := math.Sqrt(sxx * syy)
	if denom == 0 {
		return 0, n, false // a constant series has no defined correlation
	}
	return sxy / denom, n, true
}
