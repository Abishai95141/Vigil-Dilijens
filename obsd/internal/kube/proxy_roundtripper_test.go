package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The proxy is driven against a clientset whose transport is a fake http
// RoundTripper. This keeps the test hermetic (no kubelet, no API server) while
// exercising the REAL client-go request-building path, so a regression in the
// nodes/proxy or pods/proxy URL shape is caught. The receive-time clock is injected
// (charter: no time.Now in logic), so the tests assert the stamped receive-time
// EQUALS the fixed clock value exactly — proving the stamp comes from the injected
// clock, not the wall clock.

// fixedReceiveTime is the deterministic clock value injected into ProxyFetcher under
// test. It is UTC and non-wall-clock, so an impl that reached for time.Now() instead
// would not produce it.
var fixedReceiveTime = time.Date(2026, 6, 19, 8, 30, 15, 123456789, time.UTC)

// captureRT records the last request path and replays a canned response. status<0
// means "return a transport error" (network failure path).
type captureRT struct {
	gotPath   string
	gotMethod string
	calls     int
	status    int
	body      string
	netErr    error
}

func (r *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	r.gotPath = req.URL.Path
	r.gotMethod = req.Method
	if r.netErr != nil {
		return nil, r.netErr
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     http.Header{"Content-Type": []string{"text/plain; version=0.0.4"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Request:    req,
	}, nil
}

func proxyWith(t *testing.T, rt http.RoundTripper) *ProxyFetcher {
	t.Helper()
	cs, err := kubernetes.NewForConfigAndClient(&rest.Config{Host: "https://api.test"}, &http.Client{Transport: rt})
	if err != nil {
		t.Fatalf("NewForConfigAndClient: %v", err)
	}
	p := NewProxyFetcher(cs)
	// Inject a fixed receive-time clock so the stamp is deterministic and assertable.
	p.now = func() time.Time { return fixedReceiveTime }
	return p
}

// assertReceiveStamp checks the receive-time stamp is EXACTLY the injected fixed
// clock value — proving the stamp flows from the injected clock, not the wall clock.
func assertReceiveStamp(t *testing.T, ts time.Time) {
	t.Helper()
	if !ts.Equal(fixedReceiveTime) {
		t.Fatalf("receive timestamp = %v, want injected clock %v", ts, fixedReceiveTime)
	}
}

func TestNodeMetrics_URLShapeAndBody(t *testing.T) {
	rt := &captureRT{status: http.StatusOK, body: "node_cpu 1\n"}
	p := proxyWith(t, rt)

	body, ts, err := p.NodeMetrics(context.Background(), "node-a", "metrics/cadvisor")
	if err != nil {
		t.Fatalf("NodeMetrics: %v", err)
	}
	if rt.gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", rt.gotMethod)
	}
	if rt.gotPath != "/api/v1/nodes/node-a/proxy/metrics/cadvisor" {
		t.Fatalf("node-proxy URL wrong: %q", rt.gotPath)
	}
	if string(body) != "node_cpu 1\n" {
		t.Fatalf("body returned not verbatim: %q", string(body))
	}
	assertReceiveStamp(t, ts)
}

func TestPodMetrics_URLShapeWithPort(t *testing.T) {
	rt := &captureRT{status: http.StatusOK, body: "http_requests 7\n"}
	p := proxyWith(t, rt)

	body, ts, err := p.PodMetrics(context.Background(), "shop", "web-1", "9090", "metrics")
	if err != nil {
		t.Fatalf("PodMetrics: %v", err)
	}
	// The port is qualified onto the pod name in the kubelet pod-proxy URL form.
	if rt.gotPath != "/api/v1/namespaces/shop/pods/web-1:9090/proxy/metrics" {
		t.Fatalf("pod-proxy URL wrong: %q", rt.gotPath)
	}
	if string(body) != "http_requests 7\n" {
		t.Fatalf("body not verbatim: %q", string(body))
	}
	assertReceiveStamp(t, ts)
}

func TestPodMetrics_URLShapeNoPort(t *testing.T) {
	// Empty port must NOT append a ":" — the name stands alone.
	rt := &captureRT{status: http.StatusOK, body: "x 1\n"}
	p := proxyWith(t, rt)

	_, _, err := p.PodMetrics(context.Background(), "shop", "web-1", "", "metrics")
	if err != nil {
		t.Fatalf("PodMetrics: %v", err)
	}
	if rt.gotPath != "/api/v1/namespaces/shop/pods/web-1/proxy/metrics" {
		t.Fatalf("no-port pod-proxy URL wrong: %q", rt.gotPath)
	}
	if strings.Contains(rt.gotPath, "web-1:") {
		t.Fatalf("empty port leaked a colon into the URL: %q", rt.gotPath)
	}
}

func TestNodeMetrics_ServerErrorIsWrapped(t *testing.T) {
	rt := &captureRT{status: http.StatusServiceUnavailable, body: "kubelet down\n"}
	p := proxyWith(t, rt)

	body, ts, err := p.NodeMetrics(context.Background(), "node-x", "metrics")
	if err == nil {
		t.Fatalf("non-2xx must return an error")
	}
	if body != nil {
		t.Fatalf("error path must return nil body, got %q", string(body))
	}
	// Error context names which node/path failed so an operator can act.
	if !strings.Contains(err.Error(), "proxy node-x/metrics") {
		t.Fatalf("error missing node/path context: %v", err)
	}
	// Timestamp is still stamped even on error (doc 14 A12 receive-time at ingest).
	assertReceiveStamp(t, ts)
}

func TestPodMetrics_ServerErrorIsWrapped(t *testing.T) {
	rt := &captureRT{status: http.StatusNotFound, body: "no such pod\n"}
	p := proxyWith(t, rt)

	body, _, err := p.PodMetrics(context.Background(), "shop", "missing", "8080", "metrics")
	if err == nil {
		t.Fatalf("non-2xx must return an error")
	}
	if body != nil {
		t.Fatalf("error path must return nil body, got %q", string(body))
	}
	if !strings.Contains(err.Error(), "pod-proxy shop/missing:8080/metrics") {
		t.Fatalf("error missing pod/port/path context: %v", err)
	}
}

func TestNodeMetrics_TransportErrorPropagates(t *testing.T) {
	rt := &captureRT{netErr: errors.New("dial tcp: connection refused")}
	p := proxyWith(t, rt)

	body, _, err := p.NodeMetrics(context.Background(), "node-a", "metrics")
	if err == nil {
		t.Fatalf("transport error must propagate")
	}
	if body != nil {
		t.Fatalf("transport error must return nil body")
	}
	if !strings.Contains(err.Error(), "proxy node-a/metrics") {
		t.Fatalf("transport error not wrapped with context: %v", err)
	}
}

func TestProxy_ContextCancellationIsHonored(t *testing.T) {
	rt := &captureRT{status: http.StatusOK, body: "x 1\n"}
	p := proxyWith(t, rt)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, _, err := p.NodeMetrics(ctx, "node-a", "metrics")
	if err == nil {
		t.Fatalf("cancelled context must yield an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error must wrap context.Canceled, got: %v", err)
	}
}
