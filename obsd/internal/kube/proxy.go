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

// APIServerMetrics GETs the kube-apiserver's OWN root /metrics via an absolute-path
// request on the API REST client (docs/33 build 1) — the same endpoint `kubectl get
// --raw /metrics` reads. Unlike NodeMetrics/PodMetrics this is NOT a proxy subresource:
// the API server reports its own APF / admission / inflight metrics directly. RBAC
// grants get on the nonResourceURL /metrics. Same receive-time stamping.
func (p *ProxyFetcher) APIServerMetrics(ctx context.Context) ([]byte, time.Time, error) {
	body, err := p.cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	receivedAt := time.Now().UTC()
	if err != nil {
		return nil, receivedAt, fmt.Errorf("apiserver /metrics: %w", err)
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
	receivedAt := time.Now().UTC()
	if err != nil {
		return nil, receivedAt, fmt.Errorf("pod-proxy %s/%s:%s/%s: %w", namespace, name, port, path, err)
	}
	return body, receivedAt, nil
}
