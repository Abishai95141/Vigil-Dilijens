package identity

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func edgeWin() TimeWindow { return win(lcBase, lcBase.Add(30*time.Second)) }

// runs-on resolution needs the node in the lifecycle store; wait for it.
func waitNodeObserved(t *testing.T, st *Store, name string) {
	waitFor(t, 3*time.Second, func() bool { _, ok := st.NodeUID(name, lcBase.Add(time.Minute)); return ok })
}

func TestInformerBuildsRunsOnAndMountsEdges(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", UID: types.UID("node-uid-1"), CreationTimestamp: metav1.NewTime(lcBase)},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "data", UID: types.UID("pvc-uid")}}
	client := fake.NewSimpleClientset(node, pvc)
	st := newTestStore(newFakeClock(lcBase))
	cancel, _, es := startWatcher(t, client, st)
	defer cancel()
	waitNodeObserved(t, st, "worker-1")

	pod := makePod("shop", "db-0", "pod-uid-db", lcBase)
	pod.Spec.NodeName = "worker-1"
	pod.Spec.Volumes = []corev1.Volume{{
		Name:         "data",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}},
	}}
	client.CoreV1().Pods("shop").Create(context.Background(), pod, metav1.CreateOptions{})

	podKey := podCEI("db-0", "pod-uid-db").Key()
	nodeKey := nodeCEI("worker-1", "node-uid-1").Key()
	pvcCEI, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "PersistentVolumeClaim", Name: "data", UID: "pvc-uid"}, lcBase)

	waitFor(t, 3*time.Second, func() bool {
		return es.Traverse(EdgeRunsOn, podKey, nodeKey, edgeWin()) == TraversalValid
	})
	if es.Traverse(EdgeMounts, podKey, pvcCEI.Key(), edgeWin()) != TraversalValid {
		t.Errorf("mounts edge not valid")
	}

	// Deleting the pod retracts its edges.
	client.CoreV1().Pods("shop").Delete(context.Background(), "db-0", metav1.DeleteOptions{})
	waitFor(t, 3*time.Second, func() bool {
		return es.Status(EdgeRunsOn, podKey, nodeKey, lcBase) == StatusRetracted
	})
	if es.Status(EdgeMounts, podKey, pvcCEI.Key(), lcBase) != StatusRetracted {
		t.Errorf("mounts edge should be retracted after pod delete")
	}
}

func TestInformerBuildsSelectsEdge(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", UID: types.UID("svc-uid")}}
	client := fake.NewSimpleClientset(svc)
	st := newTestStore(newFakeClock(lcBase))
	cancel, _, es := startWatcher(t, client, st)
	defer cancel()

	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: "shop", Name: "web-abc", Labels: map[string]string{discoveryv1.LabelServiceName: "web"}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.0.1"},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: "web-x", UID: types.UID("pod-uid-1")},
		}},
	}
	client.DiscoveryV1().EndpointSlices("shop").Create(context.Background(), slice, metav1.CreateOptions{})

	svcKey := func() string {
		c, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Service", Name: "web", UID: "svc-uid"}, lcBase)
		return c.Key()
	}()
	podKey := podCEI("web-x", "pod-uid-1").Key()

	waitFor(t, 3*time.Second, func() bool {
		return es.Traverse(EdgeSelects, svcKey, podKey, edgeWin()) == TraversalValid
	})

	// Removing the pod from the slice retracts the selects edge.
	updated := slice.DeepCopy()
	updated.Endpoints = nil
	client.DiscoveryV1().EndpointSlices("shop").Update(context.Background(), updated, metav1.UpdateOptions{})
	waitFor(t, 3*time.Second, func() bool {
		return es.Status(EdgeSelects, svcKey, podKey, lcBase) == StatusRetracted
	})
}

// A pod moving between two EndpointSlices of the same service (a controller
// rebalance) must NOT retract the live selects edge. The per-slice diff used to
// tear it down; reconciling across all of the service's slices keeps it valid.
func TestInformerSelectsSurvivesSliceRebalance(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", UID: types.UID("svc-uid")}}
	client := fake.NewSimpleClientset(svc)
	st := newTestStore(newFakeClock(lcBase))
	cancel, _, es := startWatcher(t, client, st)
	defer cancel()

	mkSlice := func(name, podName, podUID string) *discoveryv1.EndpointSlice {
		s := &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Namespace: "shop", Name: name, Labels: map[string]string{discoveryv1.LabelServiceName: "web"}},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
		if podName != "" {
			s.Endpoints = []discoveryv1.Endpoint{{
				Addresses: []string{"10.0.0.1"},
				TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: podName, UID: types.UID(podUID)},
			}}
		}
		return s
	}

	svcKey := func() string {
		c, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Service", Name: "web", UID: "svc-uid"}, lcBase)
		return c.Key()
	}()
	podKey := podCEI("web-x", "pod-uid-1").Key()
	ctx := context.Background()

	// Pod P backed by slice web-1.
	client.DiscoveryV1().EndpointSlices("shop").Create(ctx, mkSlice("web-1", "web-x", "pod-uid-1"), metav1.CreateOptions{})
	waitFor(t, 3*time.Second, func() bool { return es.Traverse(EdgeSelects, svcKey, podKey, edgeWin()) == TraversalValid })

	// Controller rebalances P into web-2, then empties web-1.
	client.DiscoveryV1().EndpointSlices("shop").Create(ctx, mkSlice("web-2", "web-x", "pod-uid-1"), metav1.CreateOptions{})
	client.DiscoveryV1().EndpointSlices("shop").Update(ctx, mkSlice("web-1", "", ""), metav1.UpdateOptions{})

	// The selects edge converges to (and stays) valid — P is still backed by web-2.
	waitFor(t, 3*time.Second, func() bool { return es.Traverse(EdgeSelects, svcKey, podKey, edgeWin()) == TraversalValid })
	// Settle, then confirm it did not get spuriously retracted by the web-1 emptying.
	time.Sleep(50 * time.Millisecond)
	if r := es.Traverse(EdgeSelects, svcKey, podKey, edgeWin()); r != TraversalValid {
		t.Errorf("selects edge = %s after rebalance, want valid (pod still backed by web-2)", r)
	}
}

func TestInformerBuildsNodeLeaseEdge(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", UID: types.UID("node-uid-1"), CreationTimestamp: metav1.NewTime(lcBase)},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	client := fake.NewSimpleClientset(node)
	st := newTestStore(newFakeClock(lcBase))
	cancel, _, es := startWatcher(t, client, st)
	defer cancel()
	waitNodeObserved(t, st, "worker-1")

	renew := metav1.NewMicroTime(lcBase.Add(10 * time.Second))
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-node-lease", Name: "worker-1"},
		Spec:       coordinationv1.LeaseSpec{RenewTime: &renew},
	}
	client.CoordinationV1().Leases("kube-node-lease").Create(context.Background(), lease, metav1.CreateOptions{})

	nodeKey := nodeCEI("worker-1", "node-uid-1").Key()
	// Liveness valid shortly after the renew time (within the 40s budget)...
	waitFor(t, 3*time.Second, func() bool {
		return es.NodeLiveness(nodeKey, lcBase.Add(20*time.Second)) == StatusValid
	})
	// ...and suspect once the renew is older than the budget.
	if es.NodeLiveness(nodeKey, lcBase.Add(60*time.Second)) != StatusSuspect {
		t.Errorf("node liveness should be suspect 60s after a 10s renew (40s budget)")
	}
}
