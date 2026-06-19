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
	// now is the injected receive-time clock (charter: no time.Now in logic). The
	// composition root (NewProxyFetcher) defaults it to a UTC wall clock; tests
	// inject a fixed clock to assert the stamped receive-time exactly.
	now func() time.Time
}

// NewProxyFetcher wraps a clientset, defaulting the receive-time clock to the UTC
// wall clock.
func NewProxyFetcher(cs kubernetes.Interface) *ProxyFetcher {
	return &ProxyFetcher{cs: cs, now: func() time.Time { return time.Now().UTC() }}
}

// NodeMetrics GETs nodes/<name>/proxy/<path> and stamps receive time (doc 14
// A12: wall-clock UTC at ingest receive).
func (p *ProxyFetcher) NodeMetrics(ctx context.Context, nodeName, path string) ([]byte, time.Time, error) {
	body, err := p.cs.CoreV1().RESTClient().Get().
		Resource("nodes").Name(nodeName).SubResource("proxy").
		Suffix(path).DoRaw(ctx)
	receivedAt := p.now()
	if err != nil {
		return nil, receivedAt, fmt.Errorf("proxy %s/%s: %w", nodeName, path, err)
	}
	return body, receivedAt, nil
}

// PodMetrics GETs namespaces/<ns>/pods/<name>:<port>/proxy/<path> — the pods/proxy
// subresource, the sibling of nodes/proxy (doc 15 cap. A). The port is qualified
// onto the pod name (the kubelet pod-proxy URL form); <path> is the metrics suffix.
// RBAC grants get on pods/proxy. Same receive-time stamping as NodeMetrics.
func (p *ProxyFetcher) PodMetrics(ctx context.Context, namespace, name, port, path string) ([]byte, time.Time, error) {
	target := name
	if port != "" {
		target = name + ":" + port
	}
	body, err := p.cs.CoreV1().RESTClient().Get().
		Namespace(namespace).Resource("pods").Name(target).SubResource("proxy").
		Suffix(path).DoRaw(ctx)
	receivedAt := p.now()
	if err != nil {
		return nil, receivedAt, fmt.Errorf("pod-proxy %s/%s:%s/%s: %w", namespace, name, port, path, err)
	}
	return body, receivedAt, nil
}
