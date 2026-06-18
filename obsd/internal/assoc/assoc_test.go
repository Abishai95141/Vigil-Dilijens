package assoc

import (
	"math"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

// seriesAt places vals at consecutive 15s bins from the DefaultParams window start.
func seriesAt(vals ...float64) []Point {
	start := t0.Add(-DefaultParams.Window)
	pts := make([]Point, len(vals))
	for i, v := range vals {
		pts[i] = Point{At: start.Add(time.Duration(i) * DefaultParams.Bin), Value: v}
	}
	return pts
}

func reversed(pts []Point) []Point {
	out := make([]Point, len(pts))
	for i := range pts {
		out[i] = pts[len(pts)-1-i]
	}
	return out
}

func TestAssociatePerfectPositive(t *testing.T) {
	series := map[string][]Point{
		"a": seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"b": seriesAt(2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24),
	}
	edges := Associate(t0, series, DefaultParams)
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	e := edges[0]
	if e.A != "a" || e.B != "b" || e.Relation != "associated-with" {
		t.Errorf("edge shape wrong: %+v", e)
	}
	if math.Abs(e.Coefficient-1) > 1e-9 {
		t.Errorf("coefficient = %v, want ~1", e.Coefficient)
	}
	if e.Overlap != 12 {
		t.Errorf("overlap = %d, want 12", e.Overlap)
	}
}

func TestAssociatePerfectNegative(t *testing.T) {
	series := map[string][]Point{
		"a": seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"b": seriesAt(-1, -2, -3, -4, -5, -6, -7, -8, -9, -10, -11, -12),
	}
	edges := Associate(t0, series, DefaultParams)
	if len(edges) != 1 || math.Abs(edges[0].Coefficient+1) > 1e-9 {
		t.Fatalf("want one edge with coefficient ~-1, got %+v", edges)
	}
}

func TestAssociateConstantNoEdge(t *testing.T) {
	series := map[string][]Point{
		"a": seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"b": seriesAt(5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5),
	}
	if edges := Associate(t0, series, DefaultParams); len(edges) != 0 {
		t.Errorf("a constant series has no defined correlation; want 0 edges, got %+v", edges)
	}
}

func TestAssociateOverlapFloor(t *testing.T) {
	series := map[string][]Point{
		"a": seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"b": seriesAt(2, 4, 6), // only 3 overlapping bins < MinOverlap (8)
	}
	if edges := Associate(t0, series, DefaultParams); len(edges) != 0 {
		t.Errorf("overlap below floor must yield no edge, got %+v", edges)
	}
}

func TestAssociateCoefficientFloor(t *testing.T) {
	series := map[string][]Point{
		"a": seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"b": seriesAt(2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24), // r == 1
	}
	p := DefaultParams
	p.MinAbsCoefficient = 1.5 // even a perfect correlation is below this floor
	if edges := Associate(t0, series, p); len(edges) != 0 {
		t.Errorf("coefficient floor must exclude even r=1, got %+v", edges)
	}
}

// THE ASSOC-GATE (doc 20 P2): the edge set is byte-identical across runs and INVARIANT
// to input-sample order (the internal sort pins every order float arithmetic depends
// on). This is the determinism guarantee that must hold before assoc feeds any seam.
func TestAssocGateByteIdentical(t *testing.T) {
	fixture := map[string][]Point{
		"node|cpu":  seriesAt(3, 6, 9, 12, 15, 18, 21, 24, 27, 30, 33, 36),
		"node|mem":  seriesAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		"node|disk": seriesAt(12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1),
		"pod|q":     seriesAt(5, 5, 6, 5, 6, 7, 6, 7, 8, 7, 8, 9),
	}
	first := Associate(t0, fixture, DefaultParams)
	second := Associate(t0, fixture, DefaultParams)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("assoc is not deterministic across runs:\n%+v\n%+v", first, second)
	}
	// shuffle every series' sample order — the result must be identical.
	shuffled := make(map[string][]Point, len(fixture))
	for k, v := range fixture {
		shuffled[k] = reversed(v)
	}
	if got := Associate(t0, shuffled, DefaultParams); !reflect.DeepEqual(first, got) {
		t.Fatalf("assoc is not invariant to input-sample order:\n%+v\n%+v", first, got)
	}
	if len(first) == 0 {
		t.Fatal("fixture should produce at least one association (sanity)")
	}
}
