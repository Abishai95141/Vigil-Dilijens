package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
	fixtureDir = "testdata/bundle-v1"
)

var base = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func loadGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

func fpParams() observe.FPParams {
	return observe.FPParams{
		ScrapeInterval: 15 * time.Second, RateWindow: 5 * time.Minute,
		Watermark: 30 * time.Second, Band: 0.05, WellAboveFactor: 1.10,
		CooccurrenceWindow: 10 * time.Minute,
	}
}

const (
	memLimit = float64(128 << 20) // 128Mi
	podCEI   = "i|cl|shop|Pod|web-a|poduid-1"
	nodeCEI  = "i|cl||Node|n1|nodeuid-1"
)

func leakBinding() binding.Binding {
	return binding.Binding{
		CEIKey: podCEI, RoleKey: "r|cl|shop|Deployment|web", Entity: "Container", Container: "web",
		RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", Metric: "container_memory_working_set_bytes",
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{
			Kind: "config-relative", Source: binding.SourceConfig, ConfigPath: "spec.containers[].resources.limits.memory",
			Factor: 0.95, Value: 0.95 * memLimit, Unit: "bytes", Direction: "above", Window: "sustained",
			ResolvedAt: base,
		},
	}
}

// throttleBinding + psiBinding: the first-order THROTTLING_CASCADE pair (07 M2)
// — the container's throttle ratio (anchor) and the node's CPU PSI stall
// fraction (neighbour, one runs-on hop). Both default-flagged bars.
func throttleBinding() binding.Binding {
	return binding.Binding{
		CEIKey: podCEI, RoleKey: "r|cl|shop|Deployment|web", Entity: "Container", Container: "web",
		RuleID: "THR_CONTAINER_CPU_THROTTLE_RATIO", Metric: "container_cpu_cfs_throttled_periods_total",
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{
			Kind: "absolute", Source: binding.SourceDefault, Flagged: true,
			Value: 0.25, Unit: "ratio", Direction: "above", Window: "5m", ResolvedAt: base,
		},
	}
}

func psiBinding() binding.Binding {
	return binding.Binding{
		CEIKey: nodeCEI, Entity: "Node",
		RuleID: "THR_NODE_CPU_PSI_STALL", Metric: "node_pressure_cpu_waiting_seconds_total",
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{
			Kind: "absolute", Source: binding.SourceDefault, Flagged: true,
			Value: 0.10, Unit: "ratio", Direction: "above", Window: "5m", ResolvedAt: base,
		},
	}
}

// oomBinding: the cgroup OOM-kill guard (07 M5) — the downstream half of the
// authored MEMORY_LEAK -> OOM_KILL_CGROUP relation the fixture exercises.
func oomBinding() binding.Binding {
	return binding.Binding{
		CEIKey: podCEI, RoleKey: "r|cl|shop|Deployment|web", Entity: "Container", Container: "web",
		RuleID: "THR_CONTAINER_OOM_EVENTS", Metric: "container_oom_events_total",
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{
			Kind: "rate-of-change", Source: binding.SourceDefault, Flagged: true,
			Value: 1, Unit: "count", Direction: "above", Window: "15m", ResolvedAt: base,
		},
	}
}

func fixtureBindings() []binding.Binding {
	return []binding.Binding{leakBinding(), throttleBinding(), psiBinding(), oomBinding()}
}

func leakDef() qss.StreamDef {
	return qss.StreamDef{
		ID: podCEI + "|container_memory_working_set_bytes", CEIKey: podCEI, UID: "poduid-1/web",
		Kind: "Container", Metric: "container_memory_working_set_bytes", Type: "gauge",
		Node: "n1", Cadence: "scrape",
	}
}

func throttledDef() qss.StreamDef {
	return qss.StreamDef{
		ID: podCEI + "|container_cpu_cfs_throttled_periods_total", CEIKey: podCEI, UID: "poduid-1/web",
		Kind: "Container", Metric: "container_cpu_cfs_throttled_periods_total", Type: "counter",
		Node: "n1", Cadence: "scrape",
	}
}

func periodsDef() qss.StreamDef {
	return qss.StreamDef{
		ID: podCEI + "|container_cpu_cfs_periods_total", CEIKey: podCEI, UID: "poduid-1/web",
		Kind: "Container", Metric: "container_cpu_cfs_periods_total", Type: "counter",
		Node: "n1", Cadence: "scrape",
	}
}

func psiDef() qss.StreamDef {
	return qss.StreamDef{
		ID: nodeCEI + "|node_pressure_cpu_waiting_seconds_total", CEIKey: nodeCEI, UID: "nodeuid-1",
		Kind: "Node", Metric: "node_pressure_cpu_waiting_seconds_total", Type: "counter",
		Node: "n1", Cadence: "scrape",
	}
}

func oomDef() qss.StreamDef {
	return qss.StreamDef{
		ID: podCEI + "|container_oom_events_total", CEIKey: podCEI, UID: "poduid-1/web",
		Kind: "Container", Metric: "container_oom_events_total", Type: "counter",
		Node: "n1", Cadence: "scrape",
	}
}

// fixtureEdgeBudgets pins the staleness budgets the test bundles capture under.
func fixtureEdgeBudgets() map[string]time.Duration {
	return map[string]time.Duration{"runs-on": 90 * time.Second}
}

func typedBudgets() map[identity.EdgeType]time.Duration {
	return map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}
}

// buildBundle simulates the LIVE path into dir: per scrape round it appends the
// sample to an in-memory reader AND the capture, then evaluates (Materialize +
// match + Digest) exactly as live obsd does and records the tick. tamperRound,
// when >= 0, makes the CAPTURED copy of that round's sample diverge from the
// live one — recorded digests then describe a different reality than the disk.
// Returns the live tick records.
func buildBundle(t *testing.T, dir string, g *graph.Graph, tamperRound int) []TickRecord {
	t.Helper()
	cap, err := NewCapture(dir, qss.WarmConfig{SegmentDuration: 2 * time.Hour, Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("NewCapture: %v", err)
	}
	if err := cap.WriteManifest(Manifest{
		CreatedAt: base, ClusterID: "test-cluster", GraphVersion: g.Version,
		ParamsVersion: "0", Profile: "dev", FPParams: fpParams(), ScrapeInterval: 15 * time.Second,
		HotRingCapacity: qss.HotCapacity(), EdgeBudgets: fixtureEdgeBudgets(),
		Contents: []string{"readings (qss segments)", "resolved bars per epoch",
			"topology snapshot per tick", "evaluation ticks with digests"},
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	epoch, err := cap.SetBars(base, fixtureBindings())
	if err != nil || epoch != 1 {
		t.Fatalf("SetBars: epoch=%d err=%v", epoch, err)
	}
	// Idempotence: an unchanged bar set must not create a new epoch.
	if e2, _ := cap.SetBars(base.Add(time.Minute), fixtureBindings()); e2 != 1 {
		t.Fatalf("unchanged bars must keep epoch 1, got %d", e2)
	}

	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(g)
	res := &binding.Result{Bindings: fixtureBindings()}
	selected := selection.TierASet(res, g)
	tracker := detect.NewCascadeTracker(fpParams().CooccurrenceWindow)
	live := newBundleReader() // the in-memory "live" view (same read semantics as the Ingestor)
	defs := []qss.StreamDef{leakDef(), throttledDef(), periodsDef(), psiDef(), oomDef()}
	for _, d := range defs {
		live.register(d)
	}

	// Live topology: the pod runs on the node, confirmed each scrape — exactly
	// what the watcher's informers would assert. The matcher walks the per-tick
	// SNAPSHOT (recorded == evaluated), mirroring obsd.
	pod, err := identity.ParseKey(podCEI)
	if err != nil {
		t.Fatal(err)
	}
	node, err := identity.ParseKey(nodeCEI)
	if err != nil {
		t.Fatal(err)
	}
	edges := identity.NewEdgeStore(func() time.Time { return base }, typedBudgets(), 24*time.Hour)

	var recs []TickRecord
	for i := 0; i < 8; i++ {
		recv := base.Add(time.Duration(i) * 15 * time.Second)
		edges.Assert(identity.EdgeRunsOn, pod, node, recv)

		// The leak signature: working set rising 4Mi per scrape (below -> at ->
		// above -> well-above the 121.6Mi bar).
		val := float64((110 + 4*i) << 20)
		captured := val
		if i == tamperRound {
			captured = val - float64(20<<20) // the disk copy lies by -20Mi
		}
		// The OOM counter increments at round 6: the authored MEMORY_LEAK ->
		// OOM_KILL_CGROUP relation then recognizes as a CASCADE (07 M5) — the
		// leak fired earlier ticks on the same entity, inside the window.
		oom := 0.0
		if i >= 6 {
			oom = 1.0
		}
		samples := []struct {
			def  qss.StreamDef
			live float64
			disk float64
		}{
			{leakDef(), val, captured},
			// Throttle ratio: Δ10 throttled / Δ15 periods per scrape = 0.67 >> 0.25.
			{throttledDef(), float64(10 * i), float64(10 * i)},
			{periodsDef(), float64(15 * i), float64(15 * i)},
			// Node PSI: +3 stall-seconds per 15s scrape = 0.2/s >> 0.10.
			{psiDef(), float64(3 * i), float64(3 * i)},
			{oomDef(), oom, oom},
		}
		for _, s := range samples {
			live.hot.Append(s.def.ID, qss.Sample{At: recv, Value: s.live})
			if err := cap.Warm().Append(s.def, recv, qss.Sample{At: recv, Value: s.disk}); err != nil {
				t.Fatalf("warm append: %v", err)
			}
		}

		evalNow := recv.Add(time.Second)
		topoSnap := edges.Snapshot()
		topo, err := identity.NewEdgeStoreFromSnapshot(topoSnap, typedBudgets())
		if err != nil {
			t.Fatal(err)
		}
		w := identity.TimeWindow{Start: evalNow.Add(-fpParams().CooccurrenceWindow), End: evalNow}
		fps := observe.Materialize(res, rules, live, fpParams(), evalNow)
		findings := matcher.Match(fps, selected, topo, w)
		cascades := matcher.Cascades(evalNow, findings, tracker, topo, w)
		tracker.Observe(evalNow, findings)
		digest, _, derr := Digest(evalNow, fps, findings, cascades)
		if derr != nil {
			t.Fatalf("digest: %v", derr)
		}
		rec := TickRecord{EvalNow: evalNow, BarsEpoch: epoch, Topology: topoSnap, Digest: digest,
			Fingerprints: len(fps), Findings: len(findings)}
		if err := cap.Tick(rec); err != nil {
			t.Fatalf("tick: %v", err)
		}
		recs = append(recs, rec)
	}
	if err := cap.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return recs
}

// The headline M5 case: a captured bundle replays byte-identically — every
// recorded live digest is reproduced from disk through the real observation and
// detection components, and the capture contains real findings (not a trivially
// empty bundle).
func TestBundleReplaysByteIdentical(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	recs := buildBundle(t, dir, g, -1)

	fired := 0
	for _, r := range recs {
		fired += r.Findings
	}
	if fired == 0 {
		t.Fatal("the capture must contain real findings (the leak signature), or the test proves nothing")
	}

	rep, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Mismatches != 0 {
		for _, tk := range rep.Ticks {
			if !tk.Match {
				t.Errorf("tick %s: recorded %s != replayed %s", tk.EvalNow, tk.RecordedDigest, tk.ReplayedDigest)
			}
		}
		t.Fatalf("%d/%d ticks diverged", rep.Mismatches, len(rep.Ticks))
	}
	if len(rep.Ticks) != len(recs) {
		t.Errorf("ticks replayed = %d, want %d", len(rep.Ticks), len(recs))
	}
	if rep.Samples != 40 || rep.Streams != 5 {
		t.Errorf("samples=%d streams=%d, want 40/5", rep.Samples, rep.Streams)
	}
	if rep.TopologyLess {
		t.Error("a topology-recording bundle must not report TopologyLess")
	}
	// Determinism of the engine itself: a second run is identical — and the
	// canonical output proves a FIRST-ORDER finding (the cascade, with its span
	// path) replayed through the real traversal path, not just entity-local ones.
	outDir := t.TempDir()
	rep2, err := Run(Options{BundleDir: dir, Graph: g, OutDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	for i := range rep.Ticks {
		if rep.Ticks[i].ReplayedDigest != rep2.Ticks[i].ReplayedDigest {
			t.Fatalf("engine is not deterministic at tick %d", i)
		}
	}
	ticksOut, err := filepath.Glob(filepath.Join(outDir, "tick-*.json"))
	if err != nil || len(ticksOut) == 0 {
		t.Fatalf("canonical tick output missing: %v %v", ticksOut, err)
	}
	sawFirstOrder, sawStory := false, false
	for _, p := range ticksOut {
		raw, _ := os.ReadFile(p)
		s := string(raw)
		if strings.Contains(s, "PHEN_THROTTLING_CASCADE") && strings.Contains(s, `"first-order"`) {
			sawFirstOrder = true
		}
		// The 07 M5 story: the authored MEMORY_LEAK -> OOM_KILL_CGROUP relation
		// recognized as one cascade, in the replayed canonical bytes.
		var tr TickResult
		if json.Unmarshal(raw, &tr) == nil {
			for _, c := range tr.Cascades {
				if c.Trigger.Phenomenon == "PHEN_MEMORY_LEAK" && c.Downstream.Phenomenon == "PHEN_OOM_KILL_CGROUP" {
					sawStory = true
				}
			}
		}
	}
	if !sawFirstOrder {
		t.Error("the replayed output must contain the first-order THROTTLING_CASCADE finding (span path included)")
	}
	if !sawStory {
		t.Error("the replayed output must contain the recognized MEMORY_LEAK -> OOM_KILL_CGROUP cascade")
	}
}

// The gate must FAIL when the disk diverges from what live evaluation saw — a
// replay harness that cannot detect divergence is a shallow proxy.
func TestTamperedBundleIsDetected(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, 5) // round 5's captured sample lies

	rep, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Mismatches == 0 {
		t.Fatal("a tampered sample must produce digest mismatches; the gate saw none")
	}
	// Ticks before the tamper round still match (divergence starts where the lie does).
	if !rep.Ticks[0].Match || !rep.Ticks[4].Match {
		t.Error("pre-tamper ticks should match")
	}
	if rep.Ticks[5].Match {
		t.Error("the tampered tick must not match")
	}
}

// A graph other than the pinned release is refused loudly (doc 11 §3.1).
func TestGraphVersionPinEnforced(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)

	other := *g
	other.Version = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	_, err := Run(Options{BundleDir: dir, Graph: &other})
	if err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Errorf("want version-mismatch refusal, got %v", err)
	}
}

// Flipping a raw byte in a sealed segment surfaces as CRC corruption, never as
// a silent skip or a wrong-but-confident replay.
func TestCorruptSegmentRefused(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)
	segs, err := qss.ListSegments(filepath.Join(dir, segmentsDir))
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments: %v %v", segs, err)
	}
	raw, _ := os.ReadFile(segs[0].Path)
	raw[len(raw)/2] ^= 0xFF
	if err := os.WriteFile(segs[0].Path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Run(Options{BundleDir: dir, Graph: g})
	if err == nil {
		t.Fatal("corrupt segment must fail the replay")
	}
}

// The committed fixture bundle (the harness's first regression input, doc 11 M1)
// replays clean against the current ontology. If this fails after an intentional
// ontology change, regenerate with: REGEN_FIXTURE=1 go test ./obsd/internal/replay/ -run TestFixture
func TestFixtureBundleReplays(t *testing.T) {
	g := loadGraph(t)
	if os.Getenv("REGEN_FIXTURE") == "1" {
		if err := os.RemoveAll(fixtureDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
			t.Fatal(err)
		}
		buildBundle(t, fixtureDir, g, -1)
		t.Log("fixture regenerated")
	}
	if _, err := os.Stat(filepath.Join(fixtureDir, manifestName)); err != nil {
		t.Fatalf("fixture bundle missing (generate with REGEN_FIXTURE=1): %v", err)
	}
	rep, err := Run(Options{BundleDir: fixtureDir, Graph: g})
	if err != nil {
		t.Fatalf("fixture replay: %v (an ontology change invalidates the pinned fixture; regenerate with REGEN_FIXTURE=1 if intentional)", err)
	}
	if rep.Mismatches != 0 {
		t.Fatalf("fixture replay diverged: %d mismatches", rep.Mismatches)
	}
	found := 0
	for _, tk := range rep.Ticks {
		found += tk.Findings
	}
	if found == 0 {
		t.Error("fixture must carry real findings")
	}
}

// Digest canonicalization: nil and empty inputs agree (a no-binding live tick
// vs its replay), and the digest is sensitive to every component.
func TestDigestCanonicalization(t *testing.T) {
	at := base
	dNil, _, _ := Digest(at, nil, nil, nil)
	dEmpty, _, _ := Digest(at, []observe.Fingerprint{}, []detect.Finding{}, []detect.Cascade{})
	if dNil != dEmpty {
		t.Error("nil and empty must digest identically")
	}
	dOther, _, _ := Digest(at.Add(time.Nanosecond), nil, nil, nil)
	if dOther == dNil {
		t.Error("digest must be sensitive to the evaluation instant")
	}
}

// A non-finite value reaching the digest path surfaces as an ERROR — never a
// panic in the live render goroutine, never a silently wrong digest. (Defence
// in depth: the ingest gate drops non-finite samples before they reach a ring.)
func TestDigestNonFiniteIsErrorNotPanic(t *testing.T) {
	var z float64
	fp := observe.Fingerprint{CEIKey: "x", Thresholds: []observe.VariableThreshold{{Value: z / z}}}
	if _, _, err := Digest(base, []observe.Fingerprint{fp}, nil, nil); err == nil {
		t.Fatal("a NaN in a fingerprint must surface as a digest error")
	}
}

// A restart into the same bundle dir (adversarial findings): the bars epoch
// CONTINUES (no overwrite, no spurious epoch for unchanged bars), the manifest
// is verified pin-compatible, a run-start frame resets replay's reconstructed
// state at the boundary — and every tick across BOTH runs verifies.
func TestRestartContinuationReplays(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(g)
	def := leakDef()

	totalTicks := 0
	runOnce := func(offset time.Duration, n int) {
		cap, err := NewCapture(dir, qss.WarmConfig{SegmentDuration: 2 * time.Hour, Retention: 7 * 24 * time.Hour})
		if err != nil {
			t.Fatalf("NewCapture: %v", err)
		}
		if err := cap.WriteManifest(Manifest{
			CreatedAt: base, ClusterID: "test-cluster", GraphVersion: g.Version,
			ParamsVersion: "0", Profile: "dev", FPParams: fpParams(), ScrapeInterval: 15 * time.Second,
			HotRingCapacity: qss.HotCapacity(),
		}); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		epoch, err := cap.SetBars(base.Add(offset), []binding.Binding{leakBinding()})
		if err != nil {
			t.Fatalf("SetBars: %v", err)
		}
		if epoch != 1 {
			t.Fatalf("unchanged bars across a restart must keep epoch 1, got %d", epoch)
		}
		live := newBundleReader() // fresh per run — the restart's empty rings
		live.register(def)
		// Fresh tracker per run: a process restart empties the cascade memory;
		// the engine mirrors this at every run-start frame.
		res := &binding.Result{Bindings: []binding.Binding{leakBinding()}}
		selected := selection.TierASet(res, g)
		tracker := detect.NewCascadeTracker(fpParams().CooccurrenceWindow)
		for i := 0; i < n; i++ {
			recv := base.Add(offset + time.Duration(i)*15*time.Second)
			val := float64((110 + 4*i) << 20)
			live.hot.Append(def.ID, qss.Sample{At: recv, Value: val})
			if err := cap.Warm().Append(def, recv, qss.Sample{At: recv, Value: val}); err != nil {
				t.Fatal(err)
			}
			evalNow := recv.Add(time.Second)
			w := identity.TimeWindow{Start: evalNow.Add(-fpParams().CooccurrenceWindow), End: evalNow}
			fps := observe.Materialize(res, rules, live, fpParams(), evalNow)
			// This bundle predates topology recording (no budget pin), so the
			// engine evaluates with nil topology — the live sim must too.
			findings := matcher.Match(fps, selected, nil, w)
			cascades := matcher.Cascades(evalNow, findings, tracker, nil, w)
			tracker.Observe(evalNow, findings)
			digest, _, derr := Digest(evalNow, fps, findings, cascades)
			if derr != nil {
				t.Fatal(derr)
			}
			if err := cap.Tick(TickRecord{EvalNow: evalNow, BarsEpoch: epoch, Digest: digest,
				Fingerprints: len(fps), Findings: len(findings)}); err != nil {
				t.Fatal(err)
			}
			totalTicks++
		}
		if err := cap.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runOnce(0, 5)
	runOnce(3*time.Minute, 5) // same 2h window: same-start sealed segments

	// Continuation, not duplication: exactly one bars epoch on disk.
	if _, err := os.Stat(filepath.Join(dir, "bars-2.json")); err == nil {
		t.Error("unchanged bars across a restart must not mint a second epoch")
	}
	rep, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Runs != 2 {
		t.Errorf("runs replayed = %d, want 2 (one run-start per process)", rep.Runs)
	}
	// This bundle predates topology recording (no edge-budget pin): the engine
	// must replay it in the entity-local-only regime AND say so.
	if !rep.TopologyLess {
		t.Error("a pre-topology bundle must be reported TopologyLess")
	}
	if len(rep.Ticks) != totalTicks || rep.Mismatches != 0 {
		for _, tk := range rep.Ticks {
			if !tk.Match {
				t.Errorf("tick %s diverged", tk.EvalNow)
			}
		}
		t.Fatalf("ticks=%d (want %d) mismatches=%d — the restart boundary must replay exactly",
			len(rep.Ticks), totalTicks, rep.Mismatches)
	}
}

// A continued bundle must be pin-compatible: a different graph version refuses.
func TestContinuationPinCompatibility(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)
	cap, err := NewCapture(dir, qss.WarmConfig{SegmentDuration: 2 * time.Hour, Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer cap.Close()
	err = cap.WriteManifest(Manifest{
		CreatedAt: base, ClusterID: "test-cluster",
		GraphVersion:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ParamsVersion: "0", Profile: "dev", FPParams: fpParams(), ScrapeInterval: 15 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "already pinned") {
		t.Errorf("a different graph pin must refuse continuation, got %v", err)
	}
}

// A garbage manifest (zeroed parameter set) refuses to replay rather than
// mis-verifying under silently different semantics.
func TestImplausibleManifestRefused(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)
	var m Manifest
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.FPParams.Watermark = 0
	out, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{BundleDir: dir, Graph: g}); err == nil {
		t.Error("a zeroed watermark must refuse to replay")
	}
}

// Evaluation mode (doc 11 M4 / 07 M6): the recorded inputs re-evaluate under an
// OVERRIDDEN sensitivity regime — different findings may emerge, no digest is
// compared (the recorded digests describe the capture regime), and the report
// says EvaluationMode. Verification mode on the same bundle stays strict.
func TestEvaluationModeSweeps(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)

	// Verification baseline: strict, byte-identical.
	ver, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatalf("verification run: %v", err)
	}
	if ver.EvaluationMode || ver.Mismatches != 0 {
		t.Fatalf("baseline must verify cleanly: eval=%v mismatches=%d", ver.EvaluationMode, ver.Mismatches)
	}
	baseFindings := 0
	for _, tk := range ver.Ticks {
		baseFindings += tk.Findings
	}

	// Evaluation: a hostile floor (0.9) suppresses the degraded matches the
	// capture regime surfaced — fewer findings, ZERO mismatches reported, mode
	// flagged.
	override := ver.Manifest.FPParams
	override.MinCompleteness = 0.9
	ev, err := Run(Options{BundleDir: dir, Graph: g, Evaluate: &override})
	if err != nil {
		t.Fatalf("evaluation run: %v", err)
	}
	if !ev.EvaluationMode {
		t.Fatal("the report must state EVALUATION mode")
	}
	if ev.Mismatches != 0 {
		t.Fatalf("evaluation mode verifies nothing — mismatches must be 0, got %d", ev.Mismatches)
	}
	evFindings := 0
	for _, tk := range ev.Ticks {
		evFindings += tk.Findings
		if tk.RecordedDigest != "" {
			t.Fatal("evaluation outcomes must carry no recorded-digest verdicts")
		}
	}
	if evFindings >= baseFindings {
		t.Errorf("a 0.9 completeness floor must suppress degraded findings: base=%d eval=%d", baseFindings, evFindings)
	}

	// An implausible override refuses loudly.
	bad := ver.Manifest.FPParams
	bad.MinCompleteness = 1.5
	if _, err := Run(Options{BundleDir: dir, Graph: g, Evaluate: &bad}); err == nil {
		t.Error("an implausible evaluation regime must refuse")
	}
}
