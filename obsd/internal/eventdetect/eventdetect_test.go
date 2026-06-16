package eventdetect

import (
	"reflect"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
)

var eventAt = time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

func loadGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

// The two authored detections (mirror ontology/.../experimental/event-conditions-v1.yaml).
func oomDetection() events.Detection {
	return events.Detection{
		Reason: "OOMKilled", InvolvedKind: "Pod",
		Phenomenon: "PHEN_OOM_KILL_CGROUP", MemberSignal: "SIG_oomkilled_oomkilling_d6c7e936",
		TemporalOrder: "T0+PLEG", Why: "an OOMKilled IS the cgroup-OOM phenomenon's required member",
		Author: "vigil-engineering",
	}
}

func crashDetection() events.Detection {
	return events.Detection{
		Reason: "CrashLoopBackOff", InvolvedKind: "Pod",
		Phenomenon:    "PHEN_PROBE_FAILURE_RESTART",
		MemberSignal:  "SIG_container_failure_events_backoff_crashloopbackoff_error_1cee58e3",
		TemporalOrder: "T0+sustained", Why: "a CrashLoopBackOff IS the probe-failure→restart phenomenon's required member",
		Author: "vigil-engineering",
	}
}

func resolvedEvent(reason string) events.EventFinding {
	return events.EventFinding{
		Reason: reason, EntityCEI: "i|cl|shop|Pod|currency-1|uid-1",
		RoleCEI: "r|cl|shop|Deployment|currencyservice", RoleUnresolved: false,
		Namespace: "shop", Name: "currency-1", Kind: "Pod", Count: 3,
		FirstTimestamp: eventAt, LastTimestamp: eventAt,
	}
}

// Happy path: a real resolved OOMKilled event produces ONE degraded MEASURED finding
// for OOM_KILL_CGROUP — the event member observed, every other required member named
// unobservable, completeness 1/required.
func TestOOMKilledDegraded(t *testing.T) {
	g := loadGraph(t)
	out := Findings(g, []events.EventFinding{resolvedEvent("OOMKilled")}, []events.Detection{oomDetection()}, eventAt)
	if len(out) != 1 {
		t.Fatalf("want 1 finding, got %d", len(out))
	}
	f := out[0]
	if f.Phenomenon != "PHEN_OOM_KILL_CGROUP" {
		t.Errorf("phenomenon = %q", f.Phenomenon)
	}
	if f.Quality != detect.QualityDegraded {
		t.Errorf("quality = %q, want degraded", f.Quality)
	}
	if f.EntityCEI != "i|cl|shop|Pod|currency-1|uid-1" {
		t.Errorf("entity = %q (want the INSTANCE pod key, so it relates to fp findings by containment)", f.EntityCEI)
	}
	if f.RequiredMet != 1 {
		t.Errorf("requiredMet = %d, want 1 (the one discrete-event member)", f.RequiredMet)
	}
	if f.RequiredTotal != 8 { // OOM_KILL_CGROUP has 8 required members in the KG
		t.Errorf("requiredTotal = %d, want 8", f.RequiredTotal)
	}
	if len(f.Members) != 1 || f.Members[0].Metric != "k8s-event:OOMKilled" || !f.Members[0].Met {
		t.Errorf("event member evidence wrong: %+v", f.Members)
	}
	if f.Members[0].Note == "" {
		t.Errorf("event member must carry the AUTHORED note adjacent")
	}
	if len(f.Unobservable) != 7 || f.RequiredUnobserved != 7 {
		t.Errorf("unobservable = %d / requiredUnobserved = %d, want 7/7", len(f.Unobservable), f.RequiredUnobserved)
	}
	if f.Completeness <= 0 || f.Completeness >= 1 {
		t.Errorf("completeness = %v, want a degraded fraction", f.Completeness)
	}
}

func TestCrashLoopProbeFailure(t *testing.T) {
	g := loadGraph(t)
	out := Findings(g, []events.EventFinding{resolvedEvent("CrashLoopBackOff")}, []events.Detection{crashDetection()}, eventAt)
	if len(out) != 1 {
		t.Fatalf("want 1 finding, got %d", len(out))
	}
	f := out[0]
	if f.Phenomenon != "PHEN_PROBE_FAILURE_RESTART" {
		t.Errorf("phenomenon = %q", f.Phenomenon)
	}
	if f.RequiredTotal != 11 || f.RequiredMet != 1 { // 11 required members in the KG
		t.Errorf("required %d/%d, want 1/11", f.RequiredMet, f.RequiredTotal)
	}
}

// No-false-upgrade #1: a role-unresolved event NEVER produces a workload-anchored
// phenomenon finding (never guess the role).
func TestRoleUnresolvedProducesNothing(t *testing.T) {
	g := loadGraph(t)
	e := resolvedEvent("OOMKilled")
	e.RoleUnresolved = true
	e.RoleCEI = e.EntityCEI
	out := Findings(g, []events.EventFinding{e}, []events.Detection{oomDetection()}, eventAt)
	if len(out) != 0 {
		t.Fatalf("role-unresolved event must produce no phenomenon finding, got %d", len(out))
	}
}

// No-false-upgrade #2: an event reason with no authored detection is never upgraded.
func TestUnrelatedReasonIgnored(t *testing.T) {
	g := loadGraph(t)
	out := Findings(g, []events.EventFinding{resolvedEvent("Started"), resolvedEvent("BackOff")},
		[]events.Detection{oomDetection(), crashDetection()}, eventAt)
	if len(out) != 0 {
		t.Fatalf("unauthored reasons must produce nothing, got %d", len(out))
	}
}

// No-false-upgrade #3 (defence in depth): a detection whose member_signal is NOT a
// required member of the phenomenon produces nothing.
func TestNonRequiredMemberDropped(t *testing.T) {
	g := loadGraph(t)
	bad := oomDetection()
	// A real CORROBORATING member of OOM_KILL_CGROUP (never a required one).
	bad.MemberSignal = "SIG_container_memory_family_14_metrics_529498d3"
	out := Findings(g, []events.EventFinding{resolvedEvent("OOMKilled")}, []events.Detection{bad}, eventAt)
	if len(out) != 0 {
		t.Fatalf("a non-required member_signal must produce nothing, got %d", len(out))
	}
}

// Determinism: same inputs ⇒ byte-identical findings (the replay contract for the lane).
func TestDeterministic(t *testing.T) {
	g := loadGraph(t)
	evs := []events.EventFinding{resolvedEvent("OOMKilled"), resolvedEvent("CrashLoopBackOff")}
	dets := []events.Detection{oomDetection(), crashDetection()}
	a := Findings(g, evs, dets, eventAt)
	b := Findings(g, evs, dets, eventAt)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic output:\n a=%+v\n b=%+v", a, b)
	}
	if len(a) != 2 {
		t.Fatalf("want 2 findings (oom + crashloop), got %d", len(a))
	}
}

// The REAL authored overlay parses and validates referentially (member is required).
func TestRealOverlayReferentialIntegrity(t *testing.T) {
	g := loadGraph(t)
	dets, err := events.LoadEventDetections(overlayDir + "/experimental/event-conditions-v1.yaml")
	if err != nil {
		t.Fatalf("LoadEventDetections: %v", err)
	}
	if len(dets) != 2 {
		t.Fatalf("want 2 authored detections, got %d", len(dets))
	}
	for _, d := range dets {
		p := g.Phenomena[d.Phenomenon]
		if p == nil {
			t.Fatalf("detection phenomenon %q absent from graph", d.Phenomenon)
		}
		req := false
		for _, m := range p.Members {
			if m.SignalID == d.MemberSignal && m.Role == "required" {
				req = true
			}
		}
		if !req {
			t.Errorf("detection %q member_signal %q is not a REQUIRED member of %q", d.Reason, d.MemberSignal, d.Phenomenon)
		}
	}
}

// Empty inputs are safe no-ops (events-disabled and no-detections paths).
func TestEmptyInputs(t *testing.T) {
	g := loadGraph(t)
	if out := Findings(g, nil, []events.Detection{oomDetection()}, eventAt); out != nil {
		t.Errorf("no events ⇒ nil, got %v", out)
	}
	if out := Findings(g, []events.EventFinding{resolvedEvent("OOMKilled")}, nil, eventAt); out != nil {
		t.Errorf("no detections ⇒ nil, got %v", out)
	}
}
