package observe

import (
	"math"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

// classic 4-observation histogram: le=1 c=1, le=2 c=3, le=4 c=4, +Inf c=4.
func sampleHistogram() *dto.Histogram {
	return &dto.Histogram{
		SampleCount: u64(4),
		SampleSum:   f64(7),
		Bucket: []*dto.Bucket{
			{UpperBound: f64(1), CumulativeCount: u64(1)},
			{UpperBound: f64(2), CumulativeCount: u64(3)},
			{UpperBound: f64(4), CumulativeCount: u64(4)},
			{UpperBound: f64(math.Inf(1)), CumulativeCount: u64(4)},
		},
	}
}

func TestHistogramQuantilePure(t *testing.T) {
	h := sampleHistogram()
	cases := []struct {
		phi  float64
		want float64
	}{
		{0.5, 1.5},  // rank 2 → interp [1,2] over counts [1,3]
		{0.95, 3.6}, // rank 3.8 → interp [2,4] over counts [3,4]
		{0.99, 3.92},
	}
	for _, c := range cases {
		got, ok := histogramQuantile(c.phi, h)
		if !ok {
			t.Errorf("phi=%v: not computable", c.phi)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("phi=%v: got %v, want %v", c.phi, got, c.want)
		}
	}
}

func TestHistogramQuantileDegenerate(t *testing.T) {
	if _, ok := histogramQuantile(0.95, nil); ok {
		t.Error("nil histogram should be non-computable")
	}
	if _, ok := histogramQuantile(0.95, &dto.Histogram{}); ok {
		t.Error("bucketless histogram should be non-computable")
	}
	// zero observations
	z := &dto.Histogram{SampleCount: u64(0), Bucket: []*dto.Bucket{
		{UpperBound: f64(1), CumulativeCount: u64(0)},
		{UpperBound: f64(math.Inf(1)), CumulativeCount: u64(0)},
	}}
	if _, ok := histogramQuantile(0.95, z); ok {
		t.Error("zero-observation histogram should be non-computable")
	}
	// only a +Inf bucket → cannot interpolate
	inf := &dto.Histogram{SampleCount: u64(5), Bucket: []*dto.Bucket{
		{UpperBound: f64(math.Inf(1)), CumulativeCount: u64(5)},
	}}
	if _, ok := histogramQuantile(0.5, inf); ok {
		t.Error("+Inf-only histogram should be non-computable")
	}
}

func TestPSuffixDeterministic(t *testing.T) {
	cases := map[float64]string{
		0.5: "_p50", 0.9: "_p90", 0.95: "_p95", 0.99: "_p99", 0.999: "_p99_9",
	}
	for phi, want := range cases {
		if got := pSuffix(phi); got != want {
			t.Errorf("pSuffix(%v) = %q, want %q", phi, got, want)
		}
	}
}

func TestSetHistogramQuantilesNormalizes(t *testing.T) {
	in, _ := newTestIngestor(t)
	// out-of-range dropped, duplicates collapsed, sorted.
	in.SetHistogramQuantiles([]float64{0.99, 0.5, 1.5, 0.0, 0.95, 0.95})
	if len(in.histQ) != 3 {
		t.Fatalf("histQ = %+v, want 3 (0.5,0.95,0.99)", in.histQ)
	}
	if in.histQ[0].phi != 0.5 || in.histQ[2].phi != 0.99 {
		t.Errorf("histQ not sorted: %+v", in.histQ)
	}
}

const histogramExposition = `# TYPE myapp_latency_seconds histogram
myapp_latency_seconds_bucket{le="1"} 1
myapp_latency_seconds_bucket{le="2"} 3
myapp_latency_seconds_bucket{le="4"} 4
myapp_latency_seconds_bucket{le="+Inf"} 4
myapp_latency_seconds_sum 7
myapp_latency_seconds_count 4
`

// THE BYTE-IDENTICAL DEFAULT: with the lane OFF, a histogram family is skipped and
// counted exactly as v1 — no derived streams (doc 20 P0.5).
func TestHistogramSkippedWhenDisabled(t *testing.T) {
	in, _ := newTestIngestor(t)
	sum := &IngestSummary{SeriesDropped: map[string]int{}, SeriesQuarantine: map[string]int{}}
	in.ingestExposition([]byte(histogramExposition), identity.FamilyNodeExporter, "worker-1", "", "", scrapeAt, sum)

	if sum.SkippedFamilies != 1 {
		t.Errorf("skipped families = %d, want 1 (histogram skipped when lane off)", sum.SkippedFamilies)
	}
	if got := in.StreamsByUIDMetric("node-u1", "myapp_latency_seconds_p95"); len(got) != 0 {
		t.Errorf("derived streams with lane OFF = %v, want none", got)
	}
}

// THE LANE ON: the histogram becomes derived p50/p95/p99 GAUGE streams, CEI-stamped to
// the node, with the interpolated values — and is no longer counted as skipped.
func TestHistogramDerivedWhenEnabled(t *testing.T) {
	in, _ := newTestIngestor(t)
	in.SetHistogramQuantiles(DefaultHistogramQuantiles)
	sum := &IngestSummary{SeriesDropped: map[string]int{}, SeriesQuarantine: map[string]int{}}
	in.ingestExposition([]byte(histogramExposition), identity.FamilyNodeExporter, "worker-1", "", "", scrapeAt, sum)

	if sum.SkippedFamilies != 0 {
		t.Errorf("skipped families = %d, want 0 (histogram now processed)", sum.SkippedFamilies)
	}
	want := map[string]float64{
		"myapp_latency_seconds_p50": 1.5,
		"myapp_latency_seconds_p95": 3.6,
		"myapp_latency_seconds_p99": 3.92,
	}
	for metric, wantVal := range want {
		ids := in.StreamsByUIDMetric("node-u1", metric)
		if len(ids) != 1 {
			t.Fatalf("derived %s streams = %v, want exactly 1 (resolved to node-u1)", metric, ids)
		}
		m, _ := in.Meta(ids[0])
		if m.Type != "gauge" || m.Kind != "Node" {
			t.Errorf("%s meta = %+v, want gauge/Node", metric, m)
		}
		latest, ok := in.Hot().Latest(ids[0])
		if !ok || math.Abs(latest.Value-wantVal) > 1e-9 {
			t.Errorf("%s latest = %+v, want %v", metric, latest, wantVal)
		}
	}
}
