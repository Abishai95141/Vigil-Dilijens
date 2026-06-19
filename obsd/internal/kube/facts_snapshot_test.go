package kube

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

// These tests exercise SnapshotConfig and GatherFacts against a fake clientset
// (no live cluster). They assert the SHAPE of the borrowed-normativity snapshot:
// declared limits read VERBATIM, unparseable/absent values left UNDECLARED (zero /
// not-present) rather than fabricated — the charter's "never invent a bar" rule.

// qty is a small helper: resource quantities expose Value()/MilliValue() as POINTER
// methods, so the literal must be addressable.
func qty(s string) resource.Quantity { return resource.MustParse(s) }

func podWithLimits(ns, name string, conts []corev1.Container, anns map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: anns},
		Spec:       corev1.PodSpec{Containers: conts},
	}
}

func TestSnapshotConfig_ReadsDeclaredLimitsVerbatim(t *testing.T) {
	pod := podWithLimits("shop", "web", []corev1.Container{
		{
			Name: "app",
			Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
				corev1.ResourceMemory:           qty("256Mi"),
				corev1.ResourceCPU:              qty("500m"),
				corev1.ResourceEphemeralStorage: qty("1Gi"),
			}},
		},
		{Name: "sidecar"}, // declares NOTHING -> all-zero ContainerConfig
	}, nil)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceMemory: qty("8Gi")}},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: qty("10Gi")},
		}},
	}

	cs := fake.NewSimpleClientset(pod, node, pvc)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}

	pc, ok := snap.Pod("shop", "web")
	if !ok {
		t.Fatalf("pod shop/web not in snapshot")
	}
	want := []binding.ContainerConfig{
		{Name: "app", MemLimitBytes: 268435456, CPULimitMilli: 500, EphemeralStorageLimitBytes: 1073741824},
		{Name: "sidecar"}, // zero = UNDECLARED, never defaulted
	}
	if !reflect.DeepEqual(pc.Containers, want) {
		t.Fatalf("container config mismatch:\n got %#v\nwant %#v", pc.Containers, want)
	}

	nc, ok := snap.Node("node-a")
	if !ok {
		t.Fatalf("node-a not in snapshot")
	}
	if nc.AllocatableMemoryBytes != 8589934592 {
		t.Fatalf("node allocatable = %d, want 8Gi (8589934592)", nc.AllocatableMemoryBytes)
	}

	pvcCfg, ok := snap.PVC("shop", "data")
	if !ok {
		t.Fatalf("pvc shop/data not in snapshot")
	}
	if pvcCfg.RequestedStorageBytes != 10737418240 {
		t.Fatalf("pvc request = %d, want 10Gi (10737418240)", pvcCfg.RequestedStorageBytes)
	}
}

func TestSnapshotConfig_PreservesContainerSpecOrder(t *testing.T) {
	// Spec order is the deterministic order the binding engine relies on. A snapshot
	// that sorted/reordered containers would silently mis-join limits to containers.
	pod := podWithLimits("shop", "multi", []corev1.Container{
		{Name: "zeta"}, {Name: "alpha"}, {Name: "mid"},
	}, nil)
	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	pc, _ := snap.Pod("shop", "multi")
	got := []string{pc.Containers[0].Name, pc.Containers[1].Name, pc.Containers[2].Name}
	want := []string{"zeta", "alpha", "mid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("container order = %v, want spec order %v", got, want)
	}
}

func TestSnapshotConfig_SLOAnnotations(t *testing.T) {
	pod := podWithLimits("shop", "queue", []corev1.Container{{Name: "app"}}, map[string]string{
		"vigil.io/slo.queue.max_depth": "1000",      // valid -> path = "slo.queue.max_depth"
		"vigil.io/slo.latency.p99_ms":  "  42.5  ",  // whitespace trimmed
		"vigil.io/slo.bad":             "not-a-num", // unparseable -> SKIPPED, never fabricated
		"vigil.io/notslo.foo":          "5",         // wrong subdomain -> ignored
		"other.io/slo.queue.max_depth": "9",         // wrong domain -> ignored
		"team":                         "checkout",  // unrelated annotation -> ignored
	})
	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	pc, _ := snap.Pod("shop", "queue")
	want := map[string]float64{
		"slo.queue.max_depth": 1000,
		"slo.latency.p99_ms":  42.5,
	}
	if !reflect.DeepEqual(pc.SLOs, want) {
		t.Fatalf("SLOs mismatch:\n got %#v\nwant %#v", pc.SLOs, want)
	}
	// The unparseable bar must be ABSENT (charter: skip, never fabricate). Guard the
	// exact key in case a future regression default-inserts a zero.
	if _, present := pc.SLOs["slo.bad"]; present {
		t.Fatalf("unparseable SLO annotation must be skipped, but slo.bad is present")
	}
}

func TestSnapshotConfig_NoSLOsLeavesNilMap(t *testing.T) {
	// A pod declaring no vigil.io/slo.* annotation must leave SLOs nil (UNDECLARED),
	// not an empty-but-present map — the binding engine distinguishes the two.
	pod := podWithLimits("shop", "plain", []corev1.Container{{Name: "app"}}, map[string]string{"team": "x"})
	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	pc, _ := snap.Pod("shop", "plain")
	if pc.SLOs != nil {
		t.Fatalf("pod with no SLO annotations should have nil SLOs, got %#v", pc.SLOs)
	}
}

func TestSnapshotConfig_UndeclaredLimitsStayZero(t *testing.T) {
	// A container with no limits block must produce a zero ContainerConfig — the
	// resolvability hole the binding engine reports as unresolved, NEVER a default.
	pod := podWithLimits("shop", "bare", []corev1.Container{{Name: "app"}}, nil)
	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	pc, _ := snap.Pod("shop", "bare")
	got := pc.Containers[0]
	if got.MemLimitBytes != 0 || got.CPULimitMilli != 0 || got.EphemeralStorageLimitBytes != 0 {
		t.Fatalf("undeclared limits must stay zero, got %#v", got)
	}
}

func TestSnapshotConfig_LookupMissReturnsFalse(t *testing.T) {
	cs := fake.NewSimpleClientset()
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	if _, ok := snap.Pod("shop", "ghost"); ok {
		t.Fatalf("Pod lookup of absent pod returned ok=true")
	}
	if _, ok := snap.Node("ghost"); ok {
		t.Fatalf("Node lookup of absent node returned ok=true")
	}
	if _, ok := snap.PVC("shop", "ghost"); ok {
		t.Fatalf("PVC lookup of absent pvc returned ok=true")
	}
}

func TestSnapshotConfig_MaxNodeAllocatableMemory(t *testing.T) {
	cs := fake.NewSimpleClientset(
		nodeWithMem("small", "4Gi"),
		nodeWithMem("big", "16Gi"),
		nodeWithMem("mid", "8Gi"),
	)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	if got := snap.MaxNodeAllocatableMemory(); got != 17179869184 {
		t.Fatalf("MaxNodeAllocatableMemory = %d, want 16Gi (17179869184)", got)
	}
}

func TestSnapshotConfig_MaxNodeAllocatableMemoryEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset()
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	if got := snap.MaxNodeAllocatableMemory(); got != 0 {
		t.Fatalf("MaxNodeAllocatableMemory of empty snapshot = %d, want 0", got)
	}
}

func TestSnapshotConfig_PVCsDeterministicOrder(t *testing.T) {
	// PVCs() must enumerate in (namespace, name) order regardless of insertion /
	// map-iteration order — determinism is the property (same snapshot => same order).
	cs := fake.NewSimpleClientset(
		makePVC("zeta", "b"),
		makePVC("alpha", "z"),
		makePVC("alpha", "a"),
		makePVC("mid", "m"),
	)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	got := snap.PVCs()
	want := []binding.PVCRef{
		{Namespace: "alpha", Name: "a"},
		{Namespace: "alpha", Name: "z"},
		{Namespace: "mid", Name: "m"},
		{Namespace: "zeta", Name: "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PVCs order mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func nodeWithMem(name, mem string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceMemory: qty(mem)}},
	}
}

func makePVC(ns, name string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: qty("1Gi")},
		}},
	}
}
