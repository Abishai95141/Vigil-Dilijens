package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestSnapshotConfigEnumeratesInitContainers is the regression guard for the
// init-container detection gap: SnapshotConfig must surface init containers as
// first-class entries in PodConfig.Containers (in init-first, then regular, spec
// order) so container-scoped rules — THR_INIT_CONTAINER_RESTARTS_RATE above all —
// instantiate on them. Before the fix, only p.Spec.Containers was read, so a
// crash-looping init container was never a bound entity and PHEN_INIT_CONTAINER_FAILURE
// could not fire even though KSM emitted its restart counter.
func TestSnapshotConfigEnumeratesInitContainers(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "abb-genix", Name: "init-failer"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Name: "failing-init",
					// The rig's init container declares no limits (the resolvability
					// hole): its limit fields must stay 0 (undeclared), never defaulted.
				},
			},
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("32Mi"),
							corev1.ResourceCPU:    resource.MustParse("50m"),
						},
					},
				},
			},
		},
	}

	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}

	pc, ok := snap.Pod("abb-genix", "init-failer")
	if !ok {
		t.Fatalf("pod abb-genix/init-failer not in snapshot")
	}
	if len(pc.Containers) != 2 {
		t.Fatalf("want 2 containers (init + regular), got %d: %+v", len(pc.Containers), pc.Containers)
	}

	// Init container comes first (it runs first) and is UNDECLARED — a bindable
	// entity whose config-relative bars stay unbounded, but whose rate-of-change
	// restart rule (flagged default) is crossable.
	init := pc.Containers[0]
	if init.Name != "failing-init" {
		t.Fatalf("container[0] = %q, want the init container failing-init", init.Name)
	}
	if init.MemLimitBytes != 0 || init.CPULimitMilli != 0 || init.EphemeralStorageLimitBytes != 0 {
		t.Fatalf("init container limits must stay UNDECLARED (0), got %+v", init)
	}

	// Regular container follows, with its declared limits read verbatim.
	main := pc.Containers[1]
	if main.Name != "main" {
		t.Fatalf("container[1] = %q, want main", main.Name)
	}
	if got, want := main.MemLimitBytes, int64(32*1024*1024); got != want {
		t.Fatalf("main MemLimitBytes = %d, want %d", got, want)
	}
	if got, want := main.CPULimitMilli, int64(50); got != want {
		t.Fatalf("main CPULimitMilli = %d, want %d", got, want)
	}
}

// TestSnapshotConfigPodWithoutInitContainers confirms the common case is unchanged:
// a pod with only regular containers yields exactly those, byte-for-byte as before,
// so the broadened enumeration never perturbs existing bindings.
func TestSnapshotConfigPodWithoutInitContainers(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "boutique", Name: "frontend"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "server", Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
				}},
			},
		},
	}

	cs := fake.NewSimpleClientset(pod)
	snap, err := SnapshotConfig(context.Background(), cs)
	if err != nil {
		t.Fatalf("SnapshotConfig: %v", err)
	}
	pc, ok := snap.Pod("boutique", "frontend")
	if !ok {
		t.Fatalf("pod boutique/frontend not in snapshot")
	}
	if len(pc.Containers) != 1 || pc.Containers[0].Name != "server" {
		t.Fatalf("want exactly [server], got %+v", pc.Containers)
	}
	if got, want := pc.Containers[0].MemLimitBytes, int64(128*1024*1024); got != want {
		t.Fatalf("server MemLimitBytes = %d, want %d", got, want)
	}
}
