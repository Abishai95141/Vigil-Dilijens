package forecast

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
	barsPath   = "../../../obsd/internal/replay/testdata/bundle-v1/bars-1.json"
)

// The 09 M2 exit property on REAL replay-fixture inputs: the funnel over the
// committed bundle's recorded bindings + the real ontology admits exactly the
// gauge-under-a-level-bar (working set, the marquee class) and DEFERS every
// counter with its reason listed — candidates can only ever emit under §3.6
// conditions because nothing else even reaches the pipeline.
func TestEligibilityOnReplayFixtureBindings(t *testing.T) {
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	raw, err := os.ReadFile(filepath.Clean(barsPath))
	if err != nil {
		t.Fatalf("fixture bars: %v", err)
	}
	var bf struct {
		Bindings []binding.Binding `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &bf); err != nil {
		t.Fatal(err)
	}
	if len(bf.Bindings) == 0 {
		t.Fatal("fixture has no bindings")
	}

	targets, rejected := EligibleTargets(&binding.Result{Bindings: bf.Bindings}, g)
	if len(targets) != 1 {
		t.Fatalf("exactly the working-set gauge must pass the funnel, got %+v (rejected %+v)", targets, rejected)
	}
	tg := targets[0]
	if tg.Metric != "container_memory_working_set_bytes" || tg.SeriesKind != "gauge" {
		t.Errorf("wrong target admitted: %+v", tg)
	}
	if tg.BarSource != "config" || tg.BarFlagged {
		t.Errorf("the fixture's config-sourced bar must ride with provenance: %+v", tg)
	}
	if !tg.Precursor() {
		t.Errorf("working set must carry its AUTHORED T0- precursor reference from the real KG: %+v", tg.PrecursorPhenomena)
	}
	wantReasons := map[string]string{
		"container_oom_events_total":                ReasonRateGuardBar,
		"container_cpu_cfs_throttled_periods_total": ReasonDerivedRatio,
		"node_pressure_cpu_waiting_seconds_total":   ReasonCounterDeferred,
	}
	for _, r := range rejected {
		if wantReasons[r.Metric] != r.Reason {
			t.Errorf("wrong deferral for (%s,%s): %s", r.CEIKey, r.Metric, r.Reason)
		}
	}
	if len(rejected) != len(bf.Bindings)-1 {
		t.Errorf("every non-admitted pair must be listed: %d listed of %d", len(rejected), len(bf.Bindings)-1)
	}
}
