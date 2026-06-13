package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// 11 M6 — the consolidated charter battery. The piecemeal register audits (warnings
// register audit, unexplained causal-vocab audit, clock no-strings conformance) are
// joined here into ONE cross-surface sweep: every operator-facing surface payload is
// scanned for the register breaches the join must never commit (doc 01 §5, doc 11 M6).
// This is the "register audit clean on all early-warning strings" Phase-2 exit gate.

// builtSurfaces returns one populated payload per surface, each carrying the content
// most at risk of a register breach — AUTHORED notes (insights/topology), the
// PROJECTED band register (warnings), and the cascade/blast-radius vocabulary.
func builtSurfaces(t *testing.T) map[string][]byte {
	t.Helper()
	findings := []detect.Finding{{
		Phenomenon: "PHEN_THROTTLING_CASCADE", Label: "CPU throttling cascade",
		EntityCEI: podKey, Namespace: "shop", Name: "web-a", Kind: "Container",
		Span: "first-order", Quality: detect.QualityFull, Completeness: 1.0,
		RequiredMet: 2, RequiredTotal: 2, GraphVersion: "sha256:test",
		Members: []detect.MemberEvidence{
			{Metric: "container_cpu_cfs_throttled_periods_total", Role: "required", Temporal: "T0", State: "well-above", Note: "throttle ratio derivation", BarFlagged: true},
			{Metric: "node_pressure_cpu_waiting_seconds_total", Role: "required", Temporal: "T0+", State: "above", Note: "PSI waiting", Neighbour: nodeKey, Via: "runs-on", Hop: 1, EdgeResult: "valid"},
		},
		SpanPath:    []detect.EdgeStep{{Type: "runs-on", From: podKey, To: nodeKey, Result: "valid"}},
		BlastRadius: []detect.AtRisk{{CEIKey: podKey, Phenomenon: "PHEN_PROBE_FAILURE_RESTART", Related: "same-entity", Temporal: "T0+sustained", Why: "Eventual outcome"}},
	}}
	cascades := []detect.Cascade{{
		Trigger:    detect.FindingRef{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityDegraded},
		Downstream: detect.FindingRef{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityFull},
		Why:        "Eventual outcome", Temporal: "T0+terminal", Related: "same-entity",
	}}

	inv := []identity.InstanceRecord{
		{CEI: mustCEI(t, podKey), Kind: "Pod", Namespace: "shop", Name: "web-a"},
		{CEI: mustCEI(t, nodeKey), Kind: "Node", Name: "worker-1"},
	}
	edges := []identity.EdgeSnap{{Type: "runs-on", From: podKey, To: nodeKey, LastConfirmed: at.Add(-10 * time.Second)}}
	budgets := map[string]time.Duration{"runs-on": 90 * time.Second}
	unexpF := []unexplained.Finding{{Scope: nodeKey, Status: unexplained.StatusAging}}
	selected := map[string][]string{podKey: {"PHEN_THROTTLING_CASCADE"}}

	frows := []store.FindingRow{{
		Label: "Memory leak", EntityCEI: podKey, Name: "web-a", Kind: "Container", Quality: "degraded",
		FirstSeen: at.Add(-5 * time.Minute), LastSeen: at.Add(-1 * time.Minute),
	}}
	urows := []store.UnexplainedRow{{
		Scope: nodeKey, Name: "worker-1", Kind: "Node", Status: "resolved",
		FirstSeen: at.Add(-8 * time.Minute), LastSeen: at.Add(-3 * time.Minute),
	}}

	atRisk := func(phen, anchor string) []detect.AtRisk {
		return []detect.AtRisk{{CEIKey: "k", Phenomenon: phen, Related: "runs-on", Temporal: "T0+terminal", Why: "Eventual outcome"}}
	}

	surfaces := map[string]any{
		"insights": BuildInsights("cl", "sha256:test", "v0.3.0", at, findings, cascades),
		"topology": BuildTopology("cl", "v", at, inv, edges, budgets, findings, unexpF, selected, map[string]bool{podKey: true}),
		"timeline": BuildTimeline(at, frows, urows, nil),
		"warnings": BuildWarnings("v", "r", wAt, true, cycleWithCandidate(), 1, ClockHealthRow{Ready: true}, nil, atRisk),
	}
	out := map[string][]byte{}
	for name, v := range surfaces {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		out[name] = raw
	}
	return out
}

// TestCharterBatteryAllSurfacesClean is the consolidated register audit: every
// surface payload, populated with real authored notes + the PROJECTED band, must be
// register-clean (doc 11 M6 / Phase-2 exit gate).
func TestCharterBatteryAllSurfacesClean(t *testing.T) {
	for name, payload := range builtSurfaces(t) {
		if vs := CharterViolations(name, payload); len(vs) > 0 {
			for _, v := range vs {
				t.Errorf("CHARTER VIOLATION: %s", v)
			}
		}
	}
}

// TestCharterLinterHasTeeth proves the sweep is not vacuous: a payload carrying each
// banned register IS flagged. Without this, a clean sweep could mean a broken linter.
func TestCharterLinterHasTeeth(t *testing.T) {
	cases := []struct {
		payload string
		class   string
	}{
		{`{"note":"the leak caused by a bad config"}`, "causal"}, // "caused by"
		{`{"reason":"throttling because the node is full"}`, "causal"},
		{`{"projection":"working set will cross the bar in 5m"}`, "future-certainty"},
		{`{"msg":"memory is going to exceed the limit"}`, "future-certainty"},
		{`{"x":"this is a measured forecast value"}`, "fusion"},
	}
	for _, tc := range cases {
		vs := CharterViolations("honeypot", []byte(tc.payload))
		found := false
		for _, v := range vs {
			if v.Class == tc.class {
				found = true
			}
		}
		if !found {
			t.Errorf("linter missed a %s violation in %q", tc.class, tc.payload)
		}
	}
}

// TestProjectedClassStructural confirms the PROJECTED surface keeps its class
// markers STRUCTURALLY (not just lexically): every warning card declares
// isProjection and carries a band — a forecast is never a bare point (doc 09 §3.6).
func TestProjectedClassStructural(t *testing.T) {
	v := BuildWarnings("v", "r", wAt, true, cycleWithCandidate(), 1, ClockHealthRow{Ready: true}, nil, nil)
	raw, _ := json.Marshal(v)
	s := string(raw)
	if !strings.Contains(s, "PROJECTED") {
		t.Error("warnings surface must label its class PROJECTED")
	}
	if !strings.Contains(s, "isProjection") {
		t.Error("warning cards must carry the is_projection mark")
	}
	// Every projected card must carry a band (earliest..latest), never a collapsed
	// line, and the mandatory is_projection mark.
	for _, c := range v.Warnings {
		if c.EarliestAt.IsZero() && c.LatestAt.IsZero() {
			t.Errorf("PROJECTED card %s/%s has no band — a forecast never collapses to a line", c.Name, c.Metric)
		}
		if !c.IsProjection || c.Class != "PROJECTED" {
			t.Errorf("PROJECTED card %s/%s missing class markers (class=%q isProjection=%v)", c.Name, c.Metric, c.Class, c.IsProjection)
		}
	}
}
