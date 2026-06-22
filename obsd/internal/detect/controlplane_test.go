package detect

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Control-plane phenomena detection (docs/33 build 1, v0.16.0). The four phenomena
// fire from the derived single-series streams the control-plane lane aggregates:
//   - DNS_FAILURE / DNS_CACHE_THRASH: first-order, anchored on the CoreDNS pod, a ratio
//     variable on the anchor itself (the second required member + corroboration are named
//     but uncheck'd → a DEGRADED match, completeness 0.5).
//   - APISERVER_OVERLOAD_APF / WEBHOOK_LATENCY: entity-local on the control-plane node, a
//     rate-guard variable (APF / admission-webhook rejections breaching the count bar).
//
// These pin the firing LOGIC deterministically (no cluster, no timing): the live lane's
// job is only to put the crossed variable on the fingerprint; here we hand it one.

const (
	cpCoreDNSPodKey = "i|cl|kube-system|Pod|coredns-x|dns-uid-1"
	cpNodeKey       = "i|cl||Node|control-1|node-uid-1"
)

// corednsCacheThrashFP: the CoreDNS pod's CONTAINER fingerprint (keyed by the pod CEI, the
// compiler's container-anchors-on-pod convention) carrying its cache-miss RATIO over the bar.
func corednsCacheThrashFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpCoreDNSPodKey, Namespace: "kube-system", Name: "coredns-x", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_COREDNS_CACHE_MISS_RATIO", Metric: "coredns_cache_misses",
			State: observe.StateWellAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-miss", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// corednsServfailFP: the CoreDNS pod's CONTAINER fingerprint carrying its SERVFAIL/REFUSED RATIO.
func corednsServfailFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpCoreDNSPodKey, Namespace: "kube-system", Name: "coredns-x", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_COREDNS_SERVFAIL_RATIO", Metric: "coredns_dns_responses_failed",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-fail", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// apfRejectedFP: the control-plane node with the APF-rejection rate guard breached.
func apfRejectedFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpNodeKey, Name: "control-1", Kind: "Node", EvaluatedAt: evalAt,
		Rates: []observe.VariableRate{{
			RuleID: "THR_APISERVER_APF_REJECTED", Metric: "apiserver_flowcontrol_rejected",
			WindowDelta: 4, Bar: 1, Breached: true, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-apf", SampleAt: evalAt, How: "rate-guard"},
		}},
	}
}

// webhookRejectedFP: the control-plane node with the admission-webhook rejection guard breached.
func webhookRejectedFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpNodeKey, Name: "control-1", Kind: "Node", EvaluatedAt: evalAt,
		Rates: []observe.VariableRate{{
			RuleID: "THR_APISERVER_WEBHOOK_REJECTIONS", Metric: "apiserver_admission_webhook_rejections",
			WindowDelta: 3, Bar: 1, Breached: true, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-wh", SampleAt: evalAt, How: "rate-guard"},
		}},
	}
}

// newCPTopo is a minimal real topology (an empty edge store): the first-order DNS
// phenomena's anchor checks evaluate on the CoreDNS pod itself, so no edge is needed,
// but Match needs a non-nil Topology to evaluate spanned phenomena at all.
func newCPTopo(t *testing.T) *identity.EdgeStore {
	t.Helper()
	return identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
}

func TestControlPlaneDNSFiresFirstOrderOnCoreDNSPod(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := newCPTopo(t)
	_ = topo

	out := m.Match([]observe.Fingerprint{corednsCacheThrashFP()}, nil, topo, w)
	if f := findPhen(out, "PHEN_DNS_CACHE_THRASH"); f == nil {
		t.Fatalf("DNS_CACHE_THRASH should fire on the CoreDNS pod; findings: %+v", out)
	} else if f.EntityCEI != cpCoreDNSPodKey || f.Span != "first-order" || f.RequiredMet < 1 {
		t.Errorf("DNS_CACHE_THRASH = entity %s span %s met %d/%d, want CoreDNS pod / first-order / >=1 met",
			f.EntityCEI, f.Span, f.RequiredMet, f.RequiredTotal)
	}

	out = m.Match([]observe.Fingerprint{corednsServfailFP()}, nil, topo, w)
	if f := findPhen(out, "PHEN_DNS_FAILURE"); f == nil {
		t.Fatalf("DNS_FAILURE should fire on the CoreDNS pod; findings: %+v", out)
	} else if f.EntityCEI != cpCoreDNSPodKey || f.RequiredMet < 1 {
		t.Errorf("DNS_FAILURE = entity %s met %d/%d, want CoreDNS pod / >=1 met", f.EntityCEI, f.RequiredMet, f.RequiredTotal)
	}
}

func TestControlPlaneAPIServerFiresEntityLocalOnNode(t *testing.T) {
	m := NewMatcher(loadGraph(t))

	// Entity-local: no topology needed.
	if f := findPhen(m.MatchFingerprint(apfRejectedFP()), "PHEN_APISERVER_OVERLOAD_APF"); f == nil {
		t.Fatalf("APISERVER_OVERLOAD_APF should fire on the control-plane node")
	} else if f.EntityCEI != cpNodeKey || f.RequiredMet < 1 {
		t.Errorf("APISERVER_OVERLOAD_APF = entity %s met %d/%d, want control node / >=1 met", f.EntityCEI, f.RequiredMet, f.RequiredTotal)
	}

	if f := findPhen(m.MatchFingerprint(webhookRejectedFP()), "PHEN_WEBHOOK_LATENCY"); f == nil {
		t.Fatalf("WEBHOOK_LATENCY should fire on the control-plane node")
	} else if f.EntityCEI != cpNodeKey || f.RequiredMet < 1 {
		t.Errorf("WEBHOOK_LATENCY = entity %s met %d/%d, want control node / >=1 met", f.EntityCEI, f.RequiredMet, f.RequiredTotal)
	}
}

// A healthy CoreDNS pod (ratio variable present but BELOW the bar) must NOT fire — the
// conjunction's observable required member is not satisfied.
func TestControlPlaneDNSSilentWhenBelowBar(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := corednsCacheThrashFP()
	fp.Thresholds[0].State = observe.StateBelow
	if f := findPhen(m.Match([]observe.Fingerprint{fp}, nil, newCPTopo(t), w), "PHEN_DNS_CACHE_THRASH"); f != nil {
		t.Errorf("DNS_CACHE_THRASH must NOT fire below the bar, got %+v", f)
	}
}

// --- Bucket-C: PDB_VIOLATION (docs/33 build 2) -------------------------------

const cpPDBKey = "i|cl|kube-system|PodDisruptionBudget|web|pdb-uid-1"

// pdbViolatedFP: the PDB anchor (Kind=PDB) with current/desired healthy ratio crossed
// BELOW the 1.0 bar (StateAbove on a below-direction rule = a real violation).
func pdbViolatedFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpPDBKey, Namespace: "kube-system", Name: "web", Kind: "PDB", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_PDB_CURRENT_BELOW_DESIRED_HEALTHY", Metric: "kube_poddisruptionbudget_status_current_healthy",
			State: observe.StateAbove, Direction: "below", BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-pdb", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

func TestPDBViolationFiresOnPDBAnchor(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	out := m.Match([]observe.Fingerprint{pdbViolatedFP()}, nil, newCPTopo(t), w)
	if f := findPhen(out, "PHEN_PDB_VIOLATION"); f == nil {
		t.Fatalf("PDB_VIOLATION should fire on the PDB; findings: %+v", out)
	} else if f.EntityCEI != cpPDBKey || f.Span != "first-order" || f.RequiredMet < 1 {
		t.Errorf("PDB_VIOLATION = entity %s span %s met %d/%d, want the PDB / first-order / >=1 met",
			f.EntityCEI, f.Span, f.RequiredMet, f.RequiredTotal)
	}
}

// A healthy PDB (ratio at/above 1.0 → StateAtThreshold, not crossed) must NOT fire.
func TestPDBViolationSilentWhenHealthy(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := pdbViolatedFP()
	fp.Thresholds[0].State = observe.StateAtThreshold // current == desired
	if f := findPhen(m.Match([]observe.Fingerprint{fp}, nil, newCPTopo(t), w), "PHEN_PDB_VIOLATION"); f != nil {
		t.Errorf("PDB_VIOLATION must NOT fire when current==desired (ratio 1.0), got %+v", f)
	}
}

// --- Bucket-C: IMAGE_GC_EVENTS (docs/33 build 2) -----------------------------

// imageGCFP: a node with the kubelet remove_image rate guard breached (image GC active).
func imageGCFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: cpNodeKey, Name: "control-1", Kind: "Node", EvaluatedAt: evalAt,
		Rates: []observe.VariableRate{{
			RuleID: "THR_KUBELET_IMAGE_GC_REMOVALS", Metric: "kubelet_runtime_operations_total",
			WindowDelta: 3, Bar: 1, Breached: true, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-imgc", SampleAt: evalAt, How: "rate-guard"},
		}},
	}
}

func TestImageGCFiresEntityLocalOnNode(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	if f := findPhen(m.MatchFingerprint(imageGCFP()), "PHEN_IMAGE_GC_EVENTS"); f == nil {
		t.Fatalf("IMAGE_GC_EVENTS should fire on the node when remove_image removals breach the guard")
	} else if f.EntityCEI != cpNodeKey || f.RequiredMet < 1 {
		t.Errorf("IMAGE_GC_EVENTS = entity %s met %d/%d, want the node / >=1 met", f.EntityCEI, f.RequiredMet, f.RequiredTotal)
	}
}
