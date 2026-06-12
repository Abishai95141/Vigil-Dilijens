package graph

import "testing"

// The parser over the real spellings observed in the KG (the ~112-variant
// curation queue, doc 02 §3.6) — every class boundary pinned.
func TestParseSeriesShape(t *testing.T) {
	cases := []struct {
		dt   string
		want SeriesShape
	}{
		{"Gauge", SeriesShape{Gauge: true}},
		{"Gauges", SeriesShape{Gauge: true}},
		{"Counter", SeriesShape{Counter: true}},
		{"Counters + gauges", SeriesShape{Gauge: true, Counter: true}},
		{"Counters + histograms", SeriesShape{Counter: true, Histogram: true}},
		{"Counter + gauge + histogram", SeriesShape{Gauge: true, Counter: true, Histogram: true}},
		{"Histogram", SeriesShape{Histogram: true}},
		{"Info gauge", SeriesShape{Gauge: true}},
		{"Gauges (info)", SeriesShape{Gauge: true}},
		{"Gauges (state-derived)", SeriesShape{Gauge: true}},
		{"Event v1", SeriesShape{NonSeries: true}},
		{"Struct", SeriesShape{NonSeries: true}},
		{"Klog text or JSON", SeriesShape{NonSeries: true}},
		{"Structured JSON", SeriesShape{NonSeries: true}},
		{"OTLP spans", SeriesShape{NonSeries: true}},
		{"CR YAML", SeriesShape{NonSeries: true}},
		{"Plain text", SeriesShape{NonSeries: true}},
		{"Aggregated API resource (JSON)", SeriesShape{NonSeries: true}},
		{"Mixed text files", SeriesShape{NonSeries: true}},
		{"", SeriesShape{Unknown: true}},
		{"Bespoke thing", SeriesShape{Unknown: true}},
	}
	for _, c := range cases {
		if got := ParseSeriesShape(c.dt); got != c.want {
			t.Errorf("ParseSeriesShape(%q) = %+v, want %+v", c.dt, got, c.want)
		}
	}
}

// The doc 09 §3.2 funnel MEASURED on the real KG — these counts are the honest
// inputs the Phase-2 eligibility funnel starts from (the doc's 457/390/203 were
// design estimates; the graph is the source of truth). A KG change that moves
// them must be a conscious decision, not drift.
func TestForecastFunnelCountsOnRealKG(t *testing.T) {
	g := loadKG(t)
	var metric, series, gaugeOrCounter, containsGauge, nonSeries, unknown int
	for _, s := range g.Signals {
		if !s.IsMetric() {
			continue
		}
		metric++
		sh := s.Shape()
		if sh.Series() {
			series++
		}
		if sh.Gauge || sh.Counter {
			gaugeOrCounter++
		}
		if sh.Gauge {
			containsGauge++
		}
		if sh.NonSeries {
			nonSeries++
		}
		if sh.Unknown {
			unknown++
		}
	}
	t.Logf("funnel: metric=%d series=%d gauge-or-counter=%d contains-gauge=%d non-series=%d unknown=%d",
		metric, series, gaugeOrCounter, containsGauge, nonSeries, unknown)
	if unknown > 25 {
		t.Errorf("unknown series shapes grew past the stated curation queue: %d", unknown)
	}
	if containsGauge == 0 || gaugeOrCounter == 0 {
		t.Errorf("the funnel collapsed (gauge=%d, gauge-or-counter=%d) — parser or KG regression", containsGauge, gaugeOrCounter)
	}
}
