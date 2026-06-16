package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// The app-slo gate corpus (doc 15 cap. A): a STANDALONE deterministic gate that folds
// synthetic-but-real-shaped application scenarios through the REAL path — binding.Compile
// (SLO resolution from declared config) -> observe.Materialize (the gauge laddered vs the
// declared bar) -> detect.Matcher (the phenomenon fires iff crossed). The anti-shallow core
// is a LABEL ORACLE fixed by construction: a finding SHOULD appear iff the SLO is declared
// AND the queue crosses it. The undeclared-high-queue scenario proves the charter floor —
// a high queue with NO declared SLO produces NO bar and NO finding (fabricating a bar fails).
//
// Regenerate with: REGEN_APPSLO_CORPUS=1 go test ./obsd/internal/detect -run RegenAppSLOCorpus

const appSLOCorpusDir = "../../../corpus/app-slo"

var appNow = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

// appConfig is a minimal binding.EntityConfig declaring (or not) a pod's queue SLO.
type appConfig struct {
	pods map[string]binding.PodConfig
}

func (c appConfig) Pod(ns, name string) (binding.PodConfig, bool) {
	p, ok := c.pods[ns+"/"+name]
	return p, ok
}
func (c appConfig) Node(string) (binding.NodeConfig, bool)       { return binding.NodeConfig{}, false }
func (c appConfig) PVCs() []binding.PVCRef                       { return nil }
func (c appConfig) PVC(string, string) (binding.PVCConfig, bool) { return binding.PVCConfig{}, false }

// appReader is a minimal StreamReader serving ONE app metric's samples for the pod uid.
// It serves whatever family the scenario exercises (a gauge level, a last-update epoch
// gauge, or a request counter), so the gate folds all three app phenomena through the
// REAL materializer — including the counter→rate and age-from-timestamp derivations.
type appReader struct {
	uid      string
	metric   string
	expoType string       // gauge | counter
	samples  []qss.Sample // empty => no stream at all (the unobserved case)
}

func (r appReader) StreamsFor(uid, metric string) []string {
	if len(r.samples) == 0 || uid != r.uid || metric != r.metric {
		return nil
	}
	return []string{"s-app"}
}
func (r appReader) StreamType(id string) (string, bool) { return r.expoType, id == "s-app" }
func (r appReader) Latest(id string) (qss.Sample, bool) {
	if id != "s-app" || len(r.samples) == 0 {
		return qss.Sample{}, false
	}
	return r.samples[len(r.samples)-1], true
}
func (r appReader) LastN(id string, n int) []qss.Sample {
	if id != "s-app" || len(r.samples) == 0 {
		return nil
	}
	if n >= len(r.samples) {
		return r.samples
	}
	return r.samples[len(r.samples)-n:]
}

// gaugeSample is one level/last-update gauge reading 5s before evalNow (within the
// watermark, never stale). Used by the queue (depth) and freshness (epoch) families.
func gaugeSample(value float64) []qss.Sample {
	return []qss.Sample{{At: appNow.Add(-5 * time.Second), Value: value}}
}

// counterSamples is a monotonic request counter rising at ratePerSec, sampled every 15s
// across the last 60s (≤ the 2×scrape gap threshold, so no window break). Δ/Δt == rate.
func counterSamples(ratePerSec float64) []qss.Sample {
	offsets := []int{60, 45, 30, 15, 5} // seconds before evalNow, oldest→newest
	out := make([]qss.Sample, 0, len(offsets))
	for _, o := range offsets {
		elapsed := float64(60 - o) // seconds since the first sample
		out = append(out, qss.Sample{At: appNow.Add(-time.Duration(o) * time.Second), Value: ratePerSec * elapsed})
	}
	return out
}

func appFPParams() observe.FPParams {
	return observe.FPParams{
		ScrapeInterval: 15 * time.Second, RateWindow: 5 * time.Minute,
		Watermark: 30 * time.Second, Band: 0.05, WellAboveFactor: 1.10,
		CooccurrenceWindow: 10 * time.Minute,
	}
}

// appLabel is the per-scenario ground-truth ORACLE (what the python scorer grades
// against). Fixed by construction: a finding for the scenario's phenomenon SHOULD
// appear iff the SLO is declared AND the metric crosses it. The family/phenomenon/
// metric let the scorer be family-aware and assert no cross-talk (a freshness scenario
// must never light up the queue or load phenomenon).
type appLabel struct {
	Bundle      string  `json:"bundle"`
	Scenario    string  `json:"scenario"`
	Family      string  `json:"family"`     // queue | freshness | load
	Phenomenon  string  `json:"phenomenon"` // the app phenomenon this scenario exercises
	ConfigPath  string  `json:"configPath"` // the slo.* path the bar is borrowed from
	Metric      string  `json:"metric"`     // the exposed series
	SLODeclared bool    `json:"sloDeclared"`
	SLOBar      float64 `json:"sloBar"`
	ExpectFire  bool    `json:"expectFire"`
	Note        string  `json:"note"`
}

// appSpec is the Go-side scenario descriptor: the oracle PLUS how to drive the
// exposed signal (a gauge level, a last-update epoch, a request counter, or nothing).
type appSpec struct {
	lbl      appLabel
	expoType string  // gauge | counter
	noStream bool    // the app exposes no series
	gauge    float64 // queue: the depth value
	ageSecs  float64 // freshness: data age; sample value = appNow.Unix() - ageSecs
	rate     float64 // load: target req/s
}

func appSpecs() []appSpec {
	const (
		queuePhen = "PHEN_APP_QUEUE_SATURATION"
		freshPhen = "PHEN_APP_DATA_STALENESS"
		loadPhen  = "PHEN_APP_LOAD_SURGE"
	)
	q := func(s string) appLabel {
		return appLabel{Scenario: s, Family: "queue", Phenomenon: queuePhen, ConfigPath: "slo.queue.max_depth", Metric: "app_queue_depth"}
	}
	f := func(s string) appLabel {
		return appLabel{Scenario: s, Family: "freshness", Phenomenon: freshPhen, ConfigPath: "slo.freshness.max_age", Metric: "app_last_update_seconds"}
	}
	l := func(s string) appLabel {
		return appLabel{Scenario: s, Family: "load", Phenomenon: loadPhen, ConfigPath: "slo.requests.max_rate", Metric: "app_requests_total"}
	}
	with := func(b appLabel, declared bool, bar float64, fire bool, note string) appLabel {
		b.SLODeclared, b.SLOBar, b.ExpectFire, b.Note = declared, bar, fire, note
		return b
	}
	return []appSpec{
		// L4 queue (the keystone) — UNCHANGED scenarios (findings byte-identical bar the graph hash).
		{lbl: with(q("over-slo"), true, 1000, true,
			"queue 1500 > declared SLO 1000 -> PHEN_APP_QUEUE_SATURATION fires (config-sourced bar)."), expoType: "gauge", gauge: 1500},
		{lbl: with(q("under-slo"), true, 1000, false,
			"queue 500 < declared SLO 1000 -> healthy, no finding."), expoType: "gauge", gauge: 500},
		{lbl: with(q("undeclared-high-queue"), false, 0, false,
			"a HIGH queue but NO declared SLO -> unbounded, NO bar, NO finding (the charter floor: fabricating a bar fails)."), expoType: "gauge", gauge: 1500},
		{lbl: with(q("healthy-no-stream"), true, 1000, false,
			"SLO declared but the app exposes no queue series -> no variable, no finding (never invented)."), expoType: "gauge", noStream: true},

		// L6 freshness (the differentiator) — DATA AGE vs declared freshness SLO.
		{lbl: with(f("freshness-stale"), true, 30, true,
			"data age 120s > declared freshness SLO 30s -> PHEN_APP_DATA_STALENESS fires (age-from-timestamp, config bar)."), expoType: "gauge", ageSecs: 120},
		{lbl: with(f("freshness-fresh"), true, 30, false,
			"data age 8s < declared freshness SLO 30s -> fresh, no finding."), expoType: "gauge", ageSecs: 8},
		{lbl: with(f("freshness-undeclared"), false, 0, false,
			"data age 120s but NO declared freshness SLO -> unbounded, NO bar, NO finding (the charter floor)."), expoType: "gauge", ageSecs: 120},

		// L1 load surge (the trigger) — request RATE vs declared capacity SLO.
		{lbl: with(l("load-over"), true, 100, true,
			"request rate 150/s > declared capacity SLO 100/s -> PHEN_APP_LOAD_SURGE fires (counter-rate, config bar)."), expoType: "counter", rate: 150},
		{lbl: with(l("load-under"), true, 100, false,
			"request rate 40/s < declared capacity SLO 100/s -> within capacity, no finding."), expoType: "counter", rate: 40},
		{lbl: with(l("load-undeclared"), false, 0, false,
			"request rate 150/s but NO declared capacity SLO -> unbounded, NO bar, NO finding (the charter floor)."), expoType: "counter", rate: 150},
	}
}

// samplesFor builds the exposed stream for a spec's family (or nil for no-stream).
func (s appSpec) samplesFor() []qss.Sample {
	switch {
	case s.noStream:
		return nil
	case s.lbl.Family == "freshness":
		return gaugeSample(float64(appNow.Unix()) - s.ageSecs)
	case s.lbl.Family == "load":
		return counterSamples(s.rate)
	default: // queue
		return gaugeSample(s.gauge)
	}
}

// foldAppScenario runs ONE scenario through the real Compile -> Materialize -> Match path
// and returns the matcher's findings for the app pod. The scenario's declared SLO is set
// at its OWN config path, so an undeclared scenario leaves that path unset (unbounded).
func foldAppScenario(t *testing.T, g *graph.Graph, sp appSpec) []Finding {
	t.Helper()
	inst, err := identity.MintInstance(identity.InstanceCoords{Cluster: "appcl", Namespace: "traffic", Kind: "Pod", Name: "aggregation-1", UID: "uid-agg"}, appNow)
	if err != nil {
		t.Fatal(err)
	}
	role, err := identity.MintRole(identity.RoleCoords{Cluster: "appcl", Namespace: "traffic", Kind: "Deployment", RoleKey: "Deployment/aggregation"}, appNow)
	if err != nil {
		t.Fatal(err)
	}
	rec := identity.InstanceRecord{CEI: inst, RoleCEI: role, Kind: "Pod", Namespace: "traffic", Name: "aggregation-1", UID: "uid-agg"}

	pc := binding.PodConfig{}
	if sp.lbl.SLODeclared {
		pc.SLOs = map[string]float64{sp.lbl.ConfigPath: sp.lbl.SLOBar}
	}
	cfg := appConfig{pods: map[string]binding.PodConfig{"traffic/aggregation-1": pc}}

	// REAL binding: resolves the SLO bar from declared config (or unbounded).
	res := binding.Compile(g, []identity.InstanceRecord{rec}, cfg, nil, appNow)

	rules := map[string]*graph.ThresholdRule{}
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	reader := appReader{uid: "uid-agg", metric: sp.lbl.Metric, expoType: sp.expoType, samples: sp.samplesFor()}
	fps := observe.Materialize(res, rules, reader, appFPParams(), appNow)

	m := NewMatcher(g)
	var out []Finding
	for _, fp := range fps {
		out = append(out, m.MatchFingerprint(fp)...)
	}
	return out
}

func TestRegenAppSLOCorpus(t *testing.T) {
	if os.Getenv("REGEN_APPSLO_CORPUS") != "1" {
		t.Skip("set REGEN_APPSLO_CORPUS=1 to regenerate corpus/app-slo")
	}
	g := loadAppGraph(t)
	if err := os.MkdirAll(appSLOCorpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sp := range appSpecs() {
		findings := foldAppScenario(t, g, sp)
		writeAppFindings(t, sp.lbl.Scenario, findings)
		lbl := sp.lbl
		lbl.Bundle = "appslo-" + lbl.Scenario
		writeAppLabel(t, lbl)
	}
	t.Logf("regenerated %d app-slo scenarios into %s", len(appSpecs()), appSLOCorpusDir)
}

// TestAppSLOCorpusFrozenConsistent is the always-on drift guard: re-running the real path
// over the scenarios must reproduce the FROZEN corpus byte-for-byte.
func TestAppSLOCorpusFrozenConsistent(t *testing.T) {
	g := loadAppGraph(t)
	for _, sp := range appSpecs() {
		findings := foldAppScenario(t, g, sp)
		got := marshalAppFindings(findings)
		want, err := os.ReadFile(filepath.Join(appSLOCorpusDir, "findings-"+sp.lbl.Scenario+".jsonl"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN to create)", sp.lbl.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("app-slo corpus drift for %s — re-run differs from frozen (REGEN to update intentionally)", sp.lbl.Scenario)
		}
	}
}

func marshalAppFindings(findings []Finding) []byte {
	var buf []byte
	for i := range findings {
		b, _ := json.Marshal(findings[i])
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return buf
}

func writeAppFindings(t *testing.T, scenario string, findings []Finding) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(appSLOCorpusDir, "findings-"+scenario+".jsonl"), marshalAppFindings(findings), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAppLabel(t *testing.T, lbl appLabel) {
	t.Helper()
	b, err := json.MarshalIndent(lbl, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appSLOCorpusDir, "label-"+lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
