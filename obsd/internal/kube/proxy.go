package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
)

// ProxyFetcher fetches node-local metrics endpoints through the API server's
// nodes/proxy subresource (doc 14 A2: the dev/out-of-cluster path; RBAC grants
// get on nodes/proxy). The kubelet serves /metrics/cadvisor, /metrics, and
// /metrics/probes on its authenticated port; the proxy spares us kubelet TLS
// and auth plumbing in Phase 0.
type ProxyFetcher struct {
	cs kubernetes.Interface
}

// NewProxyFetcher wraps a clientset.
func NewProxyFetcher(cs kubernetes.Interface) *ProxyFetcher { return &ProxyFetcher{cs: cs} }

// NodeMetrics GETs nodes/<name>/proxy/<path> and stamps receive time (doc 14
// A12: wall-clock UTC at ingest receive).
func (p *ProxyFetcher) NodeMetrics(ctx context.Context, nodeName, path string) ([]byte, time.Time, error) {
	body, err := p.cs.CoreV1().RESTClient().Get().
		Resource("nodes").Name(nodeName).SubResource("proxy").
		Suffix(path).DoRaw(ctx)
	receivedAt := time.Now().UTC()
	if err != nil {
		return nil, receivedAt, fmt.Errorf("proxy %s/%s: %w", nodeName, path, err)
	}
	return body, receivedAt, nil
}
