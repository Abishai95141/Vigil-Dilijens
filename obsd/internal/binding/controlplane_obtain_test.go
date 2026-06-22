package binding

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// TestControlPlaneScrapedFlipsObtainability pins the docs/33 build-1 obtainability flip:
// a Metric signal whose only emitting tool is the kube-apiserver or CoreDNS is out-of-scope
// by default (their /metrics endpoints are unscraped), and becomes Obtainable exactly when
// the control-plane lane is on (ControlPlaneMetricsScraped). Off ⇒ byte-identical to before.
func TestControlPlaneScrapedFlipsObtainability(t *testing.T) {
	g := loadGraph(t)

	// Find a Metric signal emitted only by the apiserver and one emitted only by CoreDNS.
	apiSig := pickToolOnlySignal(t, g, "kube-apiserver")
	dnsSig := pickToolOnlySignal(t, g, "coredns")

	// Lane OFF (default): both honestly out-of-scope (their endpoints are unscraped).
	// kube-apiserver is a native tool (always present); CoreDNS is detected from the
	// workload corpus, so seed it present — the lane gates the SCRAPE, not the presence.
	off := PlatformFacts{
		KubeletVersion: "v1.35.0", ContainerRuntime: "containerd://2.0.0", KernelVersion: "6.12.0",
		WorkloadCorpus: []string{"coredns-abc rancher/mirrored-coredns-coredns:1.12"},
	}
	repOff := GateSignals(g, off)
	if st := repOff.PerSignal[apiSig].State; st != OutOfScopeUnobtainable {
		t.Errorf("apiserver signal %s with lane OFF = %q, want out-of-scope", apiSig, st)
	}
	if st := repOff.PerSignal[dnsSig].State; st != OutOfScopeUnobtainable {
		t.Errorf("coredns signal %s with lane OFF = %q, want out-of-scope", dnsSig, st)
	}

	// Lane ON: the unscraped-endpoint override lifts for apiserver + coredns. The apiserver
	// APF signal additionally needs CAP_APF_ENABLED (asserted here, as the operator would).
	on := off
	on.ControlPlaneMetricsScraped = true
	on.AssertedCapabilities = map[string]bool{"CAP_APF_ENABLED": true}
	repOn := GateSignals(g, on)
	if st := repOn.PerSignal[apiSig].State; st != Obtainable {
		t.Errorf("apiserver signal %s with lane ON = %q (%v), want obtainable", apiSig, st, repOn.PerSignal[apiSig].Reasons)
	}
	if st := repOn.PerSignal[dnsSig].State; st != Obtainable {
		t.Errorf("coredns signal %s with lane ON = %q (%v), want obtainable", dnsSig, st, repOn.PerSignal[dnsSig].Reasons)
	}
}

// pickToolOnlySignal returns a Metric signal whose ONLY emitting tool is the given one
// (so the unscraped-endpoint override applies to it cleanly).
func pickToolOnlySignal(t *testing.T, g *graph.Graph, tool string) string {
	t.Helper()
	best := ""
	for id, s := range g.Signals {
		if s.Modality != "Metric" || len(s.Tools) != 1 || s.Tools[0] != tool {
			continue
		}
		if best == "" || id < best { // deterministic pick
			best = id
		}
	}
	if best == "" {
		t.Fatalf("no Metric signal emitted solely by %q in the graph", tool)
	}
	return best
}
