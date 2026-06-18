package assoc_test

import (
	"math"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/assoc"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// End-to-end (doc 20 P2): two correlated series written to a real qss.HotStore are
// snapshotted exactly as cmd/obsd's assocLoop does (Keys + LastN), then associated —
// proving the hot-store -> assoc data path the live lane runs.
func TestAssociateOverHotStore(t *testing.T) {
	hot := qss.NewHotStore()
	base := time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		hot.Append("node|cpu", qss.Sample{At: at, Value: float64(i + 1)})
		hot.Append("node|mem", qss.Sample{At: at, Value: float64(2 * (i + 1))})
	}

	series := map[string][]assoc.Point{}
	for _, id := range hot.Keys() {
		for _, s := range hot.LastN(id, qss.HotCapacity()) {
			series[id] = append(series[id], assoc.Point{At: s.At, Value: s.Value})
		}
	}

	now := base.Add(13 * 15 * time.Second)
	edges := assoc.Associate(now, series, assoc.DefaultParams)
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1 (cpu~mem)", len(edges))
	}
	if edges[0].A != "node|cpu" || edges[0].B != "node|mem" || edges[0].Relation != "associated-with" {
		t.Errorf("edge wrong: %+v", edges[0])
	}
	if math.Abs(edges[0].Coefficient-1) > 1e-9 {
		t.Errorf("coefficient = %v, want ~1", edges[0].Coefficient)
	}
}
