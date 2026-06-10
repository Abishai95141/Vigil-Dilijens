//go:build integration

package identity

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
)

// TestIdentityAgainstLiveCluster runs the identity watcher against a real cluster
// (the kind dev cluster on the Linux box) and asserts it discovers a correctly-
// joined entity inventory. Gated behind the `integration` build tag so the default
// hermetic suite stays cluster-free.
//
//	just up && go test -tags=integration ./obsd/internal/identity/ -run LiveCluster -v
func TestIdentityAgainstLiveCluster(t *testing.T) {
	client, err := kube.NewClientset("") // default kubeconfig / in-cluster
	if err != nil {
		t.Fatalf("kube client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clusterID, err := ClusterID(ctx, client)
	if err != nil {
		t.Fatalf("cluster id: %v", err)
	}
	t.Logf("cluster id (kube-system UID): %s", clusterID)

	store := NewStore(time.Now, 15*time.Minute, 24*time.Hour, 250000)
	edges := NewEdgeStore(time.Now, testBudgets, 30*time.Minute)
	w, err := NewWatcher(client, store, edges, clusterID, 30*time.Second, quietLogger())
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	go func() { _ = w.Run(ctx) }()
	waitFor(t, 30*time.Second, w.HasSynced)

	// kube-system always runs pods and the cluster always has nodes; give the
	// handlers a moment to drain, then assert a non-empty, joined inventory.
	waitFor(t, 10*time.Second, func() bool { return store.Metrics().Active > 0 })
	m := store.Metrics()
	t.Logf("inventory: active=%d discovered=%d terminated=%d successions=%d",
		m.Active, m.Discovered, m.Terminated, m.Successions)

	// Every kube-system pod must have resolved to some role (or bare) — i.e. the
	// store discovered it without panicking on the real dialect of owner chains.
	if m.Active == 0 {
		t.Fatal("no active entities discovered from a live cluster")
	}
	// A node must be resolvable by name (node-exporter / cAdvisor identity path).
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes.Items) > 0 {
		name := nodes.Items[0].Name
		if _, ok := store.NodeUID(name, time.Now()); !ok {
			t.Errorf("node %q not joinable via NodeUID", name)
		}
	}
}
