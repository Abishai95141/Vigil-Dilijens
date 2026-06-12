package api

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// `at` is declared in server_test.go (same package).
const (
	podKey  = "i|cl|shop|Pod|web-a|uid-a"
	nodeKey = "i|cl||Node|worker-1|nodeuid-1"
)

// --- M2 insight feed ----------------------------------------------------------

// BuildInsights composes the "now" surface: full/degraded counts, span
// classification, the MEASURED member trail with its AUTHORED note kept as a
// SEPARATE labelled field (never fused), blast radius, and cascade stories.
func TestBuildInsights(t *testing.T) {
	findings := []detect.Finding{
		{
			Phenomenon: "PHEN_THROTTLING_CASCADE", Label: "CPU throttling cascade",
			EntityCEI: podKey, Namespace: "shop", Name: "web-a", Kind: "Container",
			Span: "first-order", Quality: detect.QualityFull, Completeness: 1.0,
			RequiredMet: 2, RequiredTotal: 2, GraphVersion: "sha256:test",
			Members: []detect.MemberEvidence{
				{Metric: "container_cpu_cfs_throttled_periods_total", Role: "required", Temporal: "T0", State: "well-above", Note: "throttle ratio derivation", BarFlagged: true},
				{Metric: "node_pressure_cpu_waiting_seconds_total", Role: "required", Temporal: "T0+", State: "above", Note: "PSI waiting", Neighbour: nodeKey, Via: "runs-on", Hop: 1, EdgeResult: "valid"},
			},
			SpanPath:    []detect.EdgeStep{{Type: "runs-on", From: podKey, To: nodeKey, Result: "valid"}},
			BlastRadius: []detect.AtRisk{{CEIKey: podKey, Phenomenon: "PHEN_PROBE_FAILURE_RESTART", Related: "same-entity", Temporal: "T0+sustained", Why: "Probe cascade"}},
		},
		{
			Phenomenon: "PHEN_MEMORY_LEAK", Label: "Memory leak", EntityCEI: podKey,
			Namespace: "shop", Name: "web-a", Kind: "Container", Span: "",
			Quality: detect.QualityDegraded, Completeness: 0.5, RequiredMet: 1, RequiredTotal: 2,
			Members:      []detect.MemberEvidence{{Metric: "container_memory_working_set_bytes", Role: "required", State: "above", Note: "Slope > 0"}},
			Unobservable: []string{"kubelet endpoint — unobservable"},
		},
	}
	cascades := []detect.Cascade{{
		Trigger:    detect.FindingRef{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityDegraded},
		Downstream: detect.FindingRef{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityFull},
		Why:        "Eventual outcome", Temporal: "T0+terminal", Related: "same-entity",
	}}

	v := BuildInsights("cl", "sha256:test", "v0.3.0", at, findings, cascades)

	if v.Summary.Total != 2 || v.Summary.Full != 1 || v.Summary.Degraded != 1 {
		t.Errorf("summary counts wrong: %+v", v.Summary)
	}
	if v.Summary.FirstOrder != 1 || v.Summary.EntityLocal != 1 {
		t.Errorf("span classification wrong: %+v", v.Summary)
	}
	if v.Summary.Cascades != 1 || v.Summary.WithAtRisk != 1 {
		t.Errorf("cascade/at-risk counts wrong: %+v", v.Summary)
	}
	// Full before degraded.
	if v.Findings[0].Quality != "full" {
		t.Errorf("full must sort before degraded: %+v", v.Findings)
	}
	// The AUTHORED note is a separate labelled field, not paraphrased into state.
	full := v.Findings[0]
	psi := memberByMetric(full.Members, "node_pressure_cpu_waiting_seconds_total")
	if psi == nil || psi.Note != "PSI waiting" || psi.Neighbour != nodeKey || psi.Hop != 1 || psi.EdgeResult != "valid" {
		t.Errorf("neighbour member must carry its note + edge crossing: %+v", psi)
	}
	if len(full.BlastRadius) != 1 || full.BlastRadius[0].Why != "Probe cascade" {
		t.Errorf("blast radius must carry the authored relation note: %+v", full.BlastRadius)
	}
	if len(full.SpanPath) != 1 || full.SpanPath[0].Result != "valid" {
		t.Errorf("span path must render with verdicts: %+v", full.SpanPath)
	}
	// Degraded card names its unobservable member.
	if len(v.Findings[1].Unobservable) != 1 {
		t.Errorf("degraded card must keep its unobservable member: %+v", v.Findings[1])
	}
}

func memberByMetric(ms []MemberRow, metric string) *MemberRow {
	for i := range ms {
		if ms[i].Metric == metric {
			return &ms[i]
		}
	}
	return nil
}

// --- M3 topology --------------------------------------------------------------

// BuildTopology marks matched/loud/selected nodes, classifies edge validity at
// `now`, excludes dangling edges, and states truncation.
func TestBuildTopology(t *testing.T) {
	inv := []identity.InstanceRecord{
		{CEI: mustCEI(t, podKey), Kind: "Pod", Namespace: "shop", Name: "web-a"},
		{CEI: mustCEI(t, nodeKey), Kind: "Node", Name: "worker-1"},
		{CEI: mustCEI(t, "i|cl|shop|Pod|quiet|uid-q"), Kind: "Pod", Namespace: "shop", Name: "quiet"},
	}
	edges := []identity.EdgeSnap{
		{Type: "runs-on", From: podKey, To: nodeKey, LastConfirmed: at.Add(-10 * time.Second)},                      // valid
		{Type: "runs-on", From: "i|cl|shop|Pod|quiet|uid-q", To: nodeKey, LastConfirmed: at.Add(-10 * time.Minute)}, // suspect
		{Type: "mounts", From: podKey, To: "i|cl|shop|PVC|gone|uid-x", LastConfirmed: at},                           // dangling (PVC not in inventory)
	}
	budgets := map[string]time.Duration{"runs-on": 90 * time.Second}
	findings := []detect.Finding{{Phenomenon: "PHEN_THROTTLING_CASCADE", EntityCEI: podKey, Quality: detect.QualityFull}}
	unexp := []unexplained.Finding{{Scope: nodeKey, Status: unexplained.StatusAging}}
	selected := map[string][]string{podKey: {"PHEN_THROTTLING_CASCADE"}}

	v := BuildTopology("cl", "v", at, inv, edges, budgets, findings, unexp, selected, map[string]bool{podKey: true})

	if v.Summary.Nodes != 3 || v.Summary.Edges != 2 {
		t.Errorf("nodes/edges wrong (dangling mounts edge must be excluded): %+v", v.Summary)
	}
	if v.Summary.Matched != 1 || v.Summary.Loud != 1 || v.Summary.Selected != 1 {
		t.Errorf("marks wrong: %+v", v.Summary)
	}
	if v.Summary.ValidEdges != 1 || v.Summary.SuspectEdges != 1 {
		t.Errorf("edge validity classification wrong: %+v", v.Summary)
	}
	pod := nodeByKey(v.Nodes, podKey)
	if pod == nil || !pod.Matched || pod.Degraded || !pod.Selected {
		t.Errorf("pod node marks wrong: %+v", pod)
	}
	node := nodeByKey(v.Nodes, nodeKey)
	if node == nil || !node.Loud || node.Matched {
		t.Errorf("node should be loud (unexplained) but not matched: %+v", node)
	}
}

func nodeByKey(ns []TopoNode, key string) *TopoNode {
	for i := range ns {
		if ns[i].CEIKey == key {
			return &ns[i]
		}
	}
	return nil
}

func mustCEI(t *testing.T, key string) identity.CEI {
	t.Helper()
	c, err := identity.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// --- M4 timeline --------------------------------------------------------------

// BuildTimeline lays matches + unexplained on time; the projected lane is empty
// with a stated note (PROJECTED arrives in Phase 2 — never fabricated).
func TestBuildTimeline(t *testing.T) {
	findings := []store.FindingRow{{
		Label: "Memory leak", EntityCEI: podKey, Name: "web-a", Kind: "Container", Quality: "degraded",
		FirstSeen: at.Add(-5 * time.Minute), LastSeen: at.Add(-1 * time.Minute),
	}}
	unexp := []store.UnexplainedRow{{
		Scope: nodeKey, Name: "worker-1", Kind: "Node", Status: "resolved",
		FirstSeen: at.Add(-8 * time.Minute), LastSeen: at.Add(-3 * time.Minute),
	}}
	v := BuildTimeline(at, findings, unexp, nil)

	if len(v.Matches) != 1 || v.Matches[0].Class != "MEASURED" || v.Matches[0].Surface != "insight" {
		t.Errorf("match span wrong: %+v", v.Matches)
	}
	if len(v.Unexplained) != 1 || v.Unexplained[0].Surface != "unexplained" || v.Unexplained[0].Status != "resolved" {
		t.Errorf("unexplained span wrong: %+v", v.Unexplained)
	}
	if len(v.Projected) != 0 || v.ProjectedNote == "" {
		t.Errorf("projected lane must be empty with a stated note: %+v / %q", v.Projected, v.ProjectedNote)
	}
	// Window spans the earliest span start to now.
	if !v.Window.From.Equal(at.Add(-8*time.Minute)) || !v.Window.To.Equal(at) {
		t.Errorf("window wrong: %+v", v.Window)
	}
}
