package observe

import (
	"context"
	"testing"
)

// goldenKSM is a kube-state-metrics exposition slice (G2 lane): the container
// restart counter (single series per container — the crash-loop guard's input) and
// the node Ready condition (a MULTI-dimensional family: one node, several
// condition×status series). Identity rides on the series labels, NOT a scrape
// target. The node-condition dimensions must NOT collapse into one ring.
const goldenKSM = `# HELP kube_pod_container_status_restarts_total Restarts.
# TYPE kube_pod_container_status_restarts_total counter
kube_pod_container_status_restarts_total{namespace="shop",pod="web-a",uid="pod-x",container="server"} 5
# HELP kube_node_status_condition Node conditions.
# TYPE kube_node_status_condition gauge
kube_node_status_condition{node="worker-1",condition="Ready",status="true"} 1
kube_node_status_condition{node="worker-1",condition="Ready",status="false"} 0
kube_node_status_condition{node="worker-1",condition="MemoryPressure",status="false"} 0
# HELP kube_pod_status_reason The reason a pod is in its current state.
# TYPE kube_pod_status_reason gauge
kube_pod_status_reason{namespace="shop",pod="web-a",uid="pod-x",reason="Evicted"} 1
kube_pod_status_reason{namespace="shop",pod="web-a",uid="pod-x",reason="NodeLost"} 0
# HELP kube_poddisruptionbudget_status_current_healthy PDB.
# TYPE kube_poddisruptionbudget_status_current_healthy gauge
kube_poddisruptionbudget_status_current_healthy{namespace="shop",poddisruptionbudget="web"} 1
`

// KSM ingest: the restart counter resolves to the container CEI (one stream, the
// rate-guard's input); the node-condition family resolves to the node CEI but as
// DISTINCT streams (the sub-id keeps the condition×status dimensions from mis-joining
// into one ring); the unmapped PDB object metric quarantines (never guessed).
func TestScrapeKSMResolvesByLabelIdentity(t *testing.T) {
	in, _ := newTestIngestor(t)
	f := fixturePodFetcher{payloads: map[string]string{"monitoring/kube-state-metrics": goldenKSM}, at: scrapeAt}
	targets := []PodTarget{{Namespace: "monitoring", Name: "kube-state-metrics", Port: "8080", Path: "metrics"}}
	sum := in.IngestPayloads(FetchKSM(context.Background(), f, targets))

	// 6 series resolve: 1 restart counter (container) + 3 node-condition series (node)
	// + 2 pod-status-reason rows (pod). The PDB object metric is unmapped → quarantined.
	// (Derived rows are projections, not counted in the scrape accounting.)
	if sum.SeriesResolved != 6 || sum.SamplesStored != 6 {
		t.Fatalf("resolved=%d stored=%d, want 6/6 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}
	if sum.SeriesQuarantine["unmapped-metric-class"] != 1 {
		t.Errorf("quarantines = %v, want unmapped-metric-class:1 (the PDB object metric)", sum.SeriesQuarantine)
	}

	// The DERIVATION: the active Evicted row of kube_pod_status_reason is re-emitted as
	// the clean single-series kube_pod_status_evicted on the POD CEI — the member a
	// detect-condition can bind (the raw multi-reason family is ambiguous). The inactive
	// NodeLost row (value 0) is NOT derived (the derived stream exists only for the
	// entities actually in that state).
	ev := in.StreamsByUIDMetric("pod-x", "kube_pod_status_evicted")
	if len(ev) != 1 {
		t.Fatalf("derived evicted streams = %v, want exactly 1 (the Evicted row, value>0)", ev)
	}
	if s, ok := in.Latest(ev[0]); !ok || s.Value != 1 {
		t.Errorf("derived evicted sample = %+v (ok=%v), want value 1", s, ok)
	}

	// The restart counter: ONE stream under the CONTAINER CEI (UID = podUID/container).
	r := in.StreamsByUIDMetric("pod-x/server", "kube_pod_container_status_restarts_total")
	if len(r) != 1 {
		t.Fatalf("restart streams = %v, want exactly 1 (the rate-guard input)", r)
	}
	if meta, _ := in.Meta(r[0]); meta.Kind != "Container" || meta.Type != "counter" {
		t.Errorf("restart stream meta = %+v, want Container/counter", meta)
	}

	// The node-condition family: THREE DISTINCT streams under the SAME (node CEI,
	// metric) — the sub-id (full label set) prevents the dimension mis-join. Without
	// the fix these would collapse into one ring and interleave unrelated conditions.
	c := in.StreamsByUIDMetric("node-u1", "kube_node_status_condition")
	if len(c) != 3 {
		t.Fatalf("node-condition streams = %v, want 3 distinct (condition×status dimensions, not collapsed)", c)
	}
}
