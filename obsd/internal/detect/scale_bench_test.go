package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Scale characterization for the detection hot path (audit roadmap #6 / the theme's
// "hundreds of pods across namespaces on a single node"). The per-tick cost of
// detection is O(entities × phenomena × checks); these benchmarks measure that curve
// on the REAL released graph so the scaling envelope is a number, not a guess.
//
//	go test -bench=BenchmarkMatch -benchmem -run='^$' ./obsd/internal/detect/
//
// Read ns/op as the per-tick detection latency at that entity count, and B/op as the
// per-tick allocation (the GC pressure that drives steady-state RSS).

// benchFingerprints builds n distinct container fingerprints — the realistic per-tick
// input at scale: most pods healthy (variables below their bars), 1 in faultyEvery a
// rising-at-threshold working set (a real MEMORY_LEAK signature). Entities are spread
// across 20 namespaces to mirror a busy single-node cluster.
func benchFingerprints(n, faultyEvery int) []observe.Fingerprint {
	out := make([]observe.Fingerprint, 0, n)
	for i := 0; i < n; i++ {
		state := observe.StateBelow
		slope := 0.0
		if faultyEvery > 0 && i%faultyEvery == 0 {
			state = observe.StateAtThreshold
			slope = 1500 // rising
		}
		ns := fmt.Sprintf("ns-%d", i%20)
		name := fmt.Sprintf("pod-%d", i)
		out = append(out, observe.Fingerprint{
			CEIKey:      fmt.Sprintf("i|cl|%s|Pod|%s|uid-%d", ns, name, i),
			Namespace:   ns,
			Name:        name,
			Kind:        "Container",
			EvaluatedAt: evalAt,
			Thresholds: []observe.VariableThreshold{
				{
					RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", Metric: "container_memory_working_set_bytes",
					State: state, Slope: slope, SlopeSamples: 6, BarSource: "config",
					Deriv: observe.DerivationRef{StreamID: "s", SampleAt: evalAt, How: "gauge-level"},
				},
				{
					RuleID: "THR_CONTAINER_CPU_USAGE_VS_LIMIT", Metric: "container_cpu_usage_seconds_total",
					State: observe.StateBelow, BarSource: "config",
					Deriv: observe.DerivationRef{StreamID: "s", SampleAt: evalAt, How: "counter-rate"},
				},
			},
		})
	}
	return out
}

// BenchmarkMatch measures the entity-local detection pass across a growing entity
// count — the dominant per-tick cost on a single node packed with pods.
func BenchmarkMatch(b *testing.B) {
	g, err := graph.LoadWithOverlays("../../../ontology/graph/k8s_signal_kg.json", "../../../ontology/graph/overlays")
	if err != nil {
		b.Fatalf("load graph: %v", err)
	}
	m := NewMatcher(g)
	w := identity.TimeWindow{Start: evalAt.Add(-time.Minute), End: evalAt}

	for _, n := range []int{100, 500, 1000, 5000} {
		fps := benchFingerprints(n, 20) // ~5% faulty, the realistic shape
		selected := make(map[string][]string, n)
		for i := range fps {
			selected[fps[i].CEIKey] = []string{"container_memory_working_set_bytes"}
		}
		b.Run(fmt.Sprintf("entities=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var findings int
			for i := 0; i < b.N; i++ {
				findings = len(m.Match(fps, selected, nil, w))
			}
			// Per-tick output volume + a normalized cost, so the curve is readable.
			b.ReportMetric(float64(findings), "findings")
			b.ReportMetric(float64(n), "entities")
		})
	}
}
