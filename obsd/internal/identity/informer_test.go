package identity

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func boolPtr(b bool) *bool { return &b }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ownerRefMeta(kind, name, uid string) metav1.OwnerReference {
	return metav1.OwnerReference{Kind: kind, Name: name, UID: types.UID(uid), Controller: boolPtr(true)}
}

func makePod(ns, name, uid string, created time.Time, owners ...metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              name,
			UID:               types.UID(uid),
			CreationTimestamp: metav1.NewTime(created),
			OwnerReferences:   owners,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// startWatcher starts a synced watcher over the given fake clientset, returning the
// edge store it feeds alongside the cancel and watcher.
func startWatcher(t *testing.T, client *fake.Clientset, st *Store) (context.CancelFunc, *Watcher, *EdgeStore) {
	t.Helper()
	es := NewEdgeStore(st.clock, testBudgets, 30*time.Minute) // share the lifecycle store's clock
	w, err := NewWatcher(client, st, es, cluster, 0, quietLogger())
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	waitFor(t, 3*time.Second, w.HasSynced)
	return cancel, w, es
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestInformerDiscoversPodWithDeploymentRole(t *testing.T) {
	// Seed the RS so the role chain resolves to the Deployment deterministically.
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: "shop", Name: "web-abc", UID: types.UID("rs-uid"),
		OwnerReferences: []metav1.OwnerReference{ownerRefMeta("Deployment", "web", "dep-uid")},
	}}
	client := fake.NewSimpleClientset(rs)
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	cancel, _, _ := startWatcher(t, client, st)
	defer cancel()

	pod := makePod("shop", "web-x", "pod-uid-1", lcBase, ownerRefMeta("ReplicaSet", "web-abc", "rs-uid"))
	if _, err := client.CoreV1().Pods("shop").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		_, ok := st.PodUID("shop", "web-x", lcBase.Add(time.Minute))
		return ok
	})
	uid, _ := st.PodUID("shop", "web-x", lcBase.Add(time.Minute))
	if uid != "pod-uid-1" {
		t.Errorf("PodUID = %q, want pod-uid-1", uid)
	}
	rec, ok := st.Get(mustKey(t, podCoords("shop", "web-x", "pod-uid-1")))
	if !ok || rec.RoleCEI.RoleKey != "Deployment/web" {
		t.Errorf("role = %q, want Deployment/web", rec.RoleCEI.RoleKey)
	}
}

func TestInformerTerminatesPodOnDelete(t *testing.T) {
	client := fake.NewSimpleClientset()
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	cancel, _, _ := startWatcher(t, client, st)
	defer cancel()

	pod := makePod("shop", "cart-1", "uid-cart", lcBase)
	client.CoreV1().Pods("shop").Create(context.Background(), pod, metav1.CreateOptions{})
	waitFor(t, 3*time.Second, func() bool {
		_, ok := st.Get(mustKey(t, podCoords("shop", "cart-1", "uid-cart")))
		return ok
	})

	client.CoreV1().Pods("shop").Delete(context.Background(), "cart-1", metav1.DeleteOptions{})
	waitFor(t, 3*time.Second, func() bool {
		rec, ok := st.Get(mustKey(t, podCoords("shop", "cart-1", "uid-cart")))
		return ok && !rec.alive()
	})
}

func TestInformerSameNameRecreate(t *testing.T) {
	client := fake.NewSimpleClientset()
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	cancel, _, _ := startWatcher(t, client, st)
	defer cancel()

	// Pod v1 born at lcBase.
	client.CoreV1().Pods("shop").Create(context.Background(),
		makePod("shop", "web-x", "uid-OLD", lcBase), metav1.CreateOptions{})
	waitFor(t, 3*time.Second, func() bool {
		_, ok := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-OLD")))
		return ok
	})
	client.CoreV1().Pods("shop").Delete(context.Background(), "web-x", metav1.DeleteOptions{})

	// Pod v2: same name, new UID, born 1 min later.
	client.CoreV1().Pods("shop").Create(context.Background(),
		makePod("shop", "web-x", "uid-NEW", lcBase.Add(time.Minute)), metav1.CreateOptions{})
	waitFor(t, 3*time.Second, func() bool {
		return st.Metrics().Successions >= 1 ||
			func() bool { _, ok := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-NEW"))); return ok }()
	})

	// Distinct instance CEIs; the old one is dead.
	oldRec, oldOK := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-OLD")))
	newRec, ok := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-NEW")))
	if !ok {
		t.Fatal("new pod not discovered")
	}
	if oldOK && oldRec.alive() {
		t.Error("old same-name pod should be dead after recreate")
	}
	if oldOK && oldRec.CEI.Same(newRec.CEI) {
		t.Error("recreated pod shares a CEI with the dead one")
	}
}

func TestInformerDiscoversNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", UID: types.UID("node-uid-1"), CreationTimestamp: metav1.NewTime(lcBase)},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	client := fake.NewSimpleClientset(node)
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	cancel, _, _ := startWatcher(t, client, st)
	defer cancel()

	waitFor(t, 3*time.Second, func() bool {
		_, ok := st.NodeUID("worker-1", lcBase.Add(time.Minute))
		return ok
	})
	if uid, _ := st.NodeUID("worker-1", lcBase.Add(time.Minute)); uid != "node-uid-1" {
		t.Errorf("NodeUID = %q, want node-uid-1", uid)
	}
}
