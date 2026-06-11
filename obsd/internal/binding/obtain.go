package binding

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Signal obtainability (doc 04 §3.1 mechanisms 1, 3, 4): is each authored signal
// obtainable on THIS cluster at all? Absent capability or absent emitting tool ⇒
// the signal does not exist for this customer — recorded as out-of-scope with the
// reason, not failure. What the API cannot tell us is INDETERMINATE, stated —
// never assumed either way. Every one of the catalogue's signals lands in exactly
// one of these states; the coverage report enumerates them.

// PlatformFacts is the measured platform view (gathered by kube.GatherFacts; a
// fixture in tests). Everything in it is read from the cluster.
type PlatformFacts struct {
	KubeletVersion   string // e.g. "v1.35.0"
	ContainerRuntime string // e.g. "containerd://2.2.0"
	KernelVersion    string // e.g. "6.12.76-linuxkit"
	OSImage          string
	MixedKernels     bool

	// WorkloadCorpus is one lowercase "podname image image..." entry per running
	// pod; tool presence is detected against it.
	WorkloadCorpus []string

	// ToolPresent allows explicit overrides/seeds (tests, future config); the
	// detector fills it for every Tool node id.
	ToolPresent map[string]bool
}

// Obtainability states (the complete set).
type Obtainability string

const (
	Obtainable             Obtainability = "obtainable"
	OutOfScopeUnobtainable Obtainability = "out-of-scope"
	Indeterminate          Obtainability = "indeterminate"
)

// SignalAvailability is one signal's gating verdict with its reasons.
type SignalAvailability struct {
	SignalID string
	State    Obtainability
	Reasons  []string // why out-of-scope / indeterminate; empty when cleanly obtainable
	Gates    []string // distro gates ACTIVE for this signal on this platform (semantics shift, doc 04 §3.1.4)
}

// AvailabilityReport is the whole catalogue gated against one cluster.
type AvailabilityReport struct {
	PerSignal map[string]SignalAvailability
	Counts    map[Obtainability]int
	// ToolsPresent / ToolsAbsent / ToolsIndeterminate name the detection verdicts
	// (the emission-metadata side of the report).
	ToolsPresent       []string
	ToolsAbsent        []string
	ToolsIndeterminate []string
	// ActiveGates are distro/version gates that apply to this platform.
	ActiveGates []string
}

// nativeTools are obtainable on any conformant cluster we can reach at all: the
// API itself, the kubelet and its embedded cAdvisor, the runtime, and the kernel.
var nativeTools = map[string]bool{
	"k8s-api": true, "kube-apiserver": true, "kubelet": true, "cadvisor": true,
	"container-runtime": true, "kernel": true,
}

// undetectableTools cannot be detected from the API server at all (in-process
// libraries, external backends): their presence is INDETERMINATE, stated.
var undetectableTools = map[string]bool{
	"go-pprof": true, "audit-backend": true,
}

// toolNeedles maps a Tool node id to the substrings that identify it in the
// workload corpus when its own id does not appear verbatim.
var toolNeedles = map[string][]string{
	"kube-state-metrics":      {"kube-state-metrics"},
	"node-exporter":           {"node-exporter", "node_exporter"},
	"metrics-server":          {"metrics-server"},
	"coredns":                 {"coredns"},
	"kube-proxy":              {"kube-proxy"},
	"kube-scheduler":          {"kube-scheduler"},
	"kube-controller-manager": {"kube-controller-manager"},
	"etcd":                    {"etcd"},
	"cilium":                  {"cilium"},
	"hubble":                  {"hubble"},
	"calico":                  {"calico", "felix"},
	"kepler":                  {"kepler"},
	"opencost":                {"opencost"},
	"otel-collector":          {"otel-collector", "opentelemetry-collector"},
	"fluent-bit":              {"fluent-bit", "fluentbit"},
	"fluentd":                 {"fluentd"},
	"loki":                    {"loki"},
	"promtail":                {"promtail"},
	"vector":                  {"vector"},
	"tetragon":                {"tetragon"},
	"tracee":                  {"tracee"},
	"falco":                   {"falco"},
	"pixie":                   {"pixie", "px-"},
	"parca":                   {"parca"},
	"pyroscope":               {"pyroscope"},
	"istio":                   {"istio", "envoy"},
	"linkerd":                 {"linkerd"},
	"csi-driver":              {"csi-"},
}

// DetectTools fills facts.ToolPresent for every Tool node in the graph:
// native ⇒ present; runtime tools from ContainerRuntime; the rest by workload
// corpus search; undetectable stays absent from the map (= indeterminate).
func DetectTools(g *graph.Graph, facts *PlatformFacts) {
	if facts.ToolPresent == nil {
		facts.ToolPresent = map[string]bool{}
	}
	runtime := strings.ToLower(facts.ContainerRuntime)
	for id := range g.Tools {
		if _, seeded := facts.ToolPresent[id]; seeded {
			continue
		}
		switch {
		case nativeTools[id]:
			facts.ToolPresent[id] = true
		case undetectableTools[id]:
			// leave unset: indeterminate
		case id == "containerd" || id == "cri-o":
			facts.ToolPresent[id] = strings.HasPrefix(runtime, id)
		default:
			needles := toolNeedles[id]
			if len(needles) == 0 {
				needles = []string{id}
			}
			found := false
			for _, entry := range facts.WorkloadCorpus {
				for _, n := range needles {
					if strings.Contains(entry, n) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			facts.ToolPresent[id] = found
		}
	}
}

// kernelAtLeast parses "6.12.76-linuxkit"-style kernel strings.
func kernelAtLeast(kernel string, major, minor int) (bool, error) {
	m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(kernel)
	if m == nil {
		return false, fmt.Errorf("unparseable kernel version %q", kernel)
	}
	kmaj, _ := strconv.Atoi(m[1])
	kmin, _ := strconv.Atoi(m[2])
	return kmaj > major || (kmaj == major && kmin >= minor), nil
}

// Capability classes (doc 04 §3.1 mechanism 1), by how they are derivable:
//   - kernel-version caps: parsed from NodeInfo.KernelVersion.
//   - presence-sufficient caps: the named tool being deployed IS the capability.
//   - presence-necessary caps: tool absent ⇒ not met; tool present ⇒ still
//     indeterminate (a config flag/mode the API cannot read decides the rest).
//   - everything else: indeterminate, stated (kernel config, policy, feature
//     gates — a node-level probe lane comes later).
var kernelCaps = map[string][2]int{
	"CAP_KERNEL_GE_4_18": {4, 18},
	"CAP_KERNEL_GE_4_20": {4, 20},
	"CAP_KERNEL_GE_5_8":  {5, 8},
}

var presenceSufficientCaps = map[string]string{
	"CAP_KSM_DEPLOYED":  "kube-state-metrics",
	"CAP_NODE_EXPORTER": "node-exporter",
	"CAP_CILIUM":        "cilium",
	"CAP_HUBBLE":        "hubble",
	"CAP_CALICO":        "calico",
	"CAP_ISTIO":         "istio",
	"CAP_LINKERD":       "linkerd",
	"CAP_KEPLER":        "kepler",
	"CAP_OPENCOST":      "opencost",
	"CAP_PARCA":         "parca",
	"CAP_PYROSCOPE":     "pyroscope",
	"CAP_PIXIE":         "pixie",
	"CAP_TETRAGON":      "tetragon",
	"CAP_TRACEE":        "tracee",
	"CAP_FALCO_EBPF":    "falco",
	"CAP_FLUENTBIT":     "fluent-bit",
	"CAP_KUBE_PROXY":    "kube-proxy",
	"CAP_CSI_DRIVER":    "csi-driver",
}

var presenceNecessaryCaps = map[string]string{
	"CAP_CONTAINERD_METRICS":   "containerd", // metrics endpoint is a config flag
	"CAP_CRIO_METRICS":         "cri-o",
	"CAP_ETCD_METRICS_EXPOSED": "etcd",   // --listen-metrics-urls is a flag
	"CAP_CILIUM_KPR":           "cilium", // kube-proxy-replacement is a mode
}

// logAggregatorTools: CAP_LOG_AGGREGATOR is met by ANY deployed log pipeline.
var logAggregatorTools = []string{"loki", "promtail", "fluent-bit", "fluentd", "vector"}

// capabilityMet evaluates one CapabilityPrereq against the facts (facts must
// already have ToolPresent filled by DetectTools).
func capabilityMet(capID string, facts PlatformFacts) (Obtainability, string) {
	if v, ok := kernelCaps[capID]; ok {
		met, err := kernelAtLeast(facts.KernelVersion, v[0], v[1])
		if err != nil {
			return Indeterminate, fmt.Sprintf("capability %s: %v", capID, err)
		}
		if !met {
			return OutOfScopeUnobtainable, fmt.Sprintf("capability %s not met (kernel %s)", capID, facts.KernelVersion)
		}
		if facts.MixedKernels {
			return Indeterminate, fmt.Sprintf("capability %s met on sampled node but kernel versions differ across nodes", capID)
		}
		return Obtainable, ""
	}
	if tool, ok := presenceSufficientCaps[capID]; ok {
		if facts.ToolPresent[tool] {
			return Obtainable, ""
		}
		return OutOfScopeUnobtainable, fmt.Sprintf("capability %s not met (%s not deployed)", capID, tool)
	}
	if tool, ok := presenceNecessaryCaps[capID]; ok {
		if !facts.ToolPresent[tool] {
			return OutOfScopeUnobtainable, fmt.Sprintf("capability %s not met (%s not deployed)", capID, tool)
		}
		return Indeterminate, fmt.Sprintf("capability %s: %s deployed but its config is not API-derivable", capID, tool)
	}
	if capID == "CAP_LOG_AGGREGATOR" {
		for _, t := range logAggregatorTools {
			if facts.ToolPresent[t] {
				return Obtainable, ""
			}
		}
		return OutOfScopeUnobtainable, "capability CAP_LOG_AGGREGATOR not met (no log pipeline deployed)"
	}
	if capID == "CAP_CADVISOR_GE_43" {
		// cAdvisor ≥ 0.43 has shipped embedded in every kubelet since k8s 1.22.
		version := strings.TrimPrefix(facts.KubeletVersion, "v")
		if m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(version); m != nil {
			maj, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			if maj > 1 || (maj == 1 && min >= 22) {
				return Obtainable, ""
			}
			return OutOfScopeUnobtainable, fmt.Sprintf("capability CAP_CADVISOR_GE_43 not met (kubelet %s)", facts.KubeletVersion)
		}
		return Indeterminate, fmt.Sprintf("capability CAP_CADVISOR_GE_43: unparseable kubelet version %q", facts.KubeletVersion)
	}
	return Indeterminate, fmt.Sprintf("capability %s not API-derivable (needs node probe)", capID)
}

// activeGates evaluates the distro/version gates whose conditions are derivable
// from the facts. Today that is the dockershim-removal gate (k8s ≥ 1.24 with a
// non-Docker runtime) and the k3s/microk8s family (detected from the kubelet
// version string); everything else is inactive-on-this-platform.
func activeGates(g *graph.Graph, facts PlatformFacts) []string {
	var active []string
	version := strings.TrimPrefix(facts.KubeletVersion, "v")
	nonDocker := !strings.Contains(strings.ToLower(facts.ContainerRuntime), "docker")
	if m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(version); m != nil {
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if (maj > 1 || (maj == 1 && min >= 24)) && nonDocker {
			if _, ok := g.DistroGates["GATE_DOCKERSHIM_REMOVED"]; ok {
				active = append(active, "GATE_DOCKERSHIM_REMOVED")
			}
		}
	}
	lower := strings.ToLower(facts.KubeletVersion)
	if strings.Contains(lower, "k3s") {
		for id := range g.DistroGates {
			if strings.HasPrefix(id, "GATE_K3S") {
				active = append(active, id)
			}
		}
	}
	if strings.Contains(lower, "microk8s") {
		for id := range g.DistroGates {
			if strings.HasPrefix(id, "GATE_MICROK8S") {
				active = append(active, id)
			}
		}
	}
	sort.Strings(active)
	return active
}

// GateSignals runs the full availability pass: every signal lands in exactly one
// obtainability state with reasons, plus the tool/gate verdicts.
func GateSignals(g *graph.Graph, facts PlatformFacts) *AvailabilityReport {
	DetectTools(g, &facts)
	rep := &AvailabilityReport{
		PerSignal: map[string]SignalAvailability{},
		Counts:    map[Obtainability]int{},
	}

	for id := range g.Tools {
		present, known := facts.ToolPresent[id]
		switch {
		case !known:
			rep.ToolsIndeterminate = append(rep.ToolsIndeterminate, id)
		case present:
			rep.ToolsPresent = append(rep.ToolsPresent, id)
		default:
			rep.ToolsAbsent = append(rep.ToolsAbsent, id)
		}
	}
	sort.Strings(rep.ToolsPresent)
	sort.Strings(rep.ToolsAbsent)
	sort.Strings(rep.ToolsIndeterminate)
	rep.ActiveGates = activeGates(g, facts)
	activeGateSet := map[string]bool{}
	for _, a := range rep.ActiveGates {
		activeGateSet[a] = true
	}

	ids := make([]string, 0, len(g.Signals))
	for id := range g.Signals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := g.Signals[id]
		av := SignalAvailability{SignalID: id, State: Obtainable}

		// Mechanism 1 — emitting tool: at least one of the signal's tools present.
		// No tool reference at all ⇒ availability not tool-gated (rare; stated).
		if len(s.Tools) > 0 {
			anyPresent, anyIndeterminate := false, false
			var absent []string
			for _, tool := range s.Tools {
				present, known := facts.ToolPresent[tool]
				switch {
				case known && present:
					anyPresent = true
				case !known:
					anyIndeterminate = true
				default:
					absent = append(absent, tool)
				}
			}
			switch {
			case anyPresent:
				// obtainable so far
			case anyIndeterminate:
				av.State = Indeterminate
				av.Reasons = append(av.Reasons, "emitting tool not API-detectable: "+strings.Join(s.Tools, "|"))
			default:
				av.State = OutOfScopeUnobtainable
				av.Reasons = append(av.Reasons, "no emitting tool deployed: "+strings.Join(absent, "|"))
			}
		}

		// Mechanism 1 — capabilities: every referenced capability must be met;
		// not-met dominates indeterminate, out-of-scope dominates everything.
		for _, capID := range s.Capabilities {
			state, reason := capabilityMet(capID, facts)
			switch state {
			case OutOfScopeUnobtainable:
				av.State = OutOfScopeUnobtainable
				av.Reasons = append(av.Reasons, reason)
			case Indeterminate:
				if av.State == Obtainable {
					av.State = Indeterminate
				}
				av.Reasons = append(av.Reasons, reason)
			}
		}

		// Mechanism 4 — distro gates: an active gate does not remove the signal;
		// it shifts semantics. Recorded on the signal and surfaced to QA.
		for _, gate := range s.DistroGates {
			if activeGateSet[gate] {
				av.Gates = append(av.Gates, gate)
			}
		}

		rep.PerSignal[id] = av
		rep.Counts[av.State]++
	}
	return rep
}
