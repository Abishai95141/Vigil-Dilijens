package observe

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

var evalNow = time.Date(2026, 6, 11, 12, 5, 0, 0, time.UTC)

// fakeReader is a seeded StreamReader.
type fakeReader struct {
	streams map[string][]string     // uid|metric -> ids
	typ     map[string]string       // id -> expoType
	hist    map[string][]qss.Sample // id -> samples (oldest first)
}

func (f fakeReader) StreamsFor(uid, metric string) []string { return f.streams[uid+"|"+metric] }
func (f fakeReader) StreamType(id string) (string, bool)    { t, ok := f.typ[id]; return t, ok }
func (f fakeReader) Latest(id string) (qss.Sample, bool) {
	h := f.hist[id]
	if len(h) == 0 {
		return qss.Sample{}, false
	}
	return h[len(h)-1], true
}
func (f fakeReader) LastN(id string, n int) []qss.Sample {
	h := f.hist[id]
	if len(h) > n {
		h = h[len(h)-n:]
	}
	return h
}

func fpParams() FPParams {
	return FPParams{
		ScrapeInterval: 15 * time.Second, RateWindow: 5 * time.Minute,
		Watermark: 30 * time.Second, Band: 0.05, WellAboveFactor: 1.10,
		CooccurrenceWindow: 10 * time.Minute,
	}
}

// containerBinding builds a bound container binding with a config bar. As in the
// real compiler, the binding carries the POD's CEI key + the container name, so
// StreamUID() derives the podUID/container stream key.
func containerBinding(podUID, container, ruleID, metric, unit, direction string, barValue, factor float64, kind string) binding.Binding {
	return binding.Binding{
		CEIKey:  "i|cl|shop|Pod|" + podUID + "-pod|" + podUID,
		RoleKey: "r|cl|shop|Deployment|Deployment/web",
		Entity:  "Container", Container: container, RuleID: ruleID, Metric: metric,
		State: binding.StateBound, Validation: binding.ValidationVerified,
		Bar: &binding.ResolvedBar{
			Kind: kind, Source: binding.SourceConfig, Value: barValue, Factor: factor,
			Unit: unit, Direction: direction,
		},
	}
}

func s(base time.Time, pairs ...float64) []qss.Sample { return samples(base, pairs...) }

// The headline live case: a gauge working-set value laddered against its bar, with
// a full derivation reference.
func TestMaterializeGaugeThreshold(t *testing.T) {
	b := containerBinding("uid-a", "server", "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
		"container_memory_working_set_bytes", "bytes", "above", 255013683, 0.95, graph.RuleConfigRelative)
	res := &binding.Result{Bindings: []binding.Binding{b}}

	r := fakeReader{
		streams: map[string][]string{"uid-a/server|container_memory_working_set_bytes": {"s1"}},
		typ:     map[string]string{"s1": "gauge"},
		hist:    map[string][]qss.Sample{"s1": {{At: evalNow.Add(-5 * time.Second), Value: 1.3e8}}},
	}
	fps := Materialize(res, nil, r, fpParams(), evalNow)
	if len(fps) != 1 || len(fps[0].Thresholds) != 1 {
		t.Fatalf("want 1 entity with 1 threshold, got %+v", fps)
	}
	vt := fps[0].Thresholds[0]
	if vt.State != StateBelow {
		t.Errorf("130MB vs 243MB bar should be below, got %s", vt.State)
	}
	if vt.Value != 1.3e8 || vt.Deriv.How != "gauge-level" || vt.Deriv.StreamID != "s1" {
		t.Errorf("derivation wrong: %+v", vt.Deriv)
	}
	if vt.BarSource != "config" || vt.Flagged {
		t.Errorf("bar provenance should be config/unflagged: %+v", vt)
	}
	// A value over the limit → well-above (raw limit = 255013683/0.95 ≈ 268.4M).
	r.hist["s1"] = []qss.Sample{{At: evalNow, Value: 2.7e8}}
	fps = Materialize(res, nil, r, fpParams(), evalNow)
	if got := fps[0].Thresholds[0].State; got != StateWellAbove {
		t.Errorf("270MB (past raw limit) should be well-above, got %s", got)
	}
	if !fps[0].Crossed() {
		t.Error("entity with a crossed variable must report Crossed()")
	}
}

// CPU: a cumulative counter (cpu_seconds_total) compared to a millicore bar must be
// rate-converted (cores×1000), not compared raw.
func TestMaterializeCounterRate(t *testing.T) {
	b := containerBinding("uid-a", "server", "THR_CONTAINER_CPU_USAGE_VS_LIMIT",
		"container_cpu_usage_seconds_total", "millicores", "above", 180, 0.90, graph.RuleConfigRelative)
	res := &binding.Result{Bindings: []binding.Binding{b}}
	// 0.1 core for 60s: counter +6.0 over 60s → 0.1 cores → 100 millicores → below 180.
	base := evalNow.Add(-60 * time.Second)
	r := fakeReader{
		streams: map[string][]string{"uid-a/server|container_cpu_usage_seconds_total": {"c1"}},
		typ:     map[string]string{"c1": "counter"},
		hist:    map[string][]qss.Sample{"c1": s(base, 0, 100, 15, 101.5, 30, 103, 45, 104.5, 60, 106)},
	}
	fps := Materialize(res, nil, r, fpParams(), evalNow)
	vt := fps[0].Thresholds[0]
	if vt.Deriv.How != "counter-rate" {
		t.Fatalf("cpu counter must be rate-derived, got %s", vt.Deriv.How)
	}
	if vt.Value < 99 || vt.Value > 101 {
		t.Errorf("cpu value = %v millicores, want ~100", vt.Value)
	}
	if vt.State != StateBelow {
		t.Errorf("100 millicores vs 180 bar = below, got %s", vt.State)
	}
}

// Throttle ratio: two counters → Δthrottled/Δperiods over the window vs a ratio bar.
func TestMaterializeCounterRatio(t *testing.T) {
	b := containerBinding("uid-a", "server", "THR_CONTAINER_CPU_THROTTLE_RATIO",
		"container_cpu_cfs_throttled_periods_total", "ratio", "above", 0.25, 0, graph.RuleAbsolute)
	b.Bar.Source = binding.SourceDefault
	b.Bar.Flagged = true
	res := &binding.Result{Bindings: []binding.Binding{b}}
	rule := &graph.ThresholdRule{ID: b.RuleID, Metric: b.Metric, DivisorMetric: "container_cpu_cfs_periods_total"}
	rules := map[string]*graph.ThresholdRule{b.RuleID: rule}
	base := evalNow.Add(-60 * time.Second)
	r := fakeReader{
		streams: map[string][]string{
			"uid-a/server|container_cpu_cfs_throttled_periods_total": {"t1"},
			"uid-a/server|container_cpu_cfs_periods_total":           {"p1"},
		},
		typ: map[string]string{"t1": "counter", "p1": "counter"},
		hist: map[string][]qss.Sample{
			// throttled +26, periods +100 over the window → ratio 0.26: above the 0.25
			// bar but below the 0.275 well-above line (0.25×1.10).
			"t1": s(base, 0, 1000, 30, 1013, 60, 1026),
			"p1": s(base, 0, 5000, 30, 5050, 60, 5100),
		},
	}
	fps := Materialize(res, rules, r, fpParams(), evalNow)
	vt := fps[0].Thresholds[0]
	if vt.Deriv.How != "counter-ratio" || vt.Deriv.DivisorID != "p1" {
		t.Fatalf("ratio derivation wrong: %+v", vt.Deriv)
	}
	if vt.Value < 0.255 || vt.Value > 0.265 {
		t.Errorf("throttle ratio = %v, want ~0.26", vt.Value)
	}
	if vt.State != StateAbove || !vt.Flagged {
		t.Errorf("0.26 vs 0.25 default bar = above+flagged, got %s flagged=%v", vt.State, vt.Flagged)
	}
}

// Rate-of-change guard (restarts): the reset-aware window delta vs the guard count.
func TestMaterializeRateGuard(t *testing.T) {
	b := containerBinding("uid-a", "server", "THR_CONTAINER_RESTARTS_RATE",
		"kube_pod_container_status_restarts_total", "count", "above", 3, 0, graph.RuleRateOfChange)
	b.Bar.Source = binding.SourceDefault
	b.Bar.Flagged = true
	res := &binding.Result{Bindings: []binding.Binding{b}}
	base := evalNow.Add(-60 * time.Second)
	r := fakeReader{
		streams: map[string][]string{"uid-a/server|kube_pod_container_status_restarts_total": {"k1"}},
		typ:     map[string]string{"k1": "counter"},
		// 0→4 restarts in the window → breaches the guard of 3.
		hist: map[string][]qss.Sample{"k1": s(base, 0, 0, 15, 1, 30, 2, 45, 3, 60, 4)},
	}
	fps := Materialize(res, nil, r, fpParams(), evalNow)
	if len(fps[0].Rates) != 1 {
		t.Fatalf("want a rate component, got %+v", fps[0])
	}
	vr := fps[0].Rates[0]
	if vr.WindowDelta != 4 || !vr.Breached || vr.Deriv.How != "rate-guard" {
		t.Errorf("restart guard wrong: %+v", vr)
	}
	if !fps[0].Crossed() {
		t.Error("breached rate guard must make the entity Crossed()")
	}
}

// A failed-QA binding is NEVER evaluated (its evidence contradicts its semantics);
// an unbounded or out-of-scope binding has no bar to evaluate.
func TestMaterializeSkipsUnusable(t *testing.T) {
	failed := containerBinding("uid-a", "server", "R1", "m", "bytes", "above", 100, 0.95, graph.RuleConfigRelative)
	failed.Validation = binding.ValidationFailed
	unbounded := containerBinding("uid-b", "server", "R2", "m", "bytes", "above", 0, 0, graph.RuleConfigRelative)
	unbounded.Bar = nil
	oos := containerBinding("uid-c", "server", "R3", "m", "bytes", "above", 100, 0.95, graph.RuleConfigRelative)
	oos.State = binding.StateOutOfScope
	res := &binding.Result{Bindings: []binding.Binding{failed, unbounded, oos}}
	r := fakeReader{
		streams: map[string][]string{
			"uid-a/server|m": {"a"}, "uid-b/server|m": {"b"}, "uid-c/server|m": {"c"},
		},
		typ:  map[string]string{"a": "gauge", "b": "gauge", "c": "gauge"},
		hist: map[string][]qss.Sample{"a": {{At: evalNow, Value: 200}}, "b": {{At: evalNow, Value: 200}}, "c": {{At: evalNow, Value: 200}}},
	}
	if fps := Materialize(res, nil, r, fpParams(), evalNow); len(fps) != 0 {
		t.Errorf("unusable bindings must not produce fingerprints, got %+v", fps)
	}
}

// Staleness: a sample older than the watermark is flagged stale and does NOT count
// toward Crossed() (consumers treat over-age fingerprints as missing, doc 05 §6).
func TestMaterializeStaleness(t *testing.T) {
	b := containerBinding("uid-a", "server", "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
		"container_memory_working_set_bytes", "bytes", "above", 100, 0.95, graph.RuleConfigRelative)
	res := &binding.Result{Bindings: []binding.Binding{b}}
	r := fakeReader{
		streams: map[string][]string{"uid-a/server|container_memory_working_set_bytes": {"s1"}},
		typ:     map[string]string{"s1": "gauge"},
		// 2 minutes old: well past the 30s watermark. Value is over the bar.
		hist: map[string][]qss.Sample{"s1": {{At: evalNow.Add(-2 * time.Minute), Value: 200}}},
	}
	fps := Materialize(res, nil, r, fpParams(), evalNow)
	vt := fps[0].Thresholds[0]
	if !vt.Stale {
		t.Error("2-minute-old sample must be flagged stale")
	}
	if fps[0].Crossed() {
		t.Error("a stale crossed variable must NOT count as crossed (treated as missing)")
	}
}

// Determinism + ordering: same inputs → identical output; entities and components
// are sorted.
func TestMaterializeDeterministicAndSorted(t *testing.T) {
	mk := func(uid, container string) binding.Binding {
		return containerBinding(uid, container, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
			"container_memory_working_set_bytes", "bytes", "above", 100, 0.95, graph.RuleConfigRelative)
	}
	res := &binding.Result{Bindings: []binding.Binding{mk("uid-z", "z"), mk("uid-a", "a")}}
	r := fakeReader{
		streams: map[string][]string{
			"uid-z/z|container_memory_working_set_bytes": {"sz"},
			"uid-a/a|container_memory_working_set_bytes": {"sa"},
		},
		typ:  map[string]string{"sz": "gauge", "sa": "gauge"},
		hist: map[string][]qss.Sample{"sz": {{At: evalNow, Value: 50}}, "sa": {{At: evalNow, Value: 50}}},
	}
	a := Materialize(res, nil, r, fpParams(), evalNow)
	b := Materialize(res, nil, r, fpParams(), evalNow)
	if len(a) != 2 || a[0].CEIKey >= a[1].CEIKey {
		t.Errorf("entities not sorted: %v", []string{a[0].CEIKey, a[1].CEIKey})
	}
	if a[0].CEIKey != b[0].CEIKey || a[1].CEIKey != b[1].CEIKey {
		t.Error("Materialize not deterministic")
	}
}
