package binding

import (
	"strings"
	"testing"
)

// kindFacts mirrors the live dev cluster: kind v1.35 on containerd, kernel 6.12,
// only native components + coredns/kube-proxy/etcd/local-path running — no KSM,
// no node-exporter, no CNI-vendor agents.
func kindFacts() PlatformFacts {
	return PlatformFacts{
		KubeletVersion:   "v1.35.0",
		ContainerRuntime: "containerd://2.2.0",
		KernelVersion:    "6.12.76-linuxkit",
		OSImage:          "Debian GNU/Linux 12 (bookworm)",
		WorkloadCorpus: []string{
			"coredns-7d764666f9-c4kpl registry.k8s.io/coredns/coredns:v1.13.1",
			"etcd-vigil-control-plane registry.k8s.io/etcd:3.6.6-0",
			"kube-apiserver-vigil-control-plane registry.k8s.io/kube-apiserver:v1.35.0",
			"kube-controller-manager-vigil-control-plane registry.k8s.io/kube-controller-manager:v1.35.0",
			"kube-proxy-g4gtb registry.k8s.io/kube-proxy:v1.35.0",
			"kube-scheduler-vigil-control-plane registry.k8s.io/kube-scheduler:v1.35.0",
			"local-path-provisioner-67b8995b4b docker.io/kindest/local-path-provisioner:v20250214",
			"frontend-759775d795 us-central1-docker.pkg.dev/google-samples/microservices-demo/frontend:v0.10.3",
		},
	}
}

// Every one of the catalogue's 592 signals lands in exactly one obtainability
// state (doc 04 M1 exit: "every expected signal lands in a state with a stated
// reason"), and the kind-cluster verdicts are the known-correct ones. (592 =
// 589 + the 3 init-container members that wire PHEN_INIT_CONTAINER_FAILURE: the
// KSM-gated restart counter + last-terminated reason, and the kubelet crashloop event.)
func TestGateSignalsKindCluster(t *testing.T) {
	g := loadGraph(t)
	rep := GateSignals(g, kindFacts())

	total := 0
	for _, n := range rep.Counts {
		total += n
	}
	if total != 592 || len(rep.PerSignal) != 592 {
		t.Fatalf("gated %d/%d signals, want 592 in exactly one state each (counts=%v)", len(rep.PerSignal), total, rep.Counts)
	}
	for id, av := range rep.PerSignal {
		if av.State != Obtainable && len(av.Reasons) == 0 {
			t.Errorf("%s: state %s with NO stated reason", id, av.State)
		}
	}

	cases := []struct {
		sig        string
		want       Obtainability
		wantReason string
	}{
		// cAdvisor container memory family: kubelet-native, cap CADVISOR_GE_43 met on 1.35.
		{"SIG_container_memory_family_14_metrics_529498d3", Obtainable, ""},
		// node-exporter signal: tool absent AND capability not met.
		{"SIG_node_nf_conntrack_entries_6161e704", OutOfScopeUnobtainable, "node-exporter"},
		// KSM family: not deployed.
		{"SIG_kube_pod_family_30_metrics_3f7fa86f", OutOfScopeUnobtainable, "kube-state-metrics"},
		// CoreDNS: deployed in kind, BUT obsd scrapes no CoreDNS /metrics endpoint — so a
		// resolver-Metric signal whose only tool is coredns is OUT-OF-SCOPE with the accurate
		// reason (it was previously over-claimed obtainable via tool-presence, contradicting the
		// blindspot surface — the full-functionality-test HIGH finding). MODALITY-AWARE: coredns
		// Event/State signals stay obtainable.
		{"SIG_coredns_family_full_enumeration_in_network_sheet_9e645682", OutOfScopeUnobtainable, "CoreDNS /metrics endpoint not scraped"},
		// PSI metrics: node-exporter absent dominates (out-of-scope), even though
		// CONFIG_PSI would be indeterminate.
		{"SIG_node_pressure_psi_12_metrics_ba96070d", OutOfScopeUnobtainable, "node-exporter"},
		// Kubelet-/metrics-only METRIC signal: the kubelet exists but obsd does not scrape
		// its own /metrics endpoint, so it is OUT-OF-SCOPE with the accurate reason (it was
		// previously over-claimed obtainable via the native-kubelet gate). MODALITY-AWARE.
		{"SIG_kubelet_volume_stats_used_bytes_38fa9389", OutOfScopeUnobtainable, "kubelet /metrics endpoint not scraped"},
		// Kubelet-tagged EVENT signals: the "kubelet" tag names the originating component, not
		// a /metrics endpoint — obsd ingests these via the EVENTS lane, so they must stay
		// OBTAINABLE (the modality-aware guard against the over-reach the review caught).
		{"SIG_oomkilled_oomkilling_d6c7e936", Obtainable, ""},
		{"SIG_pod_eviction_evicted_7e816318", Obtainable, ""},
	}
	for _, c := range cases {
		av, ok := rep.PerSignal[c.sig]
		if !ok {
			t.Errorf("missing availability for %s", c.sig)
			continue
		}
		if av.State != c.want {
			t.Errorf("%s state = %s (%v), want %s", c.sig, av.State, av.Reasons, c.want)
		}
		if c.wantReason != "" && !strings.Contains(strings.Join(av.Reasons, " "), c.wantReason) {
			t.Errorf("%s reasons %v missing %q", c.sig, av.Reasons, c.wantReason)
		}
	}

	// Tool verdicts: native + detected present; the observability stack absent; the
	// API-undetectable ones stated indeterminate. The kubelet stays PRESENT (it exists and
	// its events/state are ingested via the events/KSM lanes) — only its own /metrics
	// endpoint is unscraped, handled modality-aware per-signal (see the kubelet-/metrics
	// METRIC case above going out-of-scope while the kubelet EVENT cases stay obtainable).
	wantPresent := []string{"cadvisor", "containerd", "coredns", "etcd", "kube-proxy", "kubelet"}
	joinedPresent := strings.Join(rep.ToolsPresent, " ")
	for _, w := range wantPresent {
		if !strings.Contains(joinedPresent, w) {
			t.Errorf("tool %s should be detected present (got %v)", w, rep.ToolsPresent)
		}
	}
	joinedAbsent := strings.Join(rep.ToolsAbsent, " ")
	for _, w := range []string{"kube-state-metrics", "node-exporter", "cilium", "falco"} {
		if !strings.Contains(joinedAbsent, w) {
			t.Errorf("tool %s should be absent (got %v)", w, rep.ToolsAbsent)
		}
	}
	if !strings.Contains(strings.Join(rep.ToolsIndeterminate, " "), "go-pprof") {
		t.Errorf("go-pprof should be indeterminate (got %v)", rep.ToolsIndeterminate)
	}

	// The dockershim-removal gate is ACTIVE (k8s 1.35 + containerd) and lands on
	// the documented container_fs_* signals as a semantics shift, not removal.
	if !strings.Contains(strings.Join(rep.ActiveGates, " "), "GATE_DOCKERSHIM_REMOVED") {
		t.Fatalf("GATE_DOCKERSHIM_REMOVED should be active on 1.35+containerd (got %v)", rep.ActiveGates)
	}
	fsAv := rep.PerSignal["SIG_container_fs_read_seconds_total_63facbfc"]
	if !strings.Contains(strings.Join(fsAv.Gates, " "), "GATE_DOCKERSHIM_REMOVED") {
		t.Errorf("container_fs signal should carry the active dockershim gate (got %v)", fsAv.Gates)
	}
}

func TestKernelAtLeast(t *testing.T) {
	cases := []struct {
		kernel string
		maj    int
		min    int
		want   bool
		err    bool
	}{
		{"6.12.76-linuxkit", 4, 20, true, false},
		{"4.19.0-generic", 4, 20, false, false},
		{"4.20.1", 4, 20, true, false},
		{"5.8.0", 5, 8, true, false},
		{"5.7.19", 5, 8, false, false},
		{"garbage", 4, 18, false, true},
	}
	for _, c := range cases {
		got, err := kernelAtLeast(c.kernel, c.maj, c.min)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("kernelAtLeast(%q, %d.%d) = (%v, %v), want (%v, err=%v)", c.kernel, c.maj, c.min, got, err, c.want, c.err)
		}
	}
}

// An old cluster (k8s 1.20 on Docker) flips the verdicts: cAdvisor cap not met,
// the dockershim gate inactive — the gating actually reads the platform.
func TestGateSignalsOldDockerCluster(t *testing.T) {
	g := loadGraph(t)
	facts := kindFacts()
	facts.KubeletVersion = "v1.20.4"
	facts.ContainerRuntime = "docker://20.10.5"
	rep := GateSignals(g, facts)

	av := rep.PerSignal["SIG_container_memory_family_14_metrics_529498d3"]
	if av.State != OutOfScopeUnobtainable || !strings.Contains(strings.Join(av.Reasons, " "), "CAP_CADVISOR_GE_43") {
		t.Errorf("on k8s 1.20 the cadvisor-43 cap must fail: %+v", av)
	}
	if strings.Contains(strings.Join(rep.ActiveGates, " "), "GATE_DOCKERSHIM_REMOVED") {
		t.Errorf("dockershim gate must NOT be active on a Docker runtime (got %v)", rep.ActiveGates)
	}
}

// Equivalence resolution over the real authored groups: the canonical dialect
// bridge resolves cAdvisor and CRI names to one canonical variable, unknown names
// resolve to nothing (the unexplained channel's territory), and resolution is
// deterministic.
func TestEquivalenceResolveRealGroups(t *testing.T) {
	g := loadGraph(t)
	r, err := NewEquivalenceResolver(g)
	if err != nil {
		t.Fatalf("NewEquivalenceResolver: %v", err)
	}
	if r.Groups() != 35 {
		t.Fatalf("compiled %d groups, want 35", r.Groups())
	}

	m := r.Resolve("container_memory_working_set_bytes")
	if len(m) != 1 || m[0].GroupID != "EQG_WORKING_SET" || m[0].Canonical != "k8s.container.memory.working_set" {
		t.Errorf("working-set resolution = %+v, want exactly EQG_WORKING_SET", m)
	}
	// The CRI/summary-API dialect of the SAME quantity resolves to the SAME group.
	m2 := r.Resolve("memory.workingSetBytes")
	if len(m2) != 1 || m2[0].GroupID != "EQG_WORKING_SET" {
		t.Errorf("CRI dialect resolution = %+v, want EQG_WORKING_SET", m2)
	}
	if m[0].Variant == m2[0].Variant {
		t.Error("different dialects should match different authored variants")
	}

	if got := r.Resolve("totally_unknown_metric_name"); len(got) != 0 {
		t.Errorf("unknown name resolved to %+v, want none", got)
	}

	// Determinism of multi-resolution order.
	a := r.Resolve("container_memory_rss")
	b := r.Resolve("container_memory_rss")
	if len(a) != len(b) || (len(a) > 0 && a[0] != b[0]) {
		t.Error("resolution is not deterministic")
	}
}
