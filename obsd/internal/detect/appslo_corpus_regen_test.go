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

// appReader is a minimal StreamReader serving one app_queue_depth sample for the pod uid.
type appReader struct {
	uid    string
	metric string
	value  float64 // <0 => no stream at all (the unobserved case)
}

func (r appReader) StreamsFor(uid, metric string) []string {
	if r.value < 0 || uid != r.uid || metric != r.metric {
		return nil
	}
	return []string{"s-app"}
}
func (r appReader) StreamType(id string) (string, bool) { return "gauge", id == "s-app" }
func (r appReader) Latest(id string) (qss.Sample, bool) {
	if id != "s-app" || r.value < 0 {
		return qss.Sample{}, false
	}
	return qss.Sample{At: appNow.Add(-5 * time.Second), Value: r.value}, true
}
func (r appReader) LastN(id string, n int) []qss.Sample {
	s, ok := r.Latest(id)
	if !ok {
		return nil
	}
	return []qss.Sample{s}
}

func appFPParams() observe.FPParams {
	return observe.FPParams{
		ScrapeInterval: 15 * time.Second, RateWindow: 5 * time.Minute,
		Watermark: 30 * time.Second, Band: 0.05, WellAboveFactor: 1.10,
		CooccurrenceWindow: 10 * time.Minute,
	}
}

type appLabel struct {
	Bundle      string  `json:"bundle"`
	Scenario    string  `json:"scenario"`
	SLODeclared bool    `json:"sloDeclared"`
	SLOBar      float64 `json:"sloBar"`
	QueueValue  float64 `json:"queueValue"`
	ExpectFire  bool    `json:"expectFire"`
	Note        string  `json:"note"`
}

func appScenarios() []appLabel {
	return []appLabel{
		{Scenario: "over-slo", SLODeclared: true, SLOBar: 1000, QueueValue: 1500, ExpectFire: true,
			Note: "queue 1500 > declared SLO 1000 -> PHEN_APP_QUEUE_SATURATION fires (config-sourced bar)."},
		{Scenario: "under-slo", SLODeclared: true, SLOBar: 1000, QueueValue: 500, ExpectFire: false,
			Note: "queue 500 < declared SLO 1000 -> healthy, no finding."},
		{Scenario: "undeclared-high-queue", SLODeclared: false, SLOBar: 0, QueueValue: 1500, ExpectFire: false,
			Note: "a HIGH queue but NO declared SLO -> unbounded, NO bar, NO finding (the charter floor: fabricating a bar fails)."},
		{Scenario: "healthy-no-stream", SLODeclared: true, SLOBar: 1000, QueueValue: -1, ExpectFire: false,
			Note: "SLO declared but the app exposes no queue series -> no variable, no finding (never invented)."},
	}
}

// foldAppScenario runs ONE scenario through the real Compile -> Materialize -> Match path
// and returns the matcher's findings for the app pod.
func foldAppScenario(t *testing.T, g *graph.Graph, sc appLabel) []Finding {
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
	if sc.SLODeclared {
		pc.SLOs = map[string]float64{"slo.queue.max_depth": sc.SLOBar}
	}
	cfg := appConfig{pods: map[string]binding.PodConfig{"traffic/aggregation-1": pc}}

	// REAL binding: resolves the SLO bar from declared config (or unbounded).
	res := binding.Compile(g, []identity.InstanceRecord{rec}, cfg, nil, appNow)

	rules := map[string]*graph.ThresholdRule{}
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	reader := appReader{uid: "uid-agg", metric: "app_queue_depth", value: sc.QueueValue}
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
	for _, sc := range appScenarios() {
		findings := foldAppScenario(t, g, sc)
		writeAppFindings(t, sc.Scenario, findings)
		sc.Bundle = "appslo-" + sc.Scenario
		writeAppLabel(t, sc)
	}
	t.Logf("regenerated %d app-slo scenarios into %s", len(appScenarios()), appSLOCorpusDir)
}

// TestAppSLOCorpusFrozenConsistent is the always-on drift guard: re-running the real path
// over the scenarios must reproduce the FROZEN corpus byte-for-byte.
func TestAppSLOCorpusFrozenConsistent(t *testing.T) {
	g := loadAppGraph(t)
	for _, sc := range appScenarios() {
		findings := foldAppScenario(t, g, sc)
		got := marshalAppFindings(findings)
		want, err := os.ReadFile(filepath.Join(appSLOCorpusDir, "findings-"+sc.Scenario+".jsonl"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN to create)", sc.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("app-slo corpus drift for %s — re-run differs from frozen (REGEN to update intentionally)", sc.Scenario)
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
