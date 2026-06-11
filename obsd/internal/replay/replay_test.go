package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
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

func leakDef() qss.StreamDef {
	return qss.StreamDef{
		ID: podCEI + "|container_memory_working_set_bytes", CEIKey: podCEI, UID: "poduid-1/web",
		Kind: "Container", Metric: "container_memory_working_set_bytes", Type: "gauge",
		Node: "n1", Cadence: "scrape",
	}
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
		Contents: []string{"readings (qss segments)", "resolved bars per epoch", "evaluation ticks with digests"},
		Absent:   []string{"topology log (lands with 07 M2 traversal)"},
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	epoch, err := cap.SetBars(base, []binding.Binding{leakBinding()})
	if err != nil || epoch != 1 {
		t.Fatalf("SetBars: epoch=%d err=%v", epoch, err)
	}
	// Idempotence: an unchanged bar set must not create a new epoch.
	if e2, _ := cap.SetBars(base.Add(time.Minute), []binding.Binding{leakBinding()}); e2 != 1 {
		t.Fatalf("unchanged bars must keep epoch 1, got %d", e2)
	}

	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(g)
	live := newBundleReader() // the in-memory "live" view (same read semantics as the Ingestor)
	def := leakDef()
	live.register(def)

	var recs []TickRecord
	for i := 0; i < 8; i++ {
		recv := base.Add(time.Duration(i) * 15 * time.Second)
		val := float64((110 + 4*i) << 20) // 110Mi rising 4Mi per scrape: below -> at -> above -> well-above
		live.hot.Append(def.ID, qss.Sample{At: recv, Value: val})
		captured := val
		if i == tamperRound {
			captured = val - float64(20<<20) // the disk copy lies by -20Mi
		}
		if err := cap.Warm().Append(def, recv, qss.Sample{At: recv, Value: captured}); err != nil {
			t.Fatalf("warm append: %v", err)
		}

		evalNow := recv.Add(time.Second)
		fps := observe.Materialize(&binding.Result{Bindings: []binding.Binding{leakBinding()}}, rules, live, fpParams(), evalNow)
		var findings []detect.Finding
		for _, fp := range fps {
			findings = append(findings, matcher.MatchFingerprint(fp)...)
		}
		digest, _ := Digest(evalNow, fps, findings)
		rec := TickRecord{EvalNow: evalNow, BarsEpoch: epoch, Digest: digest,
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
	if rep.Samples != 8 || rep.Streams != 1 {
		t.Errorf("samples=%d streams=%d, want 8/1", rep.Samples, rep.Streams)
	}
	// Determinism of the engine itself: a second run is identical.
	rep2, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	for i := range rep.Ticks {
		if rep.Ticks[i].ReplayedDigest != rep2.Ticks[i].ReplayedDigest {
			t.Fatalf("engine is not deterministic at tick %d", i)
		}
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
	dNil, _ := Digest(at, nil, nil)
	dEmpty, _ := Digest(at, []observe.Fingerprint{}, []detect.Finding{})
	if dNil != dEmpty {
		t.Error("nil and empty must digest identically")
	}
	dOther, _ := Digest(at.Add(time.Nanosecond), nil, nil)
	if dOther == dNil {
		t.Error("digest must be sensitive to the evaluation instant")
	}
}
