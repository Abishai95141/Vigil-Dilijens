package detect

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// G2 KSM lane golden tests, on the REAL ontology + the experimental KSM overlay
// (loaded behind --ksm-enabled). The authored restart-counter member of
// PHEN_PROBE_FAILURE_RESTART — left UNCHECKED in detect-conditions-v3 because no
// scrapable channel existed ("the KSM-side restart/lastState members have no
// scrapable channel here") — is now bound to the KSM gauge
// kube_pod_container_status_restarts_total via the rate guard
// THR_CONTAINER_RESTARTS_RATE. The headline assertion: this completes the authored
// THROTTLING_CASCADE -> PROBE_FAILURE_RESTART relation for live cascade recognition,
// while staying honestly DEGRADED (1 of 11 required members observable here).

// loadKSMGraph loads the base KG + the RELEASED overlays — which now include the
// KSM checks (detect-conditions-v4, promoted via governance). The check is part of
// the released graph; a finding is produced only when obsd actually scrapes KSM
// (--ksm-enabled), which is what makes the restart member observable.
func loadKSMGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

// restartBurstFP: a container whose restart counter climbed past the flagged
// crash-loop guard (>3 in the 15m window) — the KSM-sourced restart-rate variable.
func restartBurstFP(key string, at time.Time, breached bool) observe.Fingerprint {
	delta := 5.0
	if !breached {
		delta = 1.0
	}
	return observe.Fingerprint{
		CEIKey: key, Namespace: "shop", Name: "app", Kind: "Container", EvaluatedAt: at,
		Rates: []observe.VariableRate{{
			RuleID: "THR_CONTAINER_RESTARTS_RATE", Metric: "kube_pod_container_status_restarts_total",
			WindowDelta: delta, Bar: 3, Breached: breached, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-restart", SampleAt: at, How: "rate-guard"},
		}},
	}
}

// The keystone: a container's restart rate crossing the flagged crash-loop guard
// produces a DEGRADED MEASURED finding for PHEN_PROBE_FAILURE_RESTART — the FIRST
// in-digest member-check for this phenomenon (until KSM was ingested it was only
// reachable off-digest via the CrashLoopBackOff event lane).
func TestProbeFailureRestartFiresOnRestartRate(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	out := m.MatchFingerprint(restartBurstFP(containerKey, evalAt, true))
	f := findPhen(out, "PHEN_PROBE_FAILURE_RESTART")
	if f == nil {
		t.Fatalf("PHEN_PROBE_FAILURE_RESTART must fire on a crossed restart-rate guard; findings: %+v", out)
	}
	// Honestly DEGRADED: 1 of 11 required members is observable on this signal set;
	// the other ten (prober events, image-pull backoff, lastState, ...) are NAMED.
	if f.Quality != QualityDegraded {
		t.Errorf("quality = %s, want degraded (only the restart member is observable here)", f.Quality)
	}
	if f.RequiredMet != 1 {
		t.Errorf("RequiredMet = %d, want 1 (the restart counter)", f.RequiredMet)
	}
	if f.RequiredTotal != 11 {
		t.Errorf("RequiredTotal = %d, want 11 (the authored required-member set)", f.RequiredTotal)
	}
	if len(f.Members) != 1 || f.Members[0].Metric != "kube_pod_container_status_restarts_total" {
		t.Errorf("member evidence = %+v, want the restart-counter crossing", f.Members)
	}
	// Borrowed normativity: there is no customer config for restart tolerance, so the
	// bar is a FLAGGED ontology default — surfaced as lower-trust, never invented.
	if !f.Members[0].BarFlagged {
		t.Errorf("the restart guard must be a FLAGGED default (no config source for restart tolerance)")
	}
	// degrade-never-fabricate: the unobserved required members are NAMED, not faked.
	if len(f.Unobservable) == 0 {
		t.Errorf("the unobservable required members must be NAMED on the finding (degrade-never-fabricate)")
	}
}

// Under the guard (restart rate not breached) it stays SILENT — no fabricated alarm.
func TestProbeFailureRestartSilentWhenNotBreached(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	out := m.MatchFingerprint(restartBurstFP(containerKey, evalAt, false))
	if f := findPhen(out, "PHEN_PROBE_FAILURE_RESTART"); f != nil {
		t.Fatalf("a restart rate under the guard must not fire; got %+v", f)
	}
}

// No KSM restart variable at all (the lane is off, or the counter never climbed) ⇒
// no finding through the in-digest path, never invented.
func TestProbeFailureRestartSilentWhenUnobserved(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	bare := observe.Fingerprint{
		CEIKey: containerKey, Namespace: "shop", Name: "app", Kind: "Container", EvaluatedAt: evalAt,
	}
	if f := findPhen(m.MatchFingerprint(bare), "PHEN_PROBE_FAILURE_RESTART"); f != nil {
		t.Fatalf("no restart variable ⇒ no finding; got %+v", f)
	}
}

// The headline cascade: a CPU-throttled container that is ALSO crash-looping lights
// up the authored THROTTLING_CASCADE -> PROBE_FAILURE_RESTART relation as ONE story,
// with the curator's "Probe cascade" note verbatim. The restart member is what makes
// the downstream end observable — this is the G2 payoff (the cascade was dark before).
func TestThrottleToProbeRestartCascade(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	topo := edgeStore(t, evalAt.Add(-10*time.Second)) // container runs-on node

	// One container: throttled (the authored trigger, full with the node PSI hop) AND
	// crash-looping (the authored downstream, via the KSM restart rate).
	fp := throttledContainerFP()
	fp.Rates = restartBurstFP(containerKey, evalAt, true).Rates
	fps := []observe.Fingerprint{fp, nodePSIFP(observe.StateAbove)}

	findings := m.Match(fps, nil, topo, w)
	if findPhen(findings, "PHEN_THROTTLING_CASCADE") == nil {
		t.Fatalf("setup: THROTTLING_CASCADE (the trigger) must fire; findings: %+v", findings)
	}
	if findPhen(findings, "PHEN_PROBE_FAILURE_RESTART") == nil {
		t.Fatalf("setup: PROBE_FAILURE_RESTART (the downstream) must fire on the restart rate; findings: %+v", findings)
	}

	cs := m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w)
	c := findCascade(cs, "PHEN_THROTTLING_CASCADE", "PHEN_PROBE_FAILURE_RESTART")
	if c == nil {
		t.Fatalf("the authored THROTTLING_CASCADE -> PROBE_FAILURE_RESTART relation must be recognized as one story: %+v", cs)
	}
	// The AUTHORED relation surfaces verbatim — a co-occurrence lit up, never a
	// derived causal claim.
	if c.Why != "Probe cascade" {
		t.Errorf("cascade why = %q, want the authored \"Probe cascade\"", c.Why)
	}
}

// NON-GATING: the v4 KSM check is part of the released graph, but it is INERT until
// obsd scrapes KSM. A fingerprint with NO restart variable (the off-by-default state,
// no --ksm-enabled) produces no PROBE_FAILURE_RESTART finding — so a cluster without
// the KSM scrape sees byte-identical detection. (The keystone "silent when unobserved"
// case proves this directly; this is the explicit released-but-inert assertion.)
func TestKSMCheckReleasedButInertWithoutScrape(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	// A fully-formed container fingerprint carrying every NON-KSM signature (so the
	// graph is exercised) but no KSM restart variable: no finding is invented.
	noKSM := observe.Fingerprint{
		CEIKey: containerKey, Namespace: "shop", Name: "app", Kind: "Container", EvaluatedAt: evalAt,
	}
	if f := findPhen(m.MatchFingerprint(noKSM), "PHEN_PROBE_FAILURE_RESTART"); f != nil {
		t.Fatalf("the released v4 check must be INERT without a KSM restart stream; got %+v", f)
	}
}

// EVICTION_MEMORY upgrade (degraded → fuller): the node-memory anchor was the only
// scrapable member; the KSM-derived evicted-pod gauge (kube_pod_status_evicted, one
// runs-on hop from the node) adds the pod-side member. With both, the first-order
// match observes 2 required members, not 1 — a fuller, more confident DEGRADED finding.
func TestEvictionMemoryFullerWithEvictedPod(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	topo := edgeStore(t, evalAt.Add(-10*time.Second)) // podKey runs-on nodeKey

	nodeFP := observe.Fingerprint{ // the node-memory anchor (config-relative bar crossed)
		CEIKey: nodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE", Metric: "node_memory_MemAvailable_bytes",
			State: observe.StateAbove, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s-mem", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
	podFP := observe.Fingerprint{ // the evicted-pod neighbour (the derived KSM gauge)
		CEIKey: podKey, Namespace: "shop", Name: "app-7d9", Kind: "Pod", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_POD_EVICTED", Metric: "kube_pod_status_evicted",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-evicted", SampleAt: evalAt, How: "gauge-level"},
		}},
	}

	out := m.Match([]observe.Fingerprint{nodeFP, podFP}, nil, topo, w)
	f := findPhen(out, "PHEN_EVICTION_MEMORY")
	if f == nil {
		t.Fatalf("EVICTION_MEMORY should fire; got %+v", out)
	}
	if f.RequiredMet < 2 {
		t.Errorf("with the evicted-pod neighbour member observed, RequiredMet should be >=2 "+
			"(node memory + evicted pod), got %d/%d", f.RequiredMet, f.RequiredTotal)
	}
	hasEvicted := false
	for _, mem := range f.Members {
		if mem.Metric == "kube_pod_status_evicted" {
			hasEvicted = true
		}
	}
	if !hasEvicted {
		t.Errorf("the KSM-derived evicted-pod member must be in the finding evidence: %+v", f.Members)
	}
}

// Absent the runs-on edge, an evicted pod and a memory-pressured node are NOT one
// story — the neighbour member must NOT be fabricated across an unconfirmed topology
// (the validity contract: an absent edge contributes nothing).
func TestEvictionMemoryNoEvictedMemberWithoutEdge(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	// An edge store with NO runs-on asserted: the pod and node are unrelated.
	noEdges := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)

	nodeFP := observe.Fingerprint{
		CEIKey: nodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE", Metric: "node_memory_MemAvailable_bytes",
			State: observe.StateAbove, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s-mem", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
	podFP := observe.Fingerprint{
		CEIKey: podKey, Namespace: "shop", Name: "app-7d9", Kind: "Pod", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_POD_EVICTED", Metric: "kube_pod_status_evicted",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-evicted", SampleAt: evalAt, How: "gauge-level"},
		}},
	}

	f := findPhen(m.Match([]observe.Fingerprint{nodeFP, podFP}, nil, noEdges, w), "PHEN_EVICTION_MEMORY")
	if f == nil {
		t.Fatalf("EVICTION_MEMORY should still fire on the node anchor; got nil")
	}
	for _, mem := range f.Members {
		if mem.Metric == "kube_pod_status_evicted" {
			t.Errorf("without a runs-on edge the evicted-pod member must NOT be observed: %+v", f.Members)
		}
	}
}

// A throttle on one pod and a restart burst on an UNRELATED pod is two findings, not
// one story — the cascade must NOT pair topologically-unrelated entities.
func TestThrottleProbeRestartUnrelatedNoCascade(t *testing.T) {
	m := NewMatcher(loadKSMGraph(t))
	topo := edgeStore(t, evalAt.Add(-10*time.Second))

	throttle := throttledContainerFP() // on containerKey
	restartElsewhere := restartBurstFP("i|cl|other|Container|x|poduid-9/x", evalAt, true)
	findings := m.Match([]observe.Fingerprint{throttle, nodePSIFP(observe.StateAbove), restartElsewhere}, nil, topo, w)

	cs := m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w)
	if findCascade(cs, "PHEN_THROTTLING_CASCADE", "PHEN_PROBE_FAILURE_RESTART") != nil {
		t.Errorf("unrelated entities must NOT pair into a cascade: %+v", cs)
	}
}
