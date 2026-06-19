package kube

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// GatherFacts is MEASURED-only: every field is read from the API, with
// "cannot tell" left blank rather than guessed. These tests pin that the corpus is
// built lowercase + sorted (deterministic), version facts come from node[0], and
// heterogeneous kernels are reported (never silently collapsed to one version).

func nodeWithInfo(name, kubelet, runtime, kernel, osImage string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{
			KubeletVersion:          kubelet,
			ContainerRuntimeVersion: runtime,
			KernelVersion:           kernel,
			OSImage:                 osImage,
		}},
	}
}

func runningPod(ns, name string, images ...string) *corev1.Pod {
	conts := make([]corev1.Container, len(images))
	for i, img := range images {
		conts[i] = corev1.Container{Name: "c", Image: img}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{Containers: conts},
	}
}

func TestGatherFacts_VersionFromFirstNode(t *testing.T) {
	cs := fake.NewSimpleClientset(
		nodeWithInfo("node-a", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu 22.04"),
	)
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if facts.KubeletVersion != "v1.30.0" {
		t.Fatalf("KubeletVersion = %q, want v1.30.0", facts.KubeletVersion)
	}
	if facts.ContainerRuntime != "containerd://1.7.0" {
		t.Fatalf("ContainerRuntime = %q", facts.ContainerRuntime)
	}
	if facts.KernelVersion != "6.6.0" {
		t.Fatalf("KernelVersion = %q", facts.KernelVersion)
	}
	if facts.OSImage != "Ubuntu 22.04" {
		t.Fatalf("OSImage = %q", facts.OSImage)
	}
	if facts.MixedKernels {
		t.Fatalf("single node must not report MixedKernels")
	}
}

func TestGatherFacts_MixedKernelsReported(t *testing.T) {
	// Heterogeneous pools must surface the divergence, not pretend one version.
	cs := fake.NewSimpleClientset(
		nodeWithInfo("node-a", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu"),
		nodeWithInfo("node-b", "v1.30.0", "containerd://1.7.0", "5.15.0", "Ubuntu"),
	)
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if !facts.MixedKernels {
		t.Fatalf("divergent kernels not reported as MixedKernels")
	}
}

func TestGatherFacts_HomogeneousKernelsNotMixed(t *testing.T) {
	cs := fake.NewSimpleClientset(
		nodeWithInfo("node-a", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu"),
		nodeWithInfo("node-b", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu"),
		nodeWithInfo("node-c", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu"),
	)
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if facts.MixedKernels {
		t.Fatalf("identical kernels must not report MixedKernels")
	}
}

func TestGatherFacts_BlankNodeInfoLeavesVersionsBlank(t *testing.T) {
	// "cannot tell from the API" => blank, NEVER a guessed version. A node whose
	// NodeInfo the API did not populate must leave the version facts empty rather
	// than fabricate a version.
	cs := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if facts.KubeletVersion != "" || facts.KernelVersion != "" || facts.OSImage != "" || facts.ContainerRuntime != "" {
		t.Fatalf("blank NodeInfo must leave version facts blank, got %+v", facts)
	}
	if facts.MixedKernels {
		t.Fatalf("single node must not report MixedKernels")
	}
}

// TestGatherFacts_EmptyClusterIsBlankNotPanic pins that a zero-node cluster is
// handled gracefully. facts.go reslices nodes.Items[1:] to scan peer nodes for
// kernel divergence; without a length guard that reslice panics ("slice bounds out
// of range [1:0]") on an empty node list. GatherFacts must instead return cleanly
// with every API-derived version fact blank ("cannot tell from the API" => blank,
// never guessed), MixedKernels false, an initialized-but-empty ToolPresent map, and
// an empty WorkloadCorpus.
func TestGatherFacts_EmptyClusterIsBlankNotPanic(t *testing.T) {
	facts, err := GatherFacts(context.Background(), fake.NewSimpleClientset())
	if err != nil {
		t.Fatalf("GatherFacts on empty cluster: %v", err)
	}
	if facts.KubeletVersion != "" {
		t.Fatalf("empty cluster KubeletVersion = %q, want blank", facts.KubeletVersion)
	}
	if facts.KernelVersion != "" {
		t.Fatalf("empty cluster KernelVersion = %q, want blank", facts.KernelVersion)
	}
	if facts.OSImage != "" {
		t.Fatalf("empty cluster OSImage = %q, want blank", facts.OSImage)
	}
	if facts.ContainerRuntime != "" {
		t.Fatalf("empty cluster ContainerRuntime = %q, want blank", facts.ContainerRuntime)
	}
	if facts.MixedKernels {
		t.Fatalf("empty cluster must not report MixedKernels")
	}
	if facts.ToolPresent == nil {
		t.Fatalf("ToolPresent map must be initialized (non-nil) even on an empty cluster")
	}
	if len(facts.ToolPresent) != 0 {
		t.Fatalf("ToolPresent must start empty, got %v", facts.ToolPresent)
	}
	if len(facts.WorkloadCorpus) != 0 {
		t.Fatalf("empty cluster WorkloadCorpus must be empty, got %v", facts.WorkloadCorpus)
	}
}

func TestGatherFacts_WorkloadCorpusLowercaseSortedAndJoined(t *testing.T) {
	// Each entry is "podname image image..." lowercased; the whole corpus sorted so
	// the same workload set always yields the same corpus (determinism).
	cs := fake.NewSimpleClientset(
		nodeWithInfo("node-a", "v1.30.0", "containerd://1.7.0", "6.6.0", "Ubuntu"),
		runningPod("shop", "Zeta-Pod", "REG/IMG:v1"),
		runningPod("shop", "alpha-pod", "gcr.io/Prometheus:2.0", "busybox"),
	)
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	want := []string{
		"alpha-pod gcr.io/prometheus:2.0 busybox",
		"zeta-pod reg/img:v1",
	}
	if !reflect.DeepEqual(facts.WorkloadCorpus, want) {
		t.Fatalf("corpus mismatch:\n got %#v\nwant %#v", facts.WorkloadCorpus, want)
	}
}

func TestGatherFacts_ToolPresentMapInitialized(t *testing.T) {
	// ToolPresent must be a non-nil (empty) map so the detector can seed it without a
	// nil-map panic — even on a podless cluster. (A node is seeded only to avoid the
	// separately-documented empty-cluster panic in facts.go; it carries no tools.)
	cs := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if facts.ToolPresent == nil {
		t.Fatalf("ToolPresent map must be initialized (non-nil)")
	}
	if len(facts.ToolPresent) != 0 {
		t.Fatalf("ToolPresent must start empty, got %v", facts.ToolPresent)
	}
}

func TestGatherFacts_PodNameAndImageBothInCorpusEntry(t *testing.T) {
	// Tool detection scans both pod name AND image — a regression dropping either
	// would silently lose detections. Prove both appear in the same entry. (A node is
	// seeded only to dodge the documented empty-cluster panic in facts.go.)
	cs := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		runningPod("ns", "kube-state-metrics", "registry/ksm:latest"),
	)
	facts, err := GatherFacts(context.Background(), cs)
	if err != nil {
		t.Fatalf("GatherFacts: %v", err)
	}
	if len(facts.WorkloadCorpus) != 1 {
		t.Fatalf("want 1 corpus entry, got %d", len(facts.WorkloadCorpus))
	}
	entry := facts.WorkloadCorpus[0]
	if entry != "kube-state-metrics registry/ksm:latest" {
		t.Fatalf("entry missing name or image: %q", entry)
	}
}
