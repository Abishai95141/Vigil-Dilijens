package assoc

import (
	"math"
	"testing"
	"time"
)

// TestPruneConfounders: a common driver induces a spurious a~b correlation. The order-1
// conditional-independence prune must REMOVE a~b (conditioning on the driver explains it away)
// while KEEPING driver~a and driver~b (the direct associations).
func TestPruneConfounders(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	mk := func(f func(i int) float64) []Point {
		pts := make([]Point, 80)
		for i := range pts {
			pts[i] = Point{At: base.Add(time.Duration(i) * 15 * time.Second), Value: f(i)}
		}
		return pts
	}
	drv := func(i int) float64 { return math.Sin(float64(i) * 0.4) }
	// a and b each = driver + a DIFFERENT, mutually-uncorrelated idiosyncratic part (moderate
	// amplitude) ⇒ a~b is real-but-spurious (via the driver), well-conditioned for the partial.
	series := map[string][]Point{
		"driver": mk(drv),
		"a":      mk(func(i int) float64 { return drv(i) + 0.6*math.Sin(float64(i)*1.7+0.5) }),
		"b":      mk(func(i int) float64 { return drv(i) + 0.6*math.Cos(float64(i)*2.3) }),
	}
	p := DefaultParams
	p.MinAbsCoefficient = 0.5
	now := base.Add(80 * 15 * time.Second)

	edges := Associate(now, series, p)
	has := func(es []Edge, x, y string) bool {
		for _, e := range es {
			if (e.A == x && e.B == y) || (e.A == y && e.B == x) {
				return true
			}
		}
		return false
	}
	if !has(edges, "a", "b") {
		t.Fatalf("setup expects a spurious a~b in the raw associations; edges=%+v", edges)
	}

	pruned := PruneConfounders(now, series, edges, p, 0.5)
	if has(pruned, "a", "b") {
		t.Errorf("a~b should be PRUNED (explained away by the driver); pruned=%+v", pruned)
	}
	if !has(pruned, "driver", "a") || !has(pruned, "driver", "b") {
		t.Errorf("driver~a and driver~b should SURVIVE the prune; pruned=%+v", pruned)
	}

	// indepFloor<=0 disables the prune (returns input unchanged)
	if got := PruneConfounders(now, series, edges, p, 0); len(got) != len(edges) {
		t.Errorf("indepFloor<=0 should be a no-op: got %d edges, want %d", len(got), len(edges))
	}
}
