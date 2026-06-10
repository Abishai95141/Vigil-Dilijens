package identity

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ClusterID returns the cluster's stable identity coordinate: the kube-system
// namespace UID (doc 14 A9). Single-cluster scope is pinned through Phase 2;
// multi-cluster is explicitly out of scope until then. This value is the Cluster
// field of every CEI minted for the cluster.
func ClusterID(ctx context.Context, client kubernetes.Interface) (string, error) {
	ns, err := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read kube-system namespace for cluster id: %w", err)
	}
	uid := string(ns.UID)
	if uid == "" {
		return "", fmt.Errorf("kube-system namespace has no UID")
	}
	return uid, nil
}
