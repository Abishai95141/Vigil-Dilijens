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

	// 4 series resolve: 1 restart counter (container) + 3 node-condition series (node).
	// The PDB object metric is unmapped → quarantined, never guessed onto a workload.
	if sum.SeriesResolved != 4 || sum.SamplesStored != 4 {
		t.Fatalf("resolved=%d stored=%d, want 4/4 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}
	if sum.SeriesQuarantine["unmapped-metric-class"] != 1 {
		t.Errorf("quarantines = %v, want unmapped-metric-class:1 (the PDB object metric)", sum.SeriesQuarantine)
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
