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
kube_node_status_condition{node="worker-1",condition="DiskPressure",status="true"} 1
kube_node_status_condition{node="worker-1",condition="DiskPressure",status="false"} 0
# HELP kube_pod_status_reason The reason a pod is in its current state.
# TYPE kube_pod_status_reason gauge
kube_pod_status_reason{namespace="shop",pod="web-a",uid="pod-x",reason="Evicted"} 1
kube_pod_status_reason{namespace="shop",pod="web-a",uid="pod-x",reason="NodeLost"} 0
# HELP kube_persistentvolumeclaim_status_phase PVC phase (Pending/Bound/Lost).
# TYPE kube_persistentvolumeclaim_status_phase gauge
kube_persistentvolumeclaim_status_phase{namespace="shop",persistentvolumeclaim="data-cart",phase="Pending"} 1
kube_persistentvolumeclaim_status_phase{namespace="shop",persistentvolumeclaim="data-cart",phase="Bound"} 0
kube_persistentvolumeclaim_status_phase{namespace="shop",persistentvolumeclaim="data-cart",phase="Lost"} 0
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

	// 12 series resolve: 1 restart counter (container) + 5 node-condition series (node:
	// Ready true/false, MemoryPressure false, DiskPressure true/false) + 2
	// pod-status-reason rows (pod) + 3 PVC status-phase rows (the claim) + 1 PDB
	// status-current-healthy row (the budget — now a first-class identity instance, docs/33
	// build 2). (Derived rows are projections, not counted in the scrape accounting.)
	if sum.SeriesResolved != 12 || sum.SamplesStored != 12 {
		t.Fatalf("resolved=%d stored=%d, want 12/12 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}
	// The PDB object metric now resolves to the PDB CEI (uid pdb-u1) — the entity-binding fix.
	if pdb := in.StreamsByUIDMetric("pdb-u1", "kube_poddisruptionbudget_status_current_healthy"); len(pdb) != 1 {
		t.Errorf("PDB healthy streams = %v, want exactly 1 (resolved to the PDB CEI)", pdb)
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

	// The node-condition family: FIVE DISTINCT streams under the SAME (node CEI,
	// metric) — the sub-id (full label set) prevents the dimension mis-join. Without
	// the fix these would collapse into one ring and interleave unrelated conditions.
	c := in.StreamsByUIDMetric("node-u1", "kube_node_status_condition")
	if len(c) != 5 {
		t.Fatalf("node-condition streams = %v, want 5 distinct (condition×status dimensions, not collapsed)", c)
	}

	// The DISK DERIVATION (DISK_PID_INODE_PRESSURE): the active DiskPressure=true row of
	// kube_node_status_condition is re-emitted as the clean single-series
	// kube_node_status_disk_pressure on the NODE CEI — the kubelet's own disk/inode
	// eviction verdict, the member a detect-condition can bind. The healthy
	// DiskPressure=false row (value 0) is NOT derived (the derived stream exists only for
	// a node ACTUALLY under disk pressure), so a check never fires on a healthy node.
	dp := in.StreamsByUIDMetric("node-u1", "kube_node_status_disk_pressure")
	if len(dp) != 1 {
		t.Fatalf("derived disk-pressure streams = %v, want exactly 1 (the DiskPressure=true row, value>0)", dp)
	}
	if s, ok := in.Latest(dp[0]); !ok || s.Value != 1 {
		t.Errorf("derived disk-pressure sample = %+v (ok=%v), want value 1", s, ok)
	}

	// The PVC DERIVATION (VOLUME_MOUNT_FAILURE): the kube_persistentvolumeclaim_status_phase
	// rows resolve to the REAL PVC instance CEI (the dark-bar fix — a PVC is now a first-class
	// identity), and the active Pending row is re-emitted as the clean single-series
	// kube_persistentvolumeclaim_pending on that PVC CEI. The healthy Bound/Lost rows (value 0)
	// are NOT derived, so a check never fires on a bound claim.
	pp := in.StreamsByUIDMetric("pvc-u1", "kube_persistentvolumeclaim_pending")
	if len(pp) != 1 {
		t.Fatalf("derived pvc-pending streams = %v, want exactly 1 (the Pending row, value>0)", pp)
	}
	if s, ok := in.Latest(pp[0]); !ok || s.Value != 1 {
		t.Errorf("derived pvc-pending sample = %+v (ok=%v), want value 1", s, ok)
	}
}
