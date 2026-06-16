package observe

import (
	"context"
	"testing"
	"time"
)

// goldenApp is an application's own /metrics exposition (doc 15 cap. A): a queue-depth
// gauge with two label dimensions, a request counter, and a freshness gauge — NONE
// carrying k8s identity labels. Identity must come from the scrape target, and the two
// queue dimensions must NOT collapse into one ring.
const goldenApp = `# HELP app_queue_depth Pending items per queue.
# TYPE app_queue_depth gauge
app_queue_depth{queue="inbound"} 1500
app_queue_depth{queue="outbound"} 42
# HELP app_requests_total Total requests served.
# TYPE app_requests_total counter
app_requests_total{code="200"} 9000
# HELP app_last_update_seconds Unix time of last successful update.
# TYPE app_last_update_seconds gauge
app_last_update_seconds 1.78152e+09
`

type fixturePodFetcher struct {
	payloads map[string]string // "ns/name" -> body
	at       time.Time
}

func (f fixturePodFetcher) PodMetrics(_ context.Context, ns, name, _, _ string) ([]byte, time.Time, error) {
	body, ok := f.payloads[ns+"/"+name]
	if !ok {
		return nil, f.at, context.DeadlineExceeded
	}
	return []byte(body), f.at, nil
}

// App /metrics ingest: every series is attributed to the SCRAPE-TARGET pod CEI (web-a /
// pod-x), the two queue dimensions become DISTINCT streams (no mis-join), and exposition
// types are preserved. The target pod is the boutique pod the test harness already seeds.
func TestScrapeAppMetricsResolveToTargetPod(t *testing.T) {
	in, _ := newTestIngestor(t)
	f := fixturePodFetcher{payloads: map[string]string{"shop/web-a": goldenApp}, at: scrapeAt}
	targets := []PodTarget{{Namespace: "shop", Name: "web-a", Port: "8080", Path: "metrics"}}
	sum := in.IngestPayloads(FetchPodMetrics(context.Background(), f, targets))

	// 4 series resolve (2 queue dims + 1 counter + 1 freshness gauge), all to the pod.
	if sum.SeriesResolved != 4 || sum.SamplesStored != 4 {
		t.Fatalf("resolved=%d stored=%d, want 4/4 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}

	// The two queue dimensions are DISTINCT streams under the SAME pod CEI + metric —
	// the sub-id (full label set) prevents the mis-join into one ring.
	q := in.StreamsByUIDMetric("pod-x", "app_queue_depth")
	if len(q) != 2 {
		t.Fatalf("queue_depth streams = %v, want 2 distinct (inbound, outbound)", q)
	}
	for _, id := range q {
		m, _ := in.Meta(id)
		if m.Kind != "Pod" || m.Type != "gauge" || m.Cadence != "scrape" {
			t.Errorf("queue meta = %+v, want Pod/gauge/scrape", m)
		}
	}

	// The counter keeps its type; the freshness gauge resolves — all on the pod CEI.
	if c := in.StreamsByUIDMetric("pod-x", "app_requests_total"); len(c) != 1 {
		t.Errorf("requests streams = %v, want 1", c)
	} else if m, _ := in.Meta(c[0]); m.Type != "counter" {
		t.Errorf("requests type = %s, want counter", m.Type)
	}
	if fr := in.StreamsByUIDMetric("pod-x", "app_last_update_seconds"); len(fr) != 1 {
		t.Errorf("freshness streams = %v, want 1", fr)
	}
}

// A target the control plane has not seen quarantines wholesale — never guessed onto
// some other pod. (web-z is not seeded.)
func TestScrapeAppUnknownTargetQuarantines(t *testing.T) {
	in, _ := newTestIngestor(t)
	f := fixturePodFetcher{payloads: map[string]string{"shop/web-z": goldenApp}, at: scrapeAt}
	sum := in.IngestPayloads(FetchPodMetrics(context.Background(), f,
		[]PodTarget{{Namespace: "shop", Name: "web-z", Port: "8080"}}))
	if sum.SeriesResolved != 0 {
		t.Errorf("resolved = %d, want 0 (unknown target never guesses a pod)", sum.SeriesResolved)
	}
	if sum.SeriesQuarantine["unknown-pod"] != 4 {
		t.Errorf("quarantines = %v, want unknown-pod:4", sum.SeriesQuarantine)
	}
}
